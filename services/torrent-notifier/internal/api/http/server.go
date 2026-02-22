package apihttp

import (
	"context"
	"net/http"
	"time"

	"torrentstream/notifier/internal/domain"
)

// settingsRepository is the storage port used by the settings handlers.
type settingsRepository interface {
	Get(ctx context.Context) (domain.IntegrationSettings, error)
	Upsert(ctx context.Context, s domain.IntegrationSettings) error
}

// Server holds all HTTP handlers for the notifier service.
type Server struct {
	engineURL string
	repo      settingsRepository
	client    *http.Client
	mux       *http.ServeMux
}

// NewServer creates the HTTP server. repo may be nil in tests.
func NewServer(engineURL string, repo settingsRepository) *Server {
	s := &Server{
		engineURL: engineURL,
		repo:      repo,
		client:    &http.Client{Timeout: 5 * time.Second},
		mux:       http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /widget", s.handleWidget)
	s.mux.HandleFunc("/settings/integrations", s.handleIntegrationSettings)      // GET+PATCH handled inside
	s.mux.HandleFunc("POST /settings/integrations/test-jellyfin", s.handleTestJellyfin)
	s.mux.HandleFunc("POST /settings/integrations/test-emby", s.handleTestEmby)
}

// MountQBT mounts the qBittorrent API compatibility routes.
func (s *Server) MountQBT(handler http.Handler) {
	s.mux.Handle("/api/v2/", handler)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}
