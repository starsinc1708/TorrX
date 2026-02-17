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

### ✅ Abandoned AddMagnet after timeout — FIXED
- **File**: `services/torrent-engine/internal/services/torrent/engine/anacrolix/engine.go`
- **Solution**: Added cleanup goroutines on timeout/cancel paths in `Open()`. When the caller times out or cancels, a background goroutine waits for AddMagnet to complete and calls `t.Drop()` if a torrent was added.

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

**8 items fixed** (2026-02-17):

### ✅ CORS wildcard — FIXED
- **File**: `services/torrent-engine/internal/api/http/middleware.go`
- **Solution**: Reflect request `Origin` header instead of wildcard `*`. Sets `Vary: Origin` for correct caching.

### ✅ ffprobe without explicit timeout — FIXED
- **File**: `services/torrent-engine/internal/services/torrent/engine/ffprobe/ffprobe.go`
- **Solution**: Added `maxProbeTimeout` (30s). `runProbe` wraps context with `context.WithTimeout` when caller doesn't set a deadline.

### ✅ Per-provider rate limiting — FIXED
- **File**: `services/torrent-search/internal/search/provider.go`, `aggregator.go`
- **Solution**: Per-provider `rate.Limiter` (2 req/s, burst 5). Applied in both `executePreparedSearch` and `executeStreamSearch` goroutines before calling `provider.Search()`.

### ✅ Redis cache errors logged — FIXED
- **File**: `services/torrent-search/internal/search/cache.go`
- **Solution**: Replaced `_ = s.redisCache.Set(...)` with `slog.Warn` on error. Memory cache still works as fallback.

### ✅ Search query length limit — FIXED
- **File**: `services/torrent-search/internal/api/http/server.go`
- **Solution**: Added `maxQueryLength = 500`. Rejects queries > 500 chars with HTTP 400 in both `/search` and `/search/stream`.

### ✅ Domain model validation — FIXED
- **File**: `services/torrent-engine/internal/domain/record.go`
- **Solution**: Added `Validate() error` to `TorrentRecord`. Checks: non-empty ID, non-negative bytes, `DoneBytes <= TotalBytes`, valid status enum.

### ✅ SyncState race with MongoDB $max — FIXED
- **Files**: `sync_state.go`, `ports/repository.go`, `repository/mongo/repository.go`
- **Solution**: Added `UpdateProgress` method using MongoDB `$max` operator for `doneBytes`. Eliminates read-modify-write race. Added `ProgressUpdate` domain type.

### ✅ mapFiles panic logging — FIXED
- **File**: `services/torrent-engine/internal/services/torrent/engine/anacrolix/engine.go`
- **Solution**: Added `slog.Error` with stack trace in `recover()` block instead of silently returning nil.

**Remaining P3 items (deferred):**

### x1337 provider: fragile HTML regex
- **File**: `services/torrent-search/internal/providers/x1337/provider.go`
- Regex patterns (line 24-32) assume specific HTML structure. Hardcoded 40-entry scan cap.
- **Fix**: Use `goquery` for HTML parsing. Make scan cap configurable.

### Frontend accessibility
- **File**: `frontend/src/components/VideoPlayer.tsx`
- Missing `aria-label` on control buttons, error banner lacks `role="alert"`, seek position not announced.
- **Fix**: Add ARIA attributes to interactive elements. Use `aria-live` for dynamic status changes.
