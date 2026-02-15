package anacrolix

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/storage"

	"torrentstream/internal/domain"
	"torrentstream/internal/domain/ports"
	"torrentstream/internal/storage/memory"
)

var ErrSessionNotFound = domain.ErrNotFound

// defaultMaxConns is the value restored when resuming a hard-paused torrent.
// Anacrolix default is 55.
const defaultMaxConns = 55

type Config struct {
	DataDir          string
	StorageMode      string
	MemoryLimitBytes int64
	MemorySpillDir   string
}

type Engine struct {
	client         *torrent.Client
	sessions       map[domain.TorrentID]*torrent.Torrent
	modes          map[domain.TorrentID]domain.SessionMode
	mu             sync.RWMutex
	speedMu        sync.Mutex
	priorityMu     sync.Mutex
	speeds         map[domain.TorrentID]speedSample
	focusedPieces  map[domain.TorrentID]focusedPieceRange
	focusedID      domain.TorrentID // cached; always consistent with modes
	storageMode    string
	memoryProvider *memory.Provider
}

func New(cfg Config) (*Engine, error) {
	clientConfig := torrent.NewDefaultClientConfig()
	mode := strings.ToLower(strings.TrimSpace(cfg.StorageMode))
	if mode == "" {
		mode = "disk"
	}

	var provider *memory.Provider
	switch mode {
	case "disk":
		if cfg.DataDir != "" {
			clientConfig.DataDir = cfg.DataDir
		}
	case "memory", "hybrid":
		spillDir := resolveSpillDir(cfg.DataDir, cfg.MemorySpillDir)
		provider = memory.NewProvider(
			memory.WithMaxBytes(cfg.MemoryLimitBytes),
			memory.WithSpillDir(spillDir),
		)
		clientConfig.DefaultStorage = storage.NewResourcePieces(provider)
	default:
		return nil, fmt.Errorf("unsupported storage mode: %s", cfg.StorageMode)
	}

	client, err := torrent.NewClient(clientConfig)
	if err != nil {
		return nil, err
	}

	return &Engine{
		client:         client,
		sessions:       make(map[domain.TorrentID]*torrent.Torrent),
		modes:          make(map[domain.TorrentID]domain.SessionMode),
		speeds:         make(map[domain.TorrentID]speedSample),
		storageMode:    mode,
		memoryProvider: provider,
	}, nil
}

func NewWithClient(client *torrent.Client) *Engine {
	return &Engine{
		client:      client,
		sessions:    make(map[domain.TorrentID]*torrent.Torrent),
		modes:       make(map[domain.TorrentID]domain.SessionMode),
		speeds:      make(map[domain.TorrentID]speedSample),
		storageMode: "disk",
	}
}

func (e *Engine) StorageMode() string {
	if e == nil || e.storageMode == "" {
		return "disk"
	}
	return e.storageMode
}

func (e *Engine) MemoryLimitBytes() int64 {
	if e == nil || e.memoryProvider == nil {
		return 0
	}
	return e.memoryProvider.MaxBytes()
}

func (e *Engine) SpillToDisk() bool {
	if e == nil || e.memoryProvider == nil {
		return false
	}
	return e.memoryProvider.SpillToDisk()
}

func (e *Engine) SetMemoryLimitBytes(limit int64) error {
	if e == nil || e.memoryProvider == nil {
		return domain.ErrUnsupported
	}
	e.memoryProvider.SetMaxBytes(limit)
	return nil
}

// ---------------------------------------------------------------------------
// State machine
// ---------------------------------------------------------------------------

// transition validates and applies a state change. Caller must hold e.mu write lock.
func (e *Engine) transition(id domain.TorrentID, to domain.SessionMode) error {
	current, ok := e.modes[id]
	if !ok {
		return ErrSessionNotFound
	}
	if current == to {
		return nil // no-op
	}
	if !domain.CanTransition(current, to) {
		return fmt.Errorf("%w: %s -> %s for session %s", domain.ErrInvalidTransition, current, to, id)
	}
	e.modes[id] = to

	// Maintain focusedID cache.
	if to == domain.ModeFocused {
		e.focusedID = id
	} else if current == domain.ModeFocused {
		e.focusedID = ""
	}
	return nil
}

// ---------------------------------------------------------------------------
// Hard pause / resume (scheduler)
// ---------------------------------------------------------------------------

// hardPauseTorrent prevents all network activity for a torrent by disallowing
// data transfer and setting max connections to 0, which disconnects all peers.
func (e *Engine) hardPauseTorrent(t *torrent.Torrent) {
	if t == nil {
		return
	}
	t.DisallowDataDownload()
	t.DisallowDataUpload()
	t.SetMaxEstablishedConns(0)
}

// resumeTorrent re-enables data transfer and peer connections, and starts
// downloading all pieces. Use for normal Start/Resume operations.
func (e *Engine) resumeTorrent(t *torrent.Torrent) {
	if t == nil {
		return
	}
	t.SetMaxEstablishedConns(defaultMaxConns)
	t.AllowDataUpload()
	t.AllowDataDownload()
	if torrentInfoReady(t) {
		t.DownloadAll()
	}
}

// resumeTorrentForStreaming re-enables data transfer and peer connections but
// does NOT call DownloadAll(). This ensures bandwidth is used exclusively for
// pieces demanded by the reader's readahead window rather than being spread
// across the entire torrent.
func (e *Engine) resumeTorrentForStreaming(t *torrent.Torrent) {
	if t == nil {
		return
	}
	t.SetMaxEstablishedConns(defaultMaxConns)
	t.AllowDataUpload()
	t.AllowDataDownload()
}

// ---------------------------------------------------------------------------
// Session lifecycle
// ---------------------------------------------------------------------------

// addMagnetTimeout caps the time we wait for the anacrolix client to accept
// a magnet link. AddMagnet can block on an internal client mutex when the
// client is busy (e.g. resolving metadata for another torrent).
const (
	addMagnetTimeout     = 10 * time.Second
	metadataWaitTimeout  = 10 * time.Minute // Max time to wait for torrent metadata (zero-peer torrents timeout after this)
)

func (e *Engine) Open(ctx context.Context, src domain.TorrentSource) (ports.Session, error) {
	if e.client == nil {
		return nil, errors.New("torrent client not configured")
	}

	// Run AddMagnet / AddTorrentFromFile with a timeout so we never block
	// the HTTP handler indefinitely if the anacrolix client is busy.
	type addResult struct {
		t   *torrent.Torrent
		err error
	}
	ch := make(chan addResult, 1)
	go func() {
		var t *torrent.Torrent
		var err error
		if src.Magnet != "" {
			t, err = e.client.AddMagnet(src.Magnet)
		} else {
			t, err = e.client.AddTorrentFromFile(src.Torrent)
		}
		ch <- addResult{t, err}
	}()

	var t *torrent.Torrent
	select {
	case res := <-ch:
		if res.err != nil {
			return nil, res.err
		}
		t = res.t
	case <-time.After(addMagnetTimeout):
		return nil, errors.New("torrent client busy, try again later")
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	id := domain.TorrentID(t.InfoHash().HexString())

	// If this torrent is already tracked, return the existing session.
	e.mu.Lock()
	if _, exists := e.sessions[id]; exists {
		e.mu.Unlock()
		ready := torrentInfoReady(t)
		var files []domain.FileRef
		if ready {
			files = mapFiles(t)
		}
		return &Session{engine: e, torrent: t, id: id, files: files, ready: ready}, nil
	}
	e.sessions[id] = t
	e.modes[id] = domain.ModeIdle
	e.mu.Unlock()

	// Try to get info with a short timeout; if not ready, return pending session.
	shortCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	select {
	case <-t.GotInfo():
		e.mu.Lock()
		e.modes[id] = domain.ModeDownloading
		e.mu.Unlock()
		files := mapFiles(t)
		return &Session{engine: e, torrent: t, id: id, files: files, ready: true}, nil
	case <-shortCtx.Done():
		go e.waitForInfo(t, id)
		return &Session{engine: e, torrent: t, id: id, files: nil, ready: false}, nil
	}
}

// waitForInfo blocks until torrent metadata is available (with timeout), then transitions
// the session to the appropriate mode based on current engine state.
// If metadata is not received within metadataWaitTimeout, the torrent is removed to prevent goroutine leaks.
func (e *Engine) waitForInfo(t *torrent.Torrent, id domain.TorrentID) {
	select {
	case <-t.GotInfo():
		// Metadata received, proceed with transition
	case <-time.After(metadataWaitTimeout):
		// Timeout: metadata not available after long wait (likely zero-peer torrent)
		e.mu.Lock()
		if _, ok := e.sessions[id]; ok {
			t.Drop() // Release torrent resources
			delete(e.sessions, id)
			delete(e.modes, id)
		}
		e.mu.Unlock()
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	mode, ok := e.modes[id]
	if !ok {
		return // session was removed
	}
	if mode == domain.ModeStopped {
		return
	}

	// If another torrent is focused, this one should be paused.
	if e.focusedID != "" && e.focusedID != id {
		if err := e.transition(id, domain.ModePaused); err == nil {
			e.hardPauseTorrent(t)
		}
		return
	}

	if err := e.transition(id, domain.ModeDownloading); err == nil {
		t.AllowDataDownload()
		t.DownloadAll()
	}
}

func (e *Engine) Close() error {
	if e.client == nil {
		return nil
	}
	errList := e.client.Close()
	if len(errList) > 0 {
		return errList[0]
	}
	return nil
}

func (e *Engine) GetSessionState(ctx context.Context, id domain.TorrentID) (domain.SessionState, error) {
	t := e.getTorrent(id)
	if t == nil {
		return domain.SessionState{}, ErrSessionNotFound
	}

	e.mu.RLock()
	mode := e.modes[id]
	e.mu.RUnlock()

	// Check if metadata is available yet.
	select {
	case <-t.GotInfo():
		// Metadata ready — proceed.
	default:
		stats := t.Stats()
		return domain.SessionState{
			ID:        id,
			Status:    domain.TorrentPending,
			Mode:      mode,
			Peers:     stats.ActivePeers,
			UpdatedAt: time.Now().UTC(),
		}, nil
	}

	length := t.Length()
	completed := t.BytesCompleted()
	progress := float64(0)
	if length > 0 {
		progress = float64(completed) / float64(length)
	}

	stats := t.Stats()

	// Derive status from mode; override to completed if fully downloaded.
	status := mode.ToStatus()
	if length > 0 && completed >= length && status == domain.TorrentActive {
		status = domain.TorrentCompleted
		// Also update the mode if not already completed.
		e.mu.Lock()
		_ = e.transition(id, domain.ModeCompleted)
		e.mu.Unlock()
	}

	downloadSpeed, uploadSpeed := e.sampleSpeed(id, stats, time.Now().UTC())

	numPieces, bitfield := pieceBitfield(t)

	// Re-read mode after possible transition to Completed.
	e.mu.RLock()
	mode = e.modes[id]
	e.mu.RUnlock()

	return domain.SessionState{
		ID:            id,
		Status:        status,
		Mode:          mode,
		Progress:      progress,
		Peers:         stats.ActivePeers,
		DownloadSpeed: downloadSpeed,
		UploadSpeed:   uploadSpeed,
		Files:         mapFiles(t),
		NumPieces:     numPieces,
		PieceBitfield: bitfield,
		UpdatedAt:     time.Now().UTC(),
	}, nil
}

func (e *Engine) GetSession(ctx context.Context, id domain.TorrentID) (ports.Session, error) {
	t := e.getTorrent(id)
	if t == nil {
		return nil, ErrSessionNotFound
	}
	ready := torrentInfoReady(t)
	return &Session{engine: e, torrent: t, id: id, files: mapFiles(t), ready: ready}, nil
}

func (e *Engine) GetSessionMode(ctx context.Context, id domain.TorrentID) (domain.SessionMode, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	mode, ok := e.modes[id]
	if !ok {
		return "", ErrSessionNotFound
	}
	return mode, nil
}

func (e *Engine) ListActiveSessions(ctx context.Context) ([]domain.TorrentID, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	ids := make([]domain.TorrentID, 0, len(e.sessions))
	for id := range e.sessions {
		mode := e.modes[id]
		if mode == domain.ModeStopped || mode == domain.ModeCompleted {
			continue
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func (e *Engine) ListSessions(ctx context.Context) ([]domain.TorrentID, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	ids := make([]domain.TorrentID, 0, len(e.sessions))
	for id := range e.sessions {
		ids = append(ids, id)
	}
	return ids, nil
}

func (e *Engine) StopSession(ctx context.Context, id domain.TorrentID) error {
	t := e.getTorrent(id)
	if t == nil {
		return ErrSessionNotFound
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.transition(id, domain.ModeStopped); err != nil {
		return err
	}

	t.DisallowDataDownload()
	if torrentInfoReady(t) {
		e.clearFocusedPieces(id, t)
	}
	// Restore max conns in case it was hard-paused before being stopped.
	t.SetMaxEstablishedConns(defaultMaxConns)
	t.AllowDataUpload()
	return nil
}

func (e *Engine) StartSession(ctx context.Context, id domain.TorrentID) error {
	t := e.getTorrent(id)
	if t == nil {
		return ErrSessionNotFound
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// Determine target mode based on whether a focused session exists.
	targetMode := domain.ModeDownloading
	if e.focusedID != "" && e.focusedID != id {
		targetMode = domain.ModePaused
	}

	if err := e.transition(id, targetMode); err != nil {
		return err
	}

	if targetMode == domain.ModePaused {
		e.hardPauseTorrent(t)
		return nil
	}

	e.resumeTorrent(t)
	return nil
}

func (e *Engine) RemoveSession(ctx context.Context, id domain.TorrentID) error {
	t := e.getTorrent(id)
	if t == nil {
		return ErrSessionNotFound
	}
	return e.dropTorrent(id, t)
}

func (e *Engine) SetPiecePriority(ctx context.Context, id domain.TorrentID, file domain.FileRef, r domain.Range, prio domain.Priority) error {
	t := e.getTorrent(id)
	if t == nil {
		return ErrSessionNotFound
	}

	e.mu.RLock()
	mode := e.modes[id]
	e.mu.RUnlock()

	// Ignore priority requests for stopped or paused torrents.
	if mode == domain.ModeStopped || mode == domain.ModePaused {
		return nil
	}

	if !torrentInfoReady(t) {
		return nil
	}
	files := t.Files()
	if file.Index < 0 || file.Index >= len(files) {
		return ErrSessionNotFound
	}
	e.applyPiecePriority(t, id, file, r, prio)
	return nil
}

func (e *Engine) FocusSession(ctx context.Context, id domain.TorrentID) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	t, ok := e.sessions[id]
	if !ok {
		return ErrSessionNotFound
	}

	// If already focused, nothing to do.
	if e.modes[id] == domain.ModeFocused {
		return nil
	}

	// Transition the target to Focused.
	if err := e.transition(id, domain.ModeFocused); err != nil {
		return err
	}

	// Pause all other non-stopped, non-completed, non-idle sessions.
	for sid, st := range e.sessions {
		if sid == id {
			continue
		}
		mode := e.modes[sid]
		if mode == domain.ModeStopped || mode == domain.ModeCompleted || mode == domain.ModeIdle {
			continue
		}
		if mode == domain.ModePaused {
			continue // already paused
		}
		// mode is Downloading or was the previous Focused (already transitioned away above).
		if err := e.transition(sid, domain.ModePaused); err == nil {
			e.hardPauseTorrent(st)
		}
	}

	// Resume the focused torrent for streaming: enable data transfer but do
	// NOT call DownloadAll() so that only reader-demanded pieces get bandwidth.
	e.resumeTorrentForStreaming(t)

	return nil
}

func (e *Engine) UnfocusAll(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Demote the focused session back to Downloading.
	if e.focusedID != "" {
		_ = e.transition(e.focusedID, domain.ModeDownloading)
	}

	// Resume all paused sessions.
	for sid, t := range e.sessions {
		if e.modes[sid] == domain.ModePaused {
			if err := e.transition(sid, domain.ModeDownloading); err == nil {
				e.resumeTorrent(t)
			}
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func (e *Engine) getTorrent(id domain.TorrentID) *torrent.Torrent {
	e.mu.RLock()
	t := e.sessions[id]
	e.mu.RUnlock()
	if t == nil {
		return nil
	}
	select {
	case <-t.Closed():
		_ = e.dropTorrent(id, t)
		return nil
	default:
		return t
	}
}

func (e *Engine) dropTorrent(id domain.TorrentID, t *torrent.Torrent) error {
	e.mu.Lock()
	delete(e.sessions, id)
	delete(e.modes, id)
	if e.focusedID == id {
		e.focusedID = ""
	}
	e.mu.Unlock()
	e.forgetFocusedPieces(id)
	e.forgetSpeed(id)
	if t != nil {
		t.Drop()
	}
	return nil
}

// pieceBitfield returns the total piece count and a base64-encoded bitfield
// where each bit represents whether the corresponding piece is complete.
func pieceBitfield(t *torrent.Torrent) (numPieces int, encoded string) {
	if !torrentInfoReady(t) {
		return 0, ""
	}
	n := t.NumPieces()
	if n <= 0 {
		return 0, ""
	}
	byteLen := (n + 7) / 8
	buf := make([]byte, byteLen)
	for i := 0; i < n; i++ {
		if t.PieceState(i).Complete {
			buf[i/8] |= 1 << (7 - uint(i%8))
		}
	}
	return n, base64.StdEncoding.EncodeToString(buf)
}

func mapFiles(t *torrent.Torrent) (mapped []domain.FileRef) {
	if !torrentInfoReady(t) {
		return nil
	}
	defer func() {
		if recover() != nil {
			mapped = nil
		}
	}()

	files := t.Files()
	mapped = make([]domain.FileRef, 0, len(files))
	for i, f := range files {
		mapped = append(mapped, domain.FileRef{
			Index:          i,
			Path:           f.Path(),
			Length:         f.Length(),
			BytesCompleted: f.BytesCompleted(),
		})
	}
	return mapped
}

func torrentInfoReady(t *torrent.Torrent) bool {
	if t == nil {
		return false
	}
	select {
	case <-t.GotInfo():
		return true
	default:
		return false
	}
}

type speedSample struct {
	at           time.Time
	bytesRead    int64
	bytesWritten int64
}

func (e *Engine) sampleSpeed(id domain.TorrentID, stats torrent.TorrentStats, now time.Time) (int64, int64) {
	currentRead := stats.BytesReadUsefulData.Int64()
	currentWritten := stats.BytesWrittenData.Int64()

	e.speedMu.Lock()
	defer e.speedMu.Unlock()

	prev, ok := e.speeds[id]
	e.speeds[id] = speedSample{
		at:           now,
		bytesRead:    currentRead,
		bytesWritten: currentWritten,
	}

	if !ok || prev.at.IsZero() {
		return 0, 0
	}

	dt := now.Sub(prev.at).Seconds()
	if dt <= 0 {
		return 0, 0
	}

	deltaRead := currentRead - prev.bytesRead
	deltaWritten := currentWritten - prev.bytesWritten
	if deltaRead < 0 {
		deltaRead = 0
	}
	if deltaWritten < 0 {
		deltaWritten = 0
	}

	download := int64(float64(deltaRead) / dt)
	upload := int64(float64(deltaWritten) / dt)
	return download, upload
}

func (e *Engine) forgetSpeed(id domain.TorrentID) {
	e.speedMu.Lock()
	delete(e.speeds, id)
	e.speedMu.Unlock()
}

func resolveSpillDir(dataDir, configured string) string {
	dir := strings.TrimSpace(configured)
	if dir != "" {
		return dir
	}
	base := strings.TrimSpace(dataDir)
	if base == "" {
		return ""
	}
	return filepath.Join(base, ".ram-spill")
}
