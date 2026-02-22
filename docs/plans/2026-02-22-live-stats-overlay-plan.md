# Live Stats Overlay Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add a toggleable live HLS stats overlay to the video player (Alt+D) showing resolution, codec, ABR quality ladder, bandwidth, buffer bar, last fragment latency, and dropped frames.

**Architecture:** New `VideoStatsOverlay` component polls `<video>` element stats every 1 s and reacts to HLS.js `FRAG_BUFFERED` / `LEVEL_SWITCHED` events. Toggle state lives in `VideoPlayer`, persisted to `localStorage`. Button added to `VideoControls`.

**Tech Stack:** React 18, TypeScript, HLS.js, Vitest + React Testing Library, Tailwind CSS, lucide-react.

---

## Key files

| File | Action |
|------|--------|
| `frontend/src/components/VideoStatsOverlay.tsx` | **Create** — new component |
| `frontend/src/components/VideoStatsOverlay.test.tsx` | **Create** — component tests |
| `frontend/src/components/VideoPlayer.tsx` | **Modify** — add state, shortcut, render overlay |
| `frontend/src/components/VideoControls.tsx` | **Modify** — add toggle button |

---

## Context for implementer

**VideoPlayer.tsx** (~1634 lines) is the main player component. Key things to know:
- `hlsRef = useRef<Hls | null>(null)` — line 200
- `videoRef` is passed as a prop (line 26: `videoRef: React.RefObject<HTMLVideoElement>`)
- Keyboard shortcuts go through `useKeyboardShortcuts` hook but `Alt+D` needs a **separate** `useEffect` (the hook doesn't handle modifier keys)
- The video lives in `<div className="relative min-h-0 flex-1">` around line 1509 — add overlay as a sibling **after** `<VideoOverlays />`
- `Activity` is already imported from lucide-react (line 4), reuse it

**VideoControls.tsx** (~369 lines):
- Props interface starts at line 30
- lucide-react imports at lines 2–16 — add `BarChart2` here
- The screenshot `<Camera>` button is near the end — add stats button right before it

**Test pattern** (from `ErrorBoundary.test.tsx`):
```tsx
import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
```
Test environment: jsdom, setupFiles: `src/test/setup.ts`, run with `npx vitest run`.

---

## Task 1 — Create `VideoStatsOverlay` component

**Files:**
- Create: `frontend/src/components/VideoStatsOverlay.tsx`
- Create: `frontend/src/components/VideoStatsOverlay.test.tsx`

### Step 1 — Write the failing test

```tsx
// frontend/src/components/VideoStatsOverlay.test.tsx
import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { VideoStatsOverlay } from './VideoStatsOverlay';
import type Hls from 'hls.js';
import React from 'react';

const mockHlsRef = { current: null } as React.RefObject<Hls | null>;
const mockVideoRef = { current: null } as React.RefObject<HTMLVideoElement | null>;

describe('VideoStatsOverlay', () => {
  it('renders nothing when visible=false', () => {
    const { container } = render(
      <VideoStatsOverlay hlsRef={mockHlsRef} videoRef={mockVideoRef} visible={false} />,
    );
    expect(container.firstChild).toBeNull();
  });

  it('renders panel with heading when visible=true', () => {
    render(
      <VideoStatsOverlay hlsRef={mockHlsRef} videoRef={mockVideoRef} visible={true} />,
    );
    expect(screen.getByTestId('stats-overlay')).toBeInTheDocument();
    expect(screen.getByText('Video Stats')).toBeInTheDocument();
  });

  it('calls onClose when close button is clicked', () => {
    const onClose = vi.fn();
    const { getAllByRole } = render(
      <VideoStatsOverlay
        hlsRef={mockHlsRef}
        videoRef={mockVideoRef}
        visible={true}
        onClose={onClose}
      />,
    );
    getAllByRole('button')[0].click();
    expect(onClose).toHaveBeenCalledOnce();
  });
});
```

### Step 2 — Run test to verify it fails

```bash
cd frontend && npx vitest run src/components/VideoStatsOverlay.test.tsx
```

Expected: `FAIL — Cannot find module './VideoStatsOverlay'`

### Step 3 — Implement `VideoStatsOverlay`

```tsx
// frontend/src/components/VideoStatsOverlay.tsx
import React, { useEffect, useRef, useState } from 'react';
import Hls from 'hls.js';
import { X } from 'lucide-react';

export interface VideoStatsOverlayProps {
  hlsRef: React.RefObject<Hls | null>;
  videoRef: React.RefObject<HTMLVideoElement | null>;
  visible: boolean;
  onClose?: () => void;
}

interface StatsSnapshot {
  resolution: string;
  codec: string;
  currentLevel: number;
  levels: Array<{ index: number; height: number; bitrate: number }>;
  bandwidthEstMbps: number;
  fragBandwidthMbps: number;
  bufferSec: number;
  maxBufferSec: number;
  lastFragMs: number;
  droppedFrames: number;
  totalFrames: number;
}

function emptyStats(maxBufferSec = 60): StatsSnapshot {
  return {
    resolution: '—',
    codec: '—',
    currentLevel: -1,
    levels: [],
    bandwidthEstMbps: 0,
    fragBandwidthMbps: 0,
    bufferSec: 0,
    maxBufferSec,
    lastFragMs: 0,
    droppedFrames: 0,
    totalFrames: 0,
  };
}

function getBufferAhead(video: HTMLVideoElement): number {
  const ct = video.currentTime;
  for (let i = 0; i < video.buffered.length; i++) {
    if (video.buffered.start(i) <= ct + 0.1 && ct <= video.buffered.end(i)) {
      return Math.max(0, video.buffered.end(i) - ct);
    }
  }
  return 0;
}

function formatMbps(mbps: number): string {
  if (mbps <= 0) return '—';
  return mbps >= 1 ? `${mbps.toFixed(1)} Mbps` : `${(mbps * 1000).toFixed(0)} Kbps`;
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-2">
      <span className="shrink-0 text-white/50">{label}</span>
      <span className="min-w-0 truncate text-right">{value}</span>
    </div>
  );
}

function Divider() {
  return <div className="my-2 border-t border-white/10" />;
}

export const VideoStatsOverlay: React.FC<VideoStatsOverlayProps> = ({
  hlsRef,
  videoRef,
  visible,
  onClose,
}) => {
  const maxBufferSec = Number(localStorage.getItem('hlsMaxBufferLength')) || 60;
  const [stats, setStats] = useState<StatsSnapshot>(() => emptyStats(maxBufferSec));
  const lastFragBandwidthRef = useRef(0);
  const lastFragMsRef = useRef(0);

  useEffect(() => {
    if (!visible) return;

    const readStats = () => {
      const h = hlsRef.current;
      const v = videoRef.current;
      if (!v) return;

      const quality = v.getVideoPlaybackQuality?.() ?? null;
      const resolution = v.videoWidth > 0 ? `${v.videoWidth}×${v.videoHeight}` : '—';

      let codec = '—';
      let currentLevel = -1;
      let levels: StatsSnapshot['levels'] = [];
      let bandwidthEstMbps = 0;

      if (h) {
        currentLevel = h.currentLevel;
        bandwidthEstMbps = h.bandwidthEstimate / 1e6;
        levels = h.levels.map((l, i) => ({ index: i, height: l.height, bitrate: l.bitrate }));
        const lvlIndex = currentLevel >= 0 ? currentLevel : h.loadLevel;
        const lvl = h.levels[lvlIndex];
        if (lvl?.videoCodec) codec = lvl.videoCodec;
      }

      setStats({
        resolution,
        codec,
        currentLevel,
        levels,
        bandwidthEstMbps,
        fragBandwidthMbps: lastFragBandwidthRef.current,
        bufferSec: getBufferAhead(v),
        maxBufferSec: Number(localStorage.getItem('hlsMaxBufferLength')) || 60,
        lastFragMs: lastFragMsRef.current,
        droppedFrames: quality?.droppedVideoFrames ?? 0,
        totalFrames: quality?.totalVideoFrames ?? 0,
      });
    };

    const interval = setInterval(readStats, 1000);
    readStats();

    // HLS.js FRAG_BUFFERED gives us actual fragment download stats.
    // Using any because HLS.js event payloads aren't perfectly typed for all versions.
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const onFragBuffered = (_: string, data: any) => {
      const loading = data?.stats?.loading;
      const loaded: number = data?.stats?.loaded ?? 0;
      if (loading && loaded > 0) {
        const durationMs: number = loading.end - loading.start;
        if (durationMs > 0) {
          lastFragBandwidthRef.current = (loaded * 8) / (durationMs / 1000) / 1e6;
          lastFragMsRef.current = durationMs;
        }
      }
    };

    const hls = hlsRef.current;
    if (hls) {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      hls.on(Hls.Events.FRAG_BUFFERED, onFragBuffered as any);
    }

    return () => {
      clearInterval(interval);
      const h = hlsRef.current;
      if (h) {
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        h.off(Hls.Events.FRAG_BUFFERED, onFragBuffered as any);
      }
    };
  }, [visible, hlsRef, videoRef]);

  if (!visible) return null;

  const bufferPct = Math.min(100, (stats.bufferSec / stats.maxBufferSec) * 100);
  const dropPct =
    stats.totalFrames > 0
      ? ((stats.droppedFrames / stats.totalFrames) * 100).toFixed(2)
      : '0.00';
  const activeLevel =
    stats.currentLevel >= 0 ? stats.currentLevel : (hlsRef.current?.loadLevel ?? -1);

  return (
    <div
      data-testid="stats-overlay"
      className="pointer-events-none absolute left-4 top-4 z-50 w-64 rounded-lg bg-black/85 px-4 py-3 font-mono text-xs text-white/90 shadow-xl ring-1 ring-white/10"
    >
      <div className="pointer-events-auto mb-2 flex items-center justify-between">
        <span className="text-[10px] font-bold uppercase tracking-widest text-white/50">
          Video Stats
        </span>
        {onClose && (
          <button
            onClick={onClose}
            className="rounded p-0.5 text-white/40 hover:text-white/80"
            aria-label="Close stats"
          >
            <X size={12} />
          </button>
        )}
      </div>

      <div className="space-y-1">
        <Row label="Resolution" value={stats.resolution} />
        <Row label="Codec" value={stats.codec} />
      </div>

      {stats.levels.length > 0 && (
        <>
          <Divider />
          <div className="mb-1 text-[10px] uppercase tracking-wider text-white/40">
            Quality / ABR
          </div>
          {[...stats.levels]
            .sort((a, b) => b.height - a.height)
            .map((l) => (
              <div
                key={l.index}
                className={`flex items-center gap-1 ${
                  l.index === activeLevel ? 'text-emerald-400' : 'text-white/60'
                }`}
              >
                <span className="w-3 shrink-0">{l.index === activeLevel ? '▶' : ' '}</span>
                <span className="w-12 shrink-0">{l.height}p</span>
                <span>{formatMbps(l.bitrate / 1e6)}</span>
              </div>
            ))}
          <div className="mt-1 text-[10px] text-white/40">
            {stats.currentLevel === -1 ? 'Auto (ABR)' : 'Pinned'}
          </div>
        </>
      )}

      <Divider />
      <Row label="Bandwidth" value={formatMbps(stats.fragBandwidthMbps)} />
      <Row label="Estimated" value={formatMbps(stats.bandwidthEstMbps)} />

      <Divider />
      <div className="flex items-center justify-between">
        <span className="text-white/50">Buffer</span>
        <span>{stats.bufferSec.toFixed(1)} s</span>
      </div>
      <div className="mt-1 h-1.5 w-full overflow-hidden rounded-full bg-white/10">
        <div
          className="h-full rounded-full bg-emerald-500 transition-all duration-500"
          style={{ width: `${bufferPct}%` }}
        />
      </div>

      <Divider />
      <Row
        label="Last frag"
        value={stats.lastFragMs > 0 ? `${Math.round(stats.lastFragMs)} ms` : '—'}
      />
      <Row
        label="Dropped"
        value={`${stats.droppedFrames} / ${stats.totalFrames} (${dropPct}%)`}
      />
    </div>
  );
};
```

### Step 4 — Run test to verify it passes

```bash
cd frontend && npx vitest run src/components/VideoStatsOverlay.test.tsx
```

Expected: `3 tests passed`

### Step 5 — Type-check

```bash
cd frontend && npx tsc --noEmit
```

Expected: no errors.

### Step 6 — Commit

```bash
git add frontend/src/components/VideoStatsOverlay.tsx frontend/src/components/VideoStatsOverlay.test.tsx
git commit -m "feat(player): add VideoStatsOverlay component with HLS stats"
```

---

## Task 2 — Wire overlay into `VideoPlayer.tsx`

**Files:**
- Modify: `frontend/src/components/VideoPlayer.tsx`

### Step 1 — Add import

Find the existing imports near the top of the file (around line 23 where `VideoControls` is imported). Add after it:

```tsx
import { VideoStatsOverlay } from './VideoStatsOverlay';
```

### Step 2 — Add state and toggle callback

Find the state declarations block (around line 169, near `qualityMenuOpen`). Add after the `actualPlayingLevel` state:

```tsx
const [showStats, setShowStats] = useState(
  () => localStorage.getItem('showStatsOverlay') === '1',
);
const toggleStats = useCallback(() => {
  setShowStats((prev) => {
    const next = !prev;
    localStorage.setItem('showStatsOverlay', next ? '1' : '0');
    return next;
  });
}, []);
```

### Step 3 — Add Alt+D keyboard shortcut

Add a new `useEffect` **after** the `useKeyboardShortcuts(...)` call (around line 1347):

```tsx
// Alt+D — toggle stats overlay (debug shortcut)
useEffect(() => {
  const onKey = (e: KeyboardEvent) => {
    if (e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement) return;
    if (e.altKey && e.code === 'KeyD') {
      e.preventDefault();
      toggleStats();
    }
  };
  window.addEventListener('keydown', onKey);
  return () => window.removeEventListener('keydown', onKey);
}, [toggleStats]);
```

### Step 4 — Render overlay in video container

Find the `<div className="relative min-h-0 flex-1">` block (around line 1509). The block currently looks like:

```tsx
<div className="relative min-h-0 flex-1">
  <video ref={videoRef} ... />
  <VideoOverlays ... />
</div>
```

Add `<VideoStatsOverlay>` as a sibling **after** `<VideoOverlays />`:

```tsx
<div className="relative min-h-0 flex-1">
  <video ref={videoRef} ... />
  <VideoOverlays ... />
  <VideoStatsOverlay
    hlsRef={hlsRef}
    videoRef={videoRef}
    visible={showStats}
    onClose={toggleStats}
  />
</div>
```

### Step 5 — Pass props to VideoControls

Find the `<VideoControls` render (~line 1564). Add two new props at the end:

```tsx
showStats={showStats}
onToggleStats={toggleStats}
```

### Step 6 — Type-check

```bash
cd frontend && npx tsc --noEmit
```

Expected: no errors (VideoControls will report missing props until Task 3 is done — add Task 3 before running this).

### Step 7 — Commit

```bash
git add frontend/src/components/VideoPlayer.tsx
git commit -m "feat(player): wire stats overlay into VideoPlayer with Alt+D toggle"
```

---

## Task 3 — Add toggle button to `VideoControls.tsx`

**Files:**
- Modify: `frontend/src/components/VideoControls.tsx`

### Step 1 — Add `BarChart2` to lucide imports

Find line 2–16 (`import { Pause, Play, ...} from 'lucide-react'`). Add `BarChart2` to the list:

```tsx
import {
  Pause,
  Play,
  ChevronLeft,
  ChevronRight,
  SkipBack,
  SkipForward,
  Volume2,
  VolumeX,
  Settings2,
  Check,
  Camera,
  Maximize2,
  Minimize2,
  BarChart2,
} from 'lucide-react';
```

### Step 2 — Add props to `VideoControlsProps`

Find the `interface VideoControlsProps` (line ~30). Add two fields **before** the closing brace:

```tsx
  showStats: boolean;
  onToggleStats: () => void;
```

### Step 3 — Destructure new props

Find the destructuring in `export const VideoControls = React.memo(({` (around line ~84). Add at the end of the destructured list:

```tsx
  showStats,
  onToggleStats,
```

### Step 4 — Add stats toggle button

Find the `<Camera size={18} />` screenshot button (near the end of the controls row, around line ~365 in VideoControls). Add a new button **before** the Camera button:

```tsx
<button
  className={cn(
    ctrlBtnClassName,
    showStats ? 'text-primary' : '',
  )}
  onClick={onToggleStats}
  title="Stats overlay (Alt+D)"
  aria-label="Toggle stats overlay"
  aria-pressed={showStats}
>
  <BarChart2 size={16} />
</button>
```

### Step 5 — Type-check

```bash
cd frontend && npx tsc --noEmit
```

Expected: no errors.

### Step 6 — Run all tests

```bash
cd frontend && npx vitest run
```

Expected: all tests pass.

### Step 7 — Commit

```bash
git add frontend/src/components/VideoControls.tsx
git commit -m "feat(player): add stats overlay toggle button to VideoControls"
```

---

## Verification

After all three tasks:

```bash
# Type-check
cd frontend && npx tsc --noEmit

# Tests
cd frontend && npx vitest run

# Manual: start dev server, open a torrent, press Alt+D or click BarChart2 button
cd frontend && npm run dev
```

**Manual checklist:**
- [ ] Alt+D toggles overlay on/off
- [ ] BarChart2 button in controls toggles overlay; button is tinted when active
- [ ] Overlay shows Resolution, Codec, Quality ladder, Bandwidth, Buffer bar, Dropped frames
- [ ] Buffer bar fills proportionally (0–60 s default)
- [ ] ABR ladder shows ▶ next to currently playing level
- [ ] Closing with X button (in overlay) works
- [ ] State persists across page reload (`localStorage`)
- [ ] Overlay disappears when `visible=false` (no DOM node)
