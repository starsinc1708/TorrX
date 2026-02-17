import { useCallback, useEffect, useRef, useState } from 'react';
import Hls from 'hls.js';
import type { TimelinePreviewState } from '../components/VideoTimeline';

const TIMELINE_PREVIEW_THROTTLE_MS = 140;
const TIMELINE_PREVIEW_WIDTH = 176;
const TIMELINE_PREVIEW_QUALITY = 0.72;

interface UseTimelinePreviewOptions {
  streamUrl: string;
  useHls: boolean;
  seekOffset: number;
  mediaDuration: number;
  duration: number;
  progressRef: React.RefObject<HTMLDivElement | null>;
}

export function useTimelinePreview(options: UseTimelinePreviewOptions): {
  timelinePreview: TimelinePreviewState;
  updateTimelinePreview: (clientX: number) => { ratio: number; absoluteTime: number; localTime: number } | null;
  handleSeekLeave: () => void;
} {
  const { streamUrl, useHls, seekOffset, mediaDuration, duration, progressRef } = options;

  const [timelinePreview, setTimelinePreview] = useState<TimelinePreviewState>({
    visible: false,
    leftPercent: 0,
    time: 0,
    frame: null,
    loading: false,
  });

  const previewVideoRef = useRef<HTMLVideoElement | null>(null);
  const previewHlsRef = useRef<Hls | null>(null);
  const previewCanvasRef = useRef<HTMLCanvasElement | null>(null);
  const previewSeekTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const previewPendingTimeRef = useRef<number | null>(null);
  const previewRequestTokenRef = useRef(0);
  const previewReadyRef = useRef(false);
  const previewLastTimeRef = useRef<number | null>(null);

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
    [useHls, mediaDuration, duration, seekOffset, progressRef],
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

  // Set up / tear down preview video source.
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

  // Cleanup on unmount.
  useEffect(() => {
    return () => {
      disposeTimelinePreviewSource();
    };
  }, [disposeTimelinePreviewSource]);

  return { timelinePreview, updateTimelinePreview, handleSeekLeave };
}
