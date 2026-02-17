package apihttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
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
}

type GetTorrentStateUseCase interface {
	Execute(ctx context.Context, id domain.TorrentID) (domain.SessionState, error)
}

type ListTorrentStatesUseCase interface {
	Execute(ctx context.Context) ([]domain.SessionState, error)
}

type StorageSettingsController interface {
	StorageMode() string
	MemoryLimitBytes() int64
	SpillToDisk() bool
	SetMemoryLimitBytes(limit int64) error
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
	storage         StorageSettingsController
	repo            domainports.TorrentRepository
	openAPIPath     string
	hls             *hlsManager
	hlsCfg          *HLSConfig
	mediaProbe      MediaProbe
	mediaDataDir    string
	watchHistory    WatchHistoryStore
	encoding        EncodingSettingsController
	hlsSettingsCtrl HLSSettingsController
	player          PlayerSettingsController
	engine          domainports.Engine
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

func WithStorageSettings(storage StorageSettingsController) ServerOption {
	return func(s *Server) {
		s.storage = storage
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
func (s *Server) HLSCacheTotalSize() int64 {
	if s.hls == nil || s.hls.cache == nil {
		return 0
	}
	return s.hls.cache.TotalSize()
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
		s.hls = newHLSManager(s.streamTorrent, cfg, s.logger)
	}

	s.wsHub = newWSHub(s.logger)
	go s.wsHub.run()

	mux := http.NewServeMux()
	mux.HandleFunc("/torrents", s.handleTorrents)
	mux.HandleFunc("/torrents/", s.handleTorrentByID)
	mux.HandleFunc("/settings/storage", s.handleStorageSettings)
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
	s.handler = recoveryMiddleware(s.logger, rateLimitMiddleware(100, 200, metricsMiddleware(corsMiddleware(traced))))
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

// Close gracefully shuts down the WebSocket hub, disconnecting all clients.
func (s *Server) Close() {
	if s.wsHub != nil {
		s.wsHub.Close()
	}
}

func (s *Server) handleTorrents(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.handleCreateTorrent(w, r)
	case http.MethodGet:
		s.handleListTorrents(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

type storageSettingsResponse struct {
	Mode             string `json:"mode"`
	MemoryLimitBytes int64  `json:"memoryLimitBytes"`
	SpillToDisk      bool   `json:"spillToDisk"`
	DataDir          string `json:"dataDir,omitempty"`
	HLSDir           string `json:"hlsDir,omitempty"`
}

type updateStorageSettingsRequest struct {
	MemoryLimitBytes *int64 `json:"memoryLimitBytes"`
}

type playerSettingsResponse struct {
	CurrentTorrentID domain.TorrentID `json:"currentTorrentId,omitempty"`
}

type updatePlayerSettingsRequest struct {
	CurrentTorrentID *string `json:"currentTorrentId"`
}

func (s *Server) handleStorageSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleGetStorageSettings(w, r)
	case http.MethodPatch, http.MethodPut:
		s.handleUpdateStorageSettings(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleGetStorageSettings(w http.ResponseWriter, _ *http.Request) {
	if s.storage == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "storage settings are not configured")
		return
	}
	writeJSON(w, http.StatusOK, s.currentStorageSettings())
}

func (s *Server) handleUpdateStorageSettings(w http.ResponseWriter, r *http.Request) {
	if s.storage == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "storage settings are not configured")
		return
	}

	var body updateStorageSettingsRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid json")
		return
	}
	if body.MemoryLimitBytes == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "memoryLimitBytes is required")
		return
	}
	if *body.MemoryLimitBytes < 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "memoryLimitBytes must be >= 0")
		return
	}

	if err := s.storage.SetMemoryLimitBytes(*body.MemoryLimitBytes); err != nil {
		if errors.Is(err, domain.ErrUnsupported) {
			writeError(w, http.StatusConflict, "unsupported_operation", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to update storage settings")
		return
	}

	writeJSON(w, http.StatusOK, s.currentStorageSettings())
}

func (s *Server) currentStorageSettings() storageSettingsResponse {
	mode := "disk"
	limit := int64(0)
	spill := false
	if s.storage != nil {
		mode = strings.TrimSpace(s.storage.StorageMode())
		if mode == "" {
			mode = "disk"
		}
		limit = s.storage.MemoryLimitBytes()
		spill = s.storage.SpillToDisk()
	}
	dataDir := s.mediaDataDir
	hlsDir := ""
	if s.hls != nil {
		hlsDir = s.hls.baseDir
	}
	return storageSettingsResponse{
		Mode:             mode,
		MemoryLimitBytes: limit,
		SpillToDisk:      spill,
		DataDir:          dataDir,
		HLSDir:           hlsDir,
	}
}

func (s *Server) handlePlayerSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleGetPlayerSettings(w, r)
	case http.MethodPatch, http.MethodPut:
		s.handleUpdatePlayerSettings(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// BuildPlayerHealth constructs a playerHealthResponse without writing it to an HTTP response.
func (s *Server) BuildPlayerHealth(ctx context.Context) playerHealthResponse {
	resp := playerHealthResponse{
		Status:    "ok",
		CheckedAt: time.Now().UTC(),
		HLS:       hlsHealthSnapshot{},
	}

	setDegraded := func(issue string) {
		if strings.TrimSpace(issue) == "" {
			return
		}
		resp.Status = "degraded"
		resp.Issues = append(resp.Issues, issue)
	}

	if s.player != nil {
		resp.CurrentTorrentID = strings.TrimSpace(string(s.player.CurrentTorrentID()))
		resp.FocusModeEnabled = resp.CurrentTorrentID != ""
	} else {
		setDegraded("player settings are not configured")
	}

	if s.engine != nil {
		ids, err := s.engine.ListActiveSessions(ctx)
		if err != nil {
			setDegraded("failed to list active sessions")
		} else {
			resp.ActiveSessions = len(ids)
			if len(ids) > 0 {
				resp.ActiveSessionIDs = make([]string, 0, len(ids))
				for _, id := range ids {
					resp.ActiveSessionIDs = append(resp.ActiveSessionIDs, string(id))
				}
			}
		}
	} else {
		setDegraded("torrent engine is not configured")
	}

	if resp.FocusModeEnabled {
		if resp.ActiveSessions == 0 {
			setDegraded("focus mode is enabled, but there are no active sessions")
		} else if !containsString(resp.ActiveSessionIDs, resp.CurrentTorrentID) {
			setDegraded("current torrent is not present in active sessions")
		}
	}

	if s.hls != nil {
		resp.HLS = s.hls.healthSnapshot()
		if resp.HLS.LastJobError != "" && resp.HLS.LastJobErrorAt != nil {
			if resp.CheckedAt.Sub(*resp.HLS.LastJobErrorAt) <= 3*time.Minute {
				setDegraded("recent HLS failure detected")
			}
		}
		if resp.HLS.LastSeekError != "" && resp.HLS.LastSeekErrorAt != nil {
			if resp.CheckedAt.Sub(*resp.HLS.LastSeekErrorAt) <= 3*time.Minute {
				setDegraded("recent HLS seek failure detected")
			}
		}
	} else {
		setDegraded("hls manager is not configured")
	}

	return resp
}

func (s *Server) handlePlayerHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, s.BuildPlayerHealth(r.Context()))
}

func (s *Server) handleGetPlayerSettings(w http.ResponseWriter, _ *http.Request) {
	if s.player == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "player settings are not configured")
		return
	}
	writeJSON(w, http.StatusOK, playerSettingsResponse{CurrentTorrentID: s.player.CurrentTorrentID()})
}

func (s *Server) handleUpdatePlayerSettings(w http.ResponseWriter, r *http.Request) {
	if s.player == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "player settings are not configured")
		return
	}

	var body updatePlayerSettingsRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid json")
		return
	}
	if body.CurrentTorrentID == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "currentTorrentId is required")
		return
	}

	id := domain.TorrentID(strings.TrimSpace(*body.CurrentTorrentID))
	if err := s.setCurrentTorrentID(r.Context(), id); err != nil {
		writeDomainError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, playerSettingsResponse{CurrentTorrentID: s.player.CurrentTorrentID()})
	s.BroadcastPlayerSettings()
}

// Encoding settings handlers.

var validPresets = map[string]bool{
	"ultrafast": true,
	"superfast": true,
	"veryfast":  true,
	"faster":    true,
	"fast":      true,
	"medium":    true,
}

var validAudioBitrates = map[string]bool{
	"96k":  true,
	"128k": true,
	"192k": true,
	"256k": true,
}

func (s *Server) handleEncodingSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleGetEncodingSettings(w, r)
	case http.MethodPatch, http.MethodPut:
		s.handleUpdateEncodingSettings(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleGetEncodingSettings(w http.ResponseWriter, _ *http.Request) {
	if s.encoding == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "encoding settings not configured")
		return
	}
	writeJSON(w, http.StatusOK, s.encoding.Get())
}

func (s *Server) handleUpdateEncodingSettings(w http.ResponseWriter, r *http.Request) {
	if s.encoding == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "encoding settings not configured")
		return
	}

	var body app.EncodingSettings
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid json")
		return
	}

	if body.Preset != "" && !validPresets[body.Preset] {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid preset")
		return
	}
	if body.CRF < 0 || body.CRF > 51 {
		writeError(w, http.StatusBadRequest, "invalid_request", "crf must be 0-51")
		return
	}
	if body.AudioBitrate != "" && !validAudioBitrates[body.AudioBitrate] {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid audioBitrate")
		return
	}

	// Merge with current values for partial updates.
	current := s.encoding.Get()
	if body.Preset == "" {
		body.Preset = current.Preset
	}
	if body.CRF == 0 {
		body.CRF = current.CRF
	}
	if body.AudioBitrate == "" {
		body.AudioBitrate = current.AudioBitrate
	}

	if err := s.encoding.Update(body); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to update encoding settings")
		return
	}

	writeJSON(w, http.StatusOK, s.encoding.Get())
}

// HLS settings handlers.

func (s *Server) handleHLSSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleGetHLSSettings(w, r)
	case http.MethodPatch, http.MethodPut:
		s.handleUpdateHLSSettings(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleGetHLSSettings(w http.ResponseWriter, _ *http.Request) {
	if s.hlsSettingsCtrl == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "hls settings not configured")
		return
	}
	writeJSON(w, http.StatusOK, s.hlsSettingsCtrl.Get())
}

func (s *Server) handleUpdateHLSSettings(w http.ResponseWriter, r *http.Request) {
	if s.hlsSettingsCtrl == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "hls settings not configured")
		return
	}

	var body app.HLSSettings
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid json")
		return
	}

	// Merge with current values for partial updates.
	current := s.hlsSettingsCtrl.Get()
	if body.MemBufSizeMB == 0 {
		body.MemBufSizeMB = current.MemBufSizeMB
	}
	if body.CacheSizeMB == 0 {
		body.CacheSizeMB = current.CacheSizeMB
	}
	if body.CacheMaxAgeHours == 0 {
		body.CacheMaxAgeHours = current.CacheMaxAgeHours
	}
	if body.SegmentDuration == 0 {
		body.SegmentDuration = current.SegmentDuration
	}

	// Validation.
	if body.MemBufSizeMB < 0 || body.MemBufSizeMB > 4096 {
		writeError(w, http.StatusBadRequest, "invalid_request", "memBufSizeMB must be 0-4096")
		return
	}
	if body.CacheSizeMB < 100 || body.CacheSizeMB > 102400 {
		writeError(w, http.StatusBadRequest, "invalid_request", "cacheSizeMB must be 100-102400")
		return
	}
	if body.CacheMaxAgeHours < 1 || body.CacheMaxAgeHours > 8760 {
		writeError(w, http.StatusBadRequest, "invalid_request", "cacheMaxAgeHours must be 1-8760")
		return
	}
	if body.SegmentDuration < 2 || body.SegmentDuration > 10 {
		writeError(w, http.StatusBadRequest, "invalid_request", "segmentDuration must be 2-10")
		return
	}

	if err := s.hlsSettingsCtrl.Update(body); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to update hls settings")
		return
	}

	writeJSON(w, http.StatusOK, s.hlsSettingsCtrl.Get())
}

func (s *Server) handleCreateTorrent(w http.ResponseWriter, r *http.Request) {
	if s.createTorrent == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "create torrent use case not configured")
		return
	}

	contentType := r.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = ""
	}

	switch mediaType {
	case "application/json":
		s.handleCreateTorrentJSON(w, r)
	case "multipart/form-data":
		s.handleCreateTorrentMultipart(w, r)
	default:
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "unsupported content type")
	}
}

type createTorrentJSON struct {
	Magnet string `json:"magnet"`
	Name   string `json:"name,omitempty"`
}

func (s *Server) handleCreateTorrentJSON(w http.ResponseWriter, r *http.Request) {
	var body createTorrentJSON
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid json")
		return
	}

	input := usecase.CreateTorrentInput{
		Source: domain.TorrentSource{Magnet: strings.TrimSpace(body.Magnet)},
		Name:   strings.TrimSpace(body.Name),
	}

	// Cap the handler execution time so we never block indefinitely.
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	record, err := s.createTorrent.Execute(ctx, input)
	if err != nil {
		writeUseCaseError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, record)
}

func (s *Server) handleCreateTorrentMultipart(w http.ResponseWriter, r *http.Request) {
	const maxMemory = 32 << 20
	if err := r.ParseMultipartForm(maxMemory); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid multipart form")
		return
	}

	file, header, err := r.FormFile("torrent")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "missing torrent file")
		return
	}
	defer file.Close()

	path, err := saveUploadedFile(file, header.Filename)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to store torrent file")
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	input := usecase.CreateTorrentInput{
		Source: domain.TorrentSource{Torrent: path},
		Name:   name,
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	record, err := s.createTorrent.Execute(ctx, input)
	if err != nil {
		writeUseCaseError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, record)
}

type torrentSummary struct {
	ID         domain.TorrentID     `json:"id"`
	Name       string               `json:"name"`
	Status     domain.TorrentStatus `json:"status"`
	Progress   float64              `json:"progress"`
	DoneBytes  int64                `json:"doneBytes"`
	TotalBytes int64                `json:"totalBytes"`
	CreatedAt  time.Time            `json:"createdAt"`
	UpdatedAt  time.Time            `json:"updatedAt"`
	Tags       []string             `json:"tags,omitempty"`
}

type torrentListSummary struct {
	Items []torrentSummary `json:"items"`
	Count int              `json:"count"`
}

type torrentListFull struct {
	Items []domain.TorrentRecord `json:"items"`
	Count int                    `json:"count"`
}

func (s *Server) handleListTorrents(w http.ResponseWriter, r *http.Request) {
	if s.repo == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "repository not configured")
		return
	}

	status, err := parseStatus(r.URL.Query().Get("status"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid status")
		return
	}

	view := strings.TrimSpace(r.URL.Query().Get("view"))
	if view == "" {
		view = "summary"
	}
	if view != "summary" && view != "full" {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid view")
		return
	}

	search := strings.TrimSpace(r.URL.Query().Get("search"))
	tags := parseCommaSeparated(r.URL.Query().Get("tags"))
	sortBy := strings.TrimSpace(r.URL.Query().Get("sortBy"))
	if sortBy == "" {
		sortBy = "updatedAt"
	}
	if !isAllowedSortBy(sortBy) {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid sortBy")
		return
	}
	sortOrder, err := parseSortOrder(r.URL.Query().Get("sortOrder"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid sortOrder")
		return
	}

	limit, err := parsePositiveInt(r.URL.Query().Get("limit"), true)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid limit")
		return
	}
	offset, err := parsePositiveInt(r.URL.Query().Get("offset"), false)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid offset")
		return
	}
	if offset < 0 {
		offset = 0
	}

	filter := domain.TorrentFilter{
		Status:    status,
		Search:    search,
		Tags:      tags,
		SortBy:    sortBy,
		SortOrder: sortOrder,
	}
	if limit > 0 {
		filter.Limit = limit
	}
	if offset > 0 {
		filter.Offset = offset
	}
	records, err := s.repo.List(r.Context(), filter)
	if err != nil {
		writeRepoError(w, err)
		return
	}

	if view == "full" {
		writeJSON(w, http.StatusOK, torrentListFull{Items: records, Count: len(records)})
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

	writeJSON(w, http.StatusOK, torrentListSummary{Items: summaries, Count: len(summaries)})
}

func (s *Server) handleTorrentByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/torrents/")
	if path == "" {
		http.NotFound(w, r)
		return
	}

	if path == "state" {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		s.handleListTorrentStates(w, r)
		return
	}

	if path == "unfocus" {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		s.handleUnfocus(w, r)
		return
	}

	parts := strings.Split(path, "/")
	if len(parts) == 2 && parts[0] == "bulk" {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		switch parts[1] {
		case "start":
			s.handleBulkStart(w, r)
		case "stop":
			s.handleBulkStop(w, r)
		case "delete":
			s.handleBulkDelete(w, r)
		default:
			http.NotFound(w, r)
		}
		return
	}

	if len(parts) == 1 {
		id := parts[0]
		if id == "" {
			http.NotFound(w, r)
			return
		}

		switch r.Method {
		case http.MethodGet:
			s.handleGetTorrent(w, r, id)
		case http.MethodDelete:
			s.handleDeleteTorrent(w, r, id)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}

	if len(parts) >= 2 {
		id := parts[0]
		action := parts[1]
		if id == "" || action == "" {
			http.NotFound(w, r)
			return
		}
		switch action {
		case "start":
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			s.handleStartTorrent(w, r, id)
		case "stop":
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			s.handleStopTorrent(w, r, id)
		case "stream":
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			s.handleStreamTorrent(w, r, id)
		case "state":
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			s.handleGetTorrentState(w, r, id)
		case "hls":
			if r.Method != http.MethodGet && r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			s.handleHLS(w, r, id, parts[2:])
		case "media":
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			s.handleMediaInfo(w, r, id, parts[2:])
		case "focus":
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			s.handleFocus(w, r, id)
		case "tags":
			if r.Method != http.MethodPut {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			s.handleUpdateTags(w, r, id)
		default:
			http.NotFound(w, r)
		}
		return
	}

	http.NotFound(w, r)
}

func (s *Server) handleGetTorrent(w http.ResponseWriter, r *http.Request, id string) {
	if s.repo == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "repository not configured")
		return
	}

	record, err := s.repo.Get(r.Context(), domain.TorrentID(id))
	if err != nil {
		writeRepoError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, record)
}

func (s *Server) handleStartTorrent(w http.ResponseWriter, r *http.Request, id string) {
	if s.startTorrent == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "start torrent use case not configured")
		return
	}

	record, err := s.startTorrent.Execute(r.Context(), domain.TorrentID(id))
	if err != nil {
		writeDomainError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, record)
}

func (s *Server) handleStopTorrent(w http.ResponseWriter, r *http.Request, id string) {
	if s.stopTorrent == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "stop torrent use case not configured")
		return
	}

	record, err := s.stopTorrent.Execute(r.Context(), domain.TorrentID(id))
	if err != nil {
		writeDomainError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, record)
}

func (s *Server) handleDeleteTorrent(w http.ResponseWriter, r *http.Request, id string) {
	if s.deleteTorrent == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "delete torrent use case not configured")
		return
	}

	deleteFiles, err := parseBoolQuery(r.URL.Query().Get("deleteFiles"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid deleteFiles value")
		return
	}

	if err := s.deleteTorrent.Execute(r.Context(), domain.TorrentID(id), deleteFiles); err != nil {
		writeDomainError(w, err)
		return
	}

	if s.hls != nil && s.hls.cache != nil {
		s.hls.cache.PurgeTorrent(id)
	}
	if s.hls != nil && s.hls.memBuf != nil {
		s.hls.memBuf.PurgePrefix(filepath.Join(s.hls.baseDir, id))
		s.hls.memBuf.PurgePrefix(filepath.Join(s.hls.cache.BaseDir(), id))
	}

	s.invalidateMediaProbeCache(domain.TorrentID(id))

	w.WriteHeader(http.StatusNoContent)
}

type updateTagsRequest struct {
	Tags []string `json:"tags"`
}

type bulkRequest struct {
	IDs         []string `json:"ids"`
	DeleteFiles bool     `json:"deleteFiles"`
}

type bulkResultItem struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type bulkResponse struct {
	Items []bulkResultItem `json:"items"`
}

func (s *Server) handleUpdateTags(w http.ResponseWriter, r *http.Request, id string) {
	if s.repo == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "repository not configured")
		return
	}

	var body updateTagsRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid json")
		return
	}

	if err := s.repo.UpdateTags(r.Context(), domain.TorrentID(id), body.Tags); err != nil {
		writeRepoError(w, err)
		return
	}
	record, err := s.repo.Get(r.Context(), domain.TorrentID(id))
	if err != nil {
		writeRepoError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, record)
}

func (s *Server) handleBulkStart(w http.ResponseWriter, r *http.Request) {
	if s.startTorrent == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "start torrent use case not configured")
		return
	}
	req, ok := decodeBulkRequest(w, r)
	if !ok {
		return
	}

	results := make([]bulkResultItem, 0, len(req.IDs))
	for _, rawID := range req.IDs {
		id := strings.TrimSpace(rawID)
		if id == "" {
			results = append(results, bulkResultItem{ID: rawID, OK: false, Error: "empty id"})
			continue
		}
		if _, err := s.startTorrent.Execute(r.Context(), domain.TorrentID(id)); err != nil {
			results = append(results, bulkResultItem{ID: id, OK: false, Error: err.Error()})
			continue
		}
		results = append(results, bulkResultItem{ID: id, OK: true})
	}
	writeJSON(w, http.StatusOK, bulkResponse{Items: results})
}

func (s *Server) handleBulkStop(w http.ResponseWriter, r *http.Request) {
	if s.stopTorrent == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "stop torrent use case not configured")
		return
	}
	req, ok := decodeBulkRequest(w, r)
	if !ok {
		return
	}

	results := make([]bulkResultItem, 0, len(req.IDs))
	for _, rawID := range req.IDs {
		id := strings.TrimSpace(rawID)
		if id == "" {
			results = append(results, bulkResultItem{ID: rawID, OK: false, Error: "empty id"})
			continue
		}
		if _, err := s.stopTorrent.Execute(r.Context(), domain.TorrentID(id)); err != nil {
			results = append(results, bulkResultItem{ID: id, OK: false, Error: err.Error()})
			continue
		}
		results = append(results, bulkResultItem{ID: id, OK: true})
	}
	writeJSON(w, http.StatusOK, bulkResponse{Items: results})
}

func (s *Server) handleBulkDelete(w http.ResponseWriter, r *http.Request) {
	if s.deleteTorrent == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "delete torrent use case not configured")
		return
	}
	req, ok := decodeBulkRequest(w, r)
	if !ok {
		return
	}

	results := make([]bulkResultItem, 0, len(req.IDs))
	for _, rawID := range req.IDs {
		id := strings.TrimSpace(rawID)
		if id == "" {
			results = append(results, bulkResultItem{ID: rawID, OK: false, Error: "empty id"})
			continue
		}
		if err := s.deleteTorrent.Execute(r.Context(), domain.TorrentID(id), req.DeleteFiles); err != nil {
			results = append(results, bulkResultItem{ID: id, OK: false, Error: err.Error()})
			continue
		}
		if s.hls != nil && s.hls.cache != nil {
			s.hls.cache.PurgeTorrent(id)
		}
		if s.hls != nil && s.hls.memBuf != nil {
			s.hls.memBuf.PurgePrefix(filepath.Join(s.hls.baseDir, id))
			s.hls.memBuf.PurgePrefix(filepath.Join(s.hls.cache.BaseDir(), id))
		}
		s.invalidateMediaProbeCache(domain.TorrentID(id))
		results = append(results, bulkResultItem{ID: id, OK: true})
	}
	writeJSON(w, http.StatusOK, bulkResponse{Items: results})
}

func decodeBulkRequest(w http.ResponseWriter, r *http.Request) (bulkRequest, bool) {
	var req bulkRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid json")
		return bulkRequest{}, false
	}
	if len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "ids is required")
		return bulkRequest{}, false
	}
	return req, true
}

func (s *Server) handleStreamTorrent(w http.ResponseWriter, r *http.Request, id string) {
	if s.streamTorrent == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "stream torrent use case not configured")
		return
	}
	if err := s.ensureStreamingAllowed(r.Context(), domain.TorrentID(id)); err != nil {
		writeDomainError(w, err)
		return
	}

	fileIndex, err := parsePositiveInt(r.URL.Query().Get("fileIndex"), false)
	if err != nil || fileIndex < 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid fileIndex")
		return
	}

	ctx := r.Context()

	result, err := s.streamTorrent.Execute(ctx, domain.TorrentID(id), fileIndex)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if result.Reader == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "stream reader not available")
		return
	}
	defer result.Reader.Close()

	ext := strings.ToLower(path.Ext(result.File.Path))
	contentType := mime.TypeByExtension(ext)
	if contentType == "" {
		contentType = fallbackContentType(ext)
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Accept-Ranges", "bytes")

	size := result.File.Length
	rangeHeader := r.Header.Get("Range")
	if rangeHeader != "" {
		start, end, err := parseByteRange(rangeHeader, size)
		if errors.Is(err, errInvalidRange) {
			writeError(w, http.StatusBadRequest, "invalid_request", "invalid range")
			return
		}
		if errors.Is(err, errRangeNotSatisfiable) {
			if size >= 0 {
				w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", size))
			}
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}

		if _, err := result.Reader.Seek(start, io.SeekStart); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to seek stream")
			return
		}
		length := end - start + 1
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
		w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = io.CopyN(w, result.Reader, length)
		return
	}

	if size >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, result.Reader)
}

func (s *Server) handleHLS(w http.ResponseWriter, r *http.Request, id string, tail []string) {
	if s.hls == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "hls not configured")
		return
	}
	if err := s.ensureStreamingAllowed(r.Context(), domain.TorrentID(id)); err != nil {
		writeDomainError(w, err)
		return
	}

	if len(tail) < 2 {
		http.NotFound(w, r)
		return
	}

	fileIndex, err := strconv.Atoi(tail[0])
	if err != nil || fileIndex < 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid fileIndex")
		return
	}

	audioTrack, err := parseOptionalIntQuery(r.URL.Query().Get("audioTrack"), 0)
	if err != nil || audioTrack < 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid audioTrack")
		return
	}
	subtitleTrack, err := parseOptionalIntQuery(r.URL.Query().Get("subtitleTrack"), -1)
	if err != nil || subtitleTrack < -1 {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid subtitleTrack")
		return
	}

	segmentName := path.Join(tail[1:]...)
	key := hlsKey{
		id:            domain.TorrentID(id),
		fileIndex:     fileIndex,
		audioTrack:    audioTrack,
		subtitleTrack: subtitleTrack,
	}

	// Handle seek request: POST /torrents/{id}/hls/{fileIndex}/seek
	if segmentName == "seek" {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		s.handleHLSSeek(w, r, domain.TorrentID(id), fileIndex, audioTrack, subtitleTrack)
		return
	}

	job, err := s.hls.ensureJob(domain.TorrentID(id), fileIndex, audioTrack, subtitleTrack)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to start hls")
		return
	}

	if segmentName == "index.m3u8" {
		select {
		case <-job.ready:
		case <-time.After(30 * time.Second):
			writeError(w, http.StatusServiceUnavailable, "stream_unavailable", "hls playlist not ready")
			return
		}

		if job.err != nil {
			if restarted, ok := s.hls.tryAutoRestart(key, job, "request_error"); ok && restarted != nil {
				job = restarted
				select {
				case <-job.ready:
				case <-time.After(30 * time.Second):
					writeError(w, http.StatusServiceUnavailable, "stream_unavailable", "hls playlist not ready after auto-restart")
					return
				}
			}
		}

		// When subtitle source is unavailable, fall back to transcoding
		// without subtitles instead of failing the entire request.
		if job.err != nil && errors.Is(job.err, errSubtitleSourceUnavailable) && subtitleTrack >= 0 {
			s.logger.Warn("hls subtitle source unavailable, falling back to no subtitles",
				slog.String("torrentId", id),
				slog.Int("fileIndex", fileIndex),
				slog.Int("requestedSubtitleTrack", subtitleTrack),
			)
			fallbackKey := hlsKey{
				id:            domain.TorrentID(id),
				fileIndex:     fileIndex,
				audioTrack:    audioTrack,
				subtitleTrack: -1,
			}
			key = fallbackKey
			subtitleTrack = -1
			job, err = s.hls.ensureJob(domain.TorrentID(id), fileIndex, audioTrack, -1)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "internal_error", "failed to start hls without subtitles")
				return
			}
			select {
			case <-job.ready:
			case <-time.After(30 * time.Second):
				writeError(w, http.StatusServiceUnavailable, "stream_unavailable", "hls playlist not ready (subtitle fallback)")
				return
			}
		}

		if job.err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", job.err.Error())
			return
		}

		// For multi-variant jobs, job.playlist points to master.m3u8;
		// for single-variant it points to index.m3u8. Both are rewritten
		// with query params so the client forwards track selection.
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")

		if cached := cachedRewrittenPlaylist(job, job.playlist, audioTrack, subtitleTrack); cached != nil {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(cached)
			return
		}

		playlistBytes, err := os.ReadFile(job.playlist)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "playlist unavailable")
			return
		}
		playlistBytes = rewritePlaylistSegmentURLs(playlistBytes, audioTrack, subtitleTrack)
		storeRewrittenPlaylist(job, job.playlist, audioTrack, subtitleTrack, playlistBytes)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(playlistBytes)
		return
	}

	// Variant playlist request (e.g. v0/index.m3u8, v1/index.m3u8).
	if strings.HasSuffix(segmentName, ".m3u8") {
		variantPath, pathErr := safeSegmentPath(job.dir, segmentName)
		if pathErr != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "invalid segment path")
			return
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")

		if cached := cachedRewrittenPlaylist(job, variantPath, audioTrack, subtitleTrack); cached != nil {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(cached)
			return
		}

		playlistBytes, readErr := os.ReadFile(variantPath)
		if readErr != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "variant playlist unavailable")
			return
		}
		playlistBytes = rewritePlaylistSegmentURLs(playlistBytes, audioTrack, subtitleTrack)
		storeRewrittenPlaylist(job, variantPath, audioTrack, subtitleTrack, playlistBytes)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(playlistBytes)
		return
	}

	// Extract variant prefix for cache lookups (e.g. "v0" from "v0/seg-00001.ts").
	variant := ""
	if job.multiVariant {
		if idx := strings.IndexByte(segmentName, '/'); idx > 0 && segmentName[0] == 'v' {
			variant = segmentName[:idx]
		}
	}

	segmentPath, err := safeSegmentPath(job.dir, segmentName)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid segment path")
		return
	}

	// 1. Try in-memory buffer (zero disk I/O).
	if s.hls.memBuf != nil {
		if data, ok := s.hls.memBuf.Get(segmentPath); ok {
			w.Header().Set("Content-Type", "video/MP2T")
			w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
			http.ServeContent(w, r, segmentName, time.Time{}, bytes.NewReader(data))
			return
		}
	}

	// 2. Try serving from job working directory.
	if _, err := os.Stat(segmentPath); err != nil {
		// Segment not in job dir — try the HLS cache.
		if os.IsNotExist(err) && s.hls != nil && s.hls.cache != nil {
			if timeSec, ok := segmentTimeOffset(job, segmentName); ok {
				if cached, found := s.hls.cache.Lookup(string(id), fileIndex, audioTrack, subtitleTrack, variant, timeSec); found {
					// 3a. Check memBuf under cache path.
					if s.hls.memBuf != nil {
						if data, memOk := s.hls.memBuf.Get(cached.Path); memOk {
							w.Header().Set("Content-Type", "video/MP2T")
							w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
							http.ServeContent(w, r, segmentName, time.Time{}, bytes.NewReader(data))
							return
						}
					}
					// 3b. Serve from disk cache, async promote to memBuf.
					w.Header().Set("Content-Type", "video/MP2T")
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
					http.ServeFile(w, r, cached.Path)
					if s.hls.memBuf != nil {
						go func(p string) {
							if raw, readErr := os.ReadFile(p); readErr == nil {
								s.hls.memBuf.Put(p, raw)
							}
						}(cached.Path)
					}
					return
				}
			}
			http.NotFound(w, r)
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "segment unavailable")
		return
	}

	w.Header().Set("Content-Type", "video/MP2T")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	http.ServeFile(w, r, segmentPath)
	// Async promote to memBuf so subsequent requests (re-watch, multi-client) hit RAM.
	if s.hls.memBuf != nil {
		go func(p string) {
			if raw, readErr := os.ReadFile(p); readErr == nil {
				s.hls.memBuf.Put(p, raw)
			}
		}(segmentPath)
	}
}

func (s *Server) handleHLSSeek(w http.ResponseWriter, r *http.Request, id domain.TorrentID, fileIndex, audioTrack, subtitleTrack int) {
	timeStr := r.URL.Query().Get("time")
	if timeStr == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "time parameter is required")
		return
	}
	seekTime, err := strconv.ParseFloat(timeStr, 64)
	if err != nil || seekTime < 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid time parameter")
		return
	}

	job, err := s.hls.seekJob(id, fileIndex, audioTrack, subtitleTrack, seekTime)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to start hls seek")
		return
	}

	// Wait briefly for the job to become ready so we can detect early
	// errors (e.g. subtitle source unavailable). If FFmpeg is still
	// starting after the short wait, return success anyway — HLS.js on
	// the client side will poll the manifest with its built-in retry
	// logic.  This avoids the previous 30s blocking wait which caused
	// the frontend to retry the seek endpoint, killing the in-progress
	// FFmpeg job and restarting it from scratch each time.
	select {
	case <-job.ready:
	case <-time.After(5 * time.Second):
		// Job still starting — return success; client will poll manifest.
		writeJSON(w, http.StatusOK, map[string]float64{"seekTime": seekTime})
		return
	}

	// When subtitle source is unavailable during seek, fall back to
	// transcoding without subtitles instead of failing entirely.
	if job.err != nil && errors.Is(job.err, errSubtitleSourceUnavailable) && subtitleTrack >= 0 {
		s.logger.Warn("hls seek subtitle source unavailable, falling back to no subtitles",
			slog.String("torrentId", string(id)),
			slog.Int("fileIndex", fileIndex),
			slog.Int("requestedSubtitleTrack", subtitleTrack),
			slog.Float64("seekTime", seekTime),
		)
		job, err = s.hls.seekJob(id, fileIndex, audioTrack, -1, seekTime)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to start hls seek without subtitles")
			return
		}
		select {
		case <-job.ready:
		case <-time.After(5 * time.Second):
			writeJSON(w, http.StatusOK, map[string]float64{"seekTime": seekTime})
			return
		}
	}

	if job.err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", job.err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]float64{"seekTime": seekTime})
}

func (s *Server) handleMediaInfo(w http.ResponseWriter, r *http.Request, id string, tail []string) {
	const mediaProbeTimeout = 5 * time.Second

	if len(tail) != 1 {
		http.NotFound(w, r)
		return
	}
	if s.repo == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "repository not configured")
		return
	}

	fileIndex, err := strconv.Atoi(tail[0])
	if err != nil || fileIndex < 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid fileIndex")
		return
	}

	record, err := s.repo.Get(r.Context(), domain.TorrentID(id))
	if err != nil {
		writeRepoError(w, err)
		return
	}

	// Do not fail the request when probing is unavailable or file is incomplete.
	// UI can still proceed with default playback mode.
	if s.mediaProbe == nil || s.mediaDataDir == "" {
		writeJSON(w, http.StatusOK, domain.MediaInfo{Tracks: []domain.MediaTrack{}})
		return
	}

	// Check in-memory probe cache first.
	cacheKey := mediaProbeCacheKey{torrentID: domain.TorrentID(id), fileIndex: fileIndex}
	if cached, ok := s.lookupMediaProbeCache(cacheKey); ok {
		// SubtitlesReady is dynamic (depends on file existence on disk), so
		// recompute it even on cache hit.
		if fileIndex < len(record.Files) && record.Files[fileIndex].Path != "" && s.mediaDataDir != "" {
			if resolved, resolveErr := resolveDataFilePath(s.mediaDataDir, record.Files[fileIndex].Path); resolveErr == nil {
				if info, statErr := os.Stat(resolved); statErr == nil && !info.IsDir() {
					cached.SubtitlesReady = true
				}
			}
		}
		writeJSON(w, http.StatusOK, cached)
		return
	}

	filePathRel := ""
	if fileIndex < len(record.Files) {
		filePathRel = record.Files[fileIndex].Path
	}

	// Records created before metadata availability can have empty Files.
	// Fallback to active stream session to resolve selected file path.
	if filePathRel == "" && s.streamTorrent != nil {
		result, streamErr := s.streamTorrent.Execute(r.Context(), domain.TorrentID(id), fileIndex)
		if streamErr != nil {
			writeDomainError(w, streamErr)
			return
		}
		if result.Reader != nil {
			_ = result.Reader.Close()
		}
		filePathRel = result.File.Path
	}

	if filePathRel == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid fileIndex")
		return
	}

	bestInfo := domain.MediaInfo{Tracks: []domain.MediaTrack{}}

	if filePathRel != "" {
		filePath, pathErr := resolveDataFilePath(s.mediaDataDir, filePathRel)
		if pathErr == nil {
			probeCtx, probeCancel := context.WithTimeout(r.Context(), mediaProbeTimeout)
			info, probeErr := s.mediaProbe.Probe(probeCtx, filePath)
			probeCancel()
			if probeErr != nil {
				if s.logger != nil {
					s.logger.Warn("media probe by path failed",
						slog.String("torrentId", id),
						slog.Int("fileIndex", fileIndex),
						slog.String("filePath", filePath),
						slog.String("error", probeErr.Error()),
					)
				}
			} else {
				bestInfo = info
			}
		}
	}

	// For partially downloaded MKV files, path-based ffprobe may expose only
	// a subset of streams. Probe from stream reader and keep richer result.
	if s.streamTorrent != nil && len(bestInfo.Tracks) <= 1 {
		streamCtx, streamCancel := context.WithTimeout(r.Context(), mediaProbeTimeout)
		result, streamErr := s.streamTorrent.Execute(streamCtx, domain.TorrentID(id), fileIndex)
		streamCancel()
		if streamErr == nil {
			if result.Reader != nil {
				probeReaderCtx, probeReaderCancel := context.WithTimeout(r.Context(), mediaProbeTimeout)
				streamInfo, probeReaderErr := s.mediaProbe.ProbeReader(probeReaderCtx, result.Reader)
				probeReaderCancel()
				_ = result.Reader.Close()
				if probeReaderErr != nil {
					if s.logger != nil {
						s.logger.Warn("media probe by stream failed",
							slog.String("torrentId", id),
							slog.Int("fileIndex", fileIndex),
							slog.String("error", probeReaderErr.Error()),
						)
					}
				} else if len(streamInfo.Tracks) > len(bestInfo.Tracks) {
					bestInfo = streamInfo
				}
			}
		} else if s.logger != nil {
			s.logger.Warn("media stream fallback failed",
				slog.String("torrentId", id),
				slog.Int("fileIndex", fileIndex),
				slog.String("error", streamErr.Error()),
			)
		}
	}

	// Subtitles require the file to exist on disk for ffmpeg -vf subtitles.
	if filePathRel != "" && s.mediaDataDir != "" {
		if resolved, err := resolveDataFilePath(s.mediaDataDir, filePathRel); err == nil {
			if info, statErr := os.Stat(resolved); statErr == nil && !info.IsDir() {
				bestInfo.SubtitlesReady = true
			}
		}
	}

	// Cache the probe result (without SubtitlesReady, which is recomputed on hit).
	// Only cache results with more than 1 track — partially downloaded files may
	// expose incomplete track lists that would become stale once more data arrives.
	if len(bestInfo.Tracks) > 1 {
		cachedInfo := bestInfo
		cachedInfo.SubtitlesReady = false
		s.storeMediaProbeCache(cacheKey, cachedInfo)
	}

	writeJSON(w, http.StatusOK, bestInfo)
}

// lookupMediaProbeCache returns a cached MediaInfo if present and not expired.
func (s *Server) lookupMediaProbeCache(key mediaProbeCacheKey) (domain.MediaInfo, bool) {
	s.mediaCacheMu.RLock()
	entry, ok := s.mediaProbeCache[key]
	s.mediaCacheMu.RUnlock()
	if !ok {
		return domain.MediaInfo{}, false
	}
	if time.Now().After(entry.expiresAt) {
		// Expired — remove lazily.
		s.mediaCacheMu.Lock()
		if existing, stillThere := s.mediaProbeCache[key]; stillThere && time.Now().After(existing.expiresAt) {
			delete(s.mediaProbeCache, key)
		}
		s.mediaCacheMu.Unlock()
		return domain.MediaInfo{}, false
	}
	return entry.info, true
}

// storeMediaProbeCache stores a MediaInfo result in the cache with TTL.
func (s *Server) storeMediaProbeCache(key mediaProbeCacheKey, info domain.MediaInfo) {
	s.mediaCacheMu.Lock()
	s.mediaProbeCache[key] = mediaProbeCacheEntry{
		info:      info,
		expiresAt: time.Now().Add(mediaProbeCacheTTL),
	}
	s.mediaCacheMu.Unlock()
}

// invalidateMediaProbeCache removes all cached probe entries for the given torrent ID.
func (s *Server) invalidateMediaProbeCache(id domain.TorrentID) {
	s.mediaCacheMu.Lock()
	for key := range s.mediaProbeCache {
		if key.torrentID == id {
			delete(s.mediaProbeCache, key)
		}
	}
	s.mediaCacheMu.Unlock()
}

type torrentStateList struct {
	Items []domain.SessionState `json:"items"`
	Count int                   `json:"count"`
}

type playerHealthResponse struct {
	Status           string            `json:"status"`
	CheckedAt        time.Time         `json:"checkedAt"`
	CurrentTorrentID string            `json:"currentTorrentId,omitempty"`
	FocusModeEnabled bool              `json:"focusModeEnabled"`
	ActiveSessions   int               `json:"activeSessions"`
	ActiveSessionIDs []string          `json:"activeSessionIds,omitempty"`
	HLS              hlsHealthSnapshot `json:"hls"`
	Issues           []string          `json:"issues,omitempty"`
}

func (s *Server) handleGetTorrentState(w http.ResponseWriter, r *http.Request, id string) {
	if s.getState == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "torrent state use case not configured")
		return
	}

	state, err := s.getState.Execute(r.Context(), domain.TorrentID(id))
	if err != nil {
		writeDomainError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, state)
}

func (s *Server) handleListTorrentStates(w http.ResponseWriter, r *http.Request) {
	if s.listStates == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "torrent state list use case not configured")
		return
	}

	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status == "" || status != string(domain.TorrentActive) {
		writeError(w, http.StatusBadRequest, "invalid_request", "status must be active")
		return
	}

	states, err := s.listStates.Execute(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	writeJSON(w, http.StatusOK, torrentStateList{Items: states, Count: len(states)})
}

// Focus/unfocus handlers.

func (s *Server) handleFocus(w http.ResponseWriter, r *http.Request, id string) {
	if s.player != nil {
		if err := s.setCurrentTorrentID(r.Context(), domain.TorrentID(id)); err != nil {
			writeDomainError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if s.engine == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "engine not configured")
		return
	}
	if err := s.engine.FocusSession(r.Context(), domain.TorrentID(id)); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUnfocus(w http.ResponseWriter, r *http.Request) {
	if s.player != nil {
		if err := s.setCurrentTorrentID(r.Context(), ""); err != nil {
			writeDomainError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if s.engine == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "engine not configured")
		return
	}
	if err := s.engine.UnfocusAll(r.Context()); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) setCurrentTorrentID(ctx context.Context, id domain.TorrentID) error {
	if s.player == nil {
		return domain.ErrUnsupported
	}
	if err := s.player.SetCurrentTorrentID(id); err != nil {
		if id == "" || !errors.Is(err, domain.ErrNotFound) || s.startTorrent == nil {
			return err
		}
		if _, startErr := s.startTorrent.Execute(ctx, id); startErr != nil {
			return startErr
		}
		return s.player.SetCurrentTorrentID(id)
	}
	return nil
}

func (s *Server) ensureStreamingAllowed(ctx context.Context, id domain.TorrentID) error {
	if id == "" {
		return domain.ErrNotFound
	}
	return nil
}

// Watch history handlers.

func (s *Server) handleWatchHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.watchHistory == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "watch history not configured")
		return
	}

	limit, err := parsePositiveInt(r.URL.Query().Get("limit"), true)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid limit")
		return
	}
	if limit <= 0 {
		limit = 20
	}

	positions, err := s.watchHistory.ListRecent(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list watch history")
		return
	}

	writeJSON(w, http.StatusOK, positions)
}

func (s *Server) handleWatchHistoryByID(w http.ResponseWriter, r *http.Request) {
	if s.watchHistory == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "watch history not configured")
		return
	}

	tail := strings.TrimPrefix(r.URL.Path, "/watch-history/")
	parts := strings.SplitN(tail, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		http.NotFound(w, r)
		return
	}

	torrentID := domain.TorrentID(parts[0])
	fileIndex, err := strconv.Atoi(parts[1])
	if err != nil || fileIndex < 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid fileIndex")
		return
	}

	switch r.Method {
	case http.MethodGet:
		pos, err := s.watchHistory.Get(r.Context(), torrentID, fileIndex)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				writeError(w, http.StatusNotFound, "not_found", "no watch position found")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to get watch position")
			return
		}
		writeJSON(w, http.StatusOK, pos)

	case http.MethodPut:
		var body struct {
			Position    float64 `json:"position"`
			Duration    float64 `json:"duration"`
			TorrentName string  `json:"torrentName"`
			FilePath    string  `json:"filePath"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "invalid json")
			return
		}

		wp := domain.WatchPosition{
			TorrentID:   torrentID,
			FileIndex:   fileIndex,
			Position:    body.Position,
			Duration:    body.Duration,
			TorrentName: body.TorrentName,
			FilePath:    body.FilePath,
		}
		if err := s.watchHistory.Upsert(r.Context(), wp); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to save watch position")
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile(s.openAPIPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "openapi not available")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleSwagger(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, swaggerHTML)
}

type errorEnvelope struct {
	Error errorPayload `json:"error"`
}

type errorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeUseCaseError(w http.ResponseWriter, err error) {
	if errors.Is(err, usecase.ErrInvalidSource) {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid torrent source")
		return
	}
	if errors.Is(err, usecase.ErrInvalidFileIndex) {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid fileIndex")
		return
	}
	if errors.Is(err, usecase.ErrRepository) {
		writeError(w, http.StatusInternalServerError, "repository_error", err.Error())
		return
	}
	if errors.Is(err, usecase.ErrEngine) {
		writeError(w, http.StatusInternalServerError, "engine_error", err.Error())
		return
	}

	writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}

func writeRepoError(w http.ResponseWriter, err error) {
	if errors.Is(err, domain.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "torrent not found")
		return
	}

	writeError(w, http.StatusInternalServerError, "repository_error", err.Error())
}

func writeDomainError(w http.ResponseWriter, err error) {
	if errors.Is(err, domain.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "torrent not found")
		return
	}
	if errors.Is(err, usecase.ErrInvalidFileIndex) {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid fileIndex")
		return
	}
	if errors.Is(err, usecase.ErrRepository) {
		writeError(w, http.StatusInternalServerError, "repository_error", err.Error())
		return
	}
	if errors.Is(err, usecase.ErrEngine) {
		writeError(w, http.StatusInternalServerError, "engine_error", err.Error())
		return
	}

	writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorEnvelope{Error: errorPayload{Code: code, Message: message}})
}

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func saveUploadedFile(src io.Reader, filename string) (string, error) {
	base := strings.TrimSpace(filename)
	if base == "" {
		base = "torrent"
	}
	base = strings.ReplaceAll(base, string(os.PathSeparator), "_")
	ext := filepath.Ext(base)
	prefix := strings.TrimSuffix(base, ext)
	pattern := prefix + "-*" + ext

	out, err := os.CreateTemp(os.TempDir(), pattern)
	if err != nil {
		return "", err
	}
	defer out.Close()

	if _, err := io.Copy(out, src); err != nil {
		return "", err
	}

	return out.Name(), nil
}

func resolveDataFilePath(dataDir, filePath string) (string, error) {
	base := strings.TrimSpace(dataDir)
	if base == "" {
		return "", errors.New("data dir is required")
	}
	base = filepath.Clean(base)
	if abs, err := filepath.Abs(base); err == nil {
		base = abs
	}

	joined := filepath.Join(base, filepath.FromSlash(filePath))
	joined = filepath.Clean(joined)
	if abs, err := filepath.Abs(joined); err == nil {
		joined = abs
	}

	if joined != base && !strings.HasPrefix(joined, base+string(filepath.Separator)) {
		return "", errors.New("path escapes data dir")
	}
	return joined, nil
}

func rewritePlaylistSegmentURLs(playlist []byte, audioTrack, subtitleTrack int) []byte {
	values := url.Values{}
	values.Set("audioTrack", strconv.Itoa(audioTrack))
	if subtitleTrack >= 0 {
		values.Set("subtitleTrack", strconv.Itoa(subtitleTrack))
	}
	query := values.Encode()
	if query == "" {
		return playlist
	}

	lines := strings.Split(string(playlist), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(line, "?") {
			lines[i] = line + "&" + query
		} else {
			lines[i] = line + "?" + query
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

// cachedRewrittenPlaylist returns the cached rewritten playlist if the
// source file hasn't changed and track parameters match. Returns nil on miss.
func cachedRewrittenPlaylist(job *hlsJob, playlistPath string, audioTrack, subtitleTrack int) []byte {
	job.rewrittenMu.RLock()
	defer job.rewrittenMu.RUnlock()

	if job.rewrittenPlaylist == nil ||
		job.rewrittenPlaylistPath != playlistPath ||
		job.rewrittenAudioTrack != audioTrack ||
		job.rewrittenSubTrack != subtitleTrack {
		return nil
	}
	// Check if the underlying playlist file changed since we cached it.
	info, err := os.Stat(playlistPath)
	if err != nil || info.ModTime().After(job.rewrittenPlaylistMod) {
		return nil
	}
	return job.rewrittenPlaylist
}

// storeRewrittenPlaylist caches the rewritten playlist bytes for future requests.
func storeRewrittenPlaylist(job *hlsJob, playlistPath string, audioTrack, subtitleTrack int, data []byte) {
	mod := time.Now()
	if info, err := os.Stat(playlistPath); err == nil {
		mod = info.ModTime()
	}

	job.rewrittenMu.Lock()
	job.rewrittenPlaylist = data
	job.rewrittenPlaylistPath = playlistPath
	job.rewrittenPlaylistMod = mod
	job.rewrittenAudioTrack = audioTrack
	job.rewrittenSubTrack = subtitleTrack
	job.rewrittenMu.Unlock()
}

func parseStatus(value string) (*domain.TorrentStatus, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "all" {
		return nil, nil
	}
	switch value {
	case string(domain.TorrentActive), string(domain.TorrentCompleted), string(domain.TorrentStopped):
		status := domain.TorrentStatus(value)
		return &status, nil
	default:
		return nil, errors.New("invalid status")
	}
}

func parsePositiveInt(value string, requirePositive bool) (int, error) {
	if strings.TrimSpace(value) == "" {
		return -1, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, err
	}
	if requirePositive && parsed <= 0 {
		return 0, errors.New("must be > 0")
	}
	if !requirePositive && parsed < 0 {
		return 0, errors.New("must be >= 0")
	}
	return parsed, nil
}

func parseSortOrder(value string) (domain.SortOrder, error) {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" {
		return domain.SortDesc, nil
	}
	switch domain.SortOrder(trimmed) {
	case domain.SortAsc:
		return domain.SortAsc, nil
	case domain.SortDesc:
		return domain.SortDesc, nil
	default:
		return "", errors.New("invalid sort order")
	}
}

func isAllowedSortBy(value string) bool {
	switch value {
	case "name", "createdAt", "updatedAt", "totalBytes", "progress":
		return true
	default:
		return false
	}
}

func parseCommaSeparated(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func applyLimitOffset(records []domain.TorrentRecord, limit, offset int) []domain.TorrentRecord {
	if offset > 0 {
		if offset >= len(records) {
			return []domain.TorrentRecord{}
		}
		records = records[offset:]
	}
	if limit > 0 && limit < len(records) {
		records = records[:limit]
	}
	return records
}

func progressRatio(done, total int64) float64 {
	if total <= 0 {
		return 0
	}
	progress := float64(done) / float64(total)
	if progress < 0 {
		return 0
	}
	if progress > 1 {
		return 1
	}
	return progress
}

func parseBoolQuery(value string) (bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return false, nil
	}
	switch strings.ToLower(value) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, errors.New("invalid bool")
	}
}

func parseOptionalIntQuery(value string, defaultValue int) (int, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return defaultValue, nil
	}
	parsed, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, err
	}
	return parsed, nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

var (
	errInvalidRange        = errors.New("invalid range")
	errRangeNotSatisfiable = errors.New("range not satisfiable")
)

func parseByteRange(value string, size int64) (int64, int64, error) {
	if size <= 0 {
		return 0, 0, errRangeNotSatisfiable
	}

	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	if !strings.HasPrefix(lower, "bytes=") {
		return 0, 0, errInvalidRange
	}

	spec := strings.TrimSpace(value[len("bytes="):])
	if spec == "" || strings.Contains(spec, ",") {
		return 0, 0, errInvalidRange
	}

	parts := strings.SplitN(spec, "-", 2)
	if len(parts) == 1 {
		parts = append(parts, "")
	}
	if len(parts) != 2 {
		return 0, 0, errInvalidRange
	}

	startStr := strings.TrimSpace(parts[0])
	endStr := strings.TrimSpace(parts[1])

	if startStr == "" {
		if endStr == "" {
			return 0, 0, errInvalidRange
		}
		suffix, err := strconv.ParseInt(endStr, 10, 64)
		if err != nil || suffix <= 0 {
			return 0, 0, errInvalidRange
		}
		if suffix > size {
			suffix = size
		}
		start := size - suffix
		end := size - 1
		return start, end, nil
	}

	start, err := strconv.ParseInt(startStr, 10, 64)
	if err != nil || start < 0 {
		return 0, 0, errInvalidRange
	}

	if start >= size {
		return 0, 0, errRangeNotSatisfiable
	}

	if endStr == "" {
		return start, size - 1, nil
	}

	end, err := strconv.ParseInt(endStr, 10, 64)
	if err != nil || end < 0 {
		return 0, 0, errInvalidRange
	}
	if end < start {
		return 0, 0, errInvalidRange
	}
	if end >= size {
		end = size - 1
	}
	return start, end, nil
}

func fallbackContentType(ext string) string {
	switch ext {
	case ".mp4":
		return "video/mp4"
	case ".mkv":
		return "video/x-matroska"
	case ".webm":
		return "video/webm"
	case ".avi":
		return "video/x-msvideo"
	case ".mov":
		return "video/quicktime"
	case ".m4v":
		return "video/x-m4v"
	case ".mp3":
		return "audio/mpeg"
	case ".flac":
		return "audio/flac"
	case ".ogg":
		return "audio/ogg"
	default:
		return "application/octet-stream"
	}
}
