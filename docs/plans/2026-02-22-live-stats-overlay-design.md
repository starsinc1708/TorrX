# Live Stats Overlay — Design Document

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:writing-plans to create the implementation plan.

**Goal:** Add a toggleable live stats overlay to the video player that shows real-time HLS playback diagnostics (quality, bandwidth, buffer, dropped frames).

**Architecture:** A new `VideoStatsOverlay` component subscribes to HLS.js events and polls `<video>` element stats every 1 s. Rendered as an absolute-positioned panel inside the player container. Toggle via Alt+D and a button in the player controls.

**Tech Stack:** React + TypeScript, HLS.js events (`FRAG_BUFFERED`, `LEVEL_SWITCHED`, `MANIFEST_PARSED`), `video.getVideoPlaybackQuality()`, `video.buffered`, `localStorage` for persistence.

---

## Layout

```
┌─ Video Stats ────────────────────┐
│ Resolution  1920×1080            │
│ Codec       H.264 (avc1.640028) │
│                                  │
│ Quality     1080p ▸ Auto (ABR)  │
│   ▶ 1080p   6.0 Mbps  ← playing │
│     720p    3.0 Mbps             │
│     480p    1.5 Mbps             │
│                                  │
│ Bandwidth   3.2 Mbps             │
│ Estimated   8.4 Mbps             │
│                                  │
│ Buffer      ████████░░  24.3s   │
│                                  │
│ Last frag   245 ms               │
│ Dropped     0 / 4523 (0.0%)     │
└──────────────────────────────────┘
```

Positioned: `top-4 left-4`, semi-transparent dark background (`bg-black/80`), monospace font, `z-50`.

---

## Data Sources

| Stat | Source |
|------|--------|
| Resolution | `video.videoWidth × video.videoHeight` |
| Codec | `hls.levels[currentLevel].videoCodec` |
| Quality / ABR ladder | `hls.levels`, `hls.currentLevel`, `hls.loadLevel` |
| Bandwidth (actual) | last frag: `stats.loaded * 8 / (stats.loading.end - stats.loading.start)` |
| Bandwidth (estimated) | `hls.bandwidthEstimate / 1_000_000` Mbps |
| Buffer | `video.buffered.end(i) - video.currentTime` (largest range containing currentTime) |
| Last frag latency | `FRAG_BUFFERED` event: `stats.loading.end - stats.loading.start` ms |
| Dropped frames | `video.getVideoPlaybackQuality().droppedVideoFrames / totalVideoFrames` |

---

## Component Interface

```tsx
// frontend/src/components/VideoStatsOverlay.tsx
interface VideoStatsOverlayProps {
  hlsRef: React.RefObject<Hls | null>;
  videoRef: React.RefObject<HTMLVideoElement | null>;
  visible: boolean;
}

export const VideoStatsOverlay: React.FC<VideoStatsOverlayProps> = ...
```

Integration in `VideoPlayer.tsx`:
- New state: `const [showStats, setShowStats] = useState(() => localStorage.getItem('showStatsOverlay') === '1')`
- `useEffect` for `Alt+D` keydown → toggle `showStats` + persist to localStorage
- Render `<VideoStatsOverlay hlsRef={hlsRef} videoRef={videoRef} visible={showStats} />`

Button in `VideoControls.tsx`:
- New prop `showStats: boolean`, `onToggleStats: () => void`
- Small `<Activity size={16} />` icon button (from lucide-react) next to settings gear
- Active state: `text-primary` tint when stats visible

---

## Update Strategy

Inside `VideoStatsOverlay`:
1. `setInterval(updateStats, 1000)` — polls buffer + dropped frames every second
2. `hls.on(Hls.Events.FRAG_BUFFERED, ...)` — updates bandwidth + last frag latency
3. `hls.on(Hls.Events.LEVEL_SWITCHED, ...)` — updates quality row
4. `hls.on(Hls.Events.MANIFEST_PARSED, ...)` — populates ABR ladder

Attach/detach on `hlsRef.current` changes via `useEffect`.

---

## Buffer Bar

```tsx
const bufferPct = Math.min(100, (bufferSec / maxBufferLength) * 100);
// maxBufferLength from localStorage('hlsMaxBufferLength') || 60
```

Rendered as a thin CSS progress bar (`w-full h-1.5 bg-white/20 rounded`).

---

## Scope

**In scope:**
- `VideoStatsOverlay` component
- Alt+D toggle in `VideoPlayer.tsx`
- Activity icon button in `VideoControls.tsx`
- localStorage persistence

**Out of scope:**
- Graphs / history charts
- Server-side metrics
- HLS segment timeline
