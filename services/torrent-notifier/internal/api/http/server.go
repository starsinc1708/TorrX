package apihttp

import (
	"net/http"

	mongorepo "torrentstream/notifier/internal/repository/mongo"
)

// Server holds all HTTP handlers for the notifier service.
type Server struct {
	engineURL string
	repo      *mongorepo.SettingsRepository
	mux       *http.ServeMux
}

// NewServer creates the HTTP server. repo may be nil in tests.
func NewServer(engineURL string, repo *mongorepo.SettingsRepository) *Server {
	s := &Server{
		engineURL: engineURL,
		repo:      repo,
		mux:       http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("/health", s.handleHealth)
	s.mux.HandleFunc("/widget", s.handleWidget)
	s.mux.HandleFunc("/settings/integrations", s.handleIntegrationSettings)
	s.mux.HandleFunc("/settings/integrations/test-jellyfin", s.handleTestJellyfin)
	s.mux.HandleFunc("/settings/integrations/test-emby", s.handleTestEmby)
}

// MountQBT mounts the qBittorrent API compatibility routes.
func (s *Server) MountQBT(handler http.Handler) {
	s.mux.Handle("/api/v2/", handler)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}
