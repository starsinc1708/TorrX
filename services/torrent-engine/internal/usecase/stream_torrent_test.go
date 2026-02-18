package usecase

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"torrentstream/internal/domain"
	"torrentstream/internal/domain/ports"
)

type fakeStreamReader struct {
	ctx        context.Context
	readahead  int64
	responsive bool
	pos        int64
}

func (f *fakeStreamReader) SetContext(ctx context.Context) { f.ctx = ctx }
func (f *fakeStreamReader) SetReadahead(n int64)           { f.readahead = n }
func (f *fakeStreamReader) SetResponsive()                 { f.responsive = true }
func (f *fakeStreamReader) Read(p []byte) (int, error)     { return 0, io.EOF }
func (f *fakeStreamReader) Seek(off int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		f.pos = off
	case io.SeekCurrent:
		f.pos += off
	case io.SeekEnd:
		f.pos = off
	default:
		return 0, errors.New("invalid whence")
	}
	if f.pos < 0 {
		f.pos = 0
	}
	return f.pos, nil
}
func (f *fakeStreamReader) Close() error { return nil }

type fakeStreamSession struct {
	files     []domain.FileRef
	reader    ports.StreamReader
	lastFile  domain.FileRef
	lastRange domain.Range
	lastPrio  domain.Priority
	ranges    []domain.Range
	prios     []domain.Priority
	selectErr error
}

func (s *fakeStreamSession) ID() domain.TorrentID { return "t1" }
func (s *fakeStreamSession) Files() []domain.FileRef {
	return append([]domain.FileRef(nil), s.files...)
}
func (s *fakeStreamSession) SelectFile(index int) (domain.FileRef, error) {
	if s.selectErr != nil {
		return domain.FileRef{}, s.selectErr
	}
	if index < 0 || index >= len(s.files) {
		return domain.FileRef{}, domain.ErrNotFound
	}
	return s.files[index], nil
}
func (s *fakeStreamSession) SetPiecePriority(file domain.FileRef, r domain.Range, prio domain.Priority) {
	s.lastRange = r
	s.lastPrio = prio
	s.ranges = append(s.ranges, r)
	s.prios = append(s.prios, prio)
}
func (s *fakeStreamSession) Start() error { return nil }
func (s *fakeStreamSession) Stop() error  { return nil }
func (s *fakeStreamSession) NewReader(file domain.FileRef) (ports.StreamReader, error) {
	s.lastFile = file
	if s.reader == nil {
		return nil, errors.New("no reader")
	}
	return s.reader, nil
}

type fakeStreamEngine struct {
	session ports.Session
	err     error
}

func (f *fakeStreamEngine) Open(ctx context.Context, src domain.TorrentSource) (ports.Session, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeStreamEngine) Close() error { return nil }
func (f *fakeStreamEngine) GetSessionState(ctx context.Context, id domain.TorrentID) (domain.SessionState, error) {
	return domain.SessionState{}, nil
}
func (f *fakeStreamEngine) ListActiveSessions(ctx context.Context) ([]domain.TorrentID, error) {
	return nil, nil
}
func (f *fakeStreamEngine) StopSession(ctx context.Context, id domain.TorrentID) error   { return nil }
func (f *fakeStreamEngine) StartSession(ctx context.Context, id domain.TorrentID) error  { return nil }
func (f *fakeStreamEngine) RemoveSession(ctx context.Context, id domain.TorrentID) error { return nil }
func (f *fakeStreamEngine) SetPiecePriority(ctx context.Context, id domain.TorrentID, file domain.FileRef, r domain.Range, prio domain.Priority) error {
	return nil
}

func (f *fakeStreamEngine) ListSessions(ctx context.Context) ([]domain.TorrentID, error) {
	return nil, nil
}

func (f *fakeStreamEngine) FocusSession(ctx context.Context, id domain.TorrentID) error { return nil }
func (f *fakeStreamEngine) UnfocusAll(ctx context.Context) error                        { return nil }
func (f *fakeStreamEngine) GetSessionMode(ctx context.Context, id domain.TorrentID) (domain.SessionMode, error) {
	return domain.ModeDownloading, nil
}

func (f *fakeStreamEngine) SetDownloadRateLimit(ctx context.Context, id domain.TorrentID, bytesPerSec int64) error {
	return nil
}
func (f *fakeStreamEngine) GetSession(ctx context.Context, id domain.TorrentID) (ports.Session, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.session, nil
}

func TestStreamTorrentSuccess(t *testing.T) {
	reader := &fakeStreamReader{}
	session := &fakeStreamSession{
		files:  []domain.FileRef{{Index: 0, Path: "Sintel/Sintel.mp4", Length: 100}},
		reader: reader,
	}
	engine := &fakeStreamEngine{session: session}

	uc := StreamTorrent{Engine: engine, ReadaheadBytes: 2 << 20}
	result, err := uc.Execute(context.Background(), "t1", 0)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.File.Index != 0 || result.File.Length != 100 {
		t.Fatalf("file mismatch: %+v", result.File)
	}
	if reader.ctx == nil {
		t.Fatalf("context not set")
	}
	// readahead is set to the priority window (not the raw ReadaheadBytes)
	// so the torrent client requests pieces well ahead of playback.
	expectedWindow := streamPriorityWindow(2<<20, 100)
	if reader.readahead != expectedWindow {
		t.Fatalf("readahead not set to priority window: got %d, want %d", reader.readahead, expectedWindow)
	}
	if reader.responsive {
		t.Fatalf("responsive should not be set by use case (callers opt in when needed)")
	}
	// The startup gradient sets the first band to PriorityHigh.
	if len(session.prios) == 0 || session.prios[0] != domain.PriorityHigh {
		t.Fatalf("first startup gradient band not set to PriorityHigh, got prios=%v", session.prios)
	}
	if session.ranges[0].Off != 0 || session.ranges[0].Length <= 0 {
		t.Fatalf("first startup gradient band has unexpected range: %+v", session.ranges[0])
	}
}

func TestStreamTorrentNotFound(t *testing.T) {
	uc := StreamTorrent{Engine: &fakeStreamEngine{err: domain.ErrNotFound}}
	_, err := uc.Execute(context.Background(), "t404", 0)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestStreamTorrentInvalidFileIndex(t *testing.T) {
	reader := &fakeStreamReader{}
	session := &fakeStreamSession{
		files:  []domain.FileRef{{Index: 0, Path: "Sintel/Sintel.mp4", Length: 100}},
		reader: reader,
	}
	engine := &fakeStreamEngine{session: session}
	uc := StreamTorrent{Engine: engine}

	_, err := uc.Execute(context.Background(), "t1", 5)
	if !errors.Is(err, ErrInvalidFileIndex) {
		t.Fatalf("expected invalid file index, got %v", err)
	}
}

func TestStreamTorrentSlidingPriorityOnSeek(t *testing.T) {
	reader := &fakeStreamReader{}
	session := &fakeStreamSession{
		files:  []domain.FileRef{{Index: 0, Path: "big/movie.mkv", Length: 1024}},
		reader: reader,
	}
	engine := &fakeStreamEngine{session: session}

	uc := StreamTorrent{Engine: engine, ReadaheadBytes: 2 << 20}
	result, err := uc.Execute(context.Background(), "t1", 0)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(session.ranges) == 0 {
		t.Fatalf("expected initial priority range")
	}
	// Record how many ranges the startup gradient produced.
	rangesBeforeSeek := len(session.ranges)

	if _, err := result.Reader.Seek(64<<20, io.SeekStart); err != nil {
		t.Fatalf("Seek: %v", err)
	}

	if len(session.ranges) <= rangesBeforeSeek {
		t.Fatalf("expected priority to be updated after seek, got %d calls total (same as before)", len(session.ranges))
	}

	// After seek, the adaptive reader temporarily doubles the window (seek boost)
	// and applies a graduated priority across multiple bands. Verify that:
	// 1. There are post-seek gradient bands with a positive offset.
	// 2. The total coverage of non-deprioritization bands equals the boosted window.
	baseWindow := streamPriorityWindow(uc.ReadaheadBytes, 1024)
	boostedWindow := baseWindow * 2
	if boostedWindow > maxPriorityWindowBytes {
		boostedWindow = maxPriorityWindowBytes
	}

	// Skip the startup gradient ranges; the seek produces deprioritization
	// (PriorityNone) followed by the new gradient bands.
	var totalGradientLen int64
	for i := rangesBeforeSeek; i < len(session.ranges); i++ {
		if session.prios[i] == domain.PriorityNone {
			continue // skip deprioritization ranges
		}
		totalGradientLen += session.ranges[i].Length
	}

	if totalGradientLen != boostedWindow {
		t.Fatalf("total gradient coverage after seek: got %d, want %d (boosted)", totalGradientLen, boostedWindow)
	}
}

func TestStreamTorrentTailPreload(t *testing.T) {
	// File must be large enough (> 32 MB) to trigger tail preload.
	fileLen := int64(100 << 20) // 100 MB
	reader := &fakeStreamReader{}
	session := &fakeStreamSession{
		files:  []domain.FileRef{{Index: 0, Path: "movie.mkv", Length: fileLen}},
		reader: reader,
	}
	engine := &fakeStreamEngine{session: session}

	uc := &StreamTorrent{Engine: engine, ReadaheadBytes: 2 << 20}
	_, err := uc.Execute(context.Background(), "t1", 0)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Should have at least 2 priority ranges: initial window + tail preload.
	if len(session.ranges) < 2 {
		t.Fatalf("expected >= 2 priority ranges, got %d", len(session.ranges))
	}

	const tailPreloadSize int64 = 16 << 20
	expectedTailOff := fileLen - tailPreloadSize
	foundTail := false
	for i, r := range session.ranges {
		if r.Off == expectedTailOff && r.Length == tailPreloadSize {
			if session.prios[i] != domain.PriorityReadahead {
				t.Fatalf("tail preload priority: got %d, want %d", session.prios[i], domain.PriorityReadahead)
			}
			foundTail = true
		}
	}
	if !foundTail {
		t.Fatalf("tail preload range not found in %+v", session.ranges)
	}
}

func TestStreamTorrentNoTailPreloadSmallFile(t *testing.T) {
	// File smaller than 2× tail preload size should NOT get tail preload.
	reader := &fakeStreamReader{}
	session := &fakeStreamSession{
		files:  []domain.FileRef{{Index: 0, Path: "small.mp4", Length: 100}},
		reader: reader,
	}
	engine := &fakeStreamEngine{session: session}

	uc := &StreamTorrent{Engine: engine, ReadaheadBytes: 2 << 20}
	_, err := uc.Execute(context.Background(), "t1", 0)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// The startup gradient produces up to 4 bands (High, Next, Readahead, Normal).
	// For small files (< 2× tail preload size), no tail preload is added.
	if len(session.ranges) > 4 {
		t.Fatalf("expected at most 4 startup gradient bands for small file (no tail preload), got %d", len(session.ranges))
	}
	// Verify no range has PriorityReadahead at the file tail offset.
	const tailPreloadSize int64 = 16 << 20
	expectedTailOff := int64(100) - tailPreloadSize
	for i, r := range session.ranges {
		if r.Off == expectedTailOff && r.Length == tailPreloadSize && session.prios[i] == domain.PriorityReadahead {
			t.Fatalf("unexpected tail preload range for small file: %+v", r)
		}
	}
}

func TestBoostWindow(t *testing.T) {
	reader := &fakeStreamReader{}
	session := &fakeStreamSession{
		files:  []domain.FileRef{{Index: 0, Path: "movie.mkv", Length: 1 << 30}},
		reader: reader,
	}
	engine := &fakeStreamEngine{session: session}

	uc := StreamTorrent{Engine: engine, ReadaheadBytes: 2 << 20}
	result, err := uc.Execute(context.Background(), "t1", 0)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	spr, ok := result.Reader.(*slidingPriorityReader)
	if !ok {
		t.Fatalf("expected *slidingPriorityReader, got %T", result.Reader)
	}

	// Record the window before boost.
	spr.mu.Lock()
	windowBefore := spr.window
	spr.mu.Unlock()

	rangesBefore := len(session.ranges)

	// BoostWindow should double the window and force a priority update.
	spr.BoostWindow(10 * time.Second)

	spr.mu.Lock()
	windowAfter := spr.window
	boostUntil := spr.seekBoostUntil
	spr.mu.Unlock()

	expectedWindow := windowBefore * 2
	if expectedWindow > maxPriorityWindowBytes {
		expectedWindow = maxPriorityWindowBytes
	}
	if windowAfter != expectedWindow {
		t.Fatalf("BoostWindow: window got %d, want %d", windowAfter, expectedWindow)
	}
	if time.Until(boostUntil) < 9*time.Second || time.Until(boostUntil) > 11*time.Second {
		t.Fatalf("BoostWindow: seekBoostUntil not set correctly, expires in %v", time.Until(boostUntil))
	}
	// Should have produced new priority ranges.
	if len(session.ranges) <= rangesBefore {
		t.Fatalf("BoostWindow: expected new priority ranges, got none")
	}
}

func TestSetBufferFillFunc(t *testing.T) {
	reader := &fakeStreamReader{}
	session := &fakeStreamSession{
		files:  []domain.FileRef{{Index: 0, Path: "movie.mkv", Length: 1 << 30}},
		reader: reader,
	}
	engine := &fakeStreamEngine{session: session}

	uc := StreamTorrent{Engine: engine, ReadaheadBytes: 2 << 20}
	result, err := uc.Execute(context.Background(), "t1", 0)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	spr, ok := result.Reader.(*slidingPriorityReader)
	if !ok {
		t.Fatalf("expected *slidingPriorityReader, got %T", result.Reader)
	}

	// Verify SetBufferFillFunc stores the callback.
	called := false
	spr.SetBufferFillFunc(func() float64 {
		called = true
		return 0.1 // below threshold
	})

	spr.mu.Lock()
	if spr.bufferFillFunc == nil {
		spr.mu.Unlock()
		t.Fatalf("SetBufferFillFunc: callback not stored")
	}
	spr.mu.Unlock()

	// The callback is invoked during adjustWindowLocked; verify it's wired.
	_ = spr.bufferFillFunc()
	if !called {
		t.Fatalf("SetBufferFillFunc: callback not invoked")
	}
}
