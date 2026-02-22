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
