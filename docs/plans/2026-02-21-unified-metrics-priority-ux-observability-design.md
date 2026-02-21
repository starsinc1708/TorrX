# Design: Unified Metrics, Priority UX, Player Observability

**Date:** 2026-02-21
**Status:** Approved

## 1. Unified Pieces/File Pieces Model

### Problem

Two sources of progress diverge:
- **Torrent-level**: TorrentList uses `Math.max(state.progress, dbProgress)` — when WS and REST data arrive at different times, progress jumps up/down.
- **File-level**: PlayerFilesPanel computes file progress from bitfield slicing (`pieces.slice(start, end)`), while TorrentDetails uses `bytesCompleted / length`. These numbers differ because pieces can span file boundaries.

### Solution: Backend as single source of truth

**Backend** (`domain/state.go`): Add `Progress float64` and `Priority string` to `FileRef`:

```go
type FileRef struct {
    Index          int      `json:"index"`
    Path           string   `json:"path"`
    Length         int64    `json:"length"`
    BytesCompleted int64    `json:"bytesCompleted"`
    PieceStart     int      `json:"pieceStart"`
    PieceEnd       int      `json:"pieceEnd"`
    Progress       float64  `json:"progress"`   // 0.0-1.0, piece-based
    Priority       string   `json:"priority"`   // none/low/normal/high/now
}
```

**Backend** (`anacrolix/engine.go`): Compute `Progress` in `mapFiles()` by iterating the file's piece range and counting completed pieces. Map anacrolix `PiecePriority` to domain string via helper.

**Frontend**: Remove all local progress recalculations:
- TorrentList: `state?.progress ?? normalizeProgress(torrent)` (drop `Math.max`)
- TorrentDetails: `file.progress ?? 0` (drop `bytesCompleted / length`)
- PlayerFilesPanel: use `file.progress` from backend (keep PieceBar canvas for visualization)

## 2. PrioritizeActiveFileOnly — Transparent UX

### Problem

Setting toggle shows "none/low" label but files in PlayerFilesPanel and TorrentDetails show no priority indicators. User can't verify the setting actually took effect.

### Solution: Show per-file priority badges

**Backend**: Already solved by adding `Priority` to `FileRef` (section 1). Engine reads `file.Priority()` from anacrolix and maps to domain values.

**Frontend** (`PlayerFilesPanel.tsx`, `TorrentDetails.tsx`): Show colored badge next to each file name:
- `high`/`now` → primary color badge
- `normal` → green badge
- `low` → muted badge
- `none` → file row gets reduced opacity (visually deprioritized)

No changes to SettingsPage — toggle and "none/low" label remain as-is.

## 3. Player Observability

### Problem

Existing metrics cover job starts/failures and seek counts, but lack latency histograms. No way to detect slow seeks, slow startup, or long verifications before users complain.

### New Prometheus Metrics

| Metric | Type | Buckets | Source |
|--------|------|---------|--------|
| `engine_hls_seek_latency_seconds` | Histogram | 0.5, 1, 2, 5, 10, 30 | streaming_manager: SeekJob() → recordPlaylistReady() |
| `engine_hls_seek_failures_total` | Counter | — | streaming_manager: seek error path |
| `engine_hls_ttff_seconds` | Histogram | 1, 3, 5, 10, 15, 30, 60 | streaming_fsm: doReady() enter → playlist ready |
| `engine_hls_prebuffer_duration_seconds` | Histogram | 0.5, 1, 3, 5, 10, 15 | streaming_fsm: doLoading() enter → ready |
| `engine_verify_duration_seconds` | Histogram | 1, 5, 10, 30, 60, 120, 300 | engine_phase: verifying enter → verifying exit |

### Implementation

- `streaming_manager.go`: Record `seekStartedAt` at SeekJob() start; observe latency at recordPlaylistReady()
- `streaming_fsm.go`: Record `loadingStartedAt` at doLoading() entry, `readyStartedAt` at doReady() entry; observe at playlist ready
- `engine_phase.go` / `engine.go`: Record `verifyStartedAt` when entering verifying phase; observe when transitioning out

### Grafana Dashboard

New row **"Player SLO"** in `deploy/grafana/dashboards/torrent-engine.json`:
- TTFF P95 gauge
- Seek latency P95 gauge
- Prebuffer duration P95 gauge
- Seek success rate percentage
- Verify duration P95 gauge

### Alert Rules

Add to `deploy/prometheus/rules/slo.yml`:

```yaml
- alert: HLSSeekLatencyHigh
  expr: histogram_quantile(0.95, rate(engine_hls_seek_latency_seconds_bucket[5m])) > 5
  for: 5m
  labels: { severity: warning }

- alert: HLSTTFFHigh
  expr: histogram_quantile(0.95, rate(engine_hls_ttff_seconds_bucket[5m])) > 15
  for: 5m
  labels: { severity: warning }
```

## Files Modified

### Backend
- `internal/domain/state.go` — FileRef: add Progress, Priority fields
- `internal/services/torrent/engine/anacrolix/engine.go` — mapFiles(): compute Progress, Priority
- `internal/metrics/metrics.go` — add 5 new metrics
- `internal/api/http/streaming_manager.go` — seek latency tracking
- `internal/api/http/streaming_fsm.go` — TTFF and prebuffer tracking
- `internal/services/torrent/engine/anacrolix/engine_phase.go` — verify duration tracking

### Frontend
- `src/types.ts` — FileRef: add progress, priority
- `src/components/TorrentList.tsx` — remove Math.max progress fallback
- `src/components/TorrentDetails.tsx` — use file.progress, show priority badge
- `src/components/PlayerFilesPanel.tsx` — use file.progress, show priority badge

### Infra
- `deploy/grafana/dashboards/torrent-engine.json` — Player SLO row
- `deploy/prometheus/rules/slo.yml` — 2 alert rules
