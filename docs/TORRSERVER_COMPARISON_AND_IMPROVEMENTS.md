# TorrServer Comparison: Bugs Fixed and Improvement Plan

Comparison of `torrent-engine` with [TorrServer](https://github.com/YouROK/TorrServer) streaming architecture.
Identified issues, applied fixes, and remaining improvement opportunities.

---

## Fixed Issues

### 1. Memory Leak: `peakBitfield` and `rateLimits` Not Cleaned Up

**Files:** `engine.go` (3 locations: `dropTorrent`, `waitForInfo`, `evictIdleSessionLocked`)

`peakBitfield` map entries were never deleted when sessions were removed, causing
unbounded memory growth. `rateLimits` had the same issue in `waitForInfo` and eviction.

**Fix:** Added `delete(e.peakBitfield, id)` and `delete(e.rateLimits, id)` to all
three cleanup paths.

### 2. Permanently Paused Torrents After Focus Session Drop/Stop

**Files:** `engine.go` (`dropTorrent`, `StopSession`)

When `FocusSession` paused other downloading torrents (setting connections to 0),
dropping or stopping the focused torrent cleared `focusedID` but never resumed
the paused sessions. They remained with 0 connections indefinitely.

TorrServer avoids this with auto-disconnect timeouts that lazily reconnect on access.

**Fix:** Both `dropTorrent` and `StopSession` now detect when the focused session
is being removed/stopped and resume all paused torrents back to `ModeDownloading`.

### 3. Direct Streaming Premature EOF with `SetResponsive()`

**File:** `handlers_streaming.go`

`handleStreamTorrent` called `SetResponsive()` on the reader, which returns EOF
immediately when piece data isn't available. Combined with `io.Copy`/`io.CopyN`,
this silently truncated streams when seeking to undownloaded regions.

The HLS path handles this correctly via `bufferedStreamReader` which retries transient
EOFs with exponential backoff. Direct HTTP streaming had no such protection.

TorrServer uses a blocking reader that waits for pieces to arrive, plus a responsive
mode patch that returns partial data from incomplete pieces (never EOF for missing data).

**Fix:** Removed `SetResponsive()` from direct HTTP streaming. The reader now blocks
until piece data arrives, matching TorrServer's behavior. Added `Connection: close`
header to prevent keep-alive from holding readers open after playback stops.

### 4. Flat 2-Tier Priority System (Now Graduated 5-Tier)

**Files:** `domain/priority.go`, `anacrolix/priority.go`, `sliding_priority_reader.go`

All pieces in the priority window were set to `PriorityHigh` (`PiecePriorityNow`),
telling anacrolix "I need ALL 32-256 MB RIGHT NOW". The scheduler spread bandwidth
evenly across the entire window instead of focusing on the immediate read position.

TorrServer uses a 5-tier gradient:
```
Current piece      -> PiecePriorityNow       (immediate need)
Next piece         -> PiecePriorityNext       (next to be consumed)
Readahead window   -> PiecePriorityReadahead  (buffering ahead)
Beyond readahead   -> PiecePriorityHigh       (background prefetch)
Rest               -> PiecePriorityNormal     (low priority fill)
```

**Fix:** Added `PriorityNext` (3) and `PriorityReadahead` (2) to the priority enum.
Updated anacrolix mapper to use all 5 anacrolix priority levels. Rewrote
`slidingPriorityReader.applyGradientPriority()` to apply a 4-band gradient:
- First 2 MB: `PriorityHigh` (PiecePriorityNow)
- Next 2 MB: `PriorityNext` (PiecePriorityNext)
- Next 25% of window: `PriorityReadahead` (PiecePriorityReadahead)
- Remainder: `PriorityNormal` (PiecePriorityNormal)

### 5. File Boundary Deprioritization (Container Headers Lost)

**File:** `sliding_priority_reader.go`

The sliding reader aggressively deprioritized old window regions to `PriorityNone`,
including the file tail containing container headers (MP4 moov atoms, MKV seek
indices). After the reader moved forward past the initial preload window, these
pieces could be garbage collected, requiring re-download on the next seek.

TorrServer protects `max(pieceLength, 8 MB)` at both file boundaries from eviction.

**Fix:** Added `deprioritizeRange()` that clips deprioritization to exclude the first
and last 8 MB of the file (`fileBoundaryProtection = 8 MB`). These regions are
never set to `PriorityNone` regardless of reader position.

### 6. Goroutine Leak in `bufferedStreamReader` (Condvar Bridge)

**File:** `hls_buffered_reader.go`

The `Read()` method spawned a goroutine on each wait cycle to bridge `sync.Cond.Wait()`
to a channel. If context was cancelled while the goroutine was blocked in `cond.Wait()`,
and `Close()` hadn't been called yet (race between context cancellation and job cleanup),
the goroutine was permanently orphaned.

**Fix:** Replaced `sync.Cond` with a buffered channel (`dataCh chan struct{}, 1`).
The `signal()` method does a non-blocking send. `Read()` selects on `dataCh` and
`ctx.Done()` directly — no bridge goroutine needed. No possible leak path.

### 7. No Session Idle Timeout

**File:** `engine.go`

Sessions accumulated indefinitely with no cleanup. Each held peer connections,
memory buffers, and goroutines. Over days of use this caused resource exhaustion.

TorrServer disconnects torrents after 30s (capped at 60s) with no active readers,
then lazily reconnects on next access.

**Fix:** Added `IdleTimeout` config field and `idleReaper` background goroutine.
It scans every `timeout/2` (min 10s) and stops sessions that haven't been accessed
within the configured timeout. Focused, stopped, and completed sessions are never reaped.

### 8. No Explicit Memory Reclamation

**File:** `engine.go`

Go's default GC may not return freed memory to the OS promptly. On memory-constrained
systems (Docker, NAS), this causes OOM kills even when data is no longer needed.

TorrServer calls `runtime.GC()` + `debug.FreeOSMemory()` after every piece eviction.

**Fix:** Added `freeOSMemory()` helper (GC + FreeOSMemory) called after `dropTorrent`
completes. This returns freed memory to the OS immediately after session cleanup.

### 9. Codec Cache Random Eviction (Now LRU)

**File:** `hls.go`, `hls_encoding.go`

`evictCodecCacheIfNeeded()` iterated the map and deleted entries in random order
(Go map iteration is unordered). This evicted recently-used entries while keeping
stale ones.

**Fix:** Added `lastAccess time.Time` to `codecCacheEntry`. Cache reads update the
timestamp. Eviction now sorts by `lastAccess` ascending and removes oldest entries
first (LRU).

---

## Architecture Comparison: Key Design Differences

| Aspect | TorrServer | torrent-engine |
|--------|-----------|----------------|
| Streaming model | Raw HTTP via `http.ServeContent` | HLS via FFmpeg transcoding + raw HTTP fallback |
| Piece cache | Custom per-torrent (64 MB, LRU eviction) | anacrolix storage layer (disk/memory/hybrid) |
| Priority tiers | 5 (Now/Next/Readahead/High/Normal) | **5 (after fix)** |
| Reader mode | Blocking + responsive patch (partial reads) | Blocking for HTTP, responsive + retry for HLS |
| Session lifecycle | Auto-disconnect (30-60s idle) | **Idle reaper (configurable, after fix)** |
| Bandwidth focus | Hard-pause all except focused torrent | Same (FocusSession with hard-pause) |
| Preload strategy | Dual-range: file start + tail (moov atoms) | HLS preloads tail; direct streaming via priority boost |
| Memory mgmt | Explicit GC + FreeOSMemory after eviction | **GC after session drop (after fix)** |
| Peer masquerade | qBittorrent user agent | Default anacrolix user agent |
| Cache persistence | BBolt/JSON DB for torrents + settings | MongoDB for torrents + settings |

---

## Remaining Improvement Opportunities

### Implemented (P1)

#### 10. Dual-Range Preload for Direct Streaming

**Files:** `stream_torrent.go`

TorrServer preloads both the file start AND file tail when a torrent is first
accessed. The HLS path already had tail preload via `preloadFileEnds()`, but
direct streaming via `/stream` had no tail preload at all.

**Fix:** Added tail preload in `StreamTorrent.Execute()`. After setting the initial
priority window (which covers the file start), the last 16 MB of the file is boosted
to `PriorityReadahead` for files larger than 32 MB. This ensures container metadata
(MP4 moov atoms, MKV SeekHead/Cues) is available before the player seeks to the end.

#### 11. Reader Dormancy System

**Files:** `reader_dormancy.go` (new), `sliding_priority_reader.go`, `stream_torrent.go`

TorrServer puts idle readers to sleep after 60s when multiple readers exist on the
same torrent. Without dormancy, idle readers waste bandwidth requesting pieces that
no one is consuming, starving the active reader.

**Fix:** Added `readerRegistry` that tracks active readers per torrent. Each reader
records `lastAccess` on every Read/Seek. Active readers periodically check peers
(every 5s) and put idle ones (>60s) to sleep: readahead set to 0, priority window
deprioritized. When a dormant reader receives its next Read or Seek, it wakes
immediately: readahead is restored and the priority window reapplied. Single readers
are never put to sleep. `StreamTorrent.Execute` changed to pointer receiver with
lazy-initialized shared registry.

### P2 — Medium Impact

#### Connection Limit Per Reader
TorrServer limits concurrent downloading blocks per reader to
`ConnectionsLimit / numReaders`. With 25 connections and 2 readers, each gets 12.
This prevents one reader from starving another.

**Recommendation:** Expose a per-session connection limit setter in the Engine port
and adjust based on active reader count.

#### DLNA Headers on Streaming Responses
TorrServer adds DLNA compatibility headers (`transferMode.dlna.org: Streaming`,
`contentFeatures.dlna.org`) to streaming responses for smart TV and media center
compatibility.

**Recommendation:** Add DLNA headers to `handleStreamTorrent` for devices that need them.

#### User Agent Masquerade
TorrServer uses `qBittorrent/4.3.9` as the peer user agent. Some trackers and peers
may treat unknown clients differently.

**Recommendation:** Configure the anacrolix client config `PeerID` and `HTTPUserAgent`
to mimic a popular torrent client.

### P3 — Low Impact / Nice-to-Have

#### Adaptive Readahead Based on Peer Count
TorrServer had (now disabled) code to adjust readahead based on active peer count:
`pieceLength * activePeers / (1 + readers)`. This could help on torrents with few peers.

#### File Size Limit Guard
TorrServer has a `MaxSize` setting that returns HTTP 403 for files exceeding a
configured limit. This prevents accidental streaming of very large files.

#### Memory Cache High-Water Mark
The `peakCompleted` high-water mark pattern works well for byte counts but the
current speed sampling can briefly show 0 speed during post-restart re-verification
when byte counters reset. Consider tracking speed independently of library stats.
