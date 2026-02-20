package usecase

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"torrentstream/internal/domain"
	"torrentstream/internal/domain/ports"
)

// fakeDiskEngine implements ports.Engine with configurable active sessions and modes.
type fakeDiskEngine struct {
	activeSessions []domain.TorrentID
	activeErr      error
	modes          map[domain.TorrentID]domain.SessionMode
	modeErr        error
	stopErr        error
	startErr       error

	mu          sync.Mutex
	stopCalls   []domain.TorrentID
	startCalls  []domain.TorrentID
}

func (f *fakeDiskEngine) Open(ctx context.Context, src domain.TorrentSource) (ports.Session, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeDiskEngine) Close() error { return nil }
func (f *fakeDiskEngine) GetSessionState(ctx context.Context, id domain.TorrentID) (domain.SessionState, error) {
	return domain.SessionState{}, nil
}
func (f *fakeDiskEngine) GetSession(ctx context.Context, id domain.TorrentID) (ports.Session, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeDiskEngine) ListActiveSessions(ctx context.Context) ([]domain.TorrentID, error) {
	if f.activeErr != nil {
		return nil, f.activeErr
	}
	return f.activeSessions, nil
}
func (f *fakeDiskEngine) StopSession(ctx context.Context, id domain.TorrentID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopCalls = append(f.stopCalls, id)
	return f.stopErr
}
func (f *fakeDiskEngine) StartSession(ctx context.Context, id domain.TorrentID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startCalls = append(f.startCalls, id)
	return f.startErr
}
func (f *fakeDiskEngine) RemoveSession(ctx context.Context, id domain.TorrentID) error { return nil }
func (f *fakeDiskEngine) SetPiecePriority(ctx context.Context, id domain.TorrentID, file domain.FileRef, r domain.Range, prio domain.Priority) error {
	return nil
}
func (f *fakeDiskEngine) ListSessions(ctx context.Context) ([]domain.TorrentID, error) {
	return nil, nil
}
func (f *fakeDiskEngine) FocusSession(ctx context.Context, id domain.TorrentID) error { return nil }
func (f *fakeDiskEngine) UnfocusAll(ctx context.Context) error                        { return nil }
func (f *fakeDiskEngine) GetSessionMode(ctx context.Context, id domain.TorrentID) (domain.SessionMode, error) {
	if f.modeErr != nil {
		return "", f.modeErr
	}
	if f.modes != nil {
		if m, ok := f.modes[id]; ok {
			return m, nil
		}
	}
	return domain.ModeDownloading, nil
}
func (f *fakeDiskEngine) SetDownloadRateLimit(ctx context.Context, id domain.TorrentID, bytesPerSec int64) error {
	return nil
}

// ---------- stopActiveDownloads tests ----------

func TestStopActiveDownloadsEmpty(t *testing.T) {
	engine := &fakeDiskEngine{activeSessions: nil}
	dp := DiskPressure{Engine: engine, Logger: discardLogger()}
	stopped := make(map[domain.TorrentID]struct{})

	dp.stopActiveDownloads(context.Background(), stopped)

	if len(stopped) != 0 {
		t.Fatalf("expected no stopped sessions, got %d", len(stopped))
	}
}

func TestStopActiveDownloadsListError(t *testing.T) {
	engine := &fakeDiskEngine{activeErr: errors.New("list failed")}
	dp := DiskPressure{Engine: engine, Logger: discardLogger()}
	stopped := make(map[domain.TorrentID]struct{})

	dp.stopActiveDownloads(context.Background(), stopped)

	if len(stopped) != 0 {
		t.Fatalf("expected no stopped sessions on error, got %d", len(stopped))
	}
}

func TestStopActiveDownloadsSkipsFocused(t *testing.T) {
	engine := &fakeDiskEngine{
		activeSessions: []domain.TorrentID{"t1", "t2", "t3"},
		modes: map[domain.TorrentID]domain.SessionMode{
			"t1": domain.ModeDownloading,
			"t2": domain.ModeFocused,
			"t3": domain.ModeDownloading,
		},
	}
	dp := DiskPressure{Engine: engine, Logger: discardLogger()}
	stopped := make(map[domain.TorrentID]struct{})

	dp.stopActiveDownloads(context.Background(), stopped)

	if len(engine.stopCalls) != 2 {
		t.Fatalf("expected 2 stop calls, got %d: %v", len(engine.stopCalls), engine.stopCalls)
	}
	if _, ok := stopped["t1"]; !ok {
		t.Fatalf("t1 should be in stopped map")
	}
	if _, ok := stopped["t2"]; ok {
		t.Fatalf("t2 (focused) should NOT be in stopped map")
	}
	if _, ok := stopped["t3"]; !ok {
		t.Fatalf("t3 should be in stopped map")
	}
}

func TestStopActiveDownloadsStopError(t *testing.T) {
	engine := &fakeDiskEngine{
		activeSessions: []domain.TorrentID{"t1"},
		stopErr:        errors.New("stop failed"),
	}
	dp := DiskPressure{Engine: engine, Logger: discardLogger()}
	stopped := make(map[domain.TorrentID]struct{})

	dp.stopActiveDownloads(context.Background(), stopped)

	if len(engine.stopCalls) != 1 {
		t.Fatalf("expected stop to be attempted")
	}
	if _, ok := stopped["t1"]; ok {
		t.Fatalf("t1 should NOT be in stopped map when stop fails")
	}
}

func TestStopActiveDownloadsModeError(t *testing.T) {
	engine := &fakeDiskEngine{
		activeSessions: []domain.TorrentID{"t1"},
		modeErr:        errors.New("mode check failed"),
	}
	dp := DiskPressure{Engine: engine, Logger: discardLogger()}
	stopped := make(map[domain.TorrentID]struct{})

	dp.stopActiveDownloads(context.Background(), stopped)

	// When GetSessionMode fails, the session is skipped (continue)
	if len(engine.stopCalls) != 0 {
		t.Fatalf("expected no stop calls when mode check fails, got %d", len(engine.stopCalls))
	}
}

// ---------- resumeStoppedDownloads tests ----------

func TestResumeStoppedDownloadsEmpty(t *testing.T) {
	engine := &fakeDiskEngine{}
	dp := DiskPressure{Engine: engine, Logger: discardLogger()}
	stopped := make(map[domain.TorrentID]struct{})

	dp.resumeStoppedDownloads(context.Background(), stopped)

	if len(engine.startCalls) != 0 {
		t.Fatalf("expected no start calls")
	}
}

func TestResumeStoppedDownloadsSuccess(t *testing.T) {
	engine := &fakeDiskEngine{}
	dp := DiskPressure{Engine: engine, Logger: discardLogger()}
	stopped := map[domain.TorrentID]struct{}{
		"t1": {},
		"t2": {},
	}

	dp.resumeStoppedDownloads(context.Background(), stopped)

	if len(engine.startCalls) != 2 {
		t.Fatalf("expected 2 start calls, got %d", len(engine.startCalls))
	}
	if len(stopped) != 0 {
		t.Fatalf("stopped map should be cleared, has %d entries", len(stopped))
	}
}

func TestResumeStoppedDownloadsStartError(t *testing.T) {
	engine := &fakeDiskEngine{startErr: errors.New("start failed")}
	dp := DiskPressure{Engine: engine, Logger: discardLogger()}
	stopped := map[domain.TorrentID]struct{}{
		"t1": {},
	}

	dp.resumeStoppedDownloads(context.Background(), stopped)

	if len(engine.startCalls) != 1 {
		t.Fatalf("expected start to be attempted")
	}
	// Entries are deleted even on error (delete always called in loop)
	if len(stopped) != 0 {
		t.Fatalf("stopped map should be cleared even on error, has %d entries", len(stopped))
	}
}

// ---------- Run tests ----------

func TestRunDefaultInterval(t *testing.T) {
	dp := DiskPressure{
		Engine:       &fakeDiskEngine{},
		Logger:       discardLogger(),
		MinFreeBytes: 100,
	}

	// Default interval is 30s, default ResumeBytes = MinFreeBytes * 2
	// We just verify it doesn't panic and respects context cancellation.
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	dp.Run(ctx) // should return immediately
}

func TestRunResumeBytesFallback(t *testing.T) {
	// When ResumeBytes <= MinFreeBytes, it should be set to MinFreeBytes * 2
	engine := &fakeDiskEngine{
		activeSessions: []domain.TorrentID{"t1"},
	}

	freeCalls := 0
	dp := DiskPressure{
		Engine:       engine,
		Logger:       discardLogger(),
		DataDir:      "/tmp",
		MinFreeBytes: 1000,
		ResumeBytes:  500, // less than MinFreeBytes, will be overridden to 2000
		Interval:     time.Millisecond,
		diskFreeFunc: func(path string) (int64, error) {
			freeCalls++
			switch freeCalls {
			case 1:
				return 100, nil // below min → stop
			case 2:
				return 1500, nil // above min but below resume (2000) → stay paused
			default:
				return 3000, nil // above resume → resume
			}
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		// Let a few ticks pass
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	dp.Run(ctx)

	engine.mu.Lock()
	defer engine.mu.Unlock()

	if len(engine.stopCalls) == 0 {
		t.Fatalf("expected at least one stop call on low disk")
	}
}

func TestRunStopAndResumeCycle(t *testing.T) {
	engine := &fakeDiskEngine{
		activeSessions: []domain.TorrentID{"t1"},
	}

	tick := 0
	dp := DiskPressure{
		Engine:       engine,
		Logger:       discardLogger(),
		DataDir:      "/tmp",
		MinFreeBytes: 1000,
		ResumeBytes:  2000,
		Interval:     time.Millisecond,
		diskFreeFunc: func(path string) (int64, error) {
			tick++
			switch tick {
			case 1:
				return 500, nil // below min → stop
			case 2:
				return 3000, nil // above resume → resume
			default:
				return 5000, nil // stay above
			}
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	dp.Run(ctx)

	engine.mu.Lock()
	defer engine.mu.Unlock()

	if len(engine.stopCalls) == 0 {
		t.Fatalf("expected stop calls")
	}
	if len(engine.startCalls) == 0 {
		t.Fatalf("expected start calls after disk recovery")
	}
}

func TestRunDiskCheckError(t *testing.T) {
	engine := &fakeDiskEngine{}

	dp := DiskPressure{
		Engine:       engine,
		Logger:       discardLogger(),
		DataDir:      "/tmp",
		MinFreeBytes: 1000,
		ResumeBytes:  2000,
		Interval:     time.Millisecond,
		diskFreeFunc: func(path string) (int64, error) {
			return 0, errors.New("disk check failed")
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	dp.Run(ctx)

	engine.mu.Lock()
	defer engine.mu.Unlock()

	// No stop/start calls should happen when disk check errors
	if len(engine.stopCalls) != 0 {
		t.Fatalf("expected no stop calls on disk check error")
	}
	if len(engine.startCalls) != 0 {
		t.Fatalf("expected no start calls on disk check error")
	}
}

func TestRunNoStopWhenAboveThreshold(t *testing.T) {
	engine := &fakeDiskEngine{
		activeSessions: []domain.TorrentID{"t1"},
	}

	dp := DiskPressure{
		Engine:       engine,
		Logger:       discardLogger(),
		DataDir:      "/tmp",
		MinFreeBytes: 1000,
		ResumeBytes:  2000,
		Interval:     time.Millisecond,
		diskFreeFunc: func(path string) (int64, error) {
			return 5000, nil // always above threshold
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	dp.Run(ctx)

	engine.mu.Lock()
	defer engine.mu.Unlock()

	if len(engine.stopCalls) != 0 {
		t.Fatalf("expected no stop calls when above threshold")
	}
}
