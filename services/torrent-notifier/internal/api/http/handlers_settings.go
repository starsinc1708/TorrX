package apihttp

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"torrentstream/notifier/internal/domain"
	"torrentstream/notifier/internal/notifier"
)

func (s *Server) handleIntegrationSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleGetSettings(w, r)
	case http.MethodPatch, http.MethodPut:
		s.handleUpdateSettings(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	var settings domain.IntegrationSettings
	if s.repo != nil {
		var err error
		settings, err = s.repo.Get(r.Context())
		if err != nil {
			http.Error(w, "repository error", http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(settings); err != nil {
		// header already written — log only
		_ = err
	}
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	if s.repo == nil {
		http.Error(w, "no repository", http.StatusInternalServerError)
		return
	}
	var body domain.IntegrationSettings
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := s.repo.Upsert(r.Context(), body); err != nil {
		http.Error(w, "repository error", http.StatusInternalServerError)
		return
	}
	saved, err := s.repo.Get(r.Context())
	if err != nil {
		http.Error(w, "repository error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(saved); err != nil {
		_ = err
	}
}

type testResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func (s *Server) handleTestJellyfin(w http.ResponseWriter, r *http.Request) {
	s.handleTestMediaServer(w, r, func(settings domain.IntegrationSettings) domain.MediaServerConfig {
		return settings.Jellyfin
	})
}

func (s *Server) handleTestEmby(w http.ResponseWriter, r *http.Request) {
	s.handleTestMediaServer(w, r, func(settings domain.IntegrationSettings) domain.MediaServerConfig {
		return settings.Emby
	})
}

func (s *Server) handleTestMediaServer(w http.ResponseWriter, r *http.Request,
	getCfg func(domain.IntegrationSettings) domain.MediaServerConfig) {
	var settings domain.IntegrationSettings
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	cfg := getCfg(settings)
	n := notifier.New()
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	errMsg := n.TestConnection(ctx, cfg)
	result := testResult{OK: errMsg == "", Error: errMsg}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		_ = err
	}
}
