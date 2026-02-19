package apihttp

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"torrentstream/internal/app"
	"torrentstream/internal/domain"
	domainports "torrentstream/internal/domain/ports"
	"torrentstream/internal/usecase"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

type CreateTorrentUseCase interface {
	Execute(ctx context.Context, input usecase.CreateTorrentInput) (domain.TorrentRecord, error)
}

type StartTorrentUseCase interface {
	Execute(ctx context.Context, id domain.TorrentID) (domain.TorrentRecord, error)
}

type StopTorrentUseCase interface {
	Execute(ctx context.Context, id domain.TorrentID) (domain.TorrentRecord, error)
}

type DeleteTorrentUseCase interface {
	Execute(ctx context.Context, id domain.TorrentID, deleteFiles bool) error
}

type StreamTorrentUseCase interface {
	Execute(ctx context.Context, id domain.TorrentID, fileIndex int) (usecase.StreamResult, error)
	ExecuteRaw(ctx context.Context, id domain.TorrentID, fileIndex int) (usecase.StreamResult, error)
}

type GetTorrentStateUseCase interface {
	Execute(ctx context.Context, id domain.TorrentID) (domain.SessionState, error)
}

type ListTorrentStatesUseCase interface {
	Execute(ctx context.Context) ([]domain.SessionState, error)
}

type WatchHistoryStore interface {
	Upsert(ctx context.Context, wp domain.WatchPosition) error
	Get(ctx context.Context, torrentID domain.TorrentID, fileIndex int) (domain.WatchPosition, error)
	ListRecent(ctx context.Context, limit int) ([]domain.WatchPosition, error)
}

type EncodingSettingsController interface {
	Get() app.EncodingSettings
	Update(settings app.EncodingSettings) error
}

type HLSSettingsController interface {
	Get() app.HLSSettings
	Update(settings app.HLSSettings) error
}

type PlayerSettingsController interface {
	CurrentTorrentID() domain.TorrentID
	SetCurrentTorrentID(id domain.TorrentID) error
}

type MediaProbe interface {
	Probe(ctx context.Context, filePath string) (domain.MediaInfo, error)
	ProbeReader(ctx context.Context, reader io.Reader) (domain.MediaInfo, error)
}

// mediaProbeCacheEntry holds a cached ffprobe result with an expiration time.
type mediaProbeCacheEntry struct {
	info      domain.MediaInfo
	expiresAt time.Time
}

// mediaProbeCacheKey uniquely identifies a media probe request.
type mediaProbeCacheKey struct {
	torrentID domain.TorrentID
	fileIndex int
}

const mediaProbeCacheTTL = 5 * time.Minute

type Server struct {
	createTorrent   CreateTorrentUseCase
	startTorrent    StartTorrentUseCase
	stopTorrent     StopTorrentUseCase
	deleteTorrent   DeleteTorrentUseCase
	streamTorrent   StreamTorrentUseCase
	getState        GetTorrentStateUseCase
	listStates      ListTorrentStatesUseCase
	repo            domainports.TorrentRepository
	openAPIPath     string
	hls             *StreamJobManager
	hlsCfg          *HLSConfig
	mediaProbe      MediaProbe
	mediaDataDir    string
	watchHistory    WatchHistoryStore
	encoding        EncodingSettingsController
	hlsSettingsCtrl HLSSettingsController
	player          PlayerSettingsController
	engine          domainports.Engine
	allowedOrigins  []string
	logger          *slog.Logger
	handler         http.Handler
	wsHub           *wsHub
	mediaCacheMu    sync.RWMutex
	mediaProbeCache map[mediaProbeCacheKey]mediaProbeCacheEntry
}

type ServerOption func(*Server)

func WithOpenAPIPath(path string) ServerOption {
	return func(s *Server) {
		s.openAPIPath = path
	}
}

func WithRepository(repo domainports.TorrentRepository) ServerOption {
	return func(s *Server) {
		s.repo = repo
	}
}

func WithStartTorrent(uc StartTorrentUseCase) ServerOption {
	return func(s *Server) {
		s.startTorrent = uc
	}
}

func WithStopTorrent(uc StopTorrentUseCase) ServerOption {
	return func(s *Server) {
		s.stopTorrent = uc
	}
}

func WithDeleteTorrent(uc DeleteTorrentUseCase) ServerOption {
	return func(s *Server) {
		s.deleteTorrent = uc
	}
}

func WithStreamTorrent(uc StreamTorrentUseCase) ServerOption {
	return func(s *Server) {
		s.streamTorrent = uc
	}
}

func WithHLS(cfg HLSConfig) ServerOption {
	return func(s *Server) {
		s.hlsCfg = &cfg
	}
}

func WithMediaProbe(probe MediaProbe, dataDir string) ServerOption {
	return func(s *Server) {
		s.mediaProbe = probe
		s.mediaDataDir = strings.TrimSpace(dataDir)
		if s.mediaDataDir != "" {
			if abs, err := filepath.Abs(s.mediaDataDir); err == nil {
				s.mediaDataDir = abs
			}
			s.mediaDataDir = filepath.Clean(s.mediaDataDir)
		}
	}
}

func WithGetTorrentState(uc GetTorrentStateUseCase) ServerOption {
	return func(s *Server) {
		s.getState = uc
	}
}

func WithListTorrentStates(uc ListTorrentStatesUseCase) ServerOption {
	return func(s *Server) {
		s.listStates = uc
	}
}

func WithWatchHistory(store WatchHistoryStore) ServerOption {
	return func(s *Server) {
		s.watchHistory = store
	}
}

func WithEncodingSettings(ctrl EncodingSettingsController) ServerOption {
	return func(s *Server) {
		s.encoding = ctrl
	}
}

func WithPlayerSettings(ctrl PlayerSettingsController) ServerOption {
	return func(s *Server) {
		s.player = ctrl
	}
}

func WithEngine(engine domainports.Engine) ServerOption {
	return func(s *Server) {
		s.engine = engine
	}
}

// WithAllowedOrigins configures the CORS allowed origins whitelist.
// When empty (default), any origin is permitted (development mode).
func WithAllowedOrigins(origins []string) ServerOption {
	return func(s *Server) {
		s.allowedOrigins = origins
	}
}

// EncodingSettingsEngine returns the internal HLS manager as an
// app.EncodingSettingsEngine. Returns nil if HLS is not configured.
func (s *Server) EncodingSettingsEngine() app.EncodingSettingsEngine {
	if s.hls == nil {
		return nil
	}
	return s.hls
}

// SetEncodingSettings sets the encoding settings controller after construction.
func (s *Server) SetEncodingSettings(ctrl EncodingSettingsController) {
	s.encoding = ctrl
}

// HLSSettingsEngine returns the internal HLS manager as an
// app.HLSSettingsEngine. Returns nil if HLS is not configured.
func (s *Server) HLSSettingsEngine() app.HLSSettingsEngine {
	if s.hls == nil {
		return nil
	}
	return s.hls
}

// SetHLSSettings sets the HLS settings controller after construction.
func (s *Server) SetHLSSettings(ctrl HLSSettingsController) {
	s.hlsSettingsCtrl = ctrl
}

// HLSCacheTotalSize returns the current total size of the HLS segment cache in bytes.
// In the FSM architecture there is no persistent segment cache, so this always returns 0.
func (s *Server) HLSCacheTotalSize() int64 {
	return 0
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if s.wsHub == nil {
		http.Error(w, "websocket not available", http.StatusServiceUnavailable)
		return
	}
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Error("ws upgrade failed", slog.String("error", err.Error()))
		return
	}
	client := &wsClient{
		hub:  s.wsHub,
		conn: conn,
		send: make(chan []byte, 256),
	}
	s.wsHub.register <- client
	go client.writePump()
	go client.readPump()
}

// BroadcastStates sends states to all WebSocket clients.
func (s *Server) BroadcastStates(states []domain.SessionState) {
	if s.wsHub != nil {
		s.wsHub.BroadcastStates(states)
	}
}

// BroadcastTorrents lists all torrents from the repository and broadcasts
// their summaries to all connected WebSocket clients.
func (s *Server) BroadcastTorrents() {
	if s.wsHub == nil || s.repo == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	records, err := s.repo.List(ctx, domain.TorrentFilter{})
	if err != nil {
		s.logger.Debug("ws broadcast torrents failed", slog.String("error", err.Error()))
		return
	}
	summaries := make([]torrentSummary, 0, len(records))
	for _, record := range records {
		summaries = append(summaries, torrentSummary{
			ID:         record.ID,
			Name:       record.Name,
			Status:     record.Status,
			Progress:   progressRatio(record.DoneBytes, record.TotalBytes),
			DoneBytes:  record.DoneBytes,
			TotalBytes: record.TotalBytes,
			CreatedAt:  record.CreatedAt,
			UpdatedAt:  record.UpdatedAt,
			Tags:       record.Tags,
		})
	}
	s.wsHub.Broadcast("torrents", summaries)
}

// BroadcastPlayerSettings broadcasts the current player settings to all
// connected WebSocket clients.
func (s *Server) BroadcastPlayerSettings() {
	if s.wsHub == nil || s.player == nil {
		return
	}
	s.wsHub.Broadcast("player_settings", playerSettingsResponse{CurrentTorrentID: s.player.CurrentTorrentID()})
}

// BroadcastHealth broadcasts the current player health status to all
// connected WebSocket clients.
func (s *Server) BroadcastHealth(ctx context.Context) {
	if s.wsHub == nil {
		return
	}
	s.wsHub.Broadcast("health", s.BuildPlayerHealth(ctx))
}

func WithLogger(logger *slog.Logger) ServerOption {
	return func(s *Server) {
		s.logger = logger
	}
}

func NewServer(create CreateTorrentUseCase, opts ...ServerOption) *Server {
	s := &Server{
		createTorrent:   create,
		openAPIPath:     defaultOpenAPIPath(),
		mediaProbeCache: make(map[mediaProbeCacheKey]mediaProbeCacheEntry),
	}
	for _, opt := range opts {
		opt(s)
	}

	if s.logger == nil {
		s.logger = slog.Default()
	}

	if s.hls == nil && s.streamTorrent != nil {
		cfg := HLSConfig{}
		if s.hlsCfg != nil {
			cfg = *s.hlsCfg
		}
		s.hls = newStreamJobManager(s.streamTorrent, s.engine, cfg, s.logger)
	}

	s.wsHub = newWSHub(s.logger)
	go s.wsHub.run()

	mux := http.NewServeMux()
	mux.HandleFunc("/torrents", s.handleTorrents)
	mux.HandleFunc("/torrents/", s.handleTorrentByID)
	mux.HandleFunc("/settings/encoding", s.handleEncodingSettings)
	mux.HandleFunc("/settings/hls", s.handleHLSSettings)
	mux.HandleFunc("/settings/player", s.handlePlayerSettings)
	mux.HandleFunc("/watch-history", s.handleWatchHistory)
	mux.HandleFunc("/watch-history/", s.handleWatchHistoryByID)
	mux.HandleFunc("/internal/health/player", s.handlePlayerHealth)
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/swagger", s.handleSwagger)
	mux.HandleFunc("/swagger/", s.handleSwagger)
	mux.HandleFunc("/swagger/openapi.json", s.handleOpenAPI)
	mux.HandleFunc("/swagger/openapi", s.handleOpenAPI)
	mux.HandleFunc("/ws", s.handleWS)

	traced := otelhttp.NewHandler(loggingMiddleware(s.logger, mux), "torrent-engine",
		otelhttp.WithFilter(func(r *http.Request) bool {
			p := r.URL.Path
			return p != "/metrics" && p != "/internal/health/player" && !strings.HasPrefix(p, "/swagger")
		}),
	)
	s.handler = recoveryMiddleware(s.logger, rateLimitMiddleware(100, 200, metricsMiddleware(corsMiddleware(s.allowedOrigins, traced))))
	return s
}

func defaultOpenAPIPath() string {
	cwd, err := os.Getwd()
	if err != nil {
		return filepath.Join("docs", "openapi.json")
	}

	dir := cwd
	for i := 0; i < 6; i++ {
		path := filepath.Join(dir, "docs", "openapi.json")
		if _, err := os.Stat(path); err == nil {
			return path
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return filepath.Join("docs", "openapi.json")
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// Close gracefully shuts down the HLS manager (cancelling all FFmpeg jobs)
// and the WebSocket hub, disconnecting all clients.
func (s *Server) Close() {
	if s.hls != nil {
		s.hls.shutdown()
	}
	if s.wsHub != nil {
		s.wsHub.Close()
	}
}
