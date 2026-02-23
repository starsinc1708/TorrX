package apihttp

import (
	"encoding/json"
	"net/http"
	"strings"

	"torrentstream/internal/app"
	"torrentstream/internal/services/subtitles/opensubtitles"
)

func (s *Server) handleSubtitleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleGetSubtitleSettings(w, r)
	case http.MethodPatch, http.MethodPut:
		s.handleUpdateSubtitleSettings(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleGetSubtitleSettings(w http.ResponseWriter, _ *http.Request) {
	if s.subtitles == nil {
		writeJSON(w, http.StatusOK, app.SubtitleSettings{})
		return
	}
	writeJSON(w, http.StatusOK, s.subtitles.Get())
}

func (s *Server) handleUpdateSubtitleSettings(w http.ResponseWriter, r *http.Request) {
	if s.subtitles == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "subtitle settings not configured")
		return
	}

	var body app.SubtitleSettings
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid json")
		return
	}

	current := s.subtitles.Get()
	if body.APIKey == "" {
		body.APIKey = current.APIKey
	}
	if body.Languages == nil {
		body.Languages = current.Languages
	}

	if err := s.subtitles.Update(body); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to update subtitle settings")
		return
	}
	writeJSON(w, http.StatusOK, s.subtitles.Get())
}

type subtitleSearchResponse struct {
	Results []opensubtitles.SubtitleResult `json:"results"`
}

func (s *Server) handleSubtitleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.subtitles == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "subtitle settings not configured")
		return
	}

	settings := s.subtitles.Get()
	if settings.APIKey == "" {
		writeError(w, http.StatusBadRequest, "no_api_key", "OpenSubtitles API key not configured")
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("query"))
	hash := strings.TrimSpace(r.URL.Query().Get("hash"))
	langParam := strings.TrimSpace(r.URL.Query().Get("lang"))

	if query == "" && hash == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "query or hash required")
		return
	}

	var langs []string
	if langParam != "" {
		langs = strings.Split(langParam, ",")
	} else {
		langs = settings.Languages
	}

	client := opensubtitles.NewClient(settings.APIKey)

	var results []opensubtitles.SubtitleResult
	var err error

	if hash != "" {
		results, err = client.Search(r.Context(), opensubtitles.SearchParams{
			MovieHash: hash,
			Languages: langs,
		})
		if err != nil {
			writeError(w, http.StatusBadGateway, "search_failed", err.Error())
			return
		}
	}

	if len(results) == 0 && query != "" {
		results, err = client.Search(r.Context(), opensubtitles.SearchParams{
			Query:     query,
			Languages: langs,
		})
		if err != nil {
			writeError(w, http.StatusBadGateway, "search_failed", err.Error())
			return
		}
	}

	writeJSON(w, http.StatusOK, subtitleSearchResponse{Results: results})
}

type subtitleDownloadRequest struct {
	FileID int `json:"fileId"`
}

func (s *Server) handleSubtitleDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.subtitles == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "subtitle settings not configured")
		return
	}

	settings := s.subtitles.Get()
	if settings.APIKey == "" {
		writeError(w, http.StatusBadRequest, "no_api_key", "OpenSubtitles API key not configured")
		return
	}

	var body subtitleDownloadRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.FileID == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "fileId required")
		return
	}

	client := opensubtitles.NewClient(settings.APIKey)
	link, err := client.DownloadLink(r.Context(), body.FileID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "download_failed", err.Error())
		return
	}

	// Fetch the actual subtitle file and proxy it as VTT.
	resp, err := http.Get(link)
	if err != nil {
		writeError(w, http.StatusBadGateway, "fetch_failed", err.Error())
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)

	w.Write([]byte("WEBVTT\n\n"))
	buf := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
		}
		if readErr != nil {
			break
		}
	}
}
