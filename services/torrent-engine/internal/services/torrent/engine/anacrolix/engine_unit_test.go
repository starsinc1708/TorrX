package anacrolix

import (
	"context"
	"testing"
	"time"

	"github.com/anacrolix/torrent"

	"torrentstream/internal/domain"
	"torrentstream/internal/domain/ports"
)

// ---------------------------------------------------------------------------
// defaultMaxConns
// ---------------------------------------------------------------------------

func TestDefaultMaxConnsIs35(t *testing.T) {
	if defaultMaxConns != 35 {
		t.Fatalf("defaultMaxConns = %d, want 35 (PRD spec)", defaultMaxConns)
	}
}

// ---------------------------------------------------------------------------
// mapPriority — 5-level mapping + unknown default
// ---------------------------------------------------------------------------

func TestMapPriority(t *testing.T) {
	tests := []struct {
		name string
		in   domain.Priority
		want torrent.PiecePriority
	}{
		{"None", domain.PriorityNone, torrent.PiecePriorityNone},
		{"Low", domain.PriorityLow, torrent.PiecePriorityNormal}, // Low maps to Normal (anacrolix has no Low)
		{"Normal", domain.PriorityNormal, torrent.PiecePriorityNormal},
		{"Readahead", domain.PriorityReadahead, torrent.PiecePriorityReadahead},
		{"Next", domain.PriorityNext, torrent.PiecePriorityNext},
		{"High", domain.PriorityHigh, torrent.PiecePriorityNow},
		{"UnknownFallsBackToNormal", domain.Priority(99), torrent.PiecePriorityNormal},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mapPriority(tc.in)
			if got != tc.want {
				t.Fatalf("mapPriority(%d) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// State machine: transition
// ---------------------------------------------------------------------------

func newTestEngine() *Engine {
	return &Engine{
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

func TestTransitionValid(t *testing.T) {
	tests := []struct {
		name string
		from domain.SessionMode
		to   domain.SessionMode
	}{
		{"Idle->Downloading", domain.ModeIdle, domain.ModeDownloading},
		{"Idle->Paused", domain.ModeIdle, domain.ModePaused},
		{"Idle->Stopped", domain.ModeIdle, domain.ModeStopped},
		{"Downloading->Stopped", domain.ModeDownloading, domain.ModeStopped},
		{"Downloading->Focused", domain.ModeDownloading, domain.ModeFocused},
		{"Downloading->Paused", domain.ModeDownloading, domain.ModePaused},
		{"Downloading->Completed", domain.ModeDownloading, domain.ModeCompleted},
		{"Focused->Downloading", domain.ModeFocused, domain.ModeDownloading},
		{"Focused->Stopped", domain.ModeFocused, domain.ModeStopped},
		{"Focused->Completed", domain.ModeFocused, domain.ModeCompleted},
		{"Paused->Downloading", domain.ModePaused, domain.ModeDownloading},
		{"Paused->Focused", domain.ModePaused, domain.ModeFocused},
		{"Paused->Stopped", domain.ModePaused, domain.ModeStopped},
		{"Stopped->Downloading", domain.ModeStopped, domain.ModeDownloading},
		{"Stopped->Paused", domain.ModeStopped, domain.ModePaused},
		{"Stopped->Idle", domain.ModeStopped, domain.ModeIdle},
		{"Completed->Stopped", domain.ModeCompleted, domain.ModeStopped},
		{"Completed->Focused", domain.ModeCompleted, domain.ModeFocused},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEngine()
			id := domain.TorrentID("test")
			e.modes[id] = tc.from
			e.sessions[id] = nil

			if err := e.transition(id, tc.to); err != nil {
				t.Fatalf("transition(%s->%s) unexpected error: %v", tc.from, tc.to, err)
			}
			if e.modes[id] != tc.to {
				t.Fatalf("mode = %s, want %s", e.modes[id], tc.to)
			}
		})
	}
}

func TestTransitionInvalid(t *testing.T) {
	tests := []struct {
		name string
		from domain.SessionMode
		to   domain.SessionMode
	}{
		{"Idle->Focused", domain.ModeIdle, domain.ModeFocused},
		{"Idle->Completed", domain.ModeIdle, domain.ModeCompleted},
		{"Downloading->Idle", domain.ModeDownloading, domain.ModeIdle},
		{"Focused->Paused", domain.ModeFocused, domain.ModePaused},
		{"Focused->Idle", domain.ModeFocused, domain.ModeIdle},
		{"Paused->Completed", domain.ModePaused, domain.ModeCompleted},
		{"Paused->Idle", domain.ModePaused, domain.ModeIdle},
		{"Stopped->Focused", domain.ModeStopped, domain.ModeFocused},
		{"Stopped->Completed", domain.ModeStopped, domain.ModeCompleted},
		{"Completed->Downloading", domain.ModeCompleted, domain.ModeDownloading},
		{"Completed->Paused", domain.ModeCompleted, domain.ModePaused},
		{"Completed->Idle", domain.ModeCompleted, domain.ModeIdle},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEngine()
			id := domain.TorrentID("test")
			e.modes[id] = tc.from
			e.sessions[id] = nil

			err := e.transition(id, tc.to)
			if err == nil {
				t.Fatalf("transition(%s->%s) should fail but succeeded", tc.from, tc.to)
			}
		})
	}
}

func TestTransitionSameStateIsNoop(t *testing.T) {
	e := newTestEngine()
	id := domain.TorrentID("test")
	e.modes[id] = domain.ModeDownloading
	e.sessions[id] = nil

	if err := e.transition(id, domain.ModeDownloading); err != nil {
		t.Fatalf("same-state transition should be no-op, got: %v", err)
	}
}

func TestTransitionUnknownSession(t *testing.T) {
	e := newTestEngine()
	err := e.transition("nonexistent", domain.ModeDownloading)
	if err != ErrSessionNotFound {
		t.Fatalf("expected ErrSessionNotFound, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Focus cache consistency
// ---------------------------------------------------------------------------

func TestTransitionUpdatesFocusedID(t *testing.T) {
	e := newTestEngine()
	id := domain.TorrentID("t1")
	e.modes[id] = domain.ModeDownloading
	e.sessions[id] = nil

	// Transition to Focused should set focusedID.
	if err := e.transition(id, domain.ModeFocused); err != nil {
		t.Fatal(err)
	}
	if e.focusedID != id {
		t.Fatalf("focusedID = %q, want %q", e.focusedID, id)
	}

	// Transition away from Focused should clear focusedID.
	if err := e.transition(id, domain.ModeDownloading); err != nil {
		t.Fatal(err)
	}
	if e.focusedID != "" {
		t.Fatalf("focusedID = %q, want empty", e.focusedID)
	}
}

func TestTransitionFocusSwitchUpdatesCache(t *testing.T) {
	e := newTestEngine()
	id1 := domain.TorrentID("t1")
	id2 := domain.TorrentID("t2")
	e.modes[id1] = domain.ModeDownloading
	e.modes[id2] = domain.ModeDownloading
	e.sessions[id1] = nil
	e.sessions[id2] = nil

	_ = e.transition(id1, domain.ModeFocused)
	if e.focusedID != id1 {
		t.Fatalf("focusedID = %q, want %q", e.focusedID, id1)
	}

	// Unfocus id1 first (transition to Downloading), then focus id2.
	_ = e.transition(id1, domain.ModeDownloading)
	_ = e.transition(id2, domain.ModeFocused)
	if e.focusedID != id2 {
		t.Fatalf("focusedID = %q, want %q", e.focusedID, id2)
	}
}

// ---------------------------------------------------------------------------
// Session eviction
// ---------------------------------------------------------------------------

func TestEvictIdleSessionLocked_EmptySessions(t *testing.T) {
	e := newTestEngine()

	_, _, err := e.evictIdleSessionLocked()
	if err != ErrSessionLimitReached {
		t.Fatalf("expected ErrSessionLimitReached, got: %v", err)
	}
}

func TestEvictIdleSessionLocked_EvictsLRUIdle(t *testing.T) {
	e := newTestEngine()
	now := time.Now().UTC()

	// id1 accessed 10m ago, id2 accessed 5m ago, id3 active (not evictable)
	e.sessions["id1"] = nil
	e.modes["id1"] = domain.ModeStopped
	e.lastAccess["id1"] = now.Add(-10 * time.Minute)

	e.sessions["id2"] = nil
	e.modes["id2"] = domain.ModeCompleted
	e.lastAccess["id2"] = now.Add(-5 * time.Minute)

	e.sessions["id3"] = nil
	e.modes["id3"] = domain.ModeDownloading
	e.lastAccess["id3"] = now.Add(-15 * time.Minute)

	_, evictedID, err := e.evictIdleSessionLocked()
	if err != nil {
		t.Fatal(err)
	}
	// Should evict id1 (oldest idle session; id3 is Downloading, not idle)
	if evictedID != "id1" {
		t.Fatalf("evictedID = %q, want id1", evictedID)
	}
	// Maps should be cleaned up for evicted session.
	if _, ok := e.sessions["id1"]; ok {
		t.Fatal("evicted session should be removed from sessions map")
	}
	if _, ok := e.modes["id1"]; ok {
		t.Fatal("evicted session should be removed from modes map")
	}
}

func TestEvictIdleSessionLocked_FocusedNeverEvicted(t *testing.T) {
	e := newTestEngine()
	now := time.Now().UTC()

	e.sessions["focused"] = nil
	e.modes["focused"] = domain.ModeFocused
	e.lastAccess["focused"] = now.Add(-1 * time.Hour)
	e.focusedID = "focused"

	_, _, err := e.evictIdleSessionLocked()
	if err != ErrSessionLimitReached {
		t.Fatalf("expected ErrSessionLimitReached (focused should not be evicted), got: %v", err)
	}
}

func TestEvictIdleSessionLocked_PrefersIdleOverActive(t *testing.T) {
	e := newTestEngine()
	now := time.Now().UTC()

	// Active session, old access time
	e.sessions["active"] = nil
	e.modes["active"] = domain.ModeDownloading
	e.lastAccess["active"] = now.Add(-30 * time.Minute)

	// Paused session, more recent access time
	e.sessions["paused"] = nil
	e.modes["paused"] = domain.ModePaused
	e.lastAccess["paused"] = now.Add(-5 * time.Minute)

	_, evictedID, err := e.evictIdleSessionLocked()
	if err != nil {
		t.Fatal(err)
	}
	// Should evict paused (idle) even though active has older access time.
	if evictedID != "paused" {
		t.Fatalf("evictedID = %q, want paused", evictedID)
	}
}

// ---------------------------------------------------------------------------
// Engine public API with nil client
// ---------------------------------------------------------------------------

func TestGetSessionMode_NotFound(t *testing.T) {
	e := newTestEngine()
	_, err := e.GetSessionMode(context.Background(), "missing")
	if err != ErrSessionNotFound {
		t.Fatalf("expected ErrSessionNotFound, got: %v", err)
	}
}

func TestGetSessionMode_ReturnsCorrectMode(t *testing.T) {
	e := newTestEngine()
	e.sessions["t1"] = nil
	e.modes["t1"] = domain.ModeFocused

	mode, err := e.GetSessionMode(context.Background(), "t1")
	if err != nil {
		t.Fatal(err)
	}
	if mode != domain.ModeFocused {
		t.Fatalf("mode = %s, want %s", mode, domain.ModeFocused)
	}
}

func TestListSessions_Empty(t *testing.T) {
	e := newTestEngine()
	ids, err := e.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("expected 0 sessions, got %d", len(ids))
	}
}

func TestListSessions_ReturnsAll(t *testing.T) {
	e := newTestEngine()
	e.sessions["a"] = nil
	e.modes["a"] = domain.ModeDownloading
	e.sessions["b"] = nil
	e.modes["b"] = domain.ModeStopped

	ids, err := e.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(ids))
	}
}

func TestListActiveSessions_ExcludesStoppedAndCompleted(t *testing.T) {
	e := newTestEngine()
	e.sessions["active"] = nil
	e.modes["active"] = domain.ModeDownloading
	e.sessions["stopped"] = nil
	e.modes["stopped"] = domain.ModeStopped
	e.sessions["completed"] = nil
	e.modes["completed"] = domain.ModeCompleted
	e.sessions["paused"] = nil
	e.modes["paused"] = domain.ModePaused

	ids, err := e.ListActiveSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Active and Paused should be included; Stopped and Completed excluded.
	if len(ids) != 2 {
		t.Fatalf("expected 2 active sessions, got %d: %v", len(ids), ids)
	}
	found := map[domain.TorrentID]bool{}
	for _, id := range ids {
		found[id] = true
	}
	if !found["active"] || !found["paused"] {
		t.Fatalf("expected active and paused, got %v", ids)
	}
}

func TestSetDownloadRateLimit(t *testing.T) {
	e := newTestEngine()
	e.sessions["t1"] = nil
	e.modes["t1"] = domain.ModeDownloading

	// Setting rate limit for non-existent session (getTorrent returns nil for nil torrent)
	err := e.SetDownloadRateLimit(context.Background(), "missing", 1024)
	if err != ErrSessionNotFound {
		t.Fatalf("expected ErrSessionNotFound, got: %v", err)
	}

	// GetDownloadRateLimit for tracked session
	e.rateLimits["t1"] = 5000
	if got := e.GetDownloadRateLimit("t1"); got != 5000 {
		t.Fatalf("rate limit = %d, want 5000", got)
	}
	if got := e.GetDownloadRateLimit("missing"); got != 0 {
		t.Fatalf("missing rate limit = %d, want 0", got)
	}
}

// ---------------------------------------------------------------------------
// Session struct
// ---------------------------------------------------------------------------

func TestSessionID(t *testing.T) {
	s := &Session{id: domain.TorrentID("abc123")}
	if s.ID() != "abc123" {
		t.Fatalf("ID() = %q, want abc123", s.ID())
	}
}

func TestSessionFilesReturnsDefensiveCopy(t *testing.T) {
	s := &Session{
		ready: true,
		files: []domain.FileRef{{Index: 0, Path: "test.mkv", Length: 1024}},
	}
	files := s.Files()
	if len(files) != 1 || files[0].Path != "test.mkv" {
		t.Fatalf("unexpected files: %v", files)
	}
	// Modify returned slice — should not affect Session.
	files[0].Path = "modified"
	if s.files[0].Path != "test.mkv" {
		t.Fatal("Files() should return a defensive copy")
	}
}

func TestSessionReadyNilTorrent(t *testing.T) {
	s := &Session{torrent: nil, ready: false}
	if s.Ready() {
		t.Fatal("Ready() should be false for nil torrent")
	}
}

func TestSessionSelectFileNilTorrent(t *testing.T) {
	s := &Session{torrent: nil, ready: false}
	_, err := s.SelectFile(0)
	if err != ErrSessionNotFound {
		t.Fatalf("expected ErrSessionNotFound, got: %v", err)
	}
}

func TestSessionSelectFileOutOfRange(t *testing.T) {
	s := &Session{
		ready: true,
		files: []domain.FileRef{{Index: 0, Path: "a.mkv"}},
	}
	_, err := s.SelectFile(5)
	if err != ErrSessionNotFound {
		t.Fatalf("expected ErrSessionNotFound for out-of-range index, got: %v", err)
	}
	_, err = s.SelectFile(-1)
	if err != ErrSessionNotFound {
		t.Fatalf("expected ErrSessionNotFound for negative index, got: %v", err)
	}
}

func TestSessionSelectFileValid(t *testing.T) {
	s := &Session{
		ready: true,
		files: []domain.FileRef{
			{Index: 0, Path: "a.mkv", Length: 100},
			{Index: 1, Path: "b.mp4", Length: 200},
		},
	}
	f, err := s.SelectFile(1)
	if err != nil {
		t.Fatal(err)
	}
	if f.Path != "b.mp4" || f.Length != 200 {
		t.Fatalf("unexpected file: %+v", f)
	}
}

func TestSessionNewReaderNilTorrent(t *testing.T) {
	s := &Session{torrent: nil, ready: false}
	_, err := s.NewReader(domain.FileRef{Index: 0})
	if err != ErrSessionNotFound {
		t.Fatalf("expected ErrSessionNotFound, got: %v", err)
	}
}

func TestSessionStartNilEngine(t *testing.T) {
	s := &Session{engine: nil, torrent: nil}
	err := s.Start()
	if err != ErrSessionNotFound {
		t.Fatalf("expected ErrSessionNotFound, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Interface conformance (comprehensive check)
// ---------------------------------------------------------------------------

func TestEngineImplementsPortsEngine(t *testing.T) {
	var _ ports.Engine = (*Engine)(nil)
}

func TestSessionImplementsPortsSession(t *testing.T) {
	var _ ports.Session = (*Session)(nil)
}

// ---------------------------------------------------------------------------
// Speed sampling edge cases
// ---------------------------------------------------------------------------

func TestSampleSpeedNegativeDeltaClamped(t *testing.T) {
	e := &Engine{speeds: make(map[domain.TorrentID]speedSample)}
	start := time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC)
	_, _ = e.sampleSpeed("t1", statsWithCounts(1000, 500), start)

	// Simulate counter reset (new stats < previous).
	next := start.Add(1 * time.Second)
	download, upload := e.sampleSpeed("t1", statsWithCounts(50, 20), next)
	if download != 0 {
		t.Fatalf("download = %d, want 0 (negative delta should clamp to 0)", download)
	}
	if upload != 0 {
		t.Fatalf("upload = %d, want 0 (negative delta should clamp to 0)", upload)
	}
}

// ---------------------------------------------------------------------------
// focusedPieceRange helpers
// ---------------------------------------------------------------------------

func TestStoreFocusedPiecesZeroRange(t *testing.T) {
	e := newTestEngine()
	e.storeFocusedPieces("t1", focusedPieceRange{start: 5, end: 5})

	// Zero-range should be a no-op.
	if _, ok := e.focusedPieces["t1"]; ok {
		t.Fatal("zero-range should not be stored")
	}
}

func TestStoreFocusedPiecesValidRange(t *testing.T) {
	e := newTestEngine()
	e.storeFocusedPieces("t1", focusedPieceRange{start: 0, end: 10})

	r, ok := e.focusedPieces["t1"]
	if !ok {
		t.Fatal("expected focused pieces to be stored")
	}
	if r.start != 0 || r.end != 10 {
		t.Fatalf("unexpected range: %+v", r)
	}
}

func TestForgetFocusedPiecesCleanup(t *testing.T) {
	e := newTestEngine()
	e.focusedPieces["t1"] = focusedPieceRange{start: 0, end: 10}

	e.forgetFocusedPieces("t1")
	if _, ok := e.focusedPieces["t1"]; ok {
		t.Fatal("forgetFocusedPieces should remove entry")
	}
}

func TestForgetFocusedPiecesNilMap(t *testing.T) {
	e := &Engine{}
	// Should not panic even with nil focusedPieces map.
	e.forgetFocusedPieces("t1")
}

// ---------------------------------------------------------------------------
// Close with nil client
// ---------------------------------------------------------------------------

func TestCloseNilClient(t *testing.T) {
	e := &Engine{}
	if err := e.Close(); err != nil {
		t.Fatalf("Close() with nil client should succeed, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// touchLastAccess
// ---------------------------------------------------------------------------

func TestTouchLastAccess(t *testing.T) {
	e := newTestEngine()
	e.sessions["t1"] = nil
	before := time.Now().UTC()

	e.touchLastAccess("t1")

	after := time.Now().UTC()
	accessed := e.lastAccess["t1"]
	if accessed.Before(before) || accessed.After(after) {
		t.Fatalf("touchLastAccess time %v not between %v and %v", accessed, before, after)
	}
}

func TestTouchLastAccessMissing(t *testing.T) {
	e := newTestEngine()
	// Should not panic for missing session.
	e.touchLastAccess("missing")
	if _, ok := e.lastAccess["missing"]; ok {
		t.Fatal("touchLastAccess should not create entry for missing session")
	}
}
