import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import Hls from 'hls.js';
import {
  Activity,
  MonitorPlay,
  AlertTriangle,
} from 'lucide-react';
import type { FileRef, MediaTrack, PlayerHealth, SessionState } from '../types';
import type { PrebufferPhase } from '../hooks/useVideoPlayer';
import { formatBytes, formatTime } from '../utils';
import { saveWatchPosition } from '../api';
import { upsertTorrentWatchState } from '../watchState';
import { cn } from '../lib/cn';
import { Button } from './ui/button';
import { VideoTimeline, type TimelinePreviewState } from './VideoTimeline';
import { VideoOverlays } from './VideoOverlays';
import { VideoControls } from './VideoControls';

interface VideoPlayerProps {
  videoRef: React.RefObject<HTMLVideoElement>;
  streamUrl: string;
  useHls: boolean;
  files: FileRef[];
  selectedFile: FileRef | null;
  selectedFileIndex: number | null;
  torrentId: string | null;
  torrentName: string | null;
  videoError: string | null;
  audioTracks: MediaTrack[];
  subtitleTracks: MediaTrack[];
  selectedAudioTrack: number | null;
  selectedSubtitleTrack: number | null;
  subtitlesReady: boolean;
  mediaDuration: number;
  seekOffset: number;
  onHlsSeek: (absoluteTime: number) => Promise<void>;
  sessionState: SessionState | null;
  onSelectFile: (index: number) => void;
  onSelectAudioTrack: (index: number | null) => void;
  onSelectSubtitleTrack: (index: number | null) => void;
  onRetryInitialize?: () => void;
  initialPlaybackRate?: number;
  onPlaybackRateChange?: (rate: number) => void;
  initialQualityLevel?: number;
  onQualityLevelChange?: (level: number) => void;
  onOpenInfo?: () => void;
  playerHealth?: PlayerHealth | null;
  onShowHealth?: () => void;
  resumeRequest?: {
    requestId: number;
    fileIndex: number;
    position: number;
  } | null;
  onResumeHandled?: (requestId: number) => void;
  prebufferPhase?: PrebufferPhase;
  activeMode?: 'direct' | 'hls';
  onFallbackToHls?: () => void;
  trackSwitchInProgress?: boolean;
  hlsDestroyRef?: React.MutableRefObject<(() => void) | null>;
}

const trackLabel = (track: MediaTrack): string => {
  const parts: string[] = [];
  if (track.language) parts.push(track.language.toUpperCase());
  if (track.title) parts.push(track.title);
  if (track.codec) parts.push(track.codec);
  if (parts.length === 0) return `Track ${track.index}`;
  return parts.join(' / ');
};

const sourceKeyFromStreamUrl = (streamUrl: string): string => {
  if (!streamUrl) return '';
  try {
    const parsed = new URL(streamUrl, window.location.origin);
    const parts = parsed.pathname.split('/').filter(Boolean);
    const torrentsIndex = parts.indexOf('torrents');
    if (torrentsIndex === -1 || torrentsIndex + 2 >= parts.length) {
      return parsed.pathname;
    }

    const torrentId = parts[torrentsIndex + 1];
    const kind = parts[torrentsIndex + 2];
    if (kind === 'stream') {
      const fileIndex = parsed.searchParams.get('fileIndex') ?? '0';
      return `${torrentId}:${fileIndex}`;
    }
    if (kind === 'hls' && torrentsIndex + 3 < parts.length) {
      const fileIndex = parts[torrentsIndex + 3];
      const seekToken = parsed.searchParams.get('_st');
      return seekToken ? `${torrentId}:${fileIndex}:st:${seekToken}` : `${torrentId}:${fileIndex}`;
    }
    return `${torrentId}:${kind}`;
  } catch {
    const pathOnly = streamUrl.split('?')[0] ?? streamUrl;
    return pathOnly;
  }
};

type RuntimePlaybackStatus = 'idle' | 'transcoding' | 'buffering' | 'recovering' | 'error';

const TIMELINE_PREVIEW_THROTTLE_MS = 140;
const TIMELINE_PREVIEW_WIDTH = 176;
const TIMELINE_PREVIEW_QUALITY = 0.72;
const MAX_AUTO_INIT_RETRIES = 5;

const normalizePlaybackRate = (rate: number): number => {
  if (!Number.isFinite(rate)) return 1;
  if (rate < 0.25) return 0.25;
  if (rate > 2) return 2;
  return Number(rate.toFixed(2));
};

const VideoPlayer: React.FC<VideoPlayerProps> = ({
  videoRef,
  streamUrl,
  useHls,
  files,
  selectedFile,
  selectedFileIndex,
  torrentId,
  torrentName,
  videoError,
  audioTracks,
  subtitleTracks,
  selectedAudioTrack,
  selectedSubtitleTrack,
  subtitlesReady,
  mediaDuration,
  seekOffset,
  onHlsSeek,
  sessionState: _sessionState,
  onSelectFile,
  onSelectAudioTrack,
  onSelectSubtitleTrack,
  onRetryInitialize,
  initialPlaybackRate = 1,
  onPlaybackRateChange,
  initialQualityLevel = -1,
  onQualityLevelChange,
  onOpenInfo,
  playerHealth,
  onShowHealth,
  resumeRequest,
  onResumeHandled,
  prebufferPhase = 'idle',
  activeMode = 'direct',
  onFallbackToHls: _onFallbackToHls,
  trackSwitchInProgress = false,
  hlsDestroyRef,
}) => {
  const [playing, setPlaying] = useState(false);
  const [currentTime, setCurrentTime] = useState(0);
  const [duration, setDuration] = useState(0);
  const [bufferedTimelineRanges, setBufferedTimelineRanges] = useState<Array<{ start: number; end: number }>>([]);
  const [volume, setVolume] = useState(1);
  const [muted, setMuted] = useState(false);
  const [showControls, setShowControls] = useState(true);
  const [cursorHidden, setCursorHidden] = useState(false);
  const [screenshotFlash, setScreenshotFlash] = useState(false);
  const [seeking, setSeeking] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [speedMenuOpen, setSpeedMenuOpen] = useState(false);
  const [playbackRate, setPlaybackRate] = useState(1);
  const [qualityMenuOpen, setQualityMenuOpen] = useState(false);
  const [availableLevels, setAvailableLevels] = useState<Array<{
    index: number;
    width: number;
    height: number;
    bitrate: number;
    name?: string;
  }>>([]);
  const [currentQualityLevel, setCurrentQualityLevel] = useState(-1);
  const [actualPlayingLevel, setActualPlayingLevel] = useState(-1);
  const [isFullscreen, setIsFullscreen] = useState(false);
  const [hlsError, setHlsError] = useState<string | null>(null);
  const [seekStatus, setSeekStatus] = useState<'idle' | 'seeking' | 'buffering' | 'error'>('idle');
  const [seekStatusText, setSeekStatusText] = useState('');
  const [runtimeStatus, setRuntimeStatus] = useState<RuntimePlaybackStatus>('idle');
  const [runtimeStatusText, setRuntimeStatusText] = useState('');
  const [timelinePreview, setTimelinePreview] = useState<TimelinePreviewState>({
    visible: false,
    leftPercent: 0,
    time: 0,
    frame: null,
    loading: false,
  });
  const playingRef = useRef(false);
  const hideTimerRef = useRef<ReturnType<typeof setTimeout>>();
  const progressRef = useRef<HTMLDivElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const hlsRef = useRef<Hls | null>(null);
  const previewVideoRef = useRef<HTMLVideoElement | null>(null);
  const previewHlsRef = useRef<Hls | null>(null);
  const previewCanvasRef = useRef<HTMLCanvasElement | null>(null);
  const previewSeekTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const previewPendingTimeRef = useRef<number | null>(null);
  const previewRequestTokenRef = useRef(0);
  const previewReadyRef = useRef(false);
  const previewLastTimeRef = useRef<number | null>(null);
  const pendingPlayRef = useRef(false);
  const savedTimeRef = useRef<number | null>(null);
  const pendingSeekTargetRef = useRef<number | null>(null);
  const wasPlayingRef = useRef(false);
  const currentSourceKeyRef = useRef<string>('');
  const sourcePositionMapRef = useRef<Map<string, number>>(new Map());
  const lastPlaybackSnapshotRef = useRef<{ sourceKey: string; time: number; wasPlaying: boolean } | null>(null);
  const autoAdvanceRef = useRef(false);
  const shouldResumeAfterHlsSeekRef = useRef(false);
  const hlsRecoveryCountRef = useRef(0);
  const hlsRetryTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const autoInitRetryTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const autoInitRetryCountRef = useRef<Map<string, number>>(new Map());
  const trackFallbackAppliedRef = useRef<Set<string>>(new Set());
  const [stableDuration, setStableDuration] = useState(0);
  const lastSaveTimeRef = useRef(0);
  const loadTokenRef = useRef(0);
  const handledResumeRequestRef = useRef<number | null>(null);
  const previousSelectedFileIndexRef = useRef<number | null>(null);
  const forcedResumeTargetRef = useRef<{ fileIndex: number; position: number } | null>(null);
  const prevTrackSwitchRef = useRef(false);

  useEffect(() => {
    const nextRate = normalizePlaybackRate(initialPlaybackRate);
    setPlaybackRate(nextRate);
    const video = videoRef.current;
    if (!video) return;
    if (Math.abs(video.playbackRate - nextRate) < 0.001) return;
    video.playbackRate = nextRate;
  }, [initialPlaybackRate, videoRef]);

  useEffect(() => {
    if (initialQualityLevel !== undefined) {
      setCurrentQualityLevel(initialQualityLevel);
    }
  }, [initialQualityLevel]);

  const tryPlay = useCallback(() => {
    const video = videoRef.current;
    if (!video) return;
    const promise = video.play();
    if (promise && typeof promise.catch === 'function') {
      promise
        .then(() => {
          pendingPlayRef.current = false;
        })
        .catch(() => {
          pendingPlayRef.current = true;
        });
    } else {
      pendingPlayRef.current = false;
    }
  }, [videoRef]);

  const clearRuntimeStatus = useCallback(() => {
    setRuntimeStatus('idle');
    setRuntimeStatusText('');
  }, []);

  const resolveMediaDuration = useCallback((video: HTMLVideoElement): number => {
    const rawDuration = video.duration;
    if (Number.isFinite(rawDuration) && rawDuration > 0) {
      return rawDuration;
    }
    if (video.seekable.length > 0) {
      const seekableEnd = video.seekable.end(video.seekable.length - 1);
      if (Number.isFinite(seekableEnd) && seekableEnd > 0) {
        return seekableEnd;
      }
    }
    if (video.buffered.length > 0) {
      const bufferedEnd = video.buffered.end(video.buffered.length - 1);
      if (Number.isFinite(bufferedEnd) && bufferedEnd > 0) {
        return bufferedEnd;
      }
    }
    return 0;
  }, []);

  const resolveHlsSeekableEnd = useCallback((video: HTMLVideoElement): number => {
    if (video.seekable.length > 0) {
      const seekableEnd = video.seekable.end(video.seekable.length - 1);
      if (Number.isFinite(seekableEnd) && seekableEnd >= 0) {
        return seekableEnd;
      }
    }
    if (video.buffered.length > 0) {
      const bufferedEnd = video.buffered.end(video.buffered.length - 1);
      if (Number.isFinite(bufferedEnd) && bufferedEnd >= 0) {
        return bufferedEnd;
      }
    }
    return 0;
  }, []);

  const resolveTimelineTargetFromClientX = useCallback(
    (clientX: number): { ratio: number; absoluteTime: number; localTime: number } | null => {
      const bar = progressRef.current;
      if (!bar) return null;
      const rect = bar.getBoundingClientRect();
      if (rect.width <= 0) return null;
      const ratio = Math.max(0, Math.min(1, (clientX - rect.left) / rect.width));
      const effectiveDuration = useHls && mediaDuration > 0 ? mediaDuration : duration;
      const absoluteTime = ratio * (effectiveDuration || 0);
      const localTime = useHls && seekOffset > 0 ? absoluteTime - seekOffset : absoluteTime;
      return { ratio, absoluteTime, localTime };
    },
    [useHls, mediaDuration, duration, seekOffset],
  );

  const capturePreviewFrame = useCallback(
    async (absoluteTime: number) => {
      const previewVideo = previewVideoRef.current;
      if (!previewVideo || !previewReadyRef.current || !streamUrl) {
        setTimelinePreview((prev) => (prev.visible ? { ...prev, loading: false } : prev));
        return;
      }

      let targetLocalTime = useHls && seekOffset > 0 ? absoluteTime - seekOffset : absoluteTime;
      if (!Number.isFinite(targetLocalTime) || targetLocalTime < 0) {
        setTimelinePreview((prev) => (prev.visible ? { ...prev, loading: false } : prev));
        return;
      }

      if (Number.isFinite(previewVideo.duration) && previewVideo.duration > 0) {
        targetLocalTime = Math.min(targetLocalTime, Math.max(0, previewVideo.duration - 0.04));
      }

      if (
        previewLastTimeRef.current !== null &&
        Math.abs(previewLastTimeRef.current - targetLocalTime) < 0.15
      ) {
        setTimelinePreview((prev) => (prev.visible ? { ...prev, loading: false } : prev));
        return;
      }

      const requestToken = previewRequestTokenRef.current + 1;
      previewRequestTokenRef.current = requestToken;
      setTimelinePreview((prev) => (prev.visible ? { ...prev, loading: true } : prev));

      const frame = await new Promise<string | null>((resolve) => {
        let settled = false;
        const timeout = window.setTimeout(() => finalize(null), 900);

        const finalize = (result: string | null) => {
          if (settled) return;
          settled = true;
          window.clearTimeout(timeout);
          previewVideo.removeEventListener('seeked', onSeeked);
          previewVideo.removeEventListener('error', onError);
          resolve(result);
        };

        const onError = () => finalize(null);
        const onSeeked = () => {
          try {
            const width = previewVideo.videoWidth;
            const height = previewVideo.videoHeight;
            if (!width || !height) {
              finalize(null);
              return;
            }

            let canvas = previewCanvasRef.current;
            if (!canvas) {
              canvas = document.createElement('canvas');
              previewCanvasRef.current = canvas;
            }

            const previewWidth = TIMELINE_PREVIEW_WIDTH;
            const previewHeight = Math.max(1, Math.round((previewWidth * height) / width));
            canvas.width = previewWidth;
            canvas.height = previewHeight;
            const ctx = canvas.getContext('2d');
            if (!ctx) {
              finalize(null);
              return;
            }

            ctx.drawImage(previewVideo, 0, 0, previewWidth, previewHeight);
            finalize(canvas.toDataURL('image/jpeg', TIMELINE_PREVIEW_QUALITY));
          } catch {
            finalize(null);
          }
        };

        previewVideo.addEventListener('seeked', onSeeked, { once: true });
        previewVideo.addEventListener('error', onError, { once: true });
        try {
          previewVideo.currentTime = targetLocalTime;
        } catch {
          finalize(null);
        }
      });

      if (previewRequestTokenRef.current !== requestToken) {
        return;
      }

      if (frame) {
        previewLastTimeRef.current = targetLocalTime;
      }
      setTimelinePreview((prev) =>
        prev.visible
          ? {
              ...prev,
              frame: frame ?? prev.frame,
              loading: false,
            }
          : prev,
      );
    },
    [useHls, seekOffset, streamUrl],
  );

  const schedulePreviewFrameCapture = useCallback(
    (absoluteTime: number) => {
      previewPendingTimeRef.current = absoluteTime;
      if (previewSeekTimerRef.current) return;
      previewSeekTimerRef.current = window.setTimeout(() => {
        previewSeekTimerRef.current = null;
        const target = previewPendingTimeRef.current;
        previewPendingTimeRef.current = null;
        if (typeof target === 'number') {
          void capturePreviewFrame(target);
        }
      }, TIMELINE_PREVIEW_THROTTLE_MS);
    },
    [capturePreviewFrame],
  );

  const updateTimelinePreview = useCallback(
    (clientX: number) => {
      const target = resolveTimelineTargetFromClientX(clientX);
      if (!target) return null;
      setTimelinePreview((prev) => ({
        ...prev,
        visible: true,
        leftPercent: target.ratio * 100,
        time: target.absoluteTime,
      }));
      schedulePreviewFrameCapture(target.absoluteTime);
      return target;
    },
    [resolveTimelineTargetFromClientX, schedulePreviewFrameCapture],
  );

  const disposeTimelinePreviewSource = useCallback(() => {
    previewRequestTokenRef.current += 1;
    previewReadyRef.current = false;
    previewLastTimeRef.current = null;
    previewPendingTimeRef.current = null;
    if (previewSeekTimerRef.current) {
      window.clearTimeout(previewSeekTimerRef.current);
      previewSeekTimerRef.current = null;
    }
    if (previewHlsRef.current) {
      previewHlsRef.current.destroy();
      previewHlsRef.current = null;
    }
    if (previewVideoRef.current) {
      const previewVideo = previewVideoRef.current;
      previewVideo.pause();
      previewVideo.removeAttribute('src');
      previewVideo.load();
      previewVideoRef.current = null;
    }
  }, []);

  const resolveBufferedTimelineRanges = useCallback((video: HTMLVideoElement): Array<{ start: number; end: number }> => {
    const totalDuration = useHls && mediaDuration > 0 ? mediaDuration : resolveMediaDuration(video);
    if (totalDuration <= 0 || video.buffered.length === 0) {
      return [];
    }

    const offset = useHls ? seekOffset : 0;
    const ranges: Array<{ start: number; end: number }> = [];
    for (let i = 0; i < video.buffered.length; i += 1) {
      let start = video.buffered.start(i) + offset;
      let end = video.buffered.end(i) + offset;
      if (!Number.isFinite(start) || !Number.isFinite(end)) continue;
      if (end <= 0 || start >= totalDuration) continue;
      if (start < 0) start = 0;
      if (end > totalDuration) end = totalDuration;
      if (end <= start) continue;
      ranges.push({
        start: (start / totalDuration) * 100,
        end: (end / totalDuration) * 100,
      });
    }

    if (ranges.length <= 1) {
      return ranges;
    }

    ranges.sort((a, b) => a.start - b.start);
    const merged: Array<{ start: number; end: number }> = [ranges[0]];
    for (let i = 1; i < ranges.length; i += 1) {
      const current = ranges[i];
      const last = merged[merged.length - 1];
      if (current.start <= last.end + 0.2) {
        last.end = Math.max(last.end, current.end);
      } else {
        merged.push(current);
      }
    }
    return merged;
  }, [useHls, mediaDuration, seekOffset, resolveMediaDuration]);

  const syncBufferedTimelineRanges = useCallback(() => {
    const video = videoRef.current;
    if (!video) {
      setBufferedTimelineRanges([]);
      return;
    }
    setBufferedTimelineRanges(resolveBufferedTimelineRanges(video));
  }, [videoRef, resolveBufferedTimelineRanges]);

  const attemptRestoreSeek = useCallback(() => {
    const video = videoRef.current;
    if (!video) return;

    const target = pendingSeekTargetRef.current;
    if (target === null || !Number.isFinite(target) || target <= 0) {
      return;
    }

    // Avoid early seek while metadata/duration are not ready.
    const currentDuration = resolveMediaDuration(video);
    if (currentDuration <= 0) {
      return;
    }
    if (currentDuration + 0.25 < target) {
      return;
    }

    video.currentTime = target;
    if (Math.abs(video.currentTime - target) <= 1) {
      pendingSeekTargetRef.current = null;
      savedTimeRef.current = null;
      forcedResumeTargetRef.current = null;
    }
  }, [videoRef, resolveMediaDuration]);

  // Initialize source for direct/HLS playback.
  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;

    // Block source attachment while prebuffer probe or track switch is in progress.
    if (prebufferPhase === 'probing' || prebufferPhase === 'retrying' || prebufferPhase === 'error' || trackSwitchInProgress) {
      if (hlsRef.current) {
        hlsRef.current.destroy();
        hlsRef.current = null;
      }
      video.removeAttribute('src');
      video.load();
      if (hlsDestroyRef) hlsDestroyRef.current = null;
      prevTrackSwitchRef.current = trackSwitchInProgress;
      return;
    }

    // Detect transition from track-switch → ready: auto-play from new playlist start.
    const justFinishedTrackSwitch = prevTrackSwitchRef.current && !trackSwitchInProgress;
    prevTrackSwitchRef.current = false;

    const loadToken = loadTokenRef.current + 1;
    loadTokenRef.current = loadToken;

    const shouldResumeAfterSeek = shouldResumeAfterHlsSeekRef.current;
    const previousSelectedFileIndex = previousSelectedFileIndexRef.current;
    const fileChanged =
      selectedFileIndex !== null &&
      previousSelectedFileIndex !== null &&
      selectedFileIndex !== previousSelectedFileIndex;
    const firstFileSelection = selectedFileIndex !== null && previousSelectedFileIndex === null;
    previousSelectedFileIndexRef.current = selectedFileIndex;

    // After a track switch completes, force auto-play and start from playlist beginning
    // (the server already positioned the playlist at the correct seek offset).
    if (justFinishedTrackSwitch) {
      shouldResumeAfterHlsSeekRef.current = true;
      pendingSeekTargetRef.current = null;
      savedTimeRef.current = null;
    }

    const shouldAutoPlay =
      autoAdvanceRef.current || shouldResumeAfterSeek || justFinishedTrackSwitch || !video.paused || fileChanged || firstFileSelection;
    autoAdvanceRef.current = false;
    shouldResumeAfterHlsSeekRef.current = false;

    const previousSourceKey = currentSourceKeyRef.current;
    const nextSourceKey = sourceKeyFromStreamUrl(streamUrl);
    const sameSource = previousSourceKey !== '' && previousSourceKey === nextSourceKey;

    const lastSnapshot = lastPlaybackSnapshotRef.current;
    const snapshotMatchesPrevious = lastSnapshot?.sourceKey === previousSourceKey;
    const snapshotMatchesNext = lastSnapshot?.sourceKey === nextSourceKey;
    const snapshotTime =
      snapshotMatchesNext &&
      typeof lastSnapshot?.time === 'number' &&
      Number.isFinite(lastSnapshot.time) &&
      lastSnapshot.time > 0
        ? lastSnapshot.time
        : null;
    const snapshotWasPlaying = snapshotMatchesNext ? lastSnapshot?.wasPlaying ?? false : false;

    if (previousSourceKey) {
      const previousSnapshotTime =
        snapshotMatchesPrevious &&
        typeof lastSnapshot?.time === 'number' &&
        Number.isFinite(lastSnapshot.time) &&
        lastSnapshot.time > 0
          ? lastSnapshot.time
          : null;
      if (previousSnapshotTime !== null) {
        sourcePositionMapRef.current.set(previousSourceKey, previousSnapshotTime);
      } else if (Number.isFinite(video.currentTime) && video.currentTime > 0) {
        sourcePositionMapRef.current.set(previousSourceKey, video.currentTime);
      }
    }

    const rememberedTime = nextSourceKey ? sourcePositionMapRef.current.get(nextSourceKey) : undefined;
    const fallbackTime = Number.isFinite(video.currentTime) && video.currentTime > 0 ? video.currentTime : null;

    const forcedResumeTarget = forcedResumeTargetRef.current;
    const forcedMatchesSelected =
      forcedResumeTarget &&
      selectedFileIndex !== null &&
      forcedResumeTarget.fileIndex === selectedFileIndex &&
      Number.isFinite(forcedResumeTarget.position) &&
      forcedResumeTarget.position > 0;

    if (forcedMatchesSelected) {
      savedTimeRef.current = forcedResumeTarget.position;
      wasPlayingRef.current = true;
    } else {
      if (forcedResumeTarget && selectedFileIndex !== forcedResumeTarget.fileIndex) {
        forcedResumeTargetRef.current = null;
      }
      if (sameSource) {
        const resumeTime =
          snapshotTime ??
          fallbackTime ??
          (typeof rememberedTime === 'number' && rememberedTime > 0 ? rememberedTime : null);
        savedTimeRef.current = resumeTime;
        wasPlayingRef.current = snapshotMatchesNext ? snapshotWasPlaying : !video.paused;
      } else if (typeof rememberedTime === 'number' && rememberedTime > 0) {
        savedTimeRef.current = rememberedTime;
        wasPlayingRef.current = false;
      } else {
        savedTimeRef.current = null;
        wasPlayingRef.current = false;
      }
    }
    pendingSeekTargetRef.current = savedTimeRef.current;
    currentSourceKeyRef.current = nextSourceKey;

    const recordPlaybackSnapshot = () => {
      const sourceKey = currentSourceKeyRef.current;
      if (!sourceKey) return;
      const time = Number.isFinite(video.currentTime) ? video.currentTime : 0;
      lastPlaybackSnapshotRef.current = { sourceKey, time, wasPlaying: !video.paused };
    };

    if (hlsRef.current) {
      hlsRef.current.destroy();
      hlsRef.current = null;
    }

    if (hlsRetryTimerRef.current) {
      window.clearTimeout(hlsRetryTimerRef.current);
      hlsRetryTimerRef.current = null;
    }

    setHlsError(null);
    if (useHls && streamUrl) {
      setRuntimeStatus('transcoding');
      if (justFinishedTrackSwitch) {
        setRuntimeStatusText('Loading new track. Waiting for first segments...');
      } else {
        setRuntimeStatusText('Transcoding stream. Waiting for playlist...');
      }
    } else {
      clearRuntimeStatus();
    }

    const maxRecoverAttempts = 10;
    const queueHlsRecovery = (
      hls: Hls,
      action: () => void,
      status: RuntimePlaybackStatus,
      text: string,
      attempt: number,
    ) => {
      const delayMs = Math.min(4500, 350 * Math.pow(2, Math.max(0, attempt-1)));
      setRuntimeStatus(status);
      setRuntimeStatusText(text);
      if (hlsRetryTimerRef.current) {
        window.clearTimeout(hlsRetryTimerRef.current);
      }
      hlsRetryTimerRef.current = window.setTimeout(() => {
        if (loadTokenRef.current !== loadToken) return;
        try {
          action();
        } catch {
          setHlsError('Playback recovery failed');
          setRuntimeStatus('error');
          setRuntimeStatusText('Retry policy exhausted. Reload stream.');
          hls.destroy();
          if (hlsRef.current === hls) {
            hlsRef.current = null;
          }
        }
      }, delayMs);
    };

    const attachHlsHandlers = (hls: Hls, useWasPlayingSnapshot: boolean) => {
      hls.on(Hls.Events.MANIFEST_PARSED, () => {
        if (loadTokenRef.current !== loadToken) return;

        // Extract and store available quality levels
        const levels = hls.levels;
        if (levels && levels.length > 0) {
          const levelData = levels.map((level, index) => ({
            index,
            width: level.width,
            height: level.height,
            bitrate: level.bitrate,
            name: level.name,
          }));
          setAvailableLevels(levelData);

          // Apply preferred quality level if set
          if (currentQualityLevel !== undefined && currentQualityLevel >= -1) {
            // Validate level index is within bounds
            if (currentQualityLevel === -1 || currentQualityLevel < levels.length) {
              hls.currentLevel = currentQualityLevel;
            } else {
              // Invalid level, fall back to Auto
              hls.currentLevel = -1;
              setCurrentQualityLevel(-1);
            }
          }
        } else {
          setAvailableLevels([]);
        }

        setRuntimeStatus('buffering');
        if (justFinishedTrackSwitch) {
          setRuntimeStatusText('New track ready. Buffering segments...');
        } else {
          setRuntimeStatusText('Playlist ready. Buffering first fragments...');
        }
        attemptRestoreSeek();
        if (useWasPlayingSnapshot && wasPlayingRef.current) {
          wasPlayingRef.current = false;
          pendingPlayRef.current = true;
          tryPlay();
          return;
        }
        if (pendingPlayRef.current) {
          tryPlay();
        }
      });

      hls.on(Hls.Events.LEVEL_SWITCHED, (_event, data) => {
        if (loadTokenRef.current !== loadToken) return;
        setActualPlayingLevel(data.level);
      });

      hlsRecoveryCountRef.current = 0;
      hls.on(Hls.Events.ERROR, (_event, data) => {
        if (loadTokenRef.current !== loadToken) return;
        if (!data.fatal) return;

        // Unrecoverable decode error.
        if (data.details === 'bufferAppendError') {
          setHlsError('Video format cannot be decoded by your browser. Try a different track or wait for more data.');
          setRuntimeStatus('error');
          setRuntimeStatusText('Decoder rejected video fragments.');
          hls.destroy();
          hlsRef.current = null;
          return;
        }

        const httpStatus = (data.response as { code?: number } | undefined)?.code;
        hlsRecoveryCountRef.current += 1;
        const attempt = hlsRecoveryCountRef.current;
        if (attempt > maxRecoverAttempts) {
          const detail = data.details ?? 'HLS playback failed';
          setHlsError(`${detail} (retries exhausted)`);
          setRuntimeStatus('error');
          setRuntimeStatusText('Stream recovery failed. Try seek or reload.');
          hls.destroy();
          if (hlsRef.current === hls) {
            hlsRef.current = null;
          }
          return;
        }

        if (data.details === 'manifestLoadError' && httpStatus === 503) {
          if (selectedSubtitleTrack !== null && selectedSubtitleTrack >= 0) {
            queueHlsRecovery(
              hls,
              () => hls.startLoad(-1),
              'transcoding',
              `Preparing subtitle stream (${attempt}/${maxRecoverAttempts})...`,
              attempt,
            );
            return;
          }
          queueHlsRecovery(
            hls,
            () => hls.startLoad(-1),
            'transcoding',
            `Transcoding in progress (${attempt}/${maxRecoverAttempts})...`,
            attempt,
          );
          return;
        }

        if (data.type === Hls.ErrorTypes.NETWORK_ERROR) {
          queueHlsRecovery(
            hls,
            () => hls.startLoad(-1),
            'recovering',
            `Reconnecting stream (${attempt}/${maxRecoverAttempts})...`,
            attempt,
          );
          return;
        }

        if (data.type === Hls.ErrorTypes.MEDIA_ERROR) {
          queueHlsRecovery(
            hls,
            () => hls.recoverMediaError(),
            'recovering',
            `Recovering decoder (${attempt}/${maxRecoverAttempts})...`,
            attempt,
          );
          return;
        }

        queueHlsRecovery(
          hls,
          () => hls.startLoad(-1),
          'recovering',
          `Retrying playback (${attempt}/${maxRecoverAttempts})...`,
          attempt,
        );
      });
    };

    // Track switch (same torrent+file, different audio/subtitle):
    // avoid full video.load() reset which kills browser autoplay permission.
    if (sameSource && useHls && Hls.isSupported() && streamUrl) {
      const shouldAutoPlay = wasPlayingRef.current || shouldResumeAfterSeek;
      pendingPlayRef.current = shouldAutoPlay;
      // Keep autoplay permission across track switches.
      video.autoplay = shouldAutoPlay;
      const hlsMaxBuf = Number(localStorage.getItem('hlsMaxBufferLength')) || 60;
      const hls = new Hls({
        enableWorker: true,
        lowLatencyMode: false,
        backBufferLength: Math.min(hlsMaxBuf, 30),
        maxBufferLength: hlsMaxBuf,
        maxMaxBufferLength: hlsMaxBuf * 2,
        manifestLoadingMaxRetry: 4,
        manifestLoadingRetryDelay: 800,
        levelLoadingMaxRetry: 8,
        levelLoadingRetryDelay: 1000,
        fragLoadingMaxRetry: 8,
        fragLoadingRetryDelay: 1000,
        startPosition: pendingSeekTargetRef.current ?? 0,
      });
      hlsRef.current = hls;
      hls.loadSource(streamUrl);
      hls.attachMedia(video);
      attachHlsHandlers(hls, false);
      if (hlsDestroyRef) {
        hlsDestroyRef.current = () => {
          hls.destroy();
          if (hlsRef.current === hls) hlsRef.current = null;
          video.removeAttribute('src');
          video.load();
        };
      }

      return () => {
        recordPlaybackSnapshot();
        if (hlsDestroyRef) hlsDestroyRef.current = null;
        if (hlsRetryTimerRef.current) {
          window.clearTimeout(hlsRetryTimerRef.current);
          hlsRetryTimerRef.current = null;
        }
        video.autoplay = false;
        hls.destroy();
        if (hlsRef.current === hls) hlsRef.current = null;
      };
    }

    if (sameSource && useHls && streamUrl && video.canPlayType('application/vnd.apple.mpegurl')) {
      const shouldAutoPlay = wasPlayingRef.current || shouldResumeAfterSeek;
      pendingPlayRef.current = shouldAutoPlay;
      video.autoplay = shouldAutoPlay;
      video.src = streamUrl;
      return () => {
        recordPlaybackSnapshot();
        video.autoplay = false;
      };
    }

    // Full source change — reset the video element.
    pendingPlayRef.current = shouldAutoPlay;
    video.pause();
    video.removeAttribute('src');
    video.load();
    if (hlsDestroyRef) hlsDestroyRef.current = null;

    if (!streamUrl) {
      return;
    }

    if (!useHls) {
      video.src = streamUrl;
      video.load();
      return;
    }

    if (video.canPlayType('application/vnd.apple.mpegurl')) {
      video.src = streamUrl;
      video.load();
      return;
    }

    if (!Hls.isSupported()) {
      video.src = streamUrl;
      video.load();
      return;
    }

    const hlsMaxBuf = Number(localStorage.getItem('hlsMaxBufferLength')) || 60;
    const hls = new Hls({
      enableWorker: true,
      lowLatencyMode: false,
      backBufferLength: Math.min(hlsMaxBuf, 30),
      maxBufferLength: hlsMaxBuf,
      maxMaxBufferLength: hlsMaxBuf * 2,
      manifestLoadingMaxRetry: 4,
      manifestLoadingRetryDelay: 800,
      levelLoadingMaxRetry: 8,
      levelLoadingRetryDelay: 1000,
      fragLoadingMaxRetry: 8,
      fragLoadingRetryDelay: 1000,
      startPosition: pendingSeekTargetRef.current ?? 0,
    });
    hlsRef.current = hls;
    hls.loadSource(streamUrl);
    hls.attachMedia(video);
    attachHlsHandlers(hls, true);
    if (hlsDestroyRef) {
      hlsDestroyRef.current = () => {
        hls.destroy();
        if (hlsRef.current === hls) hlsRef.current = null;
        video.removeAttribute('src');
        video.load();
      };
    }

    return () => {
      recordPlaybackSnapshot();
      if (hlsDestroyRef) hlsDestroyRef.current = null;
      if (hlsRetryTimerRef.current) {
        window.clearTimeout(hlsRetryTimerRef.current);
        hlsRetryTimerRef.current = null;
      }
      hls.destroy();
      if (hlsRef.current === hls) {
        hlsRef.current = null;
      }
    };
  }, [
    streamUrl,
    useHls,
    videoRef,
    tryPlay,
    attemptRestoreSeek,
    clearRuntimeStatus,
    selectedSubtitleTrack,
    selectedFileIndex,
    prebufferPhase,
    trackSwitchInProgress,
  ]);

  useEffect(() => {
    disposeTimelinePreviewSource();
    setTimelinePreview({
      visible: false,
      leftPercent: 0,
      time: 0,
      frame: null,
      loading: false,
    });

    if (!streamUrl) {
      return;
    }

    const previewVideo = document.createElement('video');
    previewVideo.preload = 'metadata';
    previewVideo.muted = true;
    previewVideo.playsInline = true;
    previewVideo.crossOrigin = 'anonymous';
    previewVideoRef.current = previewVideo;

    const onLoadedMetadata = () => {
      previewReadyRef.current = true;
    };
    const onCanPlay = () => {
      previewReadyRef.current = true;
    };
    const onError = () => {
      previewReadyRef.current = false;
    };

    previewVideo.addEventListener('loadedmetadata', onLoadedMetadata);
    previewVideo.addEventListener('canplay', onCanPlay);
    previewVideo.addEventListener('error', onError);

    if (!useHls || previewVideo.canPlayType('application/vnd.apple.mpegurl')) {
      previewVideo.src = streamUrl;
      previewVideo.load();
    } else if (Hls.isSupported()) {
      const hlsMaxBuf = Number(localStorage.getItem('hlsMaxBufferLength')) || 60;
      const previewHls = new Hls({
        enableWorker: true,
        lowLatencyMode: false,
        backBufferLength: Math.min(hlsMaxBuf, 30),
        manifestLoadingMaxRetry: 2,
        manifestLoadingRetryDelay: 600,
        levelLoadingMaxRetry: 3,
        levelLoadingRetryDelay: 700,
        fragLoadingMaxRetry: 3,
        fragLoadingRetryDelay: 700,
      });
      previewHlsRef.current = previewHls;
      previewHls.on(Hls.Events.MANIFEST_PARSED, onCanPlay);
      previewHls.on(Hls.Events.ERROR, (_event, data) => {
        if (data.fatal) {
          previewReadyRef.current = false;
        }
      });
      previewHls.loadSource(streamUrl);
      previewHls.attachMedia(previewVideo);
    } else {
      previewVideo.src = streamUrl;
      previewVideo.load();
    }

    return () => {
      previewVideo.removeEventListener('loadedmetadata', onLoadedMetadata);
      previewVideo.removeEventListener('canplay', onCanPlay);
      previewVideo.removeEventListener('error', onError);
      disposeTimelinePreviewSource();
    };
  }, [streamUrl, useHls, disposeTimelinePreviewSource]);

  // Reset player state only on file change, not on track switch.
  useEffect(() => {
    if (savedTimeRef.current === null) {
      setPlaying(false);
      setCurrentTime(0);
      setDuration(0);
      setShowControls(true);
      setSeeking(false);
    }
    setSettingsOpen(false);
    setHlsError(null);
    if (!streamUrl) {
      clearRuntimeStatus();
    }
  }, [streamUrl, clearRuntimeStatus]);

  useEffect(() => {
    setBufferedTimelineRanges([]);
  }, [streamUrl]);

  // Poll buffered ranges as a fallback because progress events are not
  // consistently emitted across browsers/HLS implementations.
  useEffect(() => {
    if (!streamUrl) {
      setBufferedTimelineRanges([]);
      return;
    }
    syncBufferedTimelineRanges();
    const timer = window.setInterval(syncBufferedTimelineRanges, 350);
    return () => window.clearInterval(timer);
  }, [streamUrl, syncBufferedTimelineRanges]);

  useEffect(() => {
    setSeekStatus('idle');
    setSeekStatusText('');
    shouldResumeAfterHlsSeekRef.current = false;
    if (!streamUrl) {
      clearRuntimeStatus();
    }
  }, [torrentId, selectedFileIndex, streamUrl, clearRuntimeStatus]);

  // Handle pending play on canplay (fallback for non-HLS or when MANIFEST_PARSED doesn't fire).
  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    const onLoadedMetadata = () => {
      attemptRestoreSeek();
    };
    const onCanPlay = () => {
      attemptRestoreSeek();
      if (seekStatus === 'idle') {
        clearRuntimeStatus();
      }
      if (pendingPlayRef.current) {
        tryPlay();
      }
    };
    const onWaiting = () => {
      if (!streamUrl) return;
      if (seekStatus !== 'idle') return;
      if (video.paused && !pendingPlayRef.current) return;
      setRuntimeStatus('buffering');
      setRuntimeStatusText('Buffering playback...');
    };
    const onStalled = () => {
      if (!streamUrl) return;
      if (seekStatus !== 'idle') return;
      if (video.paused && !pendingPlayRef.current) return;
      setRuntimeStatus('buffering');
      setRuntimeStatusText('Waiting for next fragments...');
    };
    const onPlaying = () => {
      if (seekStatus === 'idle') {
        clearRuntimeStatus();
      }
    };
    video.addEventListener('loadedmetadata', onLoadedMetadata);
    video.addEventListener('canplay', onCanPlay);
    video.addEventListener('waiting', onWaiting);
    video.addEventListener('stalled', onStalled);
    video.addEventListener('playing', onPlaying);
    return () => {
      video.removeEventListener('loadedmetadata', onLoadedMetadata);
      video.removeEventListener('canplay', onCanPlay);
      video.removeEventListener('waiting', onWaiting);
      video.removeEventListener('stalled', onStalled);
      video.removeEventListener('playing', onPlaying);
    };
  }, [videoRef, tryPlay, attemptRestoreSeek, seekStatus, streamUrl, clearRuntimeStatus]);

  // Sync play state.
  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    const onPlay = () => {
      setPlaying(true);
      setSeekStatus('idle');
      setSeekStatusText('');
      setHlsError(null);
      clearRuntimeStatus();
      const sourceKey = currentSourceKeyRef.current || sourceKeyFromStreamUrl(streamUrl);
      if (sourceKey) {
        autoInitRetryCountRef.current.set(sourceKey, 0);
      }
    };
    const onPause = () => setPlaying(false);
    const onTimeUpdate = () => {
      if (!seeking) setCurrentTime(video.currentTime);
      const effectiveDuration = resolveMediaDuration(video);
      if (effectiveDuration > 0) {
        setDuration(effectiveDuration);
      }
      attemptRestoreSeek();
      syncBufferedTimelineRanges();
      const sourceKey = currentSourceKeyRef.current;
      if (
        pendingSeekTargetRef.current === null &&
        sourceKey &&
        Number.isFinite(video.currentTime) &&
        video.currentTime > 0
      ) {
        sourcePositionMapRef.current.set(sourceKey, video.currentTime);
      }

      // Periodic save to backend (throttled every 5 seconds).
      if (
        torrentId &&
        selectedFileIndex !== null &&
        Number.isFinite(video.currentTime) &&
        video.currentTime > 0 &&
        Number.isFinite(video.duration) &&
        video.duration > 0
      ) {
        const now = Date.now();
        if (now - lastSaveTimeRef.current >= 5000) {
          lastSaveTimeRef.current = now;
          const absPosition = seekOffset + video.currentTime;
          const absDuration = mediaDuration > 0 ? mediaDuration : video.duration;
          saveWatchPosition(
            torrentId,
            selectedFileIndex,
            absPosition,
            absDuration,
            torrentName ?? undefined,
            selectedFile?.path,
          ).catch(() => {});
          upsertTorrentWatchState({
            torrentId,
            fileIndex: selectedFileIndex,
            position: absPosition,
            duration: absDuration,
            torrentName: torrentName ?? undefined,
            filePath: selectedFile?.path,
          });
        }
      }
    };
    const onDurationChange = () => {
      const effectiveDuration = resolveMediaDuration(video);
      setDuration(effectiveDuration);
      attemptRestoreSeek();
      syncBufferedTimelineRanges();
    };
    const onProgress = () => {
      const effectiveDuration = resolveMediaDuration(video);
      if (effectiveDuration > 0) {
        setDuration(effectiveDuration);
      }
      attemptRestoreSeek();
      syncBufferedTimelineRanges();
    };
    const onVolumeChange = () => {
      setVolume(video.volume);
      setMuted(video.muted);
    };
    const onRateChange = () => {
      const nextRate = normalizePlaybackRate(video.playbackRate);
      setPlaybackRate(nextRate);
      onPlaybackRateChange?.(nextRate);
    };

    video.addEventListener('play', onPlay);
    video.addEventListener('pause', onPause);
    video.addEventListener('timeupdate', onTimeUpdate);
    video.addEventListener('durationchange', onDurationChange);
    video.addEventListener('progress', onProgress);
    video.addEventListener('volumechange', onVolumeChange);
    video.addEventListener('ratechange', onRateChange);
    return () => {
      video.removeEventListener('play', onPlay);
      video.removeEventListener('pause', onPause);
      video.removeEventListener('timeupdate', onTimeUpdate);
      video.removeEventListener('durationchange', onDurationChange);
      video.removeEventListener('progress', onProgress);
      video.removeEventListener('volumechange', onVolumeChange);
      video.removeEventListener('ratechange', onRateChange);
    };
  }, [
    videoRef,
    streamUrl,
    seeking,
    attemptRestoreSeek,
    torrentId,
    selectedFileIndex,
    torrentName,
    selectedFile,
    seekOffset,
    mediaDuration,
    resolveMediaDuration,
    syncBufferedTimelineRanges,
    clearRuntimeStatus,
    onPlaybackRateChange,
  ]);

  // Save final position on unmount or file change.
  useEffect(() => {
    return () => {
      const video = videoRef.current;
      if (
        torrentId &&
        selectedFileIndex !== null &&
        video &&
        Number.isFinite(video.currentTime) &&
        video.currentTime > 0 &&
        Number.isFinite(video.duration) &&
        video.duration > 0
      ) {
        const absPos = seekOffset + video.currentTime;
        const absDur = mediaDuration > 0 ? mediaDuration : video.duration;
        saveWatchPosition(
          torrentId,
          selectedFileIndex,
          absPos,
          absDur,
          torrentName ?? undefined,
          selectedFile?.path,
        ).catch(() => {});
        upsertTorrentWatchState({
          torrentId,
          fileIndex: selectedFileIndex,
          position: absPos,
          duration: absDur,
          torrentName: torrentName ?? undefined,
          filePath: selectedFile?.path,
        });
      }
    };
  }, [torrentId, selectedFileIndex, torrentName, selectedFile, videoRef]);

  const togglePlay = useCallback(() => {
    const video = videoRef.current;
    if (!video) return;
    if (video.paused) {
      pendingPlayRef.current = true;
      tryPlay();
    } else {
      video.pause();
      pendingPlayRef.current = false;
    }
  }, [videoRef, tryPlay]);

  const getFullscreenElement = useCallback((): Element | null => {
    const d = document as any;
    return (
      document.fullscreenElement ??
      d.webkitFullscreenElement ??
      d.mozFullScreenElement ??
      d.msFullscreenElement ??
      null
    );
  }, []);

  const toggleFullscreen = useCallback(() => {
    const d = document as any;
    const el = getFullscreenElement();
    if (el) {
      const exit =
        document.exitFullscreen ?? d.webkitExitFullscreen ?? d.mozCancelFullScreen ?? d.msExitFullscreen;
      if (typeof exit === 'function') exit.call(document);
      return;
    }

    const container = containerRef.current as any;
    if (!container) return;
    const req =
      container.requestFullscreen ??
      container.webkitRequestFullscreen ??
      container.mozRequestFullScreen ??
      container.msRequestFullscreen;
    if (typeof req === 'function') req.call(container);
  }, [getFullscreenElement]);

  useEffect(() => {
    const update = () => setIsFullscreen(Boolean(getFullscreenElement()));
    update();
    document.addEventListener('fullscreenchange', update);
    document.addEventListener('webkitfullscreenchange' as any, update);
    return () => {
      document.removeEventListener('fullscreenchange', update);
      document.removeEventListener('webkitfullscreenchange' as any, update);
    };
  }, [getFullscreenElement]);

  useEffect(() => {
    if (autoInitRetryTimerRef.current) {
      clearTimeout(autoInitRetryTimerRef.current);
      autoInitRetryTimerRef.current = null;
    }
  }, [streamUrl]);

  useEffect(() => {
    if (!hlsError || !streamUrl) return;
    if (selectedAudioTrack === null && selectedSubtitleTrack === null) return;

    const sourceKey = sourceKeyFromStreamUrl(streamUrl);
    if (!sourceKey) return;
    const fallbackKey = `${sourceKey}|a:${selectedAudioTrack ?? 'default'}|s:${selectedSubtitleTrack ?? 'off'}`;
    if (trackFallbackAppliedRef.current.has(fallbackKey)) return;
    trackFallbackAppliedRef.current.add(fallbackKey);

    if (selectedSubtitleTrack !== null) {
      onSelectSubtitleTrack(null);
    }
    if (selectedAudioTrack !== null) {
      onSelectAudioTrack(null);
    }

    setHlsError(null);
    setRuntimeStatus('recovering');
    setRuntimeStatusText('Switching to default audio/subtitles...');
  }, [hlsError, streamUrl, selectedAudioTrack, selectedSubtitleTrack, onSelectAudioTrack, onSelectSubtitleTrack]);

  useEffect(() => {
    if (!onRetryInitialize || !streamUrl) return;
    const currentError = hlsError || videoError;
    if (!currentError) return;

    const sourceKey = sourceKeyFromStreamUrl(streamUrl);
    if (!sourceKey) return;
    const currentRetries = autoInitRetryCountRef.current.get(sourceKey) ?? 0;
    if (currentRetries >= MAX_AUTO_INIT_RETRIES) return;

    if (autoInitRetryTimerRef.current) {
      clearTimeout(autoInitRetryTimerRef.current);
      autoInitRetryTimerRef.current = null;
    }

    autoInitRetryTimerRef.current = setTimeout(() => {
      autoInitRetryTimerRef.current = null;
      const nextRetry = (autoInitRetryCountRef.current.get(sourceKey) ?? 0) + 1;
      autoInitRetryCountRef.current.set(sourceKey, nextRetry);
      setRuntimeStatus('recovering');
      setRuntimeStatusText(`Reinitializing stream (${nextRetry}/${MAX_AUTO_INIT_RETRIES})...`);
      onRetryInitialize();
    }, 1100);

    return () => {
      if (autoInitRetryTimerRef.current) {
        clearTimeout(autoInitRetryTimerRef.current);
        autoInitRetryTimerRef.current = null;
      }
    };
  }, [hlsError, videoError, streamUrl, onRetryInitialize]);

  const handleOpenInfo = useCallback(() => {
    if (!onOpenInfo) return;
    const d = document as any;
    const fullscreenElement = getFullscreenElement();
    if (fullscreenElement) {
      const exit =
        document.exitFullscreen ?? d.webkitExitFullscreen ?? d.mozCancelFullScreen ?? d.msExitFullscreen;
      if (typeof exit === 'function') {
        Promise.resolve(exit.call(document))
          .catch(() => {})
          .finally(() => {
            onOpenInfo();
          });
        return;
      }
    }
    onOpenInfo();
  }, [onOpenInfo, getFullscreenElement]);

  const requestServerSeek = useCallback(
    async (absoluteTarget: number, forceResume = false) => {
      const video = videoRef.current;
      const shouldResume = forceResume || Boolean(video && !video.paused);
      shouldResumeAfterHlsSeekRef.current = shouldResume;
      pendingPlayRef.current = shouldResume;
      setSeekStatus('seeking');
      setSeekStatusText(`Seeking to ${formatTime(absoluteTarget)}...`);
      try {
        await onHlsSeek(absoluteTarget);
        setSeekStatus('buffering');
        setSeekStatusText('Preparing target fragments. Playback will continue automatically.');
      } catch {
        shouldResumeAfterHlsSeekRef.current = false;
        pendingPlayRef.current = false;
        setSeekStatus('error');
        setSeekStatusText('Seek failed. Wait for more download and try again.');
      }
    },
    [videoRef, onHlsSeek],
  );

  // Resume is applied only by explicit user action.
  useEffect(() => {
    if (!resumeRequest) return;
    if (handledResumeRequestRef.current === resumeRequest.requestId) return;
    if (selectedFileIndex === null || resumeRequest.fileIndex !== selectedFileIndex) return;

    const video = videoRef.current;
    handledResumeRequestRef.current = resumeRequest.requestId;
    const targetPosition = Math.max(0, resumeRequest.position);
    forcedResumeTargetRef.current = { fileIndex: selectedFileIndex, position: targetPosition };
    savedTimeRef.current = targetPosition;
    pendingSeekTargetRef.current = targetPosition;

    if (video && useHls) {
      const absoluteTarget = mediaDuration > 0 ? Math.min(mediaDuration, targetPosition) : targetPosition;
      const localTarget = absoluteTarget - seekOffset;
      const seekableEnd = resolveHlsSeekableEnd(video);

      if (localTarget >= 0 && localTarget <= seekableEnd + 0.25) {
        video.currentTime = localTarget;
        setCurrentTime(localTarget);
        savedTimeRef.current = null;
        pendingSeekTargetRef.current = null;
        forcedResumeTargetRef.current = null;
        pendingPlayRef.current = true;
        tryPlay();
      } else {
        void requestServerSeek(absoluteTarget, true);
      }
      onResumeHandled?.(resumeRequest.requestId);
      return;
    }

    pendingPlayRef.current = true;
    attemptRestoreSeek();
    tryPlay();
    onResumeHandled?.(resumeRequest.requestId);
  }, [
    resumeRequest,
    selectedFileIndex,
    videoRef,
    useHls,
    mediaDuration,
    seekOffset,
    resolveHlsSeekableEnd,
    requestServerSeek,
    attemptRestoreSeek,
    tryPlay,
    onResumeHandled,
  ]);

  const skip = useCallback(
    (delta: number) => {
      const video = videoRef.current;
      if (!video) return;
      pendingSeekTargetRef.current = null;

      if (useHls && mediaDuration > 0) {
        const absoluteTarget = seekOffset + video.currentTime + delta;
        const clamped = Math.max(0, Math.min(mediaDuration, absoluteTarget));
        const localTarget = clamped - seekOffset;
        const seekableEnd = resolveHlsSeekableEnd(video);
        if (localTarget >= 0 && localTarget <= seekableEnd + 0.25) {
          video.currentTime = localTarget;
        } else {
          void requestServerSeek(clamped);
        }
        return;
      }

      video.currentTime = Math.max(0, Math.min(video.duration || 0, video.currentTime + delta));
    },
    [videoRef, useHls, mediaDuration, seekOffset, requestServerSeek, resolveHlsSeekableEnd],
  );

  const toggleMute = useCallback(() => {
    const video = videoRef.current;
    if (!video) return;
    video.muted = !video.muted;
  }, [videoRef]);

  const handleVolumeChange = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      const video = videoRef.current;
      if (!video) return;
      const v = parseFloat(e.target.value);
      video.volume = v;
      if (v > 0 && video.muted) video.muted = false;
    },
    [videoRef],
  );

  const handleQualityChange = useCallback(
    (levelIndex: number) => {
      const hls = hlsRef.current;
      if (!hls || !useHls) return;

      // Validate level index
      if (levelIndex !== -1 && (levelIndex < 0 || levelIndex >= availableLevels.length)) {
        console.warn(`Invalid quality level index: ${levelIndex}`);
        return;
      }

      // Set quality level
      hls.currentLevel = levelIndex;
      setCurrentQualityLevel(levelIndex);

      // Notify parent for persistence
      onQualityLevelChange?.(levelIndex);
    },
    [useHls, availableLevels.length, onQualityLevelChange],
  );

  const handleSeekStart = useCallback(
    (e: React.MouseEvent<HTMLDivElement>) => {
      setSeeking(true);
      updateTimelinePreview(e.clientX);
    },
    [updateTimelinePreview],
  );

  const handleSeek = useCallback(
    (e: React.MouseEvent<HTMLDivElement>) => {
      const video = videoRef.current;
      if (!video) return;
      pendingSeekTargetRef.current = null;
      const target = updateTimelinePreview(e.clientX);
      if (!target) return;
      const { ratio, absoluteTime, localTime } = target;

      // When using HLS with known media duration, seek in absolute time.
      if (useHls && mediaDuration > 0) {
        const seekableEnd = resolveHlsSeekableEnd(video);

        // Seek locally only within the currently seekable range.
        if (localTime >= 0 && localTime <= seekableEnd + 0.25) {
          video.currentTime = localTime;
          setCurrentTime(localTime);
        } else {
          // Seek outside current HLS buffer - request server-side seek.
          void requestServerSeek(absoluteTime);
        }
        setSeeking(false);
        return;
      }

      video.currentTime = ratio * (video.duration || 0);
      setCurrentTime(video.currentTime);
      setSeeking(false);
    },
    [videoRef, useHls, mediaDuration, requestServerSeek, resolveHlsSeekableEnd, updateTimelinePreview],
  );

  const handleSeekMove = useCallback(
    (e: React.MouseEvent<HTMLDivElement>) => {
      const target = updateTimelinePreview(e.clientX);
      if (!target || !seeking) return;
      // setCurrentTime stores the local video time, but during drag we show via displayCurrentTime.
      setCurrentTime(target.localTime);
    },
    [seeking, updateTimelinePreview],
  );

  const handleSeekLeave = useCallback(() => {
    if (previewSeekTimerRef.current) {
      window.clearTimeout(previewSeekTimerRef.current);
      previewSeekTimerRef.current = null;
    }
    previewPendingTimeRef.current = null;
    setTimelinePreview((prev) => ({
      ...prev,
      visible: false,
      loading: false,
    }));
  }, []);

  const takeScreenshot = useCallback(async () => {
    const video = videoRef.current;
    if (!video) return;
    const canvas = document.createElement('canvas');
    canvas.width = video.videoWidth;
    canvas.height = video.videoHeight;
    const ctx = canvas.getContext('2d');
    if (!ctx) return;
    ctx.drawImage(video, 0, 0, canvas.width, canvas.height);

    try {
      const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, 'image/png'));
      if (blob) {
        await navigator.clipboard.write([new ClipboardItem({ 'image/png': blob })]);
      }
    } catch {
      const link = document.createElement('a');
      link.href = canvas.toDataURL('image/png');
      link.download = `screenshot-${Date.now()}.png`;
      link.click();
    }

    setScreenshotFlash(true);
    setTimeout(() => setScreenshotFlash(false), 400);
  }, [videoRef]);

  // Keep playingRef in sync so timeouts read current value.
  useEffect(() => { playingRef.current = playing; }, [playing]);

  // Auto-hide controls.
  const resetHideTimer = useCallback(() => {
    setShowControls(true);
    setCursorHidden(false);
    if (hideTimerRef.current) clearTimeout(hideTimerRef.current);
    if (settingsOpen || speedMenuOpen || qualityMenuOpen) return;
    hideTimerRef.current = setTimeout(() => {
      if (playingRef.current) {
        setShowControls(false);
        setCursorHidden(true);
      }
    }, 3000);
  }, [settingsOpen, speedMenuOpen, qualityMenuOpen]);

  useEffect(() => {
    if (!playing) {
      setShowControls(true);
      setCursorHidden(false);
      if (hideTimerRef.current) {
        clearTimeout(hideTimerRef.current);
        hideTimerRef.current = undefined;
      }
      return;
    }
    resetHideTimer();
  }, [playing, resetHideTimer]);

  useEffect(() => {
    if (settingsOpen || speedMenuOpen || qualityMenuOpen) {
      setShowControls(true);
      setCursorHidden(false);
      if (hideTimerRef.current) {
        clearTimeout(hideTimerRef.current);
        hideTimerRef.current = undefined;
      }
      return;
    }
    if (playing) {
      resetHideTimer();
    }
  }, [settingsOpen, speedMenuOpen, qualityMenuOpen, playing, resetHideTimer]);

  useEffect(() => {
    return () => {
      if (hideTimerRef.current) {
        clearTimeout(hideTimerRef.current);
      }
      if (hlsRetryTimerRef.current) {
        clearTimeout(hlsRetryTimerRef.current);
        hlsRetryTimerRef.current = null;
      }
      if (autoInitRetryTimerRef.current) {
        clearTimeout(autoInitRetryTimerRef.current);
        autoInitRetryTimerRef.current = null;
      }
      disposeTimelinePreviewSource();
    };
  }, [disposeTimelinePreviewSource]);

  // Keyboard shortcuts.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement) return;
      let handled = true;
      switch (e.key) {
        case ' ':
        case 'k':
          e.preventDefault();
          togglePlay();
          break;
        case 'ArrowLeft':
          e.preventDefault();
          skip(-10);
          break;
        case 'ArrowRight':
          e.preventDefault();
          skip(10);
          break;
        case 'm':
          e.preventDefault();
          toggleMute();
          break;
        case 'f':
        case 'F':
          e.preventDefault();
          toggleFullscreen();
          break;
        case 's':
          e.preventDefault();
          takeScreenshot();
          break;
        default:
          handled = false;
      }
      if (handled) resetHideTimer();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [togglePlay, skip, toggleMute, toggleFullscreen, takeScreenshot, resetHideTimer]);

  // Cache mediaDuration so the display doesn't fluctuate while HLS is still generating.
  useEffect(() => {
    if (mediaDuration > 0) setStableDuration(mediaDuration);
  }, [mediaDuration]);

  // When HLS seek is active, adjust displayed time and duration.
  const displayCurrentTime = useHls && seekOffset > 0 ? seekOffset + currentTime : currentTime;
  // Prefer full media duration; when unavailable but seekOffset > 0, use seekOffset + local
  // duration so the progress bar doesn't overflow past 100%.
  const displayDuration =
    mediaDuration > 0
      ? mediaDuration
      : stableDuration > 0
        ? stableDuration
        : useHls && seekOffset > 0
          ? Math.max(seekOffset + duration, seekOffset + 1)
          : duration;
  const progressPercent = displayDuration > 0 ? Math.min(displayCurrentTime / displayDuration * 100, 100) : 0;
  const indicatorStatus: RuntimePlaybackStatus | 'seeking' =
    seekStatus !== 'idle' ? seekStatus : runtimeStatus;
  const showStatusIndicator = indicatorStatus !== 'idle' && Boolean(streamUrl);
  const indicatorTitle =
    indicatorStatus === 'seeking'
      ? 'Seeking...'
      : indicatorStatus === 'buffering'
        ? 'Buffering...'
        : indicatorStatus === 'transcoding'
          ? 'Transcoding...'
          : indicatorStatus === 'recovering'
            ? 'Recovering playback...'
            : indicatorStatus === 'error'
              ? 'Playback issue'
              : '';
  const indicatorText = seekStatus !== 'idle' ? seekStatusText : runtimeStatusText;
  const indicatorCanContinue = seekStatus === 'buffering' || seekStatus === 'error' || runtimeStatus === 'buffering';

  const currentFilePosition = useMemo(() => {
    if (selectedFileIndex === null) return -1;
    return files.findIndex((file) => file.index === selectedFileIndex);
  }, [files, selectedFileIndex]);
  const prevFile = currentFilePosition > 0 ? files[currentFilePosition - 1] : null;
  const nextFile = currentFilePosition >= 0 && currentFilePosition < files.length - 1
    ? files[currentFilePosition + 1]
    : null;

  const selectAdjacentFile = useCallback(
    (direction: 'prev' | 'next', autoPlay: boolean) => {
      const target = direction === 'prev' ? prevFile : nextFile;
      if (!target) return;
      if (autoPlay) {
        autoAdvanceRef.current = true;
      }
      onSelectFile(target.index);
    },
    [prevFile, nextFile, onSelectFile],
  );

  // Auto-advance to the next file when playback ends.
  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    const onEnded = () => {
      selectAdjacentFile('next', true);
    };
    video.addEventListener('ended', onEnded);
    return () => {
      video.removeEventListener('ended', onEnded);
    };
  }, [videoRef, selectAdjacentFile]);

  const ctrlBtnClassName =
    'relative inline-flex h-10 w-10 items-center justify-center rounded-md text-white/90 ' +
    'transition-colors hover:bg-white/10 hover:text-white ' +
    'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-white/30 ' +
    'disabled:cursor-not-allowed disabled:opacity-50';
  const dropdownPortalContainer = isFullscreen ? (getFullscreenElement() as HTMLElement | null) : null;

  return (
    <div className="player-panel relative h-full min-h-0">
      <div className="h-full min-h-0 min-w-0">
        <div className="flex h-full min-h-0 min-w-0 flex-1 bg-black">
          <div className="flex h-full min-h-0 flex-1 flex-col gap-3 p-4">
            {!streamUrl ? (
              <div className="flex min-h-0 flex-1 flex-col items-center justify-center gap-4 rounded-lg border border-border/70 bg-black p-6 text-sm text-muted-foreground">
                <MonitorPlay size={48} strokeWidth={1} />
                <span>
                  {files.length > 1 && selectedFileIndex === null
                    ? 'Choose a file to start streaming'
                    : 'Select a torrent and file to start streaming'}
                </span>
              </div>
            ) : (
              <>
                <div className="flex items-center justify-between gap-3">
                  {selectedFile && (
                    <div className="min-w-0">
                      <div className="truncate text-sm font-semibold">{selectedFile.path}</div>
                      <div className="text-xs text-muted-foreground tabular-nums">{formatBytes(selectedFile.length)}</div>
                    </div>
                  )}
                  <div className="flex flex-shrink-0 items-center gap-2">
                    {onShowHealth ? (
                      <Button
                        variant="ghost"
                        size="icon"
                        className="relative overflow-visible text-muted-foreground hover:bg-accent hover:text-accent-foreground"
                        onClick={onShowHealth}
                        title="Playback health"
                      >
                        <Activity size={16} />
                        {playerHealth ? (
                          <span
                            className={cn(
                              'pointer-events-none absolute right-1 top-1 inline-flex h-2.5 w-2.5 rounded-full ring-2 ring-black/70',
                              playerHealth.status === 'ok' ? 'bg-emerald-400' : 'bg-amber-400',
                            )}
                          />
                        ) : null}
                      </Button>
                    ) : null}
                  </div>
                </div>

                <div
                  ref={containerRef}
                  className={cn(
                    'relative flex w-full min-h-0 flex-col overflow-hidden bg-black',
                    'flex-1 rounded-none',
                    cursorHidden ? 'cursor-none [&_*]:cursor-none' : '',
                  )}
                  onMouseMove={resetHideTimer}
                  onMouseLeave={() => {
                    setCursorHidden(false);
                    if (settingsOpen || speedMenuOpen) return;
                    if (playing) resetHideTimer();
                  }}
                >
                  <div className="relative min-h-0 flex-1">
                    <video
                      ref={videoRef}
                      className="h-full w-full cursor-pointer bg-black object-contain"
                      preload="metadata"
                      playsInline
                      onClick={togglePlay}
                    />
                    <VideoOverlays
                      screenshotFlash={screenshotFlash}
                      prebufferPhase={prebufferPhase}
                      trackSwitchInProgress={trackSwitchInProgress}
                      activeMode={activeMode}
                      onRetryInitialize={onRetryInitialize}
                      showStatusIndicator={showStatusIndicator}
                      indicatorStatus={indicatorStatus}
                      indicatorTitle={indicatorTitle}
                      indicatorText={indicatorText}
                      indicatorCanContinue={indicatorCanContinue}
                      playing={playing}
                      togglePlay={togglePlay}
                    />
                  </div>

                  <div
                    className={cn(
                      'absolute bottom-0 left-0 right-0 z-10 flex flex-col gap-2 ' +
                        'bg-gradient-to-t from-black/90 via-black/60 to-transparent ' +
                        'px-4 pb-3 pt-6 transition-all duration-200 ease-out',
                      showControls ? 'opacity-100 translate-y-0' : 'pointer-events-none opacity-0 translate-y-2',
                    )}
                  >
                    <VideoTimeline
                      ref={progressRef}
                      progressPercent={progressPercent}
                      bufferedTimelineRanges={bufferedTimelineRanges}
                      timelinePreview={timelinePreview}
                      onSeek={handleSeek}
                      onSeekStart={handleSeekStart}
                      onSeekMove={handleSeekMove}
                      onSeekLeave={handleSeekLeave}
                    />

                    <VideoControls
                      ctrlBtnClassName={ctrlBtnClassName}
                      playing={playing}
                      togglePlay={togglePlay}
                      selectAdjacentFile={selectAdjacentFile}
                      prevFile={!!prevFile}
                      nextFile={!!nextFile}
                      skip={skip}
                      toggleMute={toggleMute}
                      muted={muted}
                      volume={volume}
                      handleVolumeChange={handleVolumeChange}
                      displayCurrentTime={displayCurrentTime}
                      displayDuration={displayDuration}
                      settingsOpen={settingsOpen}
                      setSettingsOpen={setSettingsOpen}
                      dropdownPortalContainer={dropdownPortalContainer}
                      audioTracks={audioTracks}
                      selectedAudioTrack={selectedAudioTrack}
                      onSelectAudioTrack={onSelectAudioTrack}
                      trackLabel={trackLabel}
                      subtitlesReady={subtitlesReady}
                      subtitleTracks={subtitleTracks}
                      selectedSubtitleTrack={selectedSubtitleTrack}
                      onSelectSubtitleTrack={onSelectSubtitleTrack}
                      speedMenuOpen={speedMenuOpen}
                      setSpeedMenuOpen={setSpeedMenuOpen}
                      playbackRate={playbackRate}
                      qualityMenuOpen={qualityMenuOpen}
                      setQualityMenuOpen={setQualityMenuOpen}
                      availableLevels={availableLevels}
                      currentQualityLevel={currentQualityLevel}
                      actualPlayingLevel={actualPlayingLevel}
                      onQualityChange={handleQualityChange}
                      useHls={useHls}
                      videoRef={videoRef}
                      handleOpenInfo={handleOpenInfo}
                      torrentId={torrentId}
                      takeScreenshot={takeScreenshot}
                      toggleFullscreen={toggleFullscreen}
                      isFullscreen={isFullscreen}
                      streamUrl={streamUrl}
                    />
                  </div>

                  {(videoError || hlsError) && (
                    <div className="absolute bottom-20 left-3 right-3 z-30 flex items-center gap-2 rounded-lg border border-destructive/40 bg-destructive/85 px-3 py-2 text-sm text-destructive-foreground shadow-lg">
                      <AlertTriangle size={14} className="shrink-0" />
                      <span className="min-w-0 truncate">{videoError || hlsError}</span>
                      {onRetryInitialize && (
                        <button
                          type="button"
                          onClick={onRetryInitialize}
                          className="ml-auto shrink-0 rounded-md border border-white/20 bg-white/10 px-3 py-1 text-xs font-medium text-white hover:bg-white/20 transition-colors"
                        >
                          Retry
                        </button>
                      )}
                    </div>
                  )}
                </div>
              </>
            )}
          </div>
        </div>
      </div>

    </div>
  );
};

export default VideoPlayer;

