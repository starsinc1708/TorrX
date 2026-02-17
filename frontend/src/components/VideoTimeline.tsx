import React, { forwardRef } from 'react';
import { Camera, Loader2 } from 'lucide-react';
import { formatTime } from '../utils';

export type TimelinePreviewState = {
  visible: boolean;
  leftPercent: number;
  time: number;
  frame: string | null;
  loading: boolean;
};

interface VideoTimelineProps {
  progressPercent: number;
  bufferedTimelineRanges: Array<{ start: number; end: number }>;
  timelinePreview: TimelinePreviewState;
  onSeek: (e: React.MouseEvent<HTMLDivElement>) => void;
  onSeekStart: (e: React.MouseEvent<HTMLDivElement>) => void;
  onSeekMove: (e: React.MouseEvent<HTMLDivElement>) => void;
  onSeekLeave: () => void;
}

export const VideoTimeline = React.memo(forwardRef<HTMLDivElement, VideoTimelineProps>(
  (
    {
      progressPercent,
      bufferedTimelineRanges,
      timelinePreview,
      onSeek,
      onSeekStart,
      onSeekMove,
      onSeekLeave,
    },
    ref,
  ) => {
    return (
      <div
        ref={ref}
        className="group relative cursor-pointer py-1"
        onClick={onSeek}
        onMouseDown={onSeekStart}
        onMouseMove={onSeekMove}
        onMouseLeave={onSeekLeave}
      >
      {timelinePreview.visible && (
        <div
          className="pointer-events-none absolute bottom-[calc(100%+10px)] left-0 z-20 grid -translate-x-1/2 justify-items-center gap-1.5"
          style={{ left: `${timelinePreview.leftPercent}%` }}
        >
          <div className="relative w-44 overflow-hidden rounded-lg border border-white/25 bg-slate-950/95 shadow-[0_12px_28px_rgba(2,6,23,0.45)]">
            {timelinePreview.frame ? (
              <img src={timelinePreview.frame} alt="" className="block aspect-video w-full object-cover" />
            ) : (
              <div className="grid aspect-video w-full place-items-center text-slate-300/80">
                <Camera size={16} />
              </div>
            )}
            {timelinePreview.loading && (
              <div className="absolute inset-0 grid place-items-center bg-black/50 text-white">
                <Loader2 size={14} className="animate-spin" />
              </div>
            )}
          </div>
          <span className="rounded-full border border-white/20 bg-slate-950/90 px-2.5 py-1 text-[11px] font-bold leading-none tracking-wide text-white">
            {formatTime(timelinePreview.time)}
          </span>
        </div>
      )}
      <div className="relative h-1 overflow-hidden rounded-full bg-white/20 transition-all duration-150 group-hover:h-1.5">
        <div className="pointer-events-none absolute inset-0 z-10 rounded-full">
          {bufferedTimelineRanges.map((range, index) => {
            const width = Math.max(0, range.end - range.start);
            if (width <= 0) return null;
            const visualWidth = Math.max(width, 0.35);
            const left = Math.max(0, Math.min(range.start, 100 - visualWidth));
            return (
              <div
                key={`${index}-${range.start.toFixed(3)}-${range.end.toFixed(3)}`}
                className="absolute bottom-0 top-0 min-w-[2px] rounded-full bg-sky-200/60"
                style={{ left: `${left}%`, width: `${visualWidth}%` }}
              />
            );
          })}
        </div>
        <div className="relative z-20 h-full rounded-full bg-primary transition-[width] duration-100" style={{ width: `${progressPercent}%` }} />
        <div
          className="absolute top-1/2 z-30 h-3.5 w-3.5 -translate-x-1/2 -translate-y-1/2 scale-0 rounded-full bg-primary shadow-[0_0_10px_hsl(var(--primary)/0.35)] transition-transform duration-150 group-hover:scale-100"
          style={{ left: `${progressPercent}%` }}
        />
      </div>
    </div>
  );
  },
));

VideoTimeline.displayName = 'VideoTimeline';
