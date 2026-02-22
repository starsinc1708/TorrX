package apihttp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"torrentstream/internal/domain"
)

// ---------------------------------------------------------------------------
// handleDirectPlayback handler tests
// ---------------------------------------------------------------------------

func TestDirectPlaybackNoDataDir(t *testing.T) {
	// Server without mediaDataDir → 404.
	server := NewServer(&fakeCreateTorrent{})
	req := httptest.NewRequest(http.MethodGet, "/torrents/t1/direct/0", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestDirectPlaybackMethodNotAllowed(t *testing.T) {
	dir := t.TempDir()
	server := NewServer(&fakeCreateTorrent{}, WithMediaProbe(&fakeMediaProbe{}, dir))
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/torrents/t1/direct/0", nil)
			w := httptest.NewRecorder()
			server.ServeHTTP(w, req)
			if w.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405", w.Code)
			}
		})
	}
}

func TestDirectPlaybackMissingFileIndex(t *testing.T) {
	dir := t.TempDir()
	server := NewServer(&fakeCreateTorrent{}, WithMediaProbe(&fakeMediaProbe{}, dir))
	req := httptest.NewRequest(http.MethodGet, "/torrents/t1/direct/", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	// Missing file index → 400 (the router parses empty string as invalid fileIndex).
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestDirectPlaybackInvalidFileIndex(t *testing.T) {
	dir := t.TempDir()
	server := NewServer(&fakeCreateTorrent{}, WithMediaProbe(&fakeMediaProbe{}, dir))

	tests := []struct {
		name string
		path string
		want int
	}{
		{"non_numeric", "/torrents/t1/direct/abc", http.StatusBadRequest},
		{"negative", "/torrents/t1/direct/-1", http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			w := httptest.NewRecorder()
			server.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d", w.Code, tc.want)
			}
		})
	}
}

func TestDirectPlaybackFileNotFound(t *testing.T) {
	// getState returns error → repo returns error → 404.
	dir := t.TempDir()
	state := &fakeGetTorrentState{err: errors.New("not found")}
	repo := &fakeRepo{getErr: errors.New("not found")}
	server := NewServer(&fakeCreateTorrent{},
		WithMediaProbe(&fakeMediaProbe{}, dir),
		WithGetTorrentState(state),
		WithRepository(repo),
	)

	req := httptest.NewRequest(http.MethodGet, "/torrents/t1/direct/0", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestDirectPlaybackIncompleteFile(t *testing.T) {
	// File exists but BytesCompleted < Length → 404.
	dir := t.TempDir()
	state := &fakeGetTorrentState{
		result: domain.SessionState{
			Files: []domain.FileRef{
				{Path: "movie.mp4", Length: 1000, BytesCompleted: 500},
			},
		},
	}
	server := NewServer(&fakeCreateTorrent{},
		WithMediaProbe(&fakeMediaProbe{}, dir),
		WithGetTorrentState(state),
	)

	req := httptest.NewRequest(http.MethodGet, "/torrents/t1/direct/0", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for incomplete file", w.Code)
	}
}

func TestDirectPlaybackServeMP4(t *testing.T) {
	dir := t.TempDir()
	// Create a real file on disk.
	moviePath := filepath.Join(dir, "movie.mp4")
	content := []byte("fake-mp4-content")
	if err := os.WriteFile(moviePath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	state := &fakeGetTorrentState{
		result: domain.SessionState{
			Files: []domain.FileRef{
				{Path: "movie.mp4", Length: int64(len(content)), BytesCompleted: int64(len(content))},
			},
		},
	}
	server := NewServer(&fakeCreateTorrent{},
		WithMediaProbe(&fakeMediaProbe{}, dir),
		WithGetTorrentState(state),
	)

	req := httptest.NewRequest(http.MethodGet, "/torrents/t1/direct/0", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if w.Body.Len() != len(content) {
		t.Fatalf("body length = %d, want %d", w.Body.Len(), len(content))
	}
}

func TestDirectPlaybackServeM4V(t *testing.T) {
	dir := t.TempDir()
	moviePath := filepath.Join(dir, "movie.m4v")
	content := []byte("fake-m4v-content")
	if err := os.WriteFile(moviePath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	state := &fakeGetTorrentState{
		result: domain.SessionState{
			Files: []domain.FileRef{
				{Path: "movie.m4v", Length: int64(len(content)), BytesCompleted: int64(len(content))},
			},
		},
	}
	server := NewServer(&fakeCreateTorrent{},
		WithMediaProbe(&fakeMediaProbe{}, dir),
		WithGetTorrentState(state),
	)

	req := httptest.NewRequest(http.MethodGet, "/torrents/t1/direct/0", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestDirectPlaybackMP4HeadRequest(t *testing.T) {
	dir := t.TempDir()
	moviePath := filepath.Join(dir, "movie.mp4")
	content := []byte("fake-mp4-content")
	if err := os.WriteFile(moviePath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	state := &fakeGetTorrentState{
		result: domain.SessionState{
			Files: []domain.FileRef{
				{Path: "movie.mp4", Length: int64(len(content)), BytesCompleted: int64(len(content))},
			},
		},
	}
	server := NewServer(&fakeCreateTorrent{},
		WithMediaProbe(&fakeMediaProbe{}, dir),
		WithGetTorrentState(state),
	)

	req := httptest.NewRequest(http.MethodHead, "/torrents/t1/direct/0", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("body length = %d, want 0 for HEAD request", w.Body.Len())
	}
}

func TestDirectPlaybackMP4SetsDLNAHeaders(t *testing.T) {
	dir := t.TempDir()
	moviePath := filepath.Join(dir, "movie.mp4")
	content := []byte("fake-mp4-content")
	if err := os.WriteFile(moviePath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	state := &fakeGetTorrentState{
		result: domain.SessionState{
			Files: []domain.FileRef{
				{Path: "movie.mp4", Length: int64(len(content)), BytesCompleted: int64(len(content))},
			},
		},
	}
	server := NewServer(&fakeCreateTorrent{},
		WithMediaProbe(&fakeMediaProbe{}, dir),
		WithGetTorrentState(state),
	)

	req := httptest.NewRequest(http.MethodGet, "/torrents/t1/direct/0", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("transferMode.dlna.org"); got != "Streaming" {
		t.Fatalf("transferMode.dlna.org = %q", got)
	}
	if got := w.Header().Get("contentFeatures.dlna.org"); got == "" {
		t.Fatal("contentFeatures.dlna.org must be set")
	}
}

func TestDirectPlaybackUnsupportedExtension(t *testing.T) {
	// .avi file → 404 (not mp4/m4v/mkv).
	dir := t.TempDir()
	moviePath := filepath.Join(dir, "movie.avi")
	if err := os.WriteFile(moviePath, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	state := &fakeGetTorrentState{
		result: domain.SessionState{
			Files: []domain.FileRef{
				{Path: "movie.avi", Length: 4, BytesCompleted: 4},
			},
		},
	}
	server := NewServer(&fakeCreateTorrent{},
		WithMediaProbe(&fakeMediaProbe{}, dir),
		WithGetTorrentState(state),
	)

	req := httptest.NewRequest(http.MethodGet, "/torrents/t1/direct/0", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for unsupported extension", w.Code)
	}
}

func TestDirectPlaybackMKVNilHLS(t *testing.T) {
	// MKV file but hls (StreamJobManager) is nil → 404.
	dir := t.TempDir()
	moviePath := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(moviePath, []byte("fake-mkv"), 0o644); err != nil {
		t.Fatal(err)
	}

	state := &fakeGetTorrentState{
		result: domain.SessionState{
			Files: []domain.FileRef{
				{Path: "movie.mkv", Length: 8, BytesCompleted: 8},
			},
		},
	}
	// No WithStreamTorrent → hls stays nil.
	server := NewServer(&fakeCreateTorrent{},
		WithMediaProbe(&fakeMediaProbe{}, dir),
		WithGetTorrentState(state),
	)

	req := httptest.NewRequest(http.MethodGet, "/torrents/t1/direct/0", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for MKV without HLS manager", w.Code)
	}
}

func TestDirectPlaybackRepoFallback(t *testing.T) {
	// getState fails → falls back to repo for file resolution.
	dir := t.TempDir()
	moviePath := filepath.Join(dir, "video.mp4")
	content := []byte("repo-fallback-content")
	if err := os.WriteFile(moviePath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	state := &fakeGetTorrentState{err: errors.New("session not found")}
	repo := &fakeRepo{
		get: domain.TorrentRecord{
			Files: []domain.FileRef{
				{Path: "video.mp4", Length: int64(len(content)), BytesCompleted: int64(len(content))},
			},
		},
	}
	server := NewServer(&fakeCreateTorrent{},
		WithMediaProbe(&fakeMediaProbe{}, dir),
		WithGetTorrentState(state),
		WithRepository(repo),
	)

	req := httptest.NewRequest(http.MethodGet, "/torrents/t1/direct/0", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (repo fallback)", w.Code)
	}
	if repo.getCalled == 0 {
		t.Fatal("expected repo.Get to be called as fallback")
	}
}

func TestDirectPlaybackFileIndexOutOfRange(t *testing.T) {
	dir := t.TempDir()
	state := &fakeGetTorrentState{
		result: domain.SessionState{
			Files: []domain.FileRef{
				{Path: "movie.mp4", Length: 100, BytesCompleted: 100},
			},
		},
	}
	server := NewServer(&fakeCreateTorrent{},
		WithMediaProbe(&fakeMediaProbe{}, dir),
		WithGetTorrentState(state),
	)

	// fileIndex=5 but only 1 file → 404.
	req := httptest.NewRequest(http.MethodGet, "/torrents/t1/direct/5", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for out-of-range fileIndex", w.Code)
	}
}

func TestDirectPlaybackFileMissingFromDisk(t *testing.T) {
	dir := t.TempDir()
	// File reference says complete but file doesn't exist on disk.
	state := &fakeGetTorrentState{
		result: domain.SessionState{
			Files: []domain.FileRef{
				{Path: "nonexistent.mp4", Length: 100, BytesCompleted: 100},
			},
		},
	}
	server := NewServer(&fakeCreateTorrent{},
		WithMediaProbe(&fakeMediaProbe{}, dir),
		WithGetTorrentState(state),
	)

	req := httptest.NewRequest(http.MethodGet, "/torrents/t1/direct/0", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for missing file on disk", w.Code)
	}
}

func TestDirectPlaybackZeroLengthFile(t *testing.T) {
	dir := t.TempDir()
	state := &fakeGetTorrentState{
		result: domain.SessionState{
			Files: []domain.FileRef{
				{Path: "movie.mp4", Length: 0, BytesCompleted: 0},
			},
		},
	}
	server := NewServer(&fakeCreateTorrent{},
		WithMediaProbe(&fakeMediaProbe{}, dir),
		WithGetTorrentState(state),
	)

	req := httptest.NewRequest(http.MethodGet, "/torrents/t1/direct/0", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for zero-length file", w.Code)
	}
}

// ---------------------------------------------------------------------------
// resolveFileRef tests
// ---------------------------------------------------------------------------

func TestResolveFileRefFromState(t *testing.T) {
	dir := t.TempDir()
	state := &fakeGetTorrentState{
		result: domain.SessionState{
			Files: []domain.FileRef{
				{Path: "a.mp4", Length: 100, BytesCompleted: 50},
				{Path: "b.mkv", Length: 200, BytesCompleted: 200},
			},
		},
	}
	server := NewServer(&fakeCreateTorrent{},
		WithMediaProbe(&fakeMediaProbe{}, dir),
		WithGetTorrentState(state),
	)

	file, ok := server.resolveFileRef(nil, "t1", 1)
	if !ok {
		t.Fatal("expected resolveFileRef to return true")
	}
	if file.Path != "b.mkv" {
		t.Fatalf("path = %q, want %q", file.Path, "b.mkv")
	}
	if file.BytesCompleted != 200 {
		t.Fatalf("bytesCompleted = %d, want 200", file.BytesCompleted)
	}
}

func TestResolveFileRefFromRepo(t *testing.T) {
	dir := t.TempDir()
	state := &fakeGetTorrentState{err: errors.New("no session")}
	repo := &fakeRepo{
		get: domain.TorrentRecord{
			Files: []domain.FileRef{
				{Path: "c.mp4", Length: 300, BytesCompleted: 300},
			},
		},
	}
	server := NewServer(&fakeCreateTorrent{},
		WithMediaProbe(&fakeMediaProbe{}, dir),
		WithGetTorrentState(state),
		WithRepository(repo),
	)

	file, ok := server.resolveFileRef(nil, "t1", 0)
	if !ok {
		t.Fatal("expected resolveFileRef to return true via repo")
	}
	if file.Path != "c.mp4" {
		t.Fatalf("path = %q, want %q", file.Path, "c.mp4")
	}
}

func TestResolveFileRefNotFound(t *testing.T) {
	dir := t.TempDir()
	state := &fakeGetTorrentState{err: errors.New("no session")}
	repo := &fakeRepo{getErr: errors.New("not found")}
	server := NewServer(&fakeCreateTorrent{},
		WithMediaProbe(&fakeMediaProbe{}, dir),
		WithGetTorrentState(state),
		WithRepository(repo),
	)

	_, ok := server.resolveFileRef(nil, "t1", 0)
	if ok {
		t.Fatal("expected resolveFileRef to return false when both sources fail")
	}
}

func TestResolveFileRefNilSources(t *testing.T) {
	// No getState, no repo → false.
	server := NewServer(&fakeCreateTorrent{})
	_, ok := server.resolveFileRef(nil, "t1", 0)
	if ok {
		t.Fatal("expected false with nil sources")
	}
}

func TestResolveFileRefIndexOutOfRange(t *testing.T) {
	dir := t.TempDir()
	state := &fakeGetTorrentState{
		result: domain.SessionState{
			Files: []domain.FileRef{
				{Path: "only.mp4", Length: 100, BytesCompleted: 100},
			},
		},
	}
	server := NewServer(&fakeCreateTorrent{},
		WithMediaProbe(&fakeMediaProbe{}, dir),
		WithGetTorrentState(state),
	)

	_, ok := server.resolveFileRef(nil, "t1", 5)
	if ok {
		t.Fatal("expected false for out-of-range file index")
	}
}

// ---------------------------------------------------------------------------
// resolveDataFilePath tests
// ---------------------------------------------------------------------------

func TestResolveDataFilePath(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name     string
		base     string
		filePath string
		wantErr  bool
	}{
		{"valid", dir, "movie.mp4", false},
		{"nested", dir, "sub/dir/movie.mp4", false},
		{"empty_base", "", "movie.mp4", true},
		{"traversal", dir, "../../etc/passwd", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveDataFilePath(tc.base, tc.filePath)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q / %q", tc.base, tc.filePath)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got == "" {
				t.Fatal("expected non-empty path")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// StreamJobManager remux tests
// ---------------------------------------------------------------------------

func TestCheckRemuxNotStarted(t *testing.T) {
	mgr := &StreamJobManager{
		baseDir:    t.TempDir(),
		remuxCache: make(map[string]*remuxEntry),
		logger:     slog.Default(),
	}

	path, ready := mgr.checkRemux("t1", 0)
	if path != "" || ready {
		t.Fatalf("checkRemux = (%q, %v), want (\"\", false) for unstarted remux", path, ready)
	}
}

func TestCheckRemuxReady(t *testing.T) {
	mgr := &StreamJobManager{
		baseDir:    t.TempDir(),
		remuxCache: make(map[string]*remuxEntry),
		logger:     slog.Default(),
	}

	// Create the output file on disk.
	outPath := mgr.getRemuxPath("t1", 0)
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outPath, []byte("remuxed-content"), 0o644); err != nil {
		t.Fatal(err)
	}

	path, ready := mgr.checkRemux("t1", 0)
	if !ready {
		t.Fatal("expected ready=true when output file exists on disk")
	}
	if path != outPath {
		t.Fatalf("path = %q, want %q", path, outPath)
	}

	// Second call should hit cache.
	path2, ready2 := mgr.checkRemux("t1", 0)
	if !ready2 || path2 != outPath {
		t.Fatalf("second checkRemux = (%q, %v), expected cached result", path2, ready2)
	}
}

func TestCheckRemuxInProgress(t *testing.T) {
	mgr := &StreamJobManager{
		baseDir:    t.TempDir(),
		remuxCache: make(map[string]*remuxEntry),
		logger:     slog.Default(),
	}

	key := remuxCacheKey("t1", 0)
	entry := &remuxEntry{
		path:    mgr.getRemuxPath("t1", 0),
		ready:   make(chan struct{}), // not closed = in progress
		started: time.Now(),
	}
	mgr.remuxCache[key] = entry

	path, ready := mgr.checkRemux("t1", 0)
	if ready {
		t.Fatal("expected ready=false for in-progress remux")
	}
	if path == "" {
		t.Fatal("expected non-empty path for in-progress remux")
	}
}

func TestCheckRemuxCompletedWithError(t *testing.T) {
	mgr := &StreamJobManager{
		baseDir:    t.TempDir(),
		remuxCache: make(map[string]*remuxEntry),
		logger:     slog.Default(),
	}

	key := remuxCacheKey("t1", 0)
	entry := &remuxEntry{
		path:    mgr.getRemuxPath("t1", 0),
		ready:   make(chan struct{}),
		err:     errors.New("ffmpeg failed"),
		started: time.Now(),
	}
	close(entry.ready) // completed but with error
	mgr.remuxCache[key] = entry

	_, ready := mgr.checkRemux("t1", 0)
	if ready {
		t.Fatal("expected ready=false for errored remux")
	}
}

func TestTriggerRemuxIdempotent(t *testing.T) {
	mgr := &StreamJobManager{
		baseDir:     t.TempDir(),
		remuxCache:  make(map[string]*remuxEntry),
		logger:      slog.Default(),
		ffmpegPath:  "nonexistent-ffmpeg", // Will fail, but shouldn't be called twice.
		ffprobePath: "nonexistent-ffprobe",
	}

	key := remuxCacheKey("t1", 0)
	entry := &remuxEntry{
		path:    mgr.getRemuxPath("t1", 0),
		ready:   make(chan struct{}),
		started: time.Now(),
	}
	mgr.remuxCache[key] = entry

	// Calling triggerRemux when entry already exists should be a no-op.
	mgr.triggerRemux("t1", 0, "/fake/input.mkv")

	mgr.remuxCacheMu.Lock()
	if mgr.remuxCache[key] != entry {
		t.Fatal("triggerRemux replaced existing cache entry")
	}
	mgr.remuxCacheMu.Unlock()
}

func TestGetRemuxPath(t *testing.T) {
	mgr := &StreamJobManager{
		baseDir: "/tmp/hls",
	}
	got := mgr.getRemuxPath("abc123", 2)
	want := filepath.Join("/tmp/hls", "remux", "abc123", "2.mp4")
	if got != want {
		t.Fatalf("getRemuxPath = %q, want %q", got, want)
	}
}

func TestRunRemuxMkdirError(t *testing.T) {
	// Use a non-writable path to trigger mkdir error.
	mgr := &StreamJobManager{
		baseDir:         "/nonexistent/readonly/path",
		remuxCache:      make(map[string]*remuxEntry),
		codecCache:      make(map[string]*codecCacheEntry),
		resolutionCache: make(map[string]*resolutionCacheEntry),
		logger:          slog.Default(),
		ffmpegPath:      "ffmpeg",
		ffprobePath:     "ffprobe",
	}

	entry := &remuxEntry{
		path:    filepath.Join("/nonexistent/readonly/path", "remux", "t1", "0.mp4"),
		ready:   make(chan struct{}),
		started: time.Now(),
	}

	mgr.runRemux(context.Background(), entry, "/fake/input.mkv", "t1/0")

	<-entry.ready
	if entry.err == nil {
		t.Fatal("expected error for mkdir failure")
	}
}

func TestRunRemuxFFmpegNotFound(t *testing.T) {
	dir := t.TempDir()
	mgr := &StreamJobManager{
		baseDir:         dir,
		remuxCache:      make(map[string]*remuxEntry),
		codecCache:      make(map[string]*codecCacheEntry),
		resolutionCache: make(map[string]*resolutionCacheEntry),
		logger:          slog.Default(),
		ffmpegPath:      "nonexistent-ffmpeg-binary-xyz",
		ffprobePath:     "nonexistent-ffprobe-binary-xyz",
	}

	outPath := mgr.getRemuxPath("t1", 0)
	entry := &remuxEntry{
		path:    outPath,
		ready:   make(chan struct{}),
		started: time.Now(),
	}
	cacheKey := "t1/0"
	mgr.remuxCache[cacheKey] = entry

	mgr.runRemux(context.Background(), entry, "/fake/input.mkv", cacheKey)

	<-entry.ready
	if entry.err == nil {
		t.Fatal("expected error when ffmpeg binary not found")
	}

	// Verify entry was cleaned from cache on error.
	mgr.remuxCacheMu.Lock()
	_, exists := mgr.remuxCache[cacheKey]
	mgr.remuxCacheMu.Unlock()
	if exists {
		t.Fatal("expected cache entry to be removed after ffmpeg error")
	}

	// Verify temp file was cleaned up.
	tmpPath := outPath + ".tmp"
	if _, err := os.Stat(tmpPath); err == nil {
		t.Fatal("expected temp file to be removed after error")
	}
}

// ---------------------------------------------------------------------------
// Integration: MKV remux flow via handler (with mock HLS manager)
// ---------------------------------------------------------------------------

func TestDirectPlaybackMKVRemux202(t *testing.T) {
	dir := t.TempDir()
	moviePath := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(moviePath, []byte("fake-mkv-content"), 0o644); err != nil {
		t.Fatal(err)
	}

	state := &fakeGetTorrentState{
		result: domain.SessionState{
			Files: []domain.FileRef{
				{Path: "movie.mkv", Length: 16, BytesCompleted: 16},
			},
		},
	}

	// Create a StreamJobManager with a pre-populated codec cache that says "H.264".
	mgr := &StreamJobManager{
		baseDir:         t.TempDir(),
		remuxCache:      make(map[string]*remuxEntry),
		codecCache:      make(map[string]*codecCacheEntry),
		resolutionCache: make(map[string]*resolutionCacheEntry),
		logger:          slog.Default(),
		ffmpegPath:      "nonexistent-ffmpeg",
		ffprobePath:     "nonexistent-ffprobe",
	}
	// Pre-populate codec cache so isH264FileWithCache returns true.
	absPath, _ := filepath.Abs(moviePath)
	mgr.codecCache[absPath] = &codecCacheEntry{isH264: true, lastAccess: time.Now()}

	server := NewServer(&fakeCreateTorrent{},
		WithMediaProbe(&fakeMediaProbe{}, dir),
		WithGetTorrentState(state),
	)
	server.hls = mgr

	req := httptest.NewRequest(http.MethodGet, "/torrents/t1/direct/0", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (remux triggered)", w.Code)
	}
	if ra := w.Header().Get("Retry-After"); ra != "3" {
		t.Fatalf("Retry-After = %q, want %q", ra, "3")
	}
}

func TestDirectPlaybackMKVNonH264(t *testing.T) {
	dir := t.TempDir()
	moviePath := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(moviePath, []byte("fake-mkv-h265"), 0o644); err != nil {
		t.Fatal(err)
	}

	state := &fakeGetTorrentState{
		result: domain.SessionState{
			Files: []domain.FileRef{
				{Path: "movie.mkv", Length: 13, BytesCompleted: 13},
			},
		},
	}

	mgr := &StreamJobManager{
		baseDir:         t.TempDir(),
		remuxCache:      make(map[string]*remuxEntry),
		codecCache:      make(map[string]*codecCacheEntry),
		resolutionCache: make(map[string]*resolutionCacheEntry),
		logger:          slog.Default(),
		ffmpegPath:      "nonexistent-ffmpeg",
		ffprobePath:     "nonexistent-ffprobe",
	}
	// Pre-populate codec cache with isH264=false (e.g., H.265).
	absPath, _ := filepath.Abs(moviePath)
	mgr.codecCache[absPath] = &codecCacheEntry{isH264: false, lastAccess: time.Now()}

	server := NewServer(&fakeCreateTorrent{},
		WithMediaProbe(&fakeMediaProbe{}, dir),
		WithGetTorrentState(state),
	)
	server.hls = mgr

	req := httptest.NewRequest(http.MethodGet, "/torrents/t1/direct/0", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for non-H.264 MKV", w.Code)
	}
}

func TestDirectPlaybackMKVRemuxReady(t *testing.T) {
	dir := t.TempDir()
	moviePath := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(moviePath, []byte("fake-mkv-content"), 0o644); err != nil {
		t.Fatal(err)
	}

	state := &fakeGetTorrentState{
		result: domain.SessionState{
			Files: []domain.FileRef{
				{Path: "movie.mkv", Length: 16, BytesCompleted: 16},
			},
		},
	}

	hlsDir := t.TempDir()
	mgr := &StreamJobManager{
		baseDir:         hlsDir,
		remuxCache:      make(map[string]*remuxEntry),
		codecCache:      make(map[string]*codecCacheEntry),
		resolutionCache: make(map[string]*resolutionCacheEntry),
		logger:          slog.Default(),
		ffmpegPath:      "nonexistent-ffmpeg",
		ffprobePath:     "nonexistent-ffprobe",
	}

	// Pre-populate codec cache.
	absPath, _ := filepath.Abs(moviePath)
	mgr.codecCache[absPath] = &codecCacheEntry{isH264: true, lastAccess: time.Now()}

	// Create remux output file on disk.
	remuxOutPath := mgr.getRemuxPath("t1", 0)
	if err := os.MkdirAll(filepath.Dir(remuxOutPath), 0o755); err != nil {
		t.Fatal(err)
	}
	remuxContent := []byte("remuxed-mp4-content")
	if err := os.WriteFile(remuxOutPath, remuxContent, 0o644); err != nil {
		t.Fatal(err)
	}

	server := NewServer(&fakeCreateTorrent{},
		WithMediaProbe(&fakeMediaProbe{}, dir),
		WithGetTorrentState(state),
	)
	server.hls = mgr

	req := httptest.NewRequest(http.MethodGet, "/torrents/t1/direct/0", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (remux ready)", w.Code)
	}
	if w.Body.Len() != len(remuxContent) {
		t.Fatalf("body length = %d, want %d", w.Body.Len(), len(remuxContent))
	}
}

func TestDirectPlaybackMKVRemuxReadyHeadRequest(t *testing.T) {
	dir := t.TempDir()
	moviePath := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(moviePath, []byte("fake-mkv-content"), 0o644); err != nil {
		t.Fatal(err)
	}

	state := &fakeGetTorrentState{
		result: domain.SessionState{
			Files: []domain.FileRef{
				{Path: "movie.mkv", Length: 16, BytesCompleted: 16},
			},
		},
	}

	hlsDir := t.TempDir()
	mgr := &StreamJobManager{
		baseDir:         hlsDir,
		remuxCache:      make(map[string]*remuxEntry),
		codecCache:      make(map[string]*codecCacheEntry),
		resolutionCache: make(map[string]*resolutionCacheEntry),
		logger:          slog.Default(),
		ffmpegPath:      "nonexistent-ffmpeg",
		ffprobePath:     "nonexistent-ffprobe",
	}

	absPath, _ := filepath.Abs(moviePath)
	mgr.codecCache[absPath] = &codecCacheEntry{isH264: true, lastAccess: time.Now()}

	remuxOutPath := mgr.getRemuxPath("t1", 0)
	if err := os.MkdirAll(filepath.Dir(remuxOutPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(remuxOutPath, []byte("remuxed-mp4-content"), 0o644); err != nil {
		t.Fatal(err)
	}

	server := NewServer(&fakeCreateTorrent{},
		WithMediaProbe(&fakeMediaProbe{}, dir),
		WithGetTorrentState(state),
	)
	server.hls = mgr

	req := httptest.NewRequest(http.MethodHead, "/torrents/t1/direct/0", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for HEAD on ready remux", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("body length = %d, want 0 for HEAD request", w.Body.Len())
	}
}

// ---------------------------------------------------------------------------
// Concurrent remux tests
// ---------------------------------------------------------------------------

func TestTriggerRemuxConcurrent(t *testing.T) {
	dir, err := os.MkdirTemp("", "trigger-remux-concurrent-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	mgr := &StreamJobManager{
		baseDir:         dir,
		remuxCache:      make(map[string]*remuxEntry),
		codecCache:      make(map[string]*codecCacheEntry),
		resolutionCache: make(map[string]*resolutionCacheEntry),
		logger:          slog.Default(),
		ffmpegPath:      "nonexistent-ffmpeg",
		ffprobePath:     "nonexistent-ffprobe",
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mgr.triggerRemux("t1", 0, "/fake/input.mkv")
		}()
	}
	wg.Wait()

	mgr.remuxCacheMu.Lock()
	count := len(mgr.remuxCache)
	mgr.remuxCacheMu.Unlock()

	// Should only have 1 entry despite 10 concurrent triggers.
	if count != 1 {
		t.Fatalf("remuxCache has %d entries, want 1", count)
	}
}
