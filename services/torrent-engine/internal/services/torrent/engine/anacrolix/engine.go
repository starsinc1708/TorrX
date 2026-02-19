package anacrolix

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"runtime/debug"
	"sync"
	"time"

	"github.com/anacrolix/torrent"

	"torrentstream/internal/domain"
	"torrentstream/internal/domain/ports"
)

var ErrSessionNotFound = domain.ErrNotFound

// defaultMaxConns is the value restored when resuming a hard-paused torrent.
// PRD specifies 35 to balance peer connections vs resource usage.
const defaultMaxConns = 35

// ErrSessionLimitReached is returned when the maximum number of sessions is
// reached and no idle session can be evicted.
var ErrSessionLimitReached = errors.New("session limit reached")

type Config struct {
	DataDir     string
	MaxSessions int           // 0 = unlimited
	IdleTimeout time.Duration // auto-stop sessions idle longer than this; 0 = disabled
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
	peakCompleted  map[domain.TorrentID]int64  // high-water mark for BytesCompleted per torrent
	peakBitfield   map[domain.TorrentID][]byte // high-water mark for piece completion bitfield
	lastAccess     map[domain.TorrentID]time.Time // LRU tracking for session eviction
	rateLimits     map[domain.TorrentID]int64 // per-torrent download rate limit (bytes/sec); 0 = unlimited
	maxSessions    int
	idleTimeout    time.Duration
	reaperCancel   context.CancelFunc
}

func New(cfg Config) (*Engine, error) {
	clientConfig := torrent.NewDefaultClientConfig()
	if cfg.DataDir != "" {
		clientConfig.DataDir = cfg.DataDir
	}

	client, err := torrent.NewClient(clientConfig)
	if err != nil {
		return nil, err
	}

	e := &Engine{
		client:        client,
		sessions:      make(map[domain.TorrentID]*torrent.Torrent),
		modes:         make(map[domain.TorrentID]domain.SessionMode),
		speeds:        make(map[domain.TorrentID]speedSample),
		focusedPieces: make(map[domain.TorrentID]focusedPieceRange),
		peakCompleted: make(map[domain.TorrentID]int64),
		peakBitfield:  make(map[domain.TorrentID][]byte),
		lastAccess:    make(map[domain.TorrentID]time.Time),
		rateLimits:    make(map[domain.TorrentID]int64),
		maxSessions:   cfg.MaxSessions,
		idleTimeout:   cfg.IdleTimeout,
	}

	if e.idleTimeout > 0 {
		ctx, cancel := context.WithCancel(context.Background())
		e.reaperCancel = cancel
		go e.idleReaper(ctx)
	}

	return e, nil
}

func NewWithClient(client *torrent.Client) *Engine {
	return &Engine{
		client:        client,
		sessions:      make(map[domain.TorrentID]*torrent.Torrent),
		modes:         make(map[domain.TorrentID]domain.SessionMode),
		speeds:        make(map[domain.TorrentID]speedSample),
		focusedPieces: make(map[domain.TorrentID]focusedPieceRange),
		peakCompleted: make(map[domain.TorrentID]int64),
		peakBitfield:  make(map[domain.TorrentID][]byte),
		lastAccess:    make(map[domain.TorrentID]time.Time),
		rateLimits:    make(map[domain.TorrentID]int64),
	}
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
//
// All file priorities are reset to None so that previous DownloadAll() effects
// are cleared: only the sliding priority reader will set pieces to high priority
// as FFmpeg reads them. When the session returns to Downloading mode,
// resumeTorrent() → DownloadAll() re-enables all pieces.
func (e *Engine) resumeTorrentForStreaming(t *torrent.Torrent) {
	if t == nil {
		return
	}
	t.SetMaxEstablishedConns(defaultMaxConns)
	t.AllowDataUpload()
	t.AllowDataDownload()
	// Reset all piece priorities to None so other files in the torrent stop
	// consuming bandwidth while streaming. The sliding priority reader sets
	// only the needed pieces to high priority as they are demanded by FFmpeg.
	if torrentInfoReady(t) {
		for _, f := range t.Files() {
			f.SetPriority(torrent.PiecePriorityNone)
		}
	}
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
		// The goroutine may still complete AddMagnet after we return.
		// Spawn a cleanup goroutine to drop the orphaned torrent.
		go func() {
			if res := <-ch; res.t != nil {
				res.t.Drop()
			}
		}()
		return nil, errors.New("torrent client busy, try again later")
	case <-ctx.Done():
		go func() {
			if res := <-ch; res.t != nil {
				res.t.Drop()
			}
		}()
		return nil, ctx.Err()
	}

	id := domain.TorrentID(t.InfoHash().HexString())

	// If this torrent is already tracked, return the existing session.
	e.mu.Lock()
	if _, exists := e.sessions[id]; exists {
		e.lastAccess[id] = time.Now().UTC()
		e.mu.Unlock()
		ready := torrentInfoReady(t)
		var files []domain.FileRef
		if ready {
			files = mapFiles(t)
		}
		return &Session{engine: e, torrent: t, id: id, files: files, ready: ready}, nil
	}

	// Enforce session limit: evict the least-recently-used idle session.
	var evictedTorrent *torrent.Torrent
	var evictedID domain.TorrentID
	if e.maxSessions > 0 && len(e.sessions) >= e.maxSessions {
		et, eid, err := e.evictIdleSessionLocked()
		if err != nil {
			e.mu.Unlock()
			t.Drop()
			return nil, ErrSessionLimitReached
		}
		evictedTorrent = et
		evictedID = eid
	}

	e.sessions[id] = t
	e.modes[id] = domain.ModeIdle
	e.lastAccess[id] = time.Now().UTC()
	e.mu.Unlock()

	// Drop evicted torrent synchronously outside the lock to avoid
	// a race between Drop and the new session registration.
	if evictedTorrent != nil {
		e.forgetFocusedPieces(evictedID)
		e.forgetSpeed(evictedID)
		evictedTorrent.Drop()
	}

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
			delete(e.peakCompleted, id)
			delete(e.peakBitfield, id)
			delete(e.lastAccess, id)
			delete(e.rateLimits, id)
		}
		e.mu.Unlock()
		e.forgetSpeed(id)
		e.forgetFocusedPieces(id)
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
	if e.reaperCancel != nil {
		e.reaperCancel()
	}
	if e.client == nil {
		return nil
	}
	errList := e.client.Close()
	if len(errList) > 0 {
		return errList[0]
	}
	return nil
}

// idleReaper periodically scans sessions and stops those that have been idle
// (no reader access) longer than idleTimeout. Focused sessions are never
// reaped. This prevents resource accumulation from abandoned sessions.
func (e *Engine) idleReaper(ctx context.Context) {
	interval := e.idleTimeout / 2
	if interval < 10*time.Second {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.reapIdleSessions()
		}
	}
}

func (e *Engine) reapIdleSessions() {
	now := time.Now().UTC()

	e.mu.RLock()
	var candidates []domain.TorrentID
	for id := range e.sessions {
		mode := e.modes[id]
		// Never reap focused, stopped, or completed sessions.
		if mode == domain.ModeFocused || mode == domain.ModeStopped || mode == domain.ModeCompleted {
			continue
		}
		accessed := e.lastAccess[id]
		if !accessed.IsZero() && now.Sub(accessed) > e.idleTimeout {
			candidates = append(candidates, id)
		}
	}
	e.mu.RUnlock()

	for _, id := range candidates {
		slog.Info("reaping idle session",
			slog.String("torrentId", string(id)),
			slog.Duration("idleTimeout", e.idleTimeout),
		)
		_ = e.StopSession(context.Background(), id)
	}
}

func (e *Engine) GetSessionState(ctx context.Context, id domain.TorrentID) (domain.SessionState, error) {
	t := e.getTorrent(id)
	if t == nil {
		return domain.SessionState{}, ErrSessionNotFound
	}

	e.touchLastAccess(id)

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

	// Maintain high-water mark: after restart anacrolix re-verifies pieces
	// from disk and BytesCompleted() can temporarily be lower than peak.
	e.mu.Lock()
	if completed > e.peakCompleted[id] {
		e.peakCompleted[id] = completed
	} else {
		completed = e.peakCompleted[id]
	}
	e.mu.Unlock()

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

	// Apply piece bitfield high-water mark: once a piece is confirmed complete,
	// keep it marked complete even during post-restart re-verification (while
	// anacrolix is rehashing pieces from disk and PieceState.Complete temporarily
	// returns false for pieces that are actually on disk).
	if numPieces > 0 && bitfield != "" {
		if raw, err := base64.StdEncoding.DecodeString(bitfield); err == nil {
			e.mu.Lock()
			peak := e.peakBitfield[id]
			if len(peak) < len(raw) {
				extended := make([]byte, len(raw))
				copy(extended, peak)
				peak = extended
			}
			for i, b := range raw {
				peak[i] |= b
			}
			e.peakBitfield[id] = peak
			bitfield = base64.StdEncoding.EncodeToString(peak)
			e.mu.Unlock()
		}
	}

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
	e.touchLastAccess(id)
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

	wasFocused := e.modes[id] == domain.ModeFocused

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

	// If the stopped torrent was focused, resume all paused torrents so
	// they don't stay permanently stuck with 0 connections.
	if wasFocused {
		for sid, st := range e.sessions {
			if sid == id {
				continue
			}
			if e.modes[sid] == domain.ModePaused {
				if err := e.transition(sid, domain.ModeDownloading); err == nil {
					e.resumeTorrent(st)
				}
			}
		}
	}
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

	e.lastAccess[id] = time.Now().UTC()

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

func (e *Engine) SetDownloadRateLimit(ctx context.Context, id domain.TorrentID, bytesPerSec int64) error {
	t := e.getTorrent(id)
	if t == nil {
		return ErrSessionNotFound
	}

	e.mu.Lock()
	prev := e.rateLimits[id]
	if bytesPerSec <= 0 {
		delete(e.rateLimits, id)
	} else {
		e.rateLimits[id] = bytesPerSec
	}
	e.mu.Unlock()

	if prev != bytesPerSec {
		slog.Info("download rate limit changed",
			slog.String("torrentId", string(id)),
			slog.Int64("prevBytesPerSec", prev),
			slog.Int64("newBytesPerSec", bytesPerSec),
		)
	}
	return nil
}

// GetDownloadRateLimit returns the current download rate limit for a torrent
// in bytes/sec. Returns 0 if no limit is set.
func (e *Engine) GetDownloadRateLimit(id domain.TorrentID) int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.rateLimits[id]
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
	delete(e.peakCompleted, id)
	delete(e.peakBitfield, id)
	delete(e.lastAccess, id)
	delete(e.rateLimits, id)
	wasFocused := e.focusedID == id
	if wasFocused {
		e.focusedID = ""
	}

	// If the dropped torrent was focused, resume all paused torrents so
	// they don't stay permanently stuck with 0 connections.
	if wasFocused {
		for sid, st := range e.sessions {
			if e.modes[sid] == domain.ModePaused {
				if err := e.transition(sid, domain.ModeDownloading); err == nil {
					e.resumeTorrent(st)
				}
			}
		}
	}
	e.mu.Unlock()
	e.forgetFocusedPieces(id)
	e.forgetSpeed(id)
	if t != nil {
		t.Drop()
	}
	// Return memory to the OS promptly after dropping a torrent session.
	// Without this, Go's GC may hold freed memory for a long time, which
	// causes OOM on memory-constrained systems (Docker, NAS).
	freeOSMemory()
	return nil
}

// freeOSMemory triggers garbage collection and returns freed memory to the OS.
// Called after session cleanup to prevent memory accumulation.
func freeOSMemory() {
	runtime.GC()
	debug.FreeOSMemory()
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
		if r := recover(); r != nil {
			slog.Error("mapFiles panic recovered",
				slog.Any("error", r),
				slog.String("stack", string(debug.Stack())),
			)
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

// touchLastAccess updates the last-access timestamp for the given session.
func (e *Engine) touchLastAccess(id domain.TorrentID) {
	e.mu.Lock()
	if _, ok := e.sessions[id]; ok {
		e.lastAccess[id] = time.Now().UTC()
	}
	e.mu.Unlock()
}

// evictIdleSessionLocked removes the least-recently-used idle session to make
// room for a new one. Caller must hold e.mu write lock. Sessions in Focused
// mode are never evicted.
func (e *Engine) evictIdleSessionLocked() (*torrent.Torrent, domain.TorrentID, error) {
	var evictID domain.TorrentID
	var evictTime time.Time
	found := false

	for id := range e.sessions {
		mode := e.modes[id]
		if mode == domain.ModeFocused {
			continue // never evict the focused session
		}
		// Prefer idle/stopped sessions over active ones.
		isIdle := mode == domain.ModeIdle || mode == domain.ModeStopped || mode == domain.ModeCompleted || mode == domain.ModePaused
		if !isIdle {
			continue
		}
		accessed := e.lastAccess[id]
		if !found || accessed.Before(evictTime) {
			evictID = id
			evictTime = accessed
			found = true
		}
	}

	if !found {
		return nil, "", ErrSessionLimitReached
	}

	t := e.sessions[evictID]
	delete(e.sessions, evictID)
	delete(e.modes, evictID)
	delete(e.peakCompleted, evictID)
	delete(e.peakBitfield, evictID)
	delete(e.lastAccess, evictID)
	delete(e.rateLimits, evictID)
	if e.focusedID == evictID {
		e.focusedID = ""
	}

	// Return the evicted torrent so the caller can Drop() it
	// synchronously outside the lock (avoids a race condition).
	return t, evictID, nil
}

