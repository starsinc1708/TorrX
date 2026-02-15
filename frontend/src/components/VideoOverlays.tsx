import React from 'react';
import { AlertTriangle, Loader2 } from 'lucide-react';
import { cn } from '../lib/cn';
import type { PrebufferPhase } from '../hooks/useVideoPlayer';

type IndicatorStatus = 'idle' | 'transcoding' | 'buffering' | 'recovering' | 'error' | 'seeking';

interface VideoOverlaysProps {
  screenshotFlash: boolean;
  prebufferPhase: PrebufferPhase;
  trackSwitchInProgress: boolean;
  activeMode: 'hls' | 'direct';
  onRetryInitialize?: () => void;
  showStatusIndicator: boolean;
  indicatorStatus: IndicatorStatus;
  indicatorTitle: string;
  indicatorText: string;
  indicatorCanContinue: boolean;
  playing: boolean;
  togglePlay: () => void;
}

export const VideoOverlays: React.FC<VideoOverlaysProps> = ({
  screenshotFlash,
  prebufferPhase,
  trackSwitchInProgress,
  activeMode,
  onRetryInitialize,
  showStatusIndicator,
  indicatorStatus,
  indicatorTitle,
  indicatorText,
  indicatorCanContinue,
  playing,
  togglePlay,
}) => {
  return (
    <>
      {screenshotFlash && (
        <div className="pointer-events-none absolute inset-0 z-10 bg-white/90 animate-[ts-flash_400ms_ease-out_forwards] motion-reduce:animate-none" />
      )}
      {(prebufferPhase === 'probing' || prebufferPhase === 'retrying' || prebufferPhase === 'error' || trackSwitchInProgress) && (
        <div className="absolute inset-0 z-20 flex items-center justify-center bg-black/60 animate-[ts-fade-in_200ms_ease-out]">
          <div className="flex flex-col items-center gap-3 text-white/80">
            {trackSwitchInProgress ? (
              <>
                <Loader2 className="h-8 w-8 animate-spin" />
                <span className="text-sm">Preparing new track on server...</span>
              </>
            ) : prebufferPhase === 'probing' ? (
              <>
                <Loader2 className="h-8 w-8 animate-spin" />
                <span className="text-sm">
                  {activeMode === 'hls' ? 'Preparing stream...' : 'Checking stream...'}
                </span>
              </>
            ) : prebufferPhase === 'retrying' ? (
              <>
                <Loader2 className="h-8 w-8 animate-spin" />
                <span className="text-sm">Retrying stream...</span>
              </>
            ) : (
              <>
                <AlertTriangle className="h-8 w-8 text-rose-400" />
                <span className="text-sm">Stream not available</span>
                {onRetryInitialize && (
                  <button
                    type="button"
                    onClick={onRetryInitialize}
                    className="mt-1 rounded-lg bg-white/10 px-4 py-1.5 text-sm text-white/90 hover:bg-white/20 transition-colors"
                  >
                    Retry
                  </button>
                )}
              </>
            )}
          </div>
        </div>
      )}
      {showStatusIndicator && (
        <div
          className={cn(
            'absolute left-3 top-3 z-20 flex max-w-[min(90%,640px)] items-center gap-2.5 rounded-xl border ' +
              'bg-slate-950/75 px-3 py-2 text-white/90 shadow-[0_18px_50px_rgba(0,0,0,0.55)] ' +
              'backdrop-blur-md animate-[ts-fade-in_200ms_ease-out] motion-reduce:animate-none',
            indicatorStatus === 'buffering'
              ? 'border-emerald-400/25 text-emerald-50'
              : indicatorStatus === 'transcoding'
                ? 'border-orange-400/25 text-orange-50'
                : indicatorStatus === 'recovering'
                  ? 'border-sky-400/25 text-sky-50'
                  : indicatorStatus === 'error'
                    ? 'border-rose-400/25 text-rose-50'
                    : 'border-sky-300/20 text-sky-50',
          )}
        >
          {indicatorStatus === 'error' ? (
            <AlertTriangle size={16} />
          ) : (
            <Loader2 size={16} className="animate-spin" />
          )}
          <div className="flex min-w-0 flex-col gap-0.5">
            <strong className="text-[12px] font-semibold leading-tight">{indicatorTitle}</strong>
            <span className="text-[11px] leading-snug opacity-90">{indicatorText}</span>
          </div>
          {indicatorCanContinue && (
            <button
              type="button"
              className="ml-auto inline-flex h-7 items-center rounded-full border border-white/15 bg-white/5 px-3 text-[11px] font-semibold text-white/90 transition-colors hover:bg-white/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-white/25"
              onClick={togglePlay}
            >
              {playing ? 'Pause' : 'Continue'}
            </button>
          )}
        </div>
      )}
    </>
  );
};
