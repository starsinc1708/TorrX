# Roadmap

Technical debt and improvements identified via codebase audit (2026-02-15).

---

## P0 — Critical: Data Integrity & Resource Leaks

### HLS reader double-close
- **File**: `services/torrent-engine/internal/api/http/hls.go`
- `result.Reader` has both a `defer Close()` (line 373) and a manual `Close()` (line 509 when `!useReader`). Early-return paths (line 402-407) also call `Close()`, causing double-close panics.
- **Fix**: Remove the defer; close explicitly on each exit path, or use a `sync.Once`-guarded closer.

### CreateTorrent race on duplicate magnets
- **File**: `services/torrent-engine/internal/usecase/create_torrent.go`
- Two concurrent requests with the same magnet both pass `Repo.Get` (line 43), then second `Repo.Create` (line 83) fails with MongoDB duplicate key.
- **Fix**: Handle `mongo.IsDuplicateKeyError` by re-fetching, or use `FindOneAndUpdate` with upsert.

### StreamTorrent session leak on Start failure
- **File**: `services/torrent-engine/internal/usecase/stream_torrent.go`
- If `session.Start()` fails (line 74), the opened session is never closed.
- **Fix**: Add `defer session.Close()` or explicit cleanup in error path.

### DeleteTorrent: files removed before DB record
- **File**: `services/torrent-engine/internal/usecase/delete_torrent.go`
- File deletion (line 35) happens before `Repo.Delete` (line 41). If DB delete fails, files are lost but record remains.
- **Fix**: Delete files after successful DB deletion. Accumulate file-removal errors instead of aborting on first failure (line 76-78).

### MongoDB Update silent no-op
- **File**: `services/torrent-engine/internal/repository/mongo/repository.go`
- `Update` (line 93) doesn't check `res.MatchedCount`. Returns nil even if document doesn't exist.
- **Fix**: Check `MatchedCount == 0` and return `domain.ErrNotFound`.

---

## P1 — High: Reliability & Performance

### Goroutine leak in anacrolix waitForInfo
- **File**: `services/torrent-engine/internal/services/torrent/engine/anacrolix/engine.go`
- `waitForInfo` (line 262) blocks on `<-t.GotInfo()` forever with no timeout. Zero-peer torrents leak goroutines.
- **Fix**: Add `context.WithTimeout` or a configurable deadline. Clean up torrent on timeout.

### Abandoned AddMagnet after timeout
- **File**: `services/torrent-engine/internal/services/torrent/engine/anacrolix/engine.go`
- `Open()` (line 190) returns timeout error, but background goroutine may still complete `AddMagnet`. Torrent added to engine without caller knowing.
- **Fix**: Track pending operations; remove torrent if caller timed out.

### ffprobe retry blocks HLS startup
- **File**: `services/torrent-engine/internal/api/http/hls.go`
- `isH264FileWithRetry` (line 453) runs synchronously: 3 attempts x 2s = up to 6s delay before FFmpeg starts.
- **Fix**: Cache codec detection results per file, or run async and start encoding while waiting.

### Torznab serial torrent-file downloads
- **File**: `services/torrent-search/internal/providers/torznab/provider.go`
- InfoHash fallback (line 254) downloads up to 2MB torrent files per result, 4s timeout each, no concurrency limit. 50 results = +200s.
- **Fix**: Fan out with bounded concurrency (semaphore, e.g. 5 parallel). Skip download if result already has magnet.

### Search aggregator: no concurrency limit
- **File**: `services/torrent-search/internal/search/aggregator.go`
- Fan-out (line 172) spawns unlimited goroutines. Jackett with 20 indexers = 20 simultaneous requests.
- **Fix**: Use `golang.org/x/sync/semaphore` or a worker pool with configurable max concurrency.

### SSE ignores client disconnect
- **File**: `services/torrent-search/internal/api/http/server.go`
- `writeSSEEvent` (line 961) discards write errors (`_, _ =`). Search continues after client closes tab.
- **Fix**: Check write errors; return on broken pipe. Use `r.Context().Done()` between phases.

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

### VideoPlayer component too large (~2000 lines)
- **File**: `frontend/src/components/VideoPlayer.tsx`
- 29 `useState` + 33 `useRef`. Mixes HLS management, keyboard shortcuts, timeline preview, screenshot, watch-position saving.
- **Fix**: Extract `useHlsPlayer`, `useKeyboardShortcuts`, `useTimelinePreview`, `useWatchPositionSave` hooks. Consider `useReducer` for player state machine.

### SearchPage too large (1569 lines)
- **File**: `frontend/src/pages/SearchPage.tsx`
- Inline ranking profile logic, filter presets, SSE streaming, results rendering.
- **Fix**: Extract `useSearchRankingProfile`, `useSearchFilterPresets`, `useSearchStream` hooks. Split `SearchResults` into a subcomponent.

### Missing AbortController in polling hooks
- **Files**: `frontend/src/hooks/useSessionState.ts`, `useVideoPlayer.ts` (mediaInfo polling), `useTorrents.ts`
- Polling `fetch` calls can't be cancelled on unmount. Stale responses update unmounted component state.
- **Fix**: Add `AbortController` to each polling effect; abort in cleanup.

### API client: mutation deduplication ignores body
- **File**: `frontend/src/api.ts`
- Mutation key is `METHOD:url` (line 62). Two POSTs with different bodies deduplicate incorrectly.
- **Fix**: Include body hash in deduplication key, or don't deduplicate mutations.

### VideoPlayer memory leaks
- **File**: `frontend/src/components/VideoPlayer.tsx`
- Preview canvas HLS instance, some fullscreen event listeners, and keyboard handlers may not clean up in all unmount paths.
- **Fix**: Audit all `addEventListener` calls for matching `removeEventListener` in cleanup. Null out refs.

### WebSocket reconnect timer leak
- **File**: `frontend/src/hooks/useWebSocket.ts`
- Reconnect timer stored in ref (line 53) is not cleared if `connect()` is called again before timer fires.
- **Fix**: Clear previous timer before scheduling new one.

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
