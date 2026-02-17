# Player & Streaming Pipeline Optimization Plan

## 1. Stream copy for partially downloaded H.264 files

### Problem

When a torrent file is not fully downloaded, FFmpeg receives data through `pipe:0` (stdin) or an internal HTTP endpoint. In both cases stream copy is impossible — FFmpeg always re-encodes with libx264, even if the source is already H.264. This is the single biggest performance bottleneck: every playback session burns CPU on unnecessary transcoding.

The decision logic lives in `hls.go:552-555`:
```go
streamCopy := false
if isLocalFile && key.subtitleTrack < 0 && m.isH264FileWithCache(input) {
    streamCopy = true
}
```
`isLocalFile` is only true when the entire file exists on disk (`info.Size() >= result.File.Length` at line 475). For partially downloaded files, `input = "pipe:0"` and `isLocalFile = false`.

### Root cause

The completeness check at `hls.go:473-478` is binary: either the file is 100% on disk, or we fall back to pipe. There's no intermediate state where FFmpeg can read directly from a partial file.

### Solution: early direct-path handoff

**Approach A — Wait for header availability (recommended):**

The sliding priority reader already prioritizes the beginning of the file. For most video containers (MP4, MKV), FFmpeg only needs the first few MB (moov atom / matroska header) plus data at the seek position to start stream copy.

**Implementation steps:**

1. **Add a "sufficient for FFmpeg" check** in `hls.go` (around line 470):
   ```go
   // Instead of requiring 100% completion, check:
   // 1. File exists on disk (even partially via spill)
   // 2. File size >= some threshold OR piece availability covers seek range
   canDirectRead := false
   if m.dataDir != "" {
       candidatePath, pathErr := resolveDataFilePath(m.dataDir, result.File.Path)
       if pathErr == nil {
           if info, statErr := os.Stat(candidatePath); statErr == nil && !info.IsDir() {
               // Full file on disk
               if result.File.Length <= 0 || info.Size() >= result.File.Length {
                   canDirectRead = true
               }
               // Partial file: check if enough data available for FFmpeg header + seek region
               if !canDirectRead && info.Size() > 0 {
                   headerReady := info.Size() >= 10*1024*1024 // 10 MB minimum for container header
                   if headerReady && job.seekSeconds == 0 {
                       canDirectRead = true // FFmpeg will block-read; pieces are being prioritized
                   }
               }
           }
       }
   }
   ```

2. **Enable stream copy for partial direct-read files** — extend the `streamCopy` condition:
   ```go
   if canDirectRead && key.subtitleTrack < 0 && m.isH264FileWithCache(candidatePath) {
       streamCopy = true
   }
   ```

3. **Handle read stalls gracefully** — when FFmpeg reads a byte range not yet downloaded, the OS read() will either block (if anacrolix memory provider is FUSE-backed) or return short/error. For spill-to-disk files, the anacrolix reader blocks until the piece is available. For direct disk files, we need to ensure the torrent engine keeps downloading ahead of FFmpeg's read position.

   Add a background goroutine that monitors FFmpeg's read position (via `/proc/{pid}/fdinfo` or by tracking m3u8 segment count) and adjusts piece priority accordingly.

4. **Fallback** — if FFmpeg fails to start within 15 seconds with direct path, fall back to pipe mode. This is already partially handled by the 120s startup timeout.

**Approach B — Named pipe / FUSE (higher complexity):**

Create a virtual file (via `mkfifo` or FUSE mount) that exposes the torrent file as a seekable file descriptor. The FUSE handler translates `read(offset, len)` into piece priority requests + blocking reads from the torrent engine.

- Advantage: FFmpeg sees a regular file, stream copy always possible
- Disadvantage: FUSE adds latency, complexity, Linux-only, not available in Alpine Docker without extra packages

**Recommendation:** Start with Approach A. It covers ~80% of cases (start-from-beginning playback of local H.264 files) with minimal code changes.

### Files to modify

| File | Change |
|------|--------|
| `services/torrent-engine/internal/api/http/hls.go` | Extend completeness check (lines 467-492), allow partial direct read |
| `services/torrent-engine/internal/api/http/hls.go` | Extend stream copy condition (lines 552-555) |
| `services/torrent-engine/internal/usecase/stream_torrent.go` | Expose piece availability check for byte ranges |

### Expected impact

- Eliminates re-encoding for ~70-80% of playback sessions (most torrents contain H.264+AAC)
- Reduces CPU usage from ~100-200% (libx264) to ~5% (stream copy muxing)
- Reduces HLS startup time from 2-4s (first encoded segment) to <1s (first muxed segment)
- Reduces segment size by ~40% (stream copy preserves original bitrate vs CRF re-encode at potentially higher bitrate)

---

## 2. O(1) cache eviction (heap instead of full scan)

### Problem

`hls_cache.go` eviction (`evict()`, lines 389-460) collects ALL cached segments into a flat array, sorts by mtime, then iterates to remove oldest. This is O(n log n) where n = total segments in cache.

With default 10 GB cache and 4-second segments at ~2 MB each, that's ~5000 segments. Every `Store()` call (line 231-235) that pushes total size over the limit triggers a full eviction pass. During active playback with live caching (`cacheSegmentsLive`, every 2 seconds), this means a full sort of 5000 items every 2 seconds.

### Solution: min-heap by mtime

Replace the flat-array-sort approach with a persistent min-heap ordered by modification time.

**Implementation steps:**

1. **Add a min-heap field to hlsCache struct:**
   ```go
   type hlsCache struct {
       // ... existing fields ...
       evictionHeap evictionMinHeap  // min-heap ordered by mtime
   }

   type evictionEntry struct {
       path    string
       mtime   time.Time
       size    int64
       heapIdx int  // for heap.Fix() after updates
       // keys for index removal:
       torrentID string
       fileIndex int
       trackKey  string
       segIdx    int
   }

   type evictionMinHeap []*evictionEntry
   // Implement heap.Interface: Len, Less (by mtime), Swap, Push, Pop
   ```

2. **Populate heap during rebuild()** (lines 100-162):
   - After adding each segment to the index, also push an `evictionEntry` to the heap.

3. **Update heap on Store()** (lines 189-238):
   - After adding segment to index, push entry to heap.
   - Replace lines 231-235 eviction trigger with:
     ```go
     for c.totalSize > c.maxBytes && c.evictionHeap.Len() > 0 {
         oldest := heap.Pop(&c.evictionHeap).(*evictionEntry)
         c.removeSegmentByEntry(oldest)
     }
     ```

4. **Add max-age eviction to a periodic goroutine** instead of inline:
   - Run every 60 seconds, pop all entries with `mtime < now - maxAge`.
   - This avoids checking age on every Store().

5. **Handle segment access (touch) for LRU-like behavior:**
   - Currently eviction is pure mtime-based (not LRU). If LRU behavior is desired, update mtime on segment access and call `heap.Fix()`.
   - For now, mtime-based is sufficient since segments are immutable once written.

### Files to modify

| File | Change |
|------|--------|
| `services/torrent-engine/internal/api/http/hls_cache.go` | Add evictionMinHeap type, implement heap.Interface |
| `services/torrent-engine/internal/api/http/hls_cache.go` | Modify rebuild() to populate heap |
| `services/torrent-engine/internal/api/http/hls_cache.go` | Replace evict() with heap-based O(log n) eviction |
| `services/torrent-engine/internal/api/http/hls_cache.go` | Modify Store() to push to heap |

### Expected impact

- Eviction drops from O(n log n) to O(log n) per segment
- Eliminates periodic freezes during playback on large caches
- Startup rebuild is unchanged (still O(n)) but runs only once

---

## 3. Cache rewritten playlists

### Problem

`rewritePlaylistSegmentURLs()` (server.go lines 2276-2300) runs on EVERY m3u8 GET request. It splits the playlist into lines, iterates all lines, appends query parameters to segment URLs, and joins back. For a 1-hour video with 4-second segments = 900 segments = 900+ lines parsed per request.

HLS.js polls the playlist every `hls_time` interval (4 seconds by default), so this parsing runs ~15 times per minute per client.

### Solution: cache rewritten playlist per job

**Implementation steps:**

1. **Add a rewritten playlist cache to hlsJob:**
   ```go
   type hlsJob struct {
       // ... existing fields ...
       rewrittenPlaylistMu   sync.RWMutex
       rewrittenPlaylist     []byte
       rewrittenPlaylistMod  time.Time
       rewrittenAudioTrack   int
       rewrittenSubTrack     int
   }
   ```

2. **In the playlist serving handler**, before calling `rewritePlaylistSegmentURLs`:
   ```go
   job.rewrittenPlaylistMu.RLock()
   if job.rewrittenPlaylist != nil &&
      job.rewrittenAudioTrack == audioTrack &&
      job.rewrittenSubTrack == subtitleTrack {
       // Check if underlying playlist file changed
       if info, err := os.Stat(playlistPath); err == nil {
           if !info.ModTime().After(job.rewrittenPlaylistMod) {
               cached := job.rewrittenPlaylist
               job.rewrittenPlaylistMu.RUnlock()
               w.Write(cached)
               return
           }
       }
   }
   job.rewrittenPlaylistMu.RUnlock()

   // Cache miss: read, rewrite, cache
   raw, _ := os.ReadFile(playlistPath)
   rewritten := rewritePlaylistSegmentURLs(raw, audioTrack, subtitleTrack)

   job.rewrittenPlaylistMu.Lock()
   job.rewrittenPlaylist = rewritten
   job.rewrittenPlaylistMod = time.Now()
   job.rewrittenAudioTrack = audioTrack
   job.rewrittenSubTrack = subtitleTrack
   job.rewrittenPlaylistMu.Unlock()
   ```

3. **Invalidation**: The cache is automatically invalidated by mtime check. When FFmpeg writes a new segment and updates the playlist, the next request will see a newer mtime and re-parse.

### Files to modify

| File | Change |
|------|--------|
| `services/torrent-engine/internal/api/http/hls.go` | Add cache fields to hlsJob struct |
| `services/torrent-engine/internal/api/http/server.go` | Add cache check before rewritePlaylistSegmentURLs() |

### Expected impact

- Reduces playlist serving from O(segments) string operations to O(1) cache hit
- Most impactful for long videos (1000+ segments) with multiple clients

---

## 4. Persistent ffprobe codec cache

### Problem

`isH264FileWithCache()` and `isAACAudioWithCache()` (hls.go lines 1373-1420) cache ffprobe results in an in-memory map. On service restart, the cache is lost, and the first playback of each file triggers up to 3 ffprobe calls with 2-second retries = 6 seconds of blocking delay.

The codec of a file never changes, so these results are safe to persist indefinitely.

### Solution: disk-backed codec cache

**Implementation steps:**

1. **Choose storage format** — a simple JSON file alongside the HLS baseDir:
   ```
   {baseDir}/codec_cache.json
   ```
   Structure:
   ```json
   {
     "/data/torrent-abc/video.mkv": {"h264": true, "aac": false, "w": 1920, "h": 1080},
     "/data/torrent-xyz/movie.mp4": {"h264": true, "aac": true, "w": 1280, "h": 720}
   }
   ```

2. **Load on startup** in `newHLSManager()` (hls.go line 135):
   ```go
   m.loadCodecCache()  // reads codec_cache.json into m.codecCache + m.resolutionCache
   ```

3. **Save periodically** — after each new ffprobe result, schedule a debounced write (e.g., 5 seconds after last update):
   ```go
   func (m *hlsManager) scheduleCodecCacheSave() {
       // Use sync.Once or timer to coalesce multiple updates
       // Write JSON atomically (write to tmp, rename)
   }
   ```

4. **Add max-size limit** to both in-memory caches — LRU with 2000 entries:
   ```go
   const maxCodecCacheEntries = 2000

   func (m *hlsManager) isH264FileWithCache(filePath string) bool {
       // ... existing cache check ...
       // After adding new entry:
       if len(m.codecCache) > maxCodecCacheEntries {
           m.evictOldestCodecEntry()
       }
   }
   ```
   For simplicity, evict random entries when over limit (codec detection is cheap enough that occasional re-probes are fine).

### Files to modify

| File | Change |
|------|--------|
| `services/torrent-engine/internal/api/http/hls.go` | Add loadCodecCache/saveCodecCache methods |
| `services/torrent-engine/internal/api/http/hls.go` | Add LRU size limit to codecCache and resolutionCache |
| `services/torrent-engine/internal/api/http/hls.go` | Call loadCodecCache in newHLSManager() |

### Expected impact

- Eliminates 2-6 second delay on first playback after service restart
- Prevents unbounded memory growth of codec/resolution caches

---

## 5. Adaptive readahead window

### Problem

The sliding priority reader (`sliding_priority_reader.go`) uses a fixed window size calculated once at stream creation:
```go
window = readahead * 4  // = 16 MB * 4 = 64 MB, clamped to [32 MB, 256 MB]
```

This doesn't account for:
- Download speed: slow peers → window should be larger to give more lead time
- Playback bitrate: high-bitrate content consumes the window faster
- Seek patterns: after seek, window should be larger initially then shrink

### Solution: dynamic window adjustment

**Implementation steps:**

1. **Add download speed tracking** to the sliding priority reader:
   ```go
   type slidingPriorityReader struct {
       // ... existing fields ...
       bytesReadSinceLastUpdate int64
       lastUpdateTime           time.Time
       effectiveBytesPerSec     float64  // smoothed read throughput
   }
   ```

2. **Compute effective throughput** in `updatePriorityWindowLocked()`:
   ```go
   now := time.Now()
   elapsed := now.Sub(r.lastUpdateTime).Seconds()
   if elapsed > 0.5 {
       instantRate := float64(r.bytesReadSinceLastUpdate) / elapsed
       // Exponential moving average (alpha = 0.3)
       r.effectiveBytesPerSec = 0.7*r.effectiveBytesPerSec + 0.3*instantRate
       r.bytesReadSinceLastUpdate = 0
       r.lastUpdateTime = now
   }
   ```

3. **Adjust window dynamically:**
   ```go
   // Target: buffer 30 seconds of content ahead
   const targetBufferSeconds = 30.0
   dynamicWindow := int64(r.effectiveBytesPerSec * targetBufferSeconds)

   // Clamp to [minWindow, maxWindow]
   if dynamicWindow < r.minWindow { dynamicWindow = r.minWindow }
   if dynamicWindow > r.maxWindow { dynamicWindow = r.maxWindow }

   r.window = dynamicWindow
   ```

4. **Post-seek boost** — after seek, temporarily double the window:
   ```go
   func (r *slidingPriorityReader) Seek(offset int64, whence int) (int64, error) {
       // ... existing seek logic ...
       r.window = min(r.window * 2, r.maxWindow)  // boost after seek
       r.seekBoostUntil = time.Now().Add(10 * time.Second)
   }
   ```
   In `updatePriorityWindowLocked`, reduce boost after 10 seconds.

### Files to modify

| File | Change |
|------|--------|
| `services/torrent-engine/internal/usecase/sliding_priority_reader.go` | Add throughput tracking, dynamic window calculation |
| `services/torrent-engine/internal/usecase/stream_torrent.go` | Pass min/max window bounds from config |

### Expected impact

- Reduces buffer underruns on slow connections (larger window = more lead time)
- Reduces unnecessary piece prioritization on fast connections (smaller window = less wasted bandwidth)
- Faster recovery after seeks (temporary boost)

---

## 6. Intermediate buffer between torrent reader and FFmpeg

### Problem

The anacrolix torrent reader blocks `Read()` when requested pieces aren't downloaded yet. If a piece takes >12 seconds, the HLS watchdog triggers an auto-restart, killing FFmpeg and losing all encoded progress. The restart counter allows only 3 restarts before giving up entirely.

This is the main cause of playback stalls on slow connections or sparse swarms.

### Solution: buffered pipe with stall detection

**Implementation steps:**

1. **Create a buffered pipe wrapper:**
   ```go
   type bufferedStreamReader struct {
       source     io.ReadCloser  // underlying torrent reader
       buffer     *ring.Buffer   // ring buffer (e.g., 8 MB)
       fillCond   *sync.Cond     // signal when data available
       mu         sync.Mutex
       readPos    int64
       writePos   int64
       stallCount int
       ctx        context.Context
       cancel     context.CancelFunc
       logger     *slog.Logger
   }
   ```

2. **Background fill goroutine:**
   ```go
   func (b *bufferedStreamReader) fillLoop() {
       buf := make([]byte, 256*1024)  // 256 KB chunks
       for {
           n, err := b.source.Read(buf)
           if n > 0 {
               b.mu.Lock()
               b.buffer.Write(buf[:n])
               b.writePos += int64(n)
               b.fillCond.Broadcast()
               b.mu.Unlock()
           }
           if err != nil { return }
       }
   }
   ```

3. **Read with timeout instead of blocking indefinitely:**
   ```go
   func (b *bufferedStreamReader) Read(p []byte) (int, error) {
       b.mu.Lock()
       defer b.mu.Unlock()

       for b.buffer.Len() == 0 {
           // Wait with timeout instead of blocking forever
           done := make(chan struct{})
           go func() {
               b.fillCond.Wait()
               close(done)
           }()
           select {
           case <-done:
               // data available
           case <-time.After(30 * time.Second):
               b.stallCount++
               b.logger.Warn("stream buffer stall",
                   slog.Int("stallCount", b.stallCount),
                   slog.Int64("readPos", b.readPos))
               // Don't kill the job — just return what we have
               // FFmpeg will handle the underrun
               return 0, nil  // short read, FFmpeg will retry
           case <-b.ctx.Done():
               return 0, b.ctx.Err()
           }
       }

       n, _ := b.buffer.Read(p)
       b.readPos += int64(n)
       return n, nil
   }
   ```

4. **Wire into hls.go** where `cmd.Stdin = result.Reader` (line 689):
   ```go
   if useReader {
       buffered := newBufferedStreamReader(result.Reader, 8*1024*1024, ctx, m.logger)
       go buffered.fillLoop()
       cmd.Stdin = buffered
   }
   ```

5. **Expose buffer fill level** for monitoring:
   - Add a metrics gauge for buffer fill percentage
   - Log warnings when buffer drops below 25%

### Files to modify

| File | Change |
|------|--------|
| `services/torrent-engine/internal/api/http/hls.go` | New: bufferedStreamReader type |
| `services/torrent-engine/internal/api/http/hls.go` | Wire buffered reader in run() method (line 689) |
| `services/torrent-engine/internal/metrics/metrics.go` | Add buffer fill gauge |

### Expected impact

- Absorbs short network stalls (< buffer size / bitrate) without FFmpeg noticing
- Reduces false watchdog restarts from piece download latency spikes
- Provides visibility into buffer health via metrics

---

## 7. segmentTimeOffset index

### Problem

`segmentTimeOffset()` (hls.go lines 839-873) is called for every cached segment served. It parses the entire m3u8 playlist line by line, summing EXTINF durations until it finds the target segment. For a 1-hour video (900 segments), this means parsing 900 lines per segment request.

During playback, HLS.js requests segments sequentially — so this function runs 900×900 = 810,000 cumulative line iterations over a 1-hour session.

### Solution: precomputed cumulative time index

**Implementation steps:**

1. **Add a time index to hlsJob:**
   ```go
   type hlsJob struct {
       // ... existing fields ...
       timeIndexMu   sync.RWMutex
       timeIndex     map[string]float64  // segmentName → absolute start time
       timeIndexSize int                 // number of segments indexed
   }
   ```

2. **Build/update index lazily** when segmentTimeOffset is called:
   ```go
   func segmentTimeOffset(job *hlsJob, segmentName string) (float64, bool) {
       // Check existing index first
       job.timeIndexMu.RLock()
       if t, ok := job.timeIndex[segmentName]; ok {
           job.timeIndexMu.RUnlock()
           return t, true
       }
       job.timeIndexMu.RUnlock()

       // Parse playlist and rebuild full index
       segments, err := parseM3U8Segments(playlist)
       if err != nil { return 0, false }

       job.timeIndexMu.Lock()
       defer job.timeIndexMu.Unlock()

       // Rebuild only if playlist grew
       if len(segments) > job.timeIndexSize {
           cumTime := job.seekSeconds
           for i, seg := range segments {
               if i >= job.timeIndexSize {
                   // Only index new segments
               }
               if job.timeIndex == nil {
                   job.timeIndex = make(map[string]float64, len(segments))
               }
               job.timeIndex[seg.Filename] = cumTime
               cumTime += seg.Duration
           }
           job.timeIndexSize = len(segments)
       }

       if t, ok := job.timeIndex[segmentName]; ok {
           return t, true
       }
       return 0, false
   }
   ```

3. **Invalidation**: The index grows monotonically (HLS event playlist only appends). No invalidation needed — just extend when new segments appear.

### Files to modify

| File | Change |
|------|--------|
| `services/torrent-engine/internal/api/http/hls.go` | Add timeIndex fields to hlsJob |
| `services/torrent-engine/internal/api/http/hls.go` | Rewrite segmentTimeOffset() to use/build index |

### Expected impact

- Segment time lookup drops from O(n) to O(1) after first parse
- Playlist parsing happens at most once per new segment (amortized O(1))
- Most impactful for long videos and seek/resume operations

---

## 8. Reduce HLS.js backBufferLength

### Problem

Both HLS.js instantiation points in `VideoPlayer.tsx` set `backBufferLength: 120` (120 seconds). This means HLS.js keeps 2 minutes of already-played segments in the browser's SourceBuffer, consuming ~30-60 MB of RAM per session.

The default HLS.js value is 30 seconds. The 120s override was likely added for seek-back support, but server-side seek makes local back-seeking unreliable anyway (seekOffset shifts invalidate the local buffer).

### Solution: reduce to 30s, tie to user-configurable player buffer

**Implementation steps:**

1. **In VideoPlayer.tsx**, replace hardcoded `backBufferLength: 120` with a computed value:
   ```typescript
   const hlsMaxBuf = Number(localStorage.getItem('hlsMaxBufferLength')) || 60;
   const hls = new Hls({
     // ...
     backBufferLength: Math.min(hlsMaxBuf, 30),  // back buffer ≤ forward buffer, max 30s
     maxBufferLength: hlsMaxBuf,
     maxMaxBufferLength: hlsMaxBuf * 2,
     // ...
   });
   ```

2. **Apply to both instantiation points** (track-switch path ~line 886 and full-source path ~line 966).

### Files to modify

| File | Change |
|------|--------|
| `frontend/src/components/VideoPlayer.tsx` | Change `backBufferLength: 120` to `Math.min(hlsMaxBuf, 30)` in both HLS.js constructors |

### Expected impact

- Reduces browser memory usage by ~30-60 MB per session
- Improves performance on mobile/low-memory devices
- No functional regression: backward seeks beyond 30s already require server-side re-encoding

---

## Implementation order

```
Phase 1 — Quick wins (1-2 days):
  [8] Reduce backBufferLength          — trivial, immediate memory savings
  [4] Persistent ffprobe cache         — low complexity, eliminates startup delay
  [3] Cache rewritten playlists        — low complexity, reduces latency
  [7] segmentTimeOffset index          — low complexity, reduces CPU on long videos

Phase 2 — Medium effort (3-5 days):
  [2] Heap-based cache eviction        — medium complexity, eliminates playback pauses
  [5] Adaptive readahead window        — medium complexity, improves streaming stability

Phase 3 — High effort (1-2 weeks):
  [1] Stream copy for partial files    — medium-high complexity, biggest single improvement
  [6] Buffered pipe reader→FFmpeg      — high complexity, reduces stalls on slow connections
```
