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
	manager := newHLSManager(stream, nil, HLSConfig{BaseDir: t.TempDir()}, logger)

	first, _, err := manager.seekJob("t1", 0, 0, -1, 120)
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

	second, _, err := manager.seekJob("t1", 0, 0, -1, 240)
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
	manager := newHLSManager(stream, nil, HLSConfig{BaseDir: t.TempDir()}, logger)

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

	seek, _, err := manager.seekJob("t1", 0, 0, -1, 150)
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
	manager := newHLSManager(stream, nil, HLSConfig{BaseDir: t.TempDir()}, logger)

	job, _, err := manager.seekJob("t1", 0, 0, -1, 180)
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
	manager := newHLSManager(stream, nil, HLSConfig{BaseDir: t.TempDir()}, logger)

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

func TestComputeProfileHash(t *testing.T) {
	h1 := computeProfileHash("veryfast", 23, "128k", 4)
	h2 := computeProfileHash("veryfast", 23, "128k", 4)
	if h1 != h2 {
		t.Fatalf("expected same hash for same params, got %q vs %q", h1, h2)
	}

	h3 := computeProfileHash("slow", 23, "128k", 4)
	if h1 == h3 {
		t.Fatalf("expected different hash for different preset, got %q", h1)
	}

	h4 := computeProfileHash("veryfast", 28, "128k", 4)
	if h1 == h4 {
		t.Fatalf("expected different hash for different CRF, got %q", h1)
	}

	if len(h1) != 8 {
		t.Fatalf("expected 8-char hash, got %d chars: %q", len(h1), h1)
	}
}

func TestEnsureJobDirContainsProfileHash(t *testing.T) {
	preset := "veryfast"
	crf := 23
	audioBitrate := "128k"
	segDur := 4

	expectedHash := computeProfileHash(preset, crf, audioBitrate, segDur)

	dir := t.TempDir()
	mgr := &hlsManager{
		baseDir:         dir,
		preset:          preset,
		crf:             crf,
		audioBitrate:    audioBitrate,
		segmentDuration: segDur,
		jobs:            make(map[hlsKey]*hlsJob),
		codecCache:      make(map[string]*codecCacheEntry),
		resolutionCache: make(map[string]*resolutionCacheEntry),
		logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	key := hlsKey{id: "test-id", fileIndex: 0, audioTrack: 0, subtitleTrack: -1}
	jobDir := mgr.buildJobDir(key)
	if !strings.Contains(jobDir, expectedHash) {
		t.Fatalf("expected job dir %q to contain profile hash %q", jobDir, expectedHash)
	}
}

func TestFindLastSegment(t *testing.T) {
	dir := t.TempDir()

	// No .ts files → returns empty string.
	path, size := findLastSegment(dir)
	if path != "" || size != 0 {
		t.Fatalf("expected empty result for empty dir, got path=%q size=%d", path, size)
	}

	// Write two segment files with different mtimes.
	seg1 := filepath.Join(dir, "seg-00001.ts")
	seg2 := filepath.Join(dir, "seg-00002.ts")
	if err := os.WriteFile(seg1, make([]byte, 500*1024), 0644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond) // ensure different mtime
	if err := os.WriteFile(seg2, make([]byte, 1024*1024), 0644); err != nil {
		t.Fatal(err)
	}

	path, size = findLastSegment(dir)
	if path != seg2 {
		t.Fatalf("expected newest segment %q, got %q", seg2, path)
	}
	if size != 1024*1024 {
		t.Fatalf("expected size 1048576, got %d", size)
	}
}

func TestSegmentLimiterAllow(t *testing.T) {
	lim := newSegmentLimiter(10, 5) // 10 req/s burst=5

	ip := "192.168.1.1"
	for i := 0; i < 5; i++ {
		if !lim.Allow(ip) {
			t.Fatalf("request %d should be allowed within burst", i+1)
		}
	}
	if lim.Allow(ip) {
		t.Fatalf("request 6 should be denied after burst exhausted")
	}
}

func TestSegmentLimiterIsolatesIPs(t *testing.T) {
	lim := newSegmentLimiter(10, 2)

	lim.Allow("10.0.0.1")
	lim.Allow("10.0.0.1")

	if !lim.Allow("10.0.0.2") {
		t.Fatalf("IP B should be allowed when IP A is throttled")
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
