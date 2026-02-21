# Unified Metrics, Priority UX & Player Observability — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make backend the single source of truth for file progress/priority, show priority badges in UI, and add player latency metrics with Grafana panels and alerts.

**Architecture:** Three orthogonal changes that share `FileRef` as the integration point. Backend computes file progress and priority in `mapFiles()`. Frontend removes local recalculations and renders priority badges. New Prometheus histograms instrument seek/TTFF/prebuffer/verify latency in the streaming FSM.

**Tech Stack:** Go (anacrolix/torrent, prometheus), React/TypeScript (Tailwind badges), Grafana JSON dashboards, Prometheus alert rules YAML.

---

### Task 1: Add Progress and Priority fields to FileRef (backend domain)

**Files:**
- Modify: `services/torrent-engine/internal/domain/state.go:1-19`

**Step 1: Add fields to FileRef struct**

FileRef is defined in `internal/domain/file_ref.go` (or wherever it lives — search for `type FileRef struct`). Add two new fields:

```go
type FileRef struct {
	Index          int     `json:"index"`
	Path           string  `json:"path"`
	Length         int64   `json:"length"`
	BytesCompleted int64   `json:"bytesCompleted"`
	PieceStart     int     `json:"pieceStart"`
	PieceEnd       int     `json:"pieceEnd"`
	Progress       float64 `json:"progress"`
	Priority       string  `json:"priority,omitempty"`
}
```

`Progress` is 0.0–1.0 piece-based. `Priority` is one of: `"none"`, `"low"`, `"normal"`, `"high"`, `"now"`, or empty string if unknown.

**Step 2: Verify it compiles**

Run: `cd services/torrent-engine && go build ./...`
Expected: PASS (new fields are zero-valued by default, no breakage)

**Step 3: Commit**

```bash
git add services/torrent-engine/internal/domain/
git commit -m "feat(domain): add Progress and Priority fields to FileRef"
```

---

### Task 2: Compute Progress and Priority in mapFiles() (engine adapter)

**Files:**
- Modify: `services/torrent-engine/internal/services/torrent/engine/anacrolix/engine.go:874-901`

**Step 1: Write unit test for mapPriority helper**

Create or append to `services/torrent-engine/internal/services/torrent/engine/anacrolix/engine_phase_test.go`:

```go
func TestMapPriority(t *testing.T) {
	tests := []struct {
		input    torrent.PiecePriority
		expected string
	}{
		{torrent.PiecePriorityNone, "none"},
		{torrent.PiecePriorityNormal, "normal"},
		{torrent.PiecePriorityHigh, "high"},
		{torrent.PiecePriorityReadahead, "normal"},
		{torrent.PiecePriorityNow, "now"},
	}
	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			got := mapPriority(tt.input)
			if got != tt.expected {
				t.Errorf("mapPriority(%v) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd services/torrent-engine && go test ./internal/services/torrent/engine/anacrolix/ -run TestMapPriority -v`
Expected: FAIL — `mapPriority` undefined

**Step 3: Implement mapPriority and update mapFiles**

In `engine.go`, add helper before `mapFiles`:

```go
func mapPriority(p torrent.PiecePriority) string {
	switch p {
	case torrent.PiecePriorityNone:
		return "none"
	case torrent.PiecePriorityNormal, torrent.PiecePriorityReadahead:
		return "normal"
	case torrent.PiecePriorityHigh:
		return "high"
	case torrent.PiecePriorityNow:
		return "now"
	default:
		if p < torrent.PiecePriorityNormal {
			return "low"
		}
		return "normal"
	}
}
```

Update `mapFiles` (around line 874-901) to compute Progress and Priority:

```go
func mapFiles(t *torrent.Torrent) (mapped []domain.FileRef) {
	if !torrentInfoReady(t) {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			slog.Error("mapFiles panic recovered",
				slog.Any("error", r),
				slog.String("stack", string(debug.Stack())),
			)
			mapped = nil
		}
	}()

	files := t.Files()
	mapped = make([]domain.FileRef, 0, len(files))
	for i, f := range files {
		start := f.BeginPieceIndex()
		end := f.EndPieceIndex()
		total := end - start
		completed := 0
		for p := start; p < end; p++ {
			if t.PieceState(p).Complete {
				completed++
			}
		}
		progress := 0.0
		if total > 0 {
			progress = float64(completed) / float64(total)
		}

		mapped = append(mapped, domain.FileRef{
			Index:          i,
			Path:           f.Path(),
			Length:         f.Length(),
			BytesCompleted: f.BytesCompleted(),
			PieceStart:     start,
			PieceEnd:       end,
			Progress:       progress,
			Priority:       mapPriority(f.Priority()),
		})
	}
	return mapped
}
```

**Step 4: Run tests**

Run: `cd services/torrent-engine && go test ./internal/services/torrent/engine/anacrolix/ -run TestMapPriority -v`
Expected: PASS

Run: `cd services/torrent-engine && go build ./...`
Expected: PASS

**Step 5: Commit**

```bash
git add services/torrent-engine/internal/services/torrent/engine/anacrolix/
git commit -m "feat(engine): compute per-file Progress and Priority in mapFiles"
```

---

### Task 3: Update frontend types and remove progress recalculations

**Files:**
- Modify: `frontend/src/types.ts:6-13` — add progress, priority to FileRef
- Modify: `frontend/src/components/TorrentList.tsx:184-188` — remove Math.max
- Modify: `frontend/src/components/TorrentDetails.tsx:126` — use file.progress
- Modify: `frontend/src/components/PlayerFilesPanel.tsx:117-167` — use file.progress

**Step 1: Add fields to frontend FileRef type**

In `frontend/src/types.ts`, update `FileRef`:

```typescript
export interface FileRef {
  index: number;
  path: string;
  length: number;
  bytesCompleted?: number;
  pieceStart?: number;
  pieceEnd?: number;
  progress?: number;
  priority?: string;
}
```

**Step 2: Fix TorrentList.tsx progress calculation (line 184)**

Replace:
```typescript
const dbProgress = normalizeProgress(torrent);
const progress = Math.max(state?.progress ?? 0, dbProgress);
const doneBytes =
  state && torrent.totalBytes
    ? Math.max(Math.round((state.progress ?? 0) * torrent.totalBytes), torrent.doneBytes ?? 0)
    : torrent.doneBytes;
```

With:
```typescript
const progress = state?.progress ?? normalizeProgress(torrent);
const doneBytes = torrent.doneBytes;
```

**Step 3: Fix TorrentDetails.tsx progress (line 126)**

Replace:
```typescript
const progress = Math.max(sessionState?.progress ?? 0, normalizeProgress(torrent));
```

With:
```typescript
const progress = sessionState?.progress ?? normalizeProgress(torrent);
```

**Step 4: Simplify PlayerFilesPanel.tsx FilePiecesBlock (lines 117-167)**

The `FilePiecesBlock` component currently decodes the entire bitfield and slices it per-file to compute `pieceProgress`. Replace the progress calculation to use `file.progress` from backend, but keep the piece slice for the PieceBar visualization (it needs the boolean array).

Replace the `pieceProgress` calculation (line 158):
```typescript
const pieceProgress = Math.max(0, Math.min(1, completedFilePieces / totalFilePieces));
```

With:
```typescript
const pieceProgress = fileForPieces.progress ?? Math.max(0, Math.min(1, completedFilePieces / totalFilePieces));
```

This prefers backend-computed progress but falls back to local calculation if `progress` field is missing (backwards compat).

**Step 5: Type-check**

Run: `cd frontend && npx tsc --noEmit`
Expected: PASS

**Step 6: Commit**

```bash
git add frontend/src/types.ts frontend/src/components/TorrentList.tsx frontend/src/components/TorrentDetails.tsx frontend/src/components/PlayerFilesPanel.tsx
git commit -m "feat(frontend): use backend file progress, remove local recalculations"
```

---

### Task 4: Add priority badges to PlayerFilesPanel and TorrentDetails

**Files:**
- Modify: `frontend/src/components/PlayerFilesPanel.tsx` — file list rows
- Modify: `frontend/src/components/TorrentDetails.tsx` — file list rows

**Step 1: Find the file row rendering in PlayerFilesPanel**

Look for where individual files are rendered in the file list (the map over `files`). Next to the file name, add a priority badge:

```tsx
{file.priority && file.priority !== 'normal' && (
  <span className={cn(
    'ml-1.5 inline-flex rounded-full px-1.5 py-0.5 text-[10px] font-medium leading-none',
    file.priority === 'high' || file.priority === 'now'
      ? 'bg-primary/20 text-primary'
      : file.priority === 'low'
        ? 'bg-amber-500/15 text-amber-600 dark:text-amber-400'
        : file.priority === 'none'
          ? 'bg-muted text-muted-foreground'
          : ''
  )}>
    {file.priority}
  </span>
)}
```

For files with `priority === 'none'`, add `opacity-50` to the file row container.

**Step 2: Add same badge to TorrentDetails file rows**

Find where files are rendered in TorrentDetails (the organized sections and leftover files). Add the same badge pattern next to the file name/display name.

For `priority === 'none'` files, add `opacity-50` to the row.

**Step 3: Type-check and visual review**

Run: `cd frontend && npx tsc --noEmit`
Expected: PASS

**Step 4: Commit**

```bash
git add frontend/src/components/PlayerFilesPanel.tsx frontend/src/components/TorrentDetails.tsx
git commit -m "feat(frontend): show per-file priority badges in player and details"
```

---

### Task 5: Add new Prometheus metrics definitions

**Files:**
- Modify: `services/torrent-engine/internal/metrics/metrics.go:7-184`

**Step 1: Add 5 new metric variables after `HLSRateLimitBytesPerSec` (line 154)**

```go
HLSSeekLatency = prometheus.NewHistogram(prometheus.HistogramOpts{
    Namespace: "engine",
    Name:      "hls_seek_latency_seconds",
    Help:      "Latency from seek request to new playlist ready in seconds.",
    Buckets:   []float64{0.5, 1, 2, 5, 10, 30},
})

HLSSeekFailuresTotal = prometheus.NewCounter(prometheus.CounterOpts{
    Namespace: "engine",
    Name:      "hls_seek_failures_total",
    Help:      "Total number of HLS seek failures.",
})

HLSTTFFSeconds = prometheus.NewHistogram(prometheus.HistogramOpts{
    Namespace: "engine",
    Name:      "hls_ttff_seconds",
    Help:      "Time-to-first-frame: from FFmpeg start to first playlist ready.",
    Buckets:   []float64{1, 3, 5, 10, 15, 30, 60},
})

HLSPrebufferDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
    Namespace: "engine",
    Name:      "hls_prebuffer_duration_seconds",
    Help:      "Duration of the prebuffer phase before FFmpeg start.",
    Buckets:   []float64{0.5, 1, 3, 5, 10, 15},
})

VerifyDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
    Namespace: "engine",
    Name:      "verify_duration_seconds",
    Help:      "Duration of piece re-verification phase after restart.",
    Buckets:   []float64{1, 5, 10, 30, 60, 120, 300},
})
```

**Step 2: Register them in `Register()` (line 158-183)**

Add to the `reg.MustRegister(...)` call:
```go
HLSSeekLatency,
HLSSeekFailuresTotal,
HLSTTFFSeconds,
HLSPrebufferDuration,
VerifyDuration,
```

**Step 3: Verify compilation**

Run: `cd services/torrent-engine && go build ./...`
Expected: PASS

**Step 4: Commit**

```bash
git add services/torrent-engine/internal/metrics/metrics.go
git commit -m "feat(metrics): add seek latency, TTFF, prebuffer, verify duration histograms"
```

---

### Task 6: Instrument seek latency in StreamJobManager

**Files:**
- Modify: `services/torrent-engine/internal/api/http/streaming_manager.go:67-83` (struct fields)
- Modify: `services/torrent-engine/internal/api/http/streaming_manager.go:314-420` (SeekJob)
- Modify: `services/torrent-engine/internal/api/http/streaming_manager.go:1085-1113` (recordJobFailure, recordPlaylistReady)

**Step 1: Add seekStartedAt field to StreamJobManager**

In the `StreamJobManager` struct (around line 67-83), add:
```go
lastSeekStartedAt time.Time // for latency calculation
```

**Step 2: Record seek start time in SeekJob()**

In `SeekJob()`, right after `metrics.HLSSeekTotal.Inc()` (line 345), add:
```go
m.lastSeekStartedAt = time.Now()
```

**Step 3: Observe latency in recordPlaylistReady()**

In `recordPlaylistReady()` (line 1104-1113), after the lock, add:
```go
if job != nil && job.seekSeconds > 0 && !m.lastSeekStartedAt.IsZero() {
    metrics.HLSSeekLatency.Observe(time.Since(m.lastSeekStartedAt).Seconds())
    m.lastSeekStartedAt = time.Time{}
}
```

**Step 4: Record seek failure metric in recordJobFailure()**

In `recordJobFailure()` (line 1096-1100), after `m.totalSeekFailures++`, add:
```go
metrics.HLSSeekFailuresTotal.Inc()
m.lastSeekStartedAt = time.Time{} // reset
```

**Step 5: Verify**

Run: `cd services/torrent-engine && go build ./...`
Expected: PASS

**Step 6: Commit**

```bash
git add services/torrent-engine/internal/api/http/streaming_manager.go
git commit -m "feat(hls): instrument seek latency and seek failure metrics"
```

---

### Task 7: Instrument TTFF and prebuffer duration in StreamJob FSM

**Files:**
- Modify: `services/torrent-engine/internal/api/http/streaming_fsm.go:63-104` (StreamJob struct)
- Modify: `services/torrent-engine/internal/api/http/streaming_fsm.go:267-352` (doLoading)
- Modify: `services/torrent-engine/internal/api/http/streaming_fsm.go:355-460` (doReady)

**Step 1: Add timing fields to StreamJob struct**

In the `StreamJob` struct (around line 98-103), add:
```go
loadingStartedAt time.Time // set at doLoading() entry
readyStartedAt   time.Time // set at doReady() entry
```

**Step 2: Record loadingStartedAt at doLoading() entry**

At the top of `doLoading()` (line 268), add:
```go
j.loadingStartedAt = time.Now()
```

**Step 3: Record readyStartedAt at doReady() entry**

At the top of `doReady()` (line 356), add:
```go
j.readyStartedAt = time.Now()
```

**Step 4: Observe TTFF and prebuffer at playlist ready**

In `doReady()`, when playlist is detected (around line 455-460, right after `j.signalReady()`), replace the placeholder:
```go
metrics.HLSEncodeDuration.Observe(0) // placeholder
```

With:
```go
if !j.readyStartedAt.IsZero() {
    metrics.HLSTTFFSeconds.Observe(time.Since(j.readyStartedAt).Seconds())
}
if !j.loadingStartedAt.IsZero() {
    metrics.HLSPrebufferDuration.Observe(time.Since(j.loadingStartedAt).Seconds())
}
```

**Step 5: Verify**

Run: `cd services/torrent-engine && go build ./...`
Expected: PASS

**Step 6: Commit**

```bash
git add services/torrent-engine/internal/api/http/streaming_fsm.go
git commit -m "feat(hls): instrument TTFF and prebuffer duration metrics"
```

---

### Task 8: Instrument verification duration

**Files:**
- Modify: `services/torrent-engine/internal/services/torrent/engine/anacrolix/engine.go:440-520`

**Step 1: Add verification tracking to Engine struct**

In the Engine struct, add:
```go
verifyStartedAt map[domain.TorrentID]time.Time // tracks when verifying started per torrent
```

Initialize in `New()`:
```go
verifyStartedAt: make(map[domain.TorrentID]time.Time),
```

**Step 2: Track verification phase transitions in GetSessionState()**

After `deriveTransferPhase` call (line 505), add:
```go
e.mu.Lock()
if transferPhase == domain.TransferPhaseVerifying {
    if _, tracking := e.verifyStartedAt[id]; !tracking {
        e.verifyStartedAt[id] = time.Now()
    }
} else {
    if started, was := e.verifyStartedAt[id]; was {
        delete(e.verifyStartedAt, id)
        metrics.VerifyDuration.Observe(time.Since(started).Seconds())
    }
}
e.mu.Unlock()
```

Import `"torrentstream/internal/metrics"` at the top of the file.

**Step 3: Verify**

Run: `cd services/torrent-engine && go build ./...`
Expected: PASS

**Step 4: Commit**

```bash
git add services/torrent-engine/internal/services/torrent/engine/anacrolix/engine.go
git commit -m "feat(engine): track verification phase duration metric"
```

---

### Task 9: Add Grafana Player SLO dashboard row

**Files:**
- Modify: `deploy/grafana/dashboards/torrent-engine.json`

**Step 1: Add Player SLO row**

Open the dashboard JSON. Add a new row panel and 5 stat/graph panels after the existing "HLS Stability" row:

Row title: **"Player SLO"**

Panels:
1. **TTFF P95** — stat panel: `histogram_quantile(0.95, rate(engine_hls_ttff_seconds_bucket[$__rate_interval]))`
2. **Seek Latency P95** — stat panel: `histogram_quantile(0.95, rate(engine_hls_seek_latency_seconds_bucket[$__rate_interval]))`
3. **Prebuffer P95** — stat panel: `histogram_quantile(0.95, rate(engine_hls_prebuffer_duration_seconds_bucket[$__rate_interval]))`
4. **Seek Success Rate** — stat panel: `1 - rate(engine_hls_seek_failures_total[$__rate_interval]) / clamp_min(rate(engine_hls_seek_requests_total[$__rate_interval]), 1e-10)`
5. **Verify Duration P95** — stat panel: `histogram_quantile(0.95, rate(engine_verify_duration_seconds_bucket[$__rate_interval]))`

Use `unit: "s"` for latency panels, `unit: "percentunit"` for success rate.

**Step 2: Commit**

```bash
git add deploy/grafana/dashboards/torrent-engine.json
git commit -m "feat(grafana): add Player SLO row with TTFF, seek, prebuffer, verify panels"
```

---

### Task 10: Add Prometheus alert rules

**Files:**
- Modify: `deploy/prometheus/rules/slo.yml:66-183`

**Step 1: Add player alerts to slo_alerts group**

Append after the last alert (`ApiErrorBudgetExhausted`):

```yaml
      # ── Player/HLS: seek latency ─────────────────────────────────────
      - alert: HLSSeekLatencyHigh
        expr: histogram_quantile(0.95, sum(rate(engine_hls_seek_latency_seconds_bucket[5m])) by (le)) > 5
        for: 5m
        labels:
          severity: warning
          service: player
        annotations:
          summary: "HLS seek P95 latency > 5s"
          description: "HLS seek P95 latency is {{ $value | humanizeDuration }}."

      # ── Player/HLS: time-to-first-frame ──────────────────────────────
      - alert: HLSTTFFHigh
        expr: histogram_quantile(0.95, sum(rate(engine_hls_ttff_seconds_bucket[5m])) by (le)) > 15
        for: 5m
        labels:
          severity: warning
          service: player
        annotations:
          summary: "HLS TTFF P95 > 15s"
          description: "Time-to-first-frame P95 is {{ $value | humanizeDuration }}."
```

**Step 2: Commit**

```bash
git add deploy/prometheus/rules/slo.yml
git commit -m "feat(alerts): add HLS seek latency and TTFF P95 alert rules"
```

---

### Task 11: Run full test suite and verify

**Step 1: Run Go tests**

```bash
cd services/torrent-engine && go test ./...
```
Expected: All tests PASS

**Step 2: Run frontend type-check**

```bash
cd frontend && npx tsc --noEmit
```
Expected: PASS

**Step 3: Final commit if any fixups needed**

```bash
git add -A && git commit -m "fix: address test/typecheck issues from unified metrics implementation"
```
