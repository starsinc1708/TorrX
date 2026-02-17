# HLS Missing Phases Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Implement the four missing/partial refactoring phases in priority order: Quasi-Complete Mode (6), Profile Versioning (8), Watchdog last-segment monitoring (7), and HLS segment rate limiting (11).

**Architecture:** All changes are confined to `services/torrent-engine/internal/api/http/` and `internal/metrics/`. Phase 6 extends `newDataSource()` in `hls_datasource.go`. Phase 8 adds a `computeProfileHash()` helper and threads it through directory naming. Phase 7 adds last-segment mtime/size tracking in `watchJobProgress()`. Phase 11 adds a token-bucket limiter in the HLS segment handler.

**Tech Stack:** Go stdlib only (hash/fnv, runtime/debug). No new external dependencies.

---

## Task 1: Phase 6 — Quasi-Complete Mode

**Files:**
- Modify: `services/torrent-engine/internal/api/http/hls_datasource.go`
- Test: `services/torrent-engine/internal/api/http/hls_datasource_test.go`

### Step 1: Write the failing tests

Add to `hls_datasource_test.go`:

```go
func TestNewDataSourceQuasiComplete(t *testing.T) {
	// Create a temp dir with a fake video file
	dir := t.TempDir()
	filePath := filepath.Join(dir, "movie.mkv")
	// Write 10 MB of dummy data (simulates a downloaded file)
	data := make([]byte, 11*1024*1024)
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := &hlsManager{
		dataDir: dir,
		logger:  logger,
	}

	// File is 95% complete → quasi-complete
	result := usecase.StreamResult{
		Reader: io.NopCloser(bytes.NewReader(nil)),
		File: domain.FileRef{
			Path:           "movie.mkv",
			Length:         12 * 1024 * 1024,
			BytesCompleted: int64(float64(12*1024*1024) * 0.96), // 96%
		},
	}
	job := &hlsJob{seekSeconds: 300} // seeking into the middle

	ds, _ := mgr.newDataSource(result, job, hlsKey{})
	defer ds.Close()

	if _, ok := ds.(*directFileSource); !ok {
		t.Fatalf("expected directFileSource for quasi-complete file with seek, got %T", ds)
	}
}

func TestNewDataSourceQuasiCompleteThreshold(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "movie.mkv")
	data := make([]byte, 11*1024*1024)
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := &hlsManager{
		dataDir: dir,
		logger:  logger,
	}

	// File is only 80% complete — NOT quasi-complete, should use partialDirectSource (seek=0)
	result := usecase.StreamResult{
		Reader: io.NopCloser(bytes.NewReader(nil)),
		File: domain.FileRef{
			Path:           "movie.mkv",
			Length:         12 * 1024 * 1024,
			BytesCompleted: int64(float64(12*1024*1024) * 0.80),
		},
	}
	job := &hlsJob{seekSeconds: 0}

	ds, _ := mgr.newDataSource(result, job, hlsKey{})
	defer ds.Close()

	// At seek=0 with partial download it's partialDirectSource (existing behavior).
	if _, ok := ds.(*partialDirectSource); !ok {
		t.Fatalf("expected partialDirectSource for 80%% complete file at seek=0, got %T", ds)
	}
}
```

Add missing imports to the test file:
```go
import (
    "bytes"
    "io"
    "log/slog"
    "os"
    "path/filepath"
    "testing"

    "torrentstream/internal/domain"
    "torrentstream/internal/usecase"
)
```

### Step 2: Run the tests to confirm they fail

```bash
cd services/torrent-engine && go test ./internal/api/http/ -run TestNewDataSourceQuasiComplete -v
```

Expected: **FAIL** — `directFileSource` is not returned for quasi-complete file with seek.

### Step 3: Implement Quasi-Complete Mode in `hls_datasource.go`

Add constant at top of file (after existing imports):

```go
// quasiCompleteThreshold is the minimum download fraction at which a file
// is treated as "practically complete" for data source selection.
// Above this threshold, direct file reads are used even for seeking,
// avoiding the buffered pipe overhead.
const quasiCompleteThreshold = 0.95
```

In `newDataSource`, replace the current logic block inside `if m.dataDir != ""` with:

```go
func (m *hlsManager) newDataSource(result usecase.StreamResult, job *hlsJob, key hlsKey) (MediaDataSource, string) {
	fileComplete := result.File.Length <= 0 ||
		(result.File.BytesCompleted > 0 && result.File.BytesCompleted >= result.File.Length)

	// Quasi-complete: file is ≥95% downloaded — treat same as complete for local reads.
	isQuasiComplete := !fileComplete &&
		result.File.Length > 0 &&
		result.File.BytesCompleted > 0 &&
		float64(result.File.BytesCompleted)/float64(result.File.Length) >= quasiCompleteThreshold

	subtitleSourcePath := ""

	if m.dataDir != "" {
		candidatePath, pathErr := resolveDataFilePath(m.dataDir, result.File.Path)
		if pathErr == nil {
			if info, statErr := os.Stat(candidatePath); statErr == nil && !info.IsDir() {
				subtitleSourcePath = candidatePath
				if fileComplete || isQuasiComplete {
					// Fully downloaded or quasi-complete — direct file read.
					// For quasi-complete, FFmpeg naturally pauses at the undownloaded
					// tail (last ~5%), which the user is unlikely to reach.
					m.logger.Info("hls using directFileSource",
						slog.String("path", candidatePath),
						slog.Bool("quasiComplete", isQuasiComplete),
					)
					return &directFileSource{path: candidatePath, reader: result.Reader}, subtitleSourcePath
				}
				if info.Size() >= 10*1024*1024 && info.Size() < result.File.Length && job.seekSeconds == 0 {
					m.logger.Info("hls using partialDirectSource",
						slog.String("path", candidatePath),
						slog.Int64("available", info.Size()),
						slog.Int64("total", result.File.Length),
					)
					return &partialDirectSource{path: candidatePath, reader: result.Reader}, subtitleSourcePath
				}
			}
		}
	}

	if job.seekSeconds > 0 && fileComplete && m.listenAddr != "" {
		host := m.listenAddr
		if strings.HasPrefix(host, ":") {
			host = "127.0.0.1" + host
		}
		url := fmt.Sprintf("http://%s/torrents/%s/stream?fileIndex=%d",
			host, string(key.id), key.fileIndex)
		m.logger.Info("hls using httpStreamSource",
			slog.String("url", url),
		)
		return &httpStreamSource{url: url, reader: result.Reader}, subtitleSourcePath
	}

	m.logger.Info("hls using pipeSource")
	buffered := newBufferedStreamReader(result.Reader, defaultStreamBufSize, m.logger)
	return &pipeSource{buffered: buffered}, subtitleSourcePath
}
```

### Step 4: Run tests to confirm they pass

```bash
cd services/torrent-engine && go test ./internal/api/http/ -run TestNewDataSourceQuasiComplete -v
```

Expected: **PASS** both tests.

### Step 5: Run the full test suite

```bash
cd services/torrent-engine && go test ./internal/api/http/ -v 2>&1 | tail -20
```

Expected: all tests pass.

### Step 6: Commit

```bash
git add services/torrent-engine/internal/api/http/hls_datasource.go \
        services/torrent-engine/internal/api/http/hls_datasource_test.go
git commit -m "$(cat <<'EOF'
feat(hls): Phase 6 — quasi-complete mode for direct file reads

Files ≥95% downloaded now use directFileSource instead of pipeSource,
eliminating ring-buffer overhead and backoff retries for near-complete
downloads. Seeking into a quasi-complete file is fast and avoids FFmpeg
startup latency from pipe negotiation.

Co-Authored-By: Claude Sonnet 4.5 <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Phase 8 — Transcode Profile Versioning

**Files:**
- Modify: `services/torrent-engine/internal/api/http/hls.go`
- Test: `services/torrent-engine/internal/api/http/hls_test.go`

**Problem:** When encoding settings change (CRF, preset) or FFmpeg is updated, `ensureJob` reuses old cached transcodes because it only checks `playlistHasEndList` without verifying encoding parameters. A profile hash in the directory name forces a new directory for each distinct encoding profile.

### Step 1: Write the failing tests

Add to `hls_test.go`:

```go
func TestComputeProfileHash(t *testing.T) {
	// Same inputs → same hash.
	h1 := computeProfileHash("veryfast", 23, "128k", 4)
	h2 := computeProfileHash("veryfast", 23, "128k", 4)
	if h1 != h2 {
		t.Fatalf("expected same hash for same params, got %q vs %q", h1, h2)
	}

	// Different preset → different hash.
	h3 := computeProfileHash("slow", 23, "128k", 4)
	if h1 == h3 {
		t.Fatalf("expected different hash for different preset, got %q", h1)
	}

	// Different CRF → different hash.
	h4 := computeProfileHash("veryfast", 28, "128k", 4)
	if h1 == h4 {
		t.Fatalf("expected different hash for different CRF, got %q", h1)
	}

	// Hash is 8 hex chars.
	if len(h1) != 8 {
		t.Fatalf("expected 8-char hash, got %d chars: %q", len(h1), h1)
	}
}

func TestEnsureJobDirContainsProfileHash(t *testing.T) {
	// Verify that the directory created by ensureJob contains the profile hash.
	preset := "veryfast"
	crf := 23
	audioBitrate := "128k"
	segDur := 4

	expectedHash := computeProfileHash(preset, crf, audioBitrate, segDur)

	dir := t.TempDir()
	mgr := &hlsManager{
		baseDir:      dir,
		preset:       preset,
		crf:          crf,
		audioBitrate: audioBitrate,
		segmentDuration: segDur,
		jobs:         make(map[hlsKey]*hlsJob),
		codecCache:   make(map[string]*codecCacheEntry),
		resolutionCache: make(map[string]*resolutionCacheEntry),
		logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	// We can't call ensureJob without a stream use case, so just verify
	// the directory path would contain the hash.
	key := hlsKey{id: "test-id", fileIndex: 0, audioTrack: 0, subtitleTrack: -1}
	jobDir := mgr.buildJobDir(key)
	if !strings.Contains(jobDir, expectedHash) {
		t.Fatalf("expected job dir %q to contain profile hash %q", jobDir, expectedHash)
	}
}
```

### Step 2: Run the failing tests

```bash
cd services/torrent-engine && go test ./internal/api/http/ -run "TestComputeProfileHash|TestEnsureJobDirContainsProfileHash" -v
```

Expected: **FAIL** — `computeProfileHash` and `buildJobDir` are undefined.

### Step 3: Implement profile hashing in `hls.go`

Add the helper function (near the top, after package imports):

```go
import "hash/fnv"

// computeProfileHash returns an 8-char hex string that uniquely identifies
// the encoding configuration. Used to version the transcode cache directory:
// when settings change, a new directory is used and stale transcodes are
// automatically bypassed.
func computeProfileHash(preset string, crf int, audioBitrate string, segDur int) string {
	h := fnv.New32a()
	_, _ = fmt.Fprintf(h, "%s:%d:%s:%d", preset, crf, audioBitrate, segDur)
	return fmt.Sprintf("%08x", h.Sum32())
}
```

Add a helper method to `hlsManager` that builds the canonical job directory path:

```go
// buildJobDir constructs the job directory path for a given key using
// the current encoding profile hash. This ensures that changing encoding
// settings produces a new directory, invalidating stale cached transcodes.
func (m *hlsManager) buildJobDir(key hlsKey) string {
	m.mu.RLock()
	preset := m.preset
	crf := m.crf
	audioBitrate := m.audioBitrate
	segDur := m.segmentDuration
	m.mu.RUnlock()

	if segDur <= 0 {
		segDur = 4
	}
	hash := computeProfileHash(preset, crf, audioBitrate, segDur)
	dir := filepath.Join(
		m.baseDir,
		string(key.id),
		strconv.Itoa(key.fileIndex),
		fmt.Sprintf("a%d-s%d-p%s", key.audioTrack, key.subtitleTrack, hash),
	)
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}
```

In `ensureJob`, replace the manual directory construction with `buildJobDir`:

```go
// Replace this block:
//   dir := filepath.Join(
//       m.baseDir,
//       string(id),
//       strconv.Itoa(fileIndex),
//       fmt.Sprintf("a%d-s%d", audioTrack, subtitleTrack),
//   )
//   absDir, err := filepath.Abs(dir)
//   if err == nil {
//       dir = absDir
//   }
// With:
dir := m.buildJobDir(key)
```

Also update the multi-variant and single-variant playlist paths to use `dir` (they already do, no change needed there).

In `seekJob`, replace the seek-specific directory construction with `buildJobDir`-based path:

```go
// Replace the seek dir construction block:
//   dir := filepath.Join(
//       m.baseDir, string(id), strconv.Itoa(fileIndex),
//       fmt.Sprintf("a%d-s%d-seek-%d", audioTrack, subtitleTrack, time.Now().UnixNano()),
//   )
// With:
baseDir := m.buildJobDir(key) // get profile-hashed base
seekSuffix := fmt.Sprintf("%d", time.Now().UnixNano())
dir := baseDir + "-seek-" + seekSuffix
if abs, err := filepath.Abs(dir); err == nil {
    dir = abs
}
```

### Step 4: Run tests to confirm they pass

```bash
cd services/torrent-engine && go test ./internal/api/http/ -run "TestComputeProfileHash|TestEnsureJobDirContainsProfileHash" -v
```

Expected: **PASS**.

### Step 5: Run full test suite

```bash
cd services/torrent-engine && go test ./internal/api/http/ -v 2>&1 | tail -20
```

### Step 6: Commit

```bash
git add services/torrent-engine/internal/api/http/hls.go \
        services/torrent-engine/internal/api/http/hls_test.go
git commit -m "$(cat <<'EOF'
feat(hls): Phase 8 — transcode profile versioning via directory hash

computeProfileHash(preset, crf, audioBitrate, segDur) produces an 8-char
FNV32 hex string embedded in the job directory name. Changing any encoding
parameter or segment duration now automatically bypasses stale cached
transcodes without manual cache eviction.

Co-Authored-By: Claude Sonnet 4.5 <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Phase 7 — Watchdog Last-Segment Monitoring

**Files:**
- Modify: `services/torrent-engine/internal/api/http/hls.go`

**What's missing:** The watchdog only checks `playlist mtime`. If FFmpeg is alive but producing very small/stuck segments (e.g., network stall mid-segment), the mtime still updates but actual content stalls. Tracking the most recent segment's `(mtime, size)` and alerting when both are unchanged for >30s gives earlier, more accurate stall detection.

### Step 1: Write the failing test

Add to `hls_test.go`:

```go
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
	time.Sleep(5 * time.Millisecond) // ensure different mtime
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
```

### Step 2: Run the failing test

```bash
cd services/torrent-engine && go test ./internal/api/http/ -run TestFindLastSegment -v
```

Expected: **FAIL** — `findLastSegment` is undefined.

### Step 3: Implement `findLastSegment` and integrate into watchdog

Add the helper to `hls.go`:

```go
// findLastSegment returns the path and size of the most recently modified
// .ts segment file in dir. Returns ("", 0) if no segments exist.
func findLastSegment(dir string) (path string, size int64) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", 0
	}
	var latestMod time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".ts") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(latestMod) {
			latestMod = info.ModTime()
			path = filepath.Join(dir, e.Name())
			size = info.Size()
		}
	}
	return path, size
}
```

Extend `watchJobProgress` to track last-segment state. Add two local variables before the loop:

```go
lastSegPath := ""
lastSegSize := int64(0)
lastSegChangedAt := time.Now()
```

Inside the loop, after the playlist mtime check (after `m.touchJobActivity`), add:

```go
// Check last segment for stuck encoder (mtime updated but content not growing).
if segPath, segSize := findLastSegment(job.dir); segPath != "" {
    changed := segPath != lastSegPath || segSize != lastSegSize
    if changed {
        lastSegPath = segPath
        lastSegSize = segSize
        lastSegChangedAt = time.Now()
    } else if time.Since(lastSegChangedAt) >= 45*time.Second && segSize < 256*1024 {
        // Segment exists but hasn't grown in 45s and is tiny — encoder is stuck.
        m.logger.Warn("hls watchdog: last segment appears stuck (tiny and unchanged)",
            slog.String("torrentId", string(key.id)),
            slog.String("segPath", lastSegPath),
            slog.Int64("segSize", lastSegSize),
            slog.Duration("unchanged", time.Since(lastSegChangedAt)),
        )
    }
}
```

### Step 4: Run the test

```bash
cd services/torrent-engine && go test ./internal/api/http/ -run TestFindLastSegment -v
```

Expected: **PASS**.

### Step 5: Run full test suite

```bash
cd services/torrent-engine && go test ./internal/api/http/ -v 2>&1 | tail -20
```

### Step 6: Commit

```bash
git add services/torrent-engine/internal/api/http/hls.go \
        services/torrent-engine/internal/api/http/hls_test.go
git commit -m "$(cat <<'EOF'
feat(hls): Phase 7 — watchdog last-segment size/mtime monitoring

findLastSegment() scans the job dir for the newest .ts file. The watchdog
now logs a warning when the segment hasn't grown and is tiny (<256KB) for
>45s, catching FFmpeg stuck mid-segment — a false negative for the
playlist-mtime-only check.

Co-Authored-By: Claude Sonnet 4.5 <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: Phase 11 — HLS Segment Rate Limiting

**Files:**
- Modify: `services/torrent-engine/internal/api/http/server.go` (or the HLS handler file)
- Modify: `services/torrent-engine/internal/api/http/hls.go` (add per-manager limiter)

**Problem:** No rate limiting on segment or playlist GET requests. A client that polls segments aggressively (or a bug in HLS.js) can flood the server, triggering unnecessary FFmpeg loads.

**Approach:** Token bucket per client IP on the HLS watch endpoints. Use `golang.org/x/time/rate` — already available in Go ecosystem. Check if it's already a dependency.

### Step 1: Check existing dependencies

```bash
grep "golang.org/x/time" services/torrent-engine/go.mod
```

If not present, add it:

```bash
cd services/torrent-engine && go get golang.org/x/time/rate
```

### Step 2: Write the failing test

Add to `hls_test.go`:

```go
func TestSegmentLimiterAllow(t *testing.T) {
	lim := newSegmentLimiter(10, 5) // 10 req/s burst=5

	ip := "192.168.1.1"
	// First 5 requests should be allowed (burst).
	for i := 0; i < 5; i++ {
		if !lim.Allow(ip) {
			t.Fatalf("request %d should be allowed within burst", i+1)
		}
	}
	// 6th immediate request should be denied (burst exhausted, no refill yet).
	if lim.Allow(ip) {
		t.Fatalf("request 6 should be denied after burst exhausted")
	}
}

func TestSegmentLimiterIsolatesIPs(t *testing.T) {
	lim := newSegmentLimiter(10, 2)

	// IP A exhausts burst.
	lim.Allow("10.0.0.1")
	lim.Allow("10.0.0.1")

	// IP B should still have full burst.
	if !lim.Allow("10.0.0.2") {
		t.Fatalf("IP B should be allowed when IP A is throttled")
	}
}
```

### Step 3: Run the failing tests

```bash
cd services/torrent-engine && go test ./internal/api/http/ -run "TestSegmentLimiter" -v
```

Expected: **FAIL** — `newSegmentLimiter` is undefined.

### Step 4: Implement `segmentLimiter` in `hls.go`

Add after the imports:

```go
import (
    "golang.org/x/time/rate"
    // ... existing imports
)

// segmentLimiter enforces per-IP rate limits on HLS segment requests.
// It uses a token bucket: r tokens/sec sustained, burst allows b tokens at once.
type segmentLimiter struct {
    mu      sync.Mutex
    limiters map[string]*rate.Limiter
    r        rate.Limit
    b        int
}

func newSegmentLimiter(rps float64, burst int) *segmentLimiter {
    return &segmentLimiter{
        limiters: make(map[string]*rate.Limiter),
        r:        rate.Limit(rps),
        b:        burst,
    }
}

func (l *segmentLimiter) Allow(ip string) bool {
    l.mu.Lock()
    lim, ok := l.limiters[ip]
    if !ok {
        lim = rate.NewLimiter(l.r, l.b)
        l.limiters[ip] = lim
    }
    l.mu.Unlock()
    return lim.Allow()
}
```

Add a `segmentLimiter` field to `hlsManager`:

```go
type hlsManager struct {
    // ... existing fields ...
    segLimiter *segmentLimiter // rate limiter for segment/playlist requests
}
```

Initialize in `newHLSManager`:

```go
mgr.segLimiter = newSegmentLimiter(50, 20) // 50 req/s sustained, burst 20
```

### Step 5: Wire the limiter into the segment handler

In the HLS handler (wherever segment GETs are served — likely `hls_handler.go` or the watch route in `server.go`), add a check at the top of the segment handler:

```go
// Extract client IP for rate limiting.
clientIP, _, _ := net.SplitHostPort(r.RemoteAddr)
if clientIP == "" {
    clientIP = r.RemoteAddr
}
if !m.segLimiter.Allow(clientIP) {
    http.Error(w, "too many requests", http.StatusTooManyRequests)
    return
}
```

Find the exact handler function by running:

```bash
grep -n "seg-\|\.ts\|ServeSegment\|HandleSegment" services/torrent-engine/internal/api/http/*.go | head -20
```

Then apply the rate-limit check at the beginning of that handler, before any file I/O.

### Step 6: Run all tests

```bash
cd services/torrent-engine && go test ./internal/api/http/ -run "TestSegmentLimiter" -v
cd services/torrent-engine && go test ./internal/api/http/ -v 2>&1 | tail -20
```

Expected: all tests pass.

### Step 7: Commit

```bash
git add services/torrent-engine/internal/api/http/hls.go \
        services/torrent-engine/internal/api/http/hls_test.go \
        services/torrent-engine/go.mod services/torrent-engine/go.sum
git commit -m "$(cat <<'EOF'
feat(hls): Phase 11 — per-IP segment rate limiting (token bucket)

segmentLimiter implements a per-IP token bucket (50 req/s, burst 20)
applied to HLS segment/playlist requests. Prevents runaway HLS.js polling
or abusive clients from flooding FFmpeg job creation.

Co-Authored-By: Claude Sonnet 4.5 <noreply@anthropic.com>
EOF
)"
```

---

## Verification

After all 4 tasks, run the complete backend test suite:

```bash
cd services/torrent-engine && go test ./... 2>&1 | tail -30
```

All packages should pass. Then build to verify no compile errors:

```bash
cd services/torrent-engine && go build ./...
```

---

## Notes for Executor

- **Task 2** (profile hash): The `buildJobDir` method acquires `m.mu.RLock()` internally. `ensureJob` and `seekJob` both hold `m.mu.Lock()` — extract the hash computation **before** acquiring the lock, or use the unlocked field reads already present in those functions. Simplest: compute the hash at the start of each function before locking.

- **Task 4** (rate limiter): If `golang.org/x/time/rate` is not in `go.mod`, run `go get golang.org/x/time/rate` first. The token bucket will accumulate unused IP entries over time — a production improvement would be to evict entries older than 5 minutes, but for now the fixed burst/rate is sufficient.

- **Task 3** (last-segment): The 256KB threshold and 45s window are conservative. Adjust based on observed behavior. A 4K stream at 10 Mbps produces ~5MB per 4s segment; 256KB is well below any normal segment.
