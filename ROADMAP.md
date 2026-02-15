# Roadmap

Technical debt and improvements identified via codebase audit (2026-02-15).

**Last updated:** 2026-02-15

---

## ✅ P0 — Critical: Data Integrity & Resource Leaks (COMPLETED)

All P0 bugs have been fixed as of 2026-02-15:

### ✅ HLS reader double-close — FIXED
- **File**: `services/torrent-engine/internal/api/http/hls.go`
- **Solution**: Removed `defer result.Reader.Close()` and added explicit `Close()` calls on each exit path (subtitle error, cmd.Start failure). Reader is closed when `!useReader` or passed to ffmpeg stdin when `useReader=true`.

### ✅ CreateTorrent race on duplicate magnets — FIXED
- **File**: `services/torrent-engine/internal/usecase/create_torrent.go`
- **Solution**: Added `domain.ErrAlreadyExists` error type. Repository.Create now detects `mongo.IsDuplicateKeyError` and returns `ErrAlreadyExists`. CreateTorrent catches this error and re-fetches the existing record.

### ✅ StreamTorrent session leak on Start failure — FIXED
- **File**: `services/torrent-engine/internal/usecase/stream_torrent.go`
- **Solution**: Added `session.Stop()` cleanup in error path when `session.Start()` fails.

### ✅ DeleteTorrent: files removed before DB record — FIXED
- **File**: `services/torrent-engine/internal/usecase/delete_torrent.go`
- **Solution**: Swapped operation order: DB record is deleted first, then files. This prevents orphaned DB records if file deletion fails. Also fixed `removeTorrentFiles` to accumulate errors instead of aborting on first failure.

### ✅ MongoDB Update silent no-op — FIXED
- **File**: `services/torrent-engine/internal/repository/mongo/repository.go`
- **Solution**: Added `res.MatchedCount == 0` check. Returns `domain.ErrNotFound` when document doesn't exist, matching the pattern used in `UpdateTags` and `Delete`.

---

## P1 — High: Reliability & Performance

**Top-3 priority bugs fixed** (commit `73644ac`, 2026-02-15):

### ✅ Goroutine leak in anacrolix waitForInfo — FIXED
- **File**: `services/torrent-engine/internal/services/torrent/engine/anacrolix/engine.go`
- **Solution**: Added `metadataWaitTimeout` (10 min). Changed `waitForInfo` to use `select` with `time.After`. Zero-peer torrents now timeout and cleanup automatically.

### ✅ ffprobe retry blocks HLS startup — FIXED
- **File**: `services/torrent-engine/internal/api/http/hls.go`
- **Solution**: Added `codecCache` map with RWMutex. Created `isH264FileWithCache()` and `isAACAudioWithCache()` methods. Cache eliminates repeated 6s ffprobe calls.

### ✅ SSE ignores client disconnect — FIXED
- **File**: `services/torrent-search/internal/api/http/server.go`
- **Solution**: Changed `writeSSEEvent` to return error. Added error checking on all calls + `r.Context().Done()` checks between phases. Search terminates on disconnect.

---

**Remaining P1 bugs (lower priority):**

### Abandoned AddMagnet after timeout
- **File**: `services/torrent-engine/internal/services/torrent/engine/anacrolix/engine.go`
- `Open()` (line 190) returns timeout error, but background goroutine may still complete `AddMagnet`. Torrent added to engine without caller knowing.
- **Fix**: Track pending operations; remove torrent if caller timed out.

### Torznab serial torrent-file downloads
- **File**: `services/torrent-search/internal/providers/torznab/provider.go`
- InfoHash fallback (line 254) downloads up to 2MB torrent files per result, 4s timeout each, no concurrency limit. 50 results = +200s.
- **Fix**: Fan out with bounded concurrency (semaphore, e.g. 5 parallel). Skip download if result already has magnet.

### Search aggregator: no concurrency limit
- **File**: `services/torrent-search/internal/search/aggregator.go`
- Fan-out (line 172) spawns unlimited goroutines. Jackett with 20 indexers = 20 simultaneous requests.
- **Fix**: Use `golang.org/x/sync/semaphore` or a worker pool with configurable max concurrency.

### Cache warmer runs sequentially
- **File**: `services/torrent-search/internal/search/cache.go`
- `runWarmCycle` (line 78) refreshes queries one by one. With 12 queries x 15s each, cycle takes 180s on a 5min interval.
- **Fix**: Parallelize with bounded concurrency. Respect context cancellation between queries.

### Cache stale-while-revalidate race
- **File**: `services/torrent-search/internal/search/cache.go`
- `entry.refreshing = true` (line 170) is set under read-side lock but not atomically. Two requests can both trigger background refresh.
- **Fix**: Use `sync.Once` per entry, or check-and-set under write lock.

---

## P2 — Medium: Frontend Stability

**3 bugs fixed** (commit `2a61f52`, 2026-02-15):

### ✅ Missing AbortController in useSessionState — FIXED
- **File**: `frontend/src/hooks/useSessionState.ts`
- **Solution**: Added AbortController ref. getTorrentState accepts AbortSignal. Aborts pending requests on unmount/new poll. Prevents stale responses updating unmounted components.

### ✅ API client: mutation deduplication ignores body — FIXED
- **File**: `frontend/src/api.ts`
- **Solution**: Removed deduplication for mutations (POST/PUT/DELETE). Mutations execute independently. Deleted inflightMutations map.

### ✅ WebSocket reconnect timer leak — FIXED
- **File**: `frontend/src/hooks/useWebSocket.ts`
- **Solution**: Clear existing reconnect timer before scheduling new one in onclose. Prevents timer accumulation during connection flaps.

---

**Remaining P2 items (refactoring/optimization):**

### VideoPlayer component too large (~2000 lines)
- **File**: `frontend/src/components/VideoPlayer.tsx`
- 29 `useState` + 33 `useRef`. Mixes HLS management, keyboard shortcuts, timeline preview, screenshot, watch-position saving.
- **Fix**: Extract `useHlsPlayer`, `useKeyboardShortcuts`, `useTimelinePreview`, `useWatchPositionSave` hooks. Consider `useReducer` for player state machine.

### SearchPage too large (1569 lines)
- **File**: `frontend/src/pages/SearchPage.tsx`
- Inline ranking profile logic, filter presets, SSE streaming, results rendering.
- **Fix**: Extract `useSearchRankingProfile`, `useSearchFilterPresets`, `useSearchStream` hooks. Split `SearchResults` into a subcomponent.

### VideoPlayer memory leaks (partial - needs audit)
- **File**: `frontend/src/components/VideoPlayer.tsx`
- Preview canvas HLS instance, some fullscreen event listeners, and keyboard handlers may not clean up in all unmount paths.
- **Fix**: Audit all `addEventListener` calls for matching `removeEventListener` in cleanup. Null out refs.

---

## P3 — Low: Hardening & Polish

### CORS wildcard
- **File**: `services/torrent-engine/internal/api/http/server.go`
- `Access-Control-Allow-Origin: *` with no restrictions.
- **Fix**: Use origin allowlist or reflect request origin for self-hosted deployments.

### ffprobe without explicit timeout
- **File**: `services/torrent-engine/internal/services/torrent/engine/ffprobe/ffprobe.go`
- `runProbe` (line 61) relies on caller's context for timeout. Some callers don't set a deadline.
- **Fix**: Wrap with `context.WithTimeout` internally (e.g. 30s max).

### No provider rate limiting
- **File**: `services/torrent-search/internal/search/aggregator.go`
- No token bucket / leaky bucket. Risk of IP bans or API key revocation (Jackett/Prowlarr).
- **Fix**: Per-provider rate limiter (`golang.org/x/time/rate`).

### x1337 provider: fragile HTML regex
- **File**: `services/torrent-search/internal/providers/x1337/provider.go`
- Regex patterns (line 24-32) assume specific HTML structure. Hardcoded 40-entry scan cap.
- **Fix**: Use `goquery` for HTML parsing. Make scan cap configurable.

### Redis cache errors swallowed
- **File**: `services/torrent-search/internal/search/cache.go`
- `_ = s.redisCache.Set(...)` (line 202). Silent failures leave Redis and in-memory cache inconsistent.
- **Fix**: Log Redis errors. Consider falling back to memory-only gracefully.

### No query length limit on search API
- **File**: `services/torrent-search/internal/api/http/server.go`
- No max length on `q` parameter (line 140). Extremely long query strings can cause issues.
- **Fix**: Reject queries longer than 500 characters.

### Domain model lacks validation
- **File**: `services/torrent-engine/internal/domain/record.go`
- No `Validate()` method. No constraint that `TotalBytes >= DoneBytes`, no status transition guard.
- **Fix**: Add `Validate() error` to `TorrentRecord`. Enforce invariants at domain boundary.

### Frontend accessibility
- **File**: `frontend/src/components/VideoPlayer.tsx`
- Missing `aria-label` on control buttons, error banner lacks `role="alert"`, seek position not announced.
- **Fix**: Add ARIA attributes to interactive elements. Use `aria-live` for dynamic status changes.

### SyncState race on concurrent updates
- **File**: `services/torrent-engine/internal/usecase/sync_state.go`
- Read-modify-write on `DoneBytes` (line 82) without transaction isolation. Concurrent syncs can overwrite each other.
- **Fix**: Use MongoDB `$max` operator for `doneBytes` update instead of read-modify-write.

### mapFiles swallows panics silently
- **File**: `services/torrent-engine/internal/services/torrent/engine/anacrolix/engine.go`
- `recover()` in `mapFiles` (line 619) returns nil without logging. Hides bugs in anacrolix library.
- **Fix**: Log panic stack trace before returning nil.
