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

**4 bugs fixed** (commit `017ce4a`, 2026-02-15):

### ✅ Search aggregator: no concurrency limit — FIXED
- **File**: `services/torrent-search/internal/search/aggregator.go`
- **Solution**: Added semaphore with maxConcurrentProviders=10. Prevents overwhelming system when Jackett has 20+ indexers.

### ✅ Torznab serial torrent-file downloads — FIXED
- **File**: `services/torrent-search/internal/providers/torznab/provider.go`
- **Solution**: Added prefetchMissingInfoHashes() with bounded concurrency (5 workers). RuTracker via Jackett/Prowlarr now downloads in parallel. 50 results: 200s → 40s.

### ✅ Cache warmer runs sequentially — FIXED
- **File**: `services/torrent-search/internal/search/cache.go`
- **Solution**: Parallelized runWarmCycle with semaphore (3 workers). Added context cancellation checks. 12 queries: 180s → 60s.

### ✅ Cache stale-while-revalidate race — FIXED
- **File**: `services/torrent-search/internal/search/cache.go`
- **Solution**: Added sync.Once per cache entry (refreshOnce field). Ensures only one refresh per stale period, prevents duplicate work.

---

**Remaining P1 bugs (lower priority):**

### Abandoned AddMagnet after timeout
- **File**: `services/torrent-engine/internal/services/torrent/engine/anacrolix/engine.go`
- `Open()` (line 190) returns timeout error, but background goroutine may still complete `AddMagnet`. Torrent added to engine without caller knowing.
- **Fix**: Track pending operations; remove torrent if caller timed out.

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

**Work completed** (2026-02-15):

### ✅ VideoPlayer memory leaks audit — PASSED
- **File**: `frontend/src/components/VideoPlayer.tsx`
- **Result**: No memory leaks found. All event listeners have matching cleanup, HLS instances properly destroyed, timers properly cleared.
- **Created hooks** (not yet integrated):
  - `useWatchPositionSave` - Autosave watch position logic
  - `useKeyboardShortcuts` - Keyboard shortcut handling

### 📋 Refactoring documentation created
- **File**: `docs/frontend-refactoring-guide.md`
- Comprehensive guide for refactoring VideoPlayer (~1941 lines) and SearchPage (~1568 lines)
- Detailed hook extraction plan with interfaces and benefits
- Implementation strategy and testing recommendations
- **Status**: Documentation complete, implementation deferred due to regression risk

**Remaining P2 items (deferred - low priority):**

### VideoPlayer component refactoring (deferred)
- **File**: `frontend/src/components/VideoPlayer.tsx`
- See `docs/frontend-refactoring-guide.md` for detailed plan
- Two hooks already created in `frontend/src/hooks/`
- **Risk**: High - complex component, significant regression potential
- **Recommendation**: Defer unless specific bugs arise

### SearchPage component refactoring (deferred)
- **File**: `frontend/src/pages/SearchPage.tsx`
- See `docs/frontend-refactoring-guide.md` for detailed plan
- **Risk**: Medium-High - 25 state variables, complex filtering logic
- **Recommendation**: Defer unless specific bugs arise

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
