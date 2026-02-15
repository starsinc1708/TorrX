package apihttp

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"torrentstream/internal/domain"
	"torrentstream/internal/usecase"
)

type blockingStreamUseCase struct {
	started chan struct{}
}

func (f *blockingStreamUseCase) Execute(ctx context.Context, _ domain.TorrentID, _ int) (usecase.StreamResult, error) {
	select {
	case <-f.started:
	default:
		close(f.started)
	}
	<-ctx.Done()
	return usecase.StreamResult{}, ctx.Err()
}

type failingStreamUseCase struct {
	err error
}

func (f *failingStreamUseCase) Execute(ctx context.Context, _ domain.TorrentID, _ int) (usecase.StreamResult, error) {
	return usecase.StreamResult{}, f.err
}

func TestSeekJobUsesUniqueDirectoryAndCancelsPreviousJob(t *testing.T) {
	stream := &blockingStreamUseCase{started: make(chan struct{})}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager := newHLSManager(stream, HLSConfig{BaseDir: t.TempDir()}, logger)

	first, err := manager.seekJob("t1", 0, 0, -1, 120)
	if err != nil {
		t.Fatalf("first seekJob returned error: %v", err)
	}
	if first.cancel == nil {
		t.Fatalf("first job cancel is nil")
	}

	select {
	case <-stream.started:
	case <-time.After(2 * time.Second):
		t.Fatalf("first job did not start")
	}

	second, err := manager.seekJob("t1", 0, 0, -1, 240)
	if err != nil {
		t.Fatalf("second seekJob returned error: %v", err)
	}
	if second.cancel == nil {
		t.Fatalf("second job cancel is nil")
	}
	if first.dir == second.dir {
		t.Fatalf("seek job reused directory: %q", first.dir)
	}

	// Stop second job to avoid goroutine leak in test.
	second.cancel()
}

func TestSeekJobDirectoryIsNotNestedUnderRegularJobDirectory(t *testing.T) {
	stream := &blockingStreamUseCase{started: make(chan struct{})}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager := newHLSManager(stream, HLSConfig{BaseDir: t.TempDir()}, logger)

	regular, err := manager.ensureJob("t1", 0, 0, -1)
	if err != nil {
		t.Fatalf("ensureJob returned error: %v", err)
	}
	if regular.cancel == nil {
		t.Fatalf("regular job cancel is nil")
	}

	select {
	case <-stream.started:
	case <-time.After(2 * time.Second):
		t.Fatalf("regular job did not start")
	}

	seek, err := manager.seekJob("t1", 0, 0, -1, 150)
	if err != nil {
		t.Fatalf("seekJob returned error: %v", err)
	}
	if seek.cancel == nil {
		t.Fatalf("seek job cancel is nil")
	}

	regularRoot := filepath.Clean(regular.dir) + string(filepath.Separator)
	seekDir := filepath.Clean(seek.dir)
	if strings.HasPrefix(seekDir, regularRoot) {
		t.Fatalf("seek directory %q is nested under regular directory %q", seekDir, regular.dir)
	}

	seek.cancel()
}

func TestHLSHealthSnapshotTracksSeekFailures(t *testing.T) {
	stream := &failingStreamUseCase{err: context.DeadlineExceeded}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager := newHLSManager(stream, HLSConfig{BaseDir: t.TempDir()}, logger)

	job, err := manager.seekJob("t1", 0, 0, -1, 180)
	if err != nil {
		t.Fatalf("seekJob returned error: %v", err)
	}

	select {
	case <-job.ready:
	case <-time.After(2 * time.Second):
		t.Fatalf("seek job did not finish")
	}

	snapshot := manager.healthSnapshot()
	if snapshot.TotalSeekRequests != 1 {
		t.Fatalf("unexpected seek request count: %d", snapshot.TotalSeekRequests)
	}
	if snapshot.TotalSeekFailures != 1 {
		t.Fatalf("unexpected seek failure count: %d", snapshot.TotalSeekFailures)
	}
	if snapshot.LastSeekError == "" {
		t.Fatalf("expected last seek error")
	}
	if snapshot.LastSeekAt == nil {
		t.Fatalf("expected last seek timestamp")
	}
}

func TestHLSAutoRestartUpdatesTelemetry(t *testing.T) {
	stream := &failingStreamUseCase{err: context.DeadlineExceeded}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager := newHLSManager(stream, HLSConfig{BaseDir: t.TempDir()}, logger)

	key := hlsKey{id: "t1", fileIndex: 0, audioTrack: 0, subtitleTrack: -1}
	dir := t.TempDir()
	job := newHLSJob(dir, 0)
	manager.jobs[key] = job

	restartedJob, ok := manager.tryAutoRestart(key, job, "test")
	if !ok || restartedJob == nil {
		t.Fatalf("expected auto-restart to succeed")
	}
	if restartedJob == job {
		t.Fatalf("expected a new job instance after restart")
	}
	if restartedJob.restartCount != 1 {
		t.Fatalf("unexpected restart count: %d", restartedJob.restartCount)
	}

	snapshot := manager.healthSnapshot()
	if snapshot.TotalAutoRestarts != 1 {
		t.Fatalf("unexpected auto-restart count: %d", snapshot.TotalAutoRestarts)
	}
	if snapshot.LastAutoRestartReason != "test" {
		t.Fatalf("unexpected auto-restart reason: %q", snapshot.LastAutoRestartReason)
	}
}

func TestPlaylistHasEndList(t *testing.T) {
	withEndList := filepath.Join(t.TempDir(), "with-endlist.m3u8")
	if err := os.WriteFile(withEndList, []byte("#EXTM3U\n#EXTINF:4,\nseg-0.ts\n#EXT-X-ENDLIST\n"), 0o644); err != nil {
		t.Fatalf("write withEndList: %v", err)
	}
	if !playlistHasEndList(withEndList) {
		t.Fatalf("expected endlist to be detected")
	}

	withoutEndList := filepath.Join(t.TempDir(), "without-endlist.m3u8")
	if err := os.WriteFile(withoutEndList, []byte("#EXTM3U\n#EXTINF:4,\nseg-0.ts\n"), 0o644); err != nil {
		t.Fatalf("write withoutEndList: %v", err)
	}
	if playlistHasEndList(withoutEndList) {
		t.Fatalf("did not expect endlist to be detected")
	}
}
