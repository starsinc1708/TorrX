import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  buildHlsUrl,
  buildStreamUrl,
  getMediaInfo,
  hlsSeek,
  isApiError,
  probeDirectStream,
  probeHlsManifest,
} from '../api';
import type { FileRef, MediaInfo, MediaTrack, SessionState, TorrentRecord } from '../types';
import { extractExtension, playableExtensions } from '../utils';

export type PrebufferPhase =
  | 'idle'     // no probe started
  | 'probing'  // HEAD / manifest fetch in progress
  | 'ready'    // stream confirmed available — safe to attach
  | 'retrying' // probe failed, auto-retrying after delay
  | 'error';   // all probes failed

const findDefaultAudioTrack = (info: MediaInfo | null): number => {
  if (!info) return 0;
  const audioTracks = info.tracks.filter((t) => t.type === 'audio');
  if (audioTracks.length === 0) return 0;
  const explicitDefault = audioTracks.find((t) => t.default);
  return explicitDefault?.index ?? audioTracks[0].index ?? 0;
};

const sleep = (ms: number) =>
  new Promise<void>((resolve) => {
    window.setTimeout(resolve, ms);
  });

const isRetryableSeekError = (error: unknown): boolean => {
  if (!isApiError(error)) return false;
  if (error.code === 'stream_unavailable' || error.code === 'request_failed') return true;
  return typeof error.status === 'number' && [500, 502, 503, 504].includes(error.status);
};

const HLS_PROBE_MAX_ATTEMPTS = 15;
const HLS_PROBE_INTERVAL_MS = 2000;

export function useVideoPlayer(selectedTorrent: TorrentRecord | null, sessionState: SessionState | null) {
  const [selectedFileIndex, setSelectedFileIndex] = useState<number | null>(null);
  const [audioTrack, setAudioTrack] = useState<number | null>(null);
  const [subtitleTrack, setSubtitleTrack] = useState<number | null>(null);
  const [mediaInfo, setMediaInfo] = useState<MediaInfo | null>(null);
  const [videoError, setVideoError] = useState<string | null>(null);
  const [seekOffset, setSeekOffset] = useState(0);
  const [seekToken, setSeekToken] = useState(0);
  const [streamRetryToken, setStreamRetryToken] = useState(0);
  const [activeMode, setActiveMode] = useState<'direct' | 'hls'>('direct');
  const [prebufferPhase, setPrebufferPhase] = useState<PrebufferPhase>('idle');
  const [trackSwitchInProgress, setTrackSwitchInProgress] = useState(false);
  const [mediaInfoToken, setMediaInfoToken] = useState(0);

  const videoRef = useRef<HTMLVideoElement>(null);
  const probeRetryCountRef = useRef(0);
  const mediaRetryCountRef = useRef(0);
  const mediaInfoInFlightRef = useRef(false);
  const probeAbortRef = useRef<AbortController | null>(null);
  const resumePositionRef = useRef<number | null>(null);
  const trackSwitchTokenRef = useRef(0);
  /** Populated by VideoPlayer — synchronously destroys the active HLS.js instance. */
  const hlsDestroyRef = useRef<(() => void) | null>(null);
  /** Debounce timer for rapid HLS seeks (e.g. timeline scrubbing). */
  const seekDebounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  /** AbortController for the currently in-flight debounced HLS seek request. */
  const seekAbortRef = useRef<AbortController | null>(null);
  /** Resolve callback of the previous debounced seek promise (to avoid hanging). */
  const seekResolveRef = useRef<(() => void) | null>(null);

  const availableFiles = useMemo<FileRef[]>(() => {
    const fromRecord = selectedTorrent?.files ?? [];
    if (fromRecord.length > 0) return fromRecord;
    return sessionState?.files ?? [];
  }, [selectedTorrent?.files, sessionState?.files]);

  const selectedFile = useMemo<FileRef | null>(() => {
    if (selectedFileIndex === null) return null;
    return availableFiles.find((f) => f.index === selectedFileIndex) ?? null;
  }, [availableFiles, selectedFileIndex]);

  const audioTracks = useMemo<MediaTrack[]>(
    () => mediaInfo?.tracks.filter((track) => track.type === 'audio') ?? [],
    [mediaInfo],
  );
  const subtitleTracks = useMemo<MediaTrack[]>(
    () => mediaInfo?.tracks.filter((track) => track.type === 'subtitle') ?? [],
    [mediaInfo],
  );

  const directPlayable = useMemo(() => {
    if (!selectedFile) return false;
    const ext = extractExtension(selectedFile.path);
    return playableExtensions.has(ext);
  }, [selectedFile]);

  // Static preference: should we *prefer* HLS based on file type + track selection?
  const preferHls = useMemo(() => {
    if (!selectedFile) return false;
    if (!directPlayable) return true;
    if (audioTrack !== null) return true;
    if (subtitleTrack !== null) return true;
    return false;
  }, [selectedFile, directPlayable, audioTrack, subtitleTrack]);

  // Derived from runtime activeMode (can change via fallback).
  const useHls = activeMode === 'hls';

  // Sync activeMode when static preference changes (file/track selection).
  useEffect(() => {
    setActiveMode(preferHls ? 'hls' : 'direct');
  }, [preferHls]);

  // Always compute both URLs so fallback can switch instantly.
  const appendToken = useCallback((value: string, key: string, token: number) => {
    if (token <= 0) return value;
    return `${value}${value.includes('?') ? '&' : '?'}${key}=${token}`;
  }, []);

  const directStreamUrl = useMemo(() => {
    if (!selectedTorrent || selectedFileIndex === null) return '';
    return appendToken(buildStreamUrl(selectedTorrent.id, selectedFileIndex), '_rt', streamRetryToken);
  }, [selectedTorrent, selectedFileIndex, streamRetryToken, appendToken]);

  const hlsStreamUrl = useMemo(() => {
    if (!selectedTorrent || selectedFileIndex === null) return '';
    const audio = audioTrack ?? findDefaultAudioTrack(mediaInfo);
    let url = buildHlsUrl(selectedTorrent.id, selectedFileIndex, {
      audioTrack: audio,
      subtitleTrack,
    });
    if (seekToken > 0) {
      url = appendToken(url, '_st', seekToken);
    }
    return appendToken(url, '_rt', streamRetryToken);
  }, [selectedTorrent, selectedFileIndex, audioTrack, subtitleTrack, mediaInfo, seekToken, streamRetryToken, appendToken]);

  // The URL that VideoPlayer actually uses — switches with activeMode.
  const streamUrl = useHls ? hlsStreamUrl : directStreamUrl;

  // --- Prebuffer probe logic ---

  const runProbe = useCallback(
    async (
      mode: 'direct' | 'hls',
      torrentId: string,
      fileIndex: number,
      hlsUrl: string,
      signal: AbortSignal,
      resumePos: number | null,
      fileComplete: boolean,
    ): Promise<{ success: boolean; mode: 'direct' | 'hls'; seekOffset?: number }> => {
      // For complete files with browser-playable format, try direct first — it's faster than HLS.
      // Skip direct probe for non-playable formats (e.g. MKV HEVC) to avoid
      // a wasted direct→HLS fallback cycle that loses the resume position.
      if ((fileComplete && directPlayable) || mode === 'direct') {
        const ok = await probeDirectStream(torrentId, fileIndex, signal);
        if (signal.aborted) return { success: false, mode };
        if (ok) return { success: true, mode: 'direct' };
        // Direct probe failed — fall through to HLS.
        if (mode === 'direct') mode = 'hls';
      }

      // HLS probe with resume-during-probe optimization.
      let probeSeekOffset: number | undefined;
      if (resumePos !== null && resumePos > 0) {
        try {
          const audio = audioTrack ?? findDefaultAudioTrack(mediaInfo);
          const result = await hlsSeek(torrentId, fileIndex, resumePos, {
            audioTrack: audio,
            subtitleTrack,
          });
          if (signal.aborted) return { success: false, mode };
          probeSeekOffset = result.seekTime;
        } catch {
          // Seek failed — still try probing from start.
          if (signal.aborted) return { success: false, mode };
        }
      }

      // Poll manifest until segments appear.
      // Use the base HLS URL (without seek/retry tokens) if we did a seek,
      // since the seek creates a new manifest.
      const manifestUrl = probeSeekOffset !== undefined
        ? buildHlsUrl(torrentId, fileIndex, {
            audioTrack: audioTrack ?? findDefaultAudioTrack(mediaInfo),
            subtitleTrack,
          })
        : hlsUrl;
      for (let attempt = 0; attempt < HLS_PROBE_MAX_ATTEMPTS; attempt += 1) {
        if (signal.aborted) return { success: false, mode };
        const ok = await probeHlsManifest(manifestUrl, signal);
        if (signal.aborted) return { success: false, mode };
        if (ok) return { success: true, mode: 'hls', seekOffset: probeSeekOffset };
        if (attempt < HLS_PROBE_MAX_ATTEMPTS - 1) {
          // Use faster polling for first attempts (cache hit scenario).
          const interval = attempt < 3 ? 500 : HLS_PROBE_INTERVAL_MS;
          await sleep(interval);
        }
      }
      return { success: false, mode: 'hls' };
    },
    [audioTrack, subtitleTrack, mediaInfo, directPlayable],
  );

  // Launch probe whenever file selection, retry token, or mode changes.
  useEffect(() => {
    if (!selectedTorrent || selectedFileIndex === null) {
      setPrebufferPhase('idle');
      return;
    }

    probeRetryCountRef.current = 0;

    // Abort any running probe.
    probeAbortRef.current?.abort();
    const controller = new AbortController();
    probeAbortRef.current = controller;

    const currentMode = preferHls ? 'hls' : 'direct';
    setActiveMode(currentMode);
    setPrebufferPhase('probing');

    const resumePos = resumePositionRef.current;
    const fileComplete = sessionState?.progress != null && sessionState.progress >= 0.99;

    void (async () => {
      const result = await runProbe(
        currentMode,
        selectedTorrent.id,
        selectedFileIndex,
        hlsStreamUrl,
        controller.signal,
        resumePos,
        fileComplete,
      );
      if (controller.signal.aborted) return;

      if (result.success) {
        setActiveMode(result.mode);
        if (result.seekOffset !== undefined) {
          setSeekOffset(result.seekOffset);
          setSeekToken(Date.now());
        }
        setPrebufferPhase('ready');
      } else {
        if (probeRetryCountRef.current < 5) {
          probeRetryCountRef.current += 1;
          setPrebufferPhase('retrying');
          const delay = Math.min(probeRetryCountRef.current * 3000, 10000);
          await sleep(delay);
          if (controller.signal.aborted) return;
          setStreamRetryToken((t) => t + 1);
          return;
        }
        setPrebufferPhase('error');
      }
    })();

    return () => {
      controller.abort();
    };
    // We intentionally depend on streamRetryToken to re-probe on retry.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedTorrent?.id, selectedFileIndex, streamRetryToken]);

  // --- Fallback trigger (called from VideoPlayer on decode error) ---

  const triggerFallbackToHls = useCallback(() => {
    if (activeMode === 'hls') return;
    if (!selectedTorrent || selectedFileIndex === null) return;

    // Abort any running probe.
    probeAbortRef.current?.abort();
    const controller = new AbortController();
    probeAbortRef.current = controller;

    setActiveMode('hls');
    setPrebufferPhase('probing');
    setVideoError(null);

    // Preserve resume position so HLS starts from the right offset.
    // Use the video's current playback position if no explicit resume was set.
    const video = videoRef.current;
    const currentPos = video && Number.isFinite(video.currentTime) && video.currentTime > 0
      ? video.currentTime
      : null;
    const resumePos = resumePositionRef.current ?? currentPos;

    void (async () => {
      const result = await runProbe(
        'hls',
        selectedTorrent.id,
        selectedFileIndex,
        hlsStreamUrl,
        controller.signal,
        resumePos,
        false,
      );
      if (controller.signal.aborted) return;
      if (result.success) {
        if (result.seekOffset !== undefined) {
          setSeekOffset(result.seekOffset);
          setSeekToken(Date.now());
        }
        setPrebufferPhase('ready');
      } else {
        setPrebufferPhase('error');
        setVideoError('Stream unavailable. HLS transcode did not become ready.');
      }
    })();
  }, [activeMode, selectedTorrent, selectedFileIndex, hlsStreamUrl, runProbe]);

  /** Set a resume position that will be used during the next HLS probe. */
  const setPrebufferResumePosition = useCallback((position: number | null) => {
    resumePositionRef.current = position;
  }, []);

  // --- Media info polling (unchanged) ---

  useEffect(() => {
    if (!selectedTorrent || selectedFileIndex === null) {
      setMediaInfo(null);
      mediaRetryCountRef.current = 0;
      return;
    }

    let cancelled = false;
    let timer: number | null = null;

    let gotTracks = false;

    const fetchInfo = async () => {
      if (mediaInfoInFlightRef.current) return;
      mediaInfoInFlightRef.current = true;
      try {
        const info = await getMediaInfo(selectedTorrent.id, selectedFileIndex);
        if (cancelled) return;
        setMediaInfo((prev) => {
          if (!prev) return info;
          return info.tracks.length >= prev.tracks.length ? info : prev;
        });
        // Defense-in-depth: if the source has a non-zero startTime and we
        // haven't applied a seek offset yet, record it so the player can
        // compensate if HLS segments carry original PTS values.
        if (info.startTime && info.startTime > 0 && seekOffset === 0) {
          setSeekOffset(info.startTime);
        }
        // Stop polling once we have track info — no need to keep fetching.
        if (info.tracks.length > 0) {
          gotTracks = true;
          if (timer !== null) {
            window.clearInterval(timer);
            timer = null;
          }
        }
      } catch {
        if (cancelled) return;
        setMediaInfo((prev) => prev ?? { tracks: [], duration: 0, subtitlesReady: false });
      } finally {
        mediaInfoInFlightRef.current = false;
      }

      mediaRetryCountRef.current += 1;
      if (mediaRetryCountRef.current >= 12 && timer !== null) {
        window.clearInterval(timer);
        timer = null;
      }
    };

    mediaRetryCountRef.current = 0;
    void fetchInfo();
    timer = window.setInterval(() => {
      if (gotTracks) {
        if (timer !== null) { window.clearInterval(timer); timer = null; }
        return;
      }
      void fetchInfo();
    }, 5000);

    return () => {
      cancelled = true;
      if (timer !== null) {
        window.clearInterval(timer);
      }
    };
  }, [selectedTorrent?.id, selectedFileIndex, mediaInfoToken]);

  // Reset when torrent changes.
  useEffect(() => {
    setSelectedFileIndex(null);
    setAudioTrack(null);
    setSubtitleTrack(null);
    setMediaInfo(null);
    setVideoError(null);
    setSeekOffset(0);
    setSeekToken(0);
    setStreamRetryToken(0);
    setPrebufferPhase('idle');
    setTrackSwitchInProgress(false);
    trackSwitchTokenRef.current += 1;
    resumePositionRef.current = null;
    // Cancel any pending/in-flight debounced seek.
    if (seekDebounceRef.current !== null) {
      clearTimeout(seekDebounceRef.current);
      seekDebounceRef.current = null;
    }
    if (seekAbortRef.current) {
      seekAbortRef.current.abort();
      seekAbortRef.current = null;
    }
    if (seekResolveRef.current) {
      seekResolveRef.current();
      seekResolveRef.current = null;
    }
  }, [selectedTorrent?.id]);

  // Reset track selection when file changes.
  useEffect(() => {
    setAudioTrack(null);
    setSubtitleTrack(null);
    setVideoError(null);
    setSeekOffset(0);
    setSeekToken(0);
    setStreamRetryToken(0);
    setTrackSwitchInProgress(false);
    trackSwitchTokenRef.current += 1;
    resumePositionRef.current = null;
    // Cancel any pending/in-flight debounced seek.
    if (seekDebounceRef.current !== null) {
      clearTimeout(seekDebounceRef.current);
      seekDebounceRef.current = null;
    }
    if (seekAbortRef.current) {
      seekAbortRef.current.abort();
      seekAbortRef.current = null;
    }
    if (seekResolveRef.current) {
      seekResolveRef.current();
      seekResolveRef.current = null;
    }
  }, [selectedFileIndex]);

  // Clean up debounced seek on unmount.
  useEffect(() => {
    return () => {
      if (seekDebounceRef.current !== null) clearTimeout(seekDebounceRef.current);
      seekAbortRef.current?.abort();
      if (seekResolveRef.current) seekResolveRef.current();
    };
  }, []);

  // Map native HTMLVideoElement errors.
  // Auto-fallback to HLS on decode/format errors when in direct mode.
  useEffect(() => {
    if (useHls) return;
    const video = videoRef.current;
    if (!video) return;

    const onError = () => {
      const err = video.error;
      if (!err) return;
      // Auto-fallback for decode (3) and not-supported (4) errors.
      if ((err.code === 3 || err.code === 4) && activeMode === 'direct') {
        triggerFallbackToHls();
        return;
      }
      const messages: Record<number, string> = {
        1: 'Video loading aborted',
        2: 'Network error while loading video',
        3: 'Video decoding failed - format may not be supported by your browser',
        4: 'Video format not supported by your browser',
      };
      setVideoError(messages[err.code] ?? `Video error (code ${err.code})`);
    };

    video.addEventListener('error', onError);
    return () => video.removeEventListener('error', onError);
  }, [videoRef, streamUrl, useHls, activeMode, triggerFallbackToHls]);

  const selectFile = useCallback((index: number) => {
    setSelectedFileIndex(index);
    setVideoError(null);
  }, []);

  const selectAudioTrack = useCallback(
    (index: number | null) => {
      // In HLS mode, tell the server to start transcoding from the current
      // position with the new audio track BEFORE changing the stream URL.
      // Otherwise the server starts a new FFmpeg job from 0:00.
      if (useHls && selectedTorrent && selectedFileIndex !== null) {
        const video = videoRef.current;
        const currentTime = video && Number.isFinite(video.currentTime) ? video.currentTime : 0;
        if (currentTime > 0) {
          const audio = index ?? findDefaultAudioTrack(mediaInfo);
          const token = ++trackSwitchTokenRef.current;
          // Synchronously destroy HLS.js to stop all manifest/segment requests
          // for the old track *before* the React re-render cycle.
          hlsDestroyRef.current?.();
          setTrackSwitchInProgress(true);
          setVideoError(null);
          hlsSeek(selectedTorrent.id, selectedFileIndex, currentTime, {
            audioTrack: audio,
            subtitleTrack,
          })
            .then((result) => {
              if (trackSwitchTokenRef.current !== token) return;
              setSeekOffset(result.seekTime);
              setAudioTrack(index);
              setSeekToken(Date.now());
              setTrackSwitchInProgress(false);
            })
            .catch(() => {
              if (trackSwitchTokenRef.current !== token) return;
              // Seek failed — fall through to track switch from beginning.
              setAudioTrack(index);
              setTrackSwitchInProgress(false);
            });
          return;
        }
      }
      setAudioTrack(index);
      setVideoError(null);
    },
    [useHls, selectedTorrent, selectedFileIndex, mediaInfo, subtitleTrack, videoRef],
  );

  const selectSubtitleTrack = useCallback(
    (index: number | null) => {
      if (useHls && selectedTorrent && selectedFileIndex !== null) {
        const video = videoRef.current;
        const currentTime = video && Number.isFinite(video.currentTime) ? video.currentTime : 0;
        if (currentTime > 0) {
          const audio = audioTrack ?? findDefaultAudioTrack(mediaInfo);
          const token = ++trackSwitchTokenRef.current;
          hlsDestroyRef.current?.();
          setTrackSwitchInProgress(true);
          setVideoError(null);
          hlsSeek(selectedTorrent.id, selectedFileIndex, currentTime, {
            audioTrack: audio,
            subtitleTrack: index,
          })
            .then((result) => {
              if (trackSwitchTokenRef.current !== token) return;
              setSeekOffset(result.seekTime);
              setSubtitleTrack(index);
              setSeekToken(Date.now());
              setTrackSwitchInProgress(false);
            })
            .catch(() => {
              if (trackSwitchTokenRef.current !== token) return;
              setSubtitleTrack(index);
              setTrackSwitchInProgress(false);
            });
          return;
        }
      }
      setSubtitleTrack(index);
      setVideoError(null);
    },
    [useHls, selectedTorrent, selectedFileIndex, audioTrack, mediaInfo, videoRef],
  );

  const hlsSeekTo = useCallback(
    (absoluteTime: number): Promise<void> => {
      if (!selectedTorrent || selectedFileIndex === null) return Promise.resolve();

      // Clamp to valid range.
      const duration = mediaInfo?.duration ?? 0;
      let clamped = absoluteTime;
      if (duration > 0) {
        clamped = Math.max(0, Math.min(clamped, duration));
      } else {
        // Duration unknown — just prevent negative values.
        clamped = Math.max(0, clamped);
      }

      // Debounce rapid seeks — cancel any pending timer and in-flight request.
      if (seekDebounceRef.current !== null) {
        clearTimeout(seekDebounceRef.current);
        seekDebounceRef.current = null;
      }
      if (seekAbortRef.current) {
        seekAbortRef.current.abort();
        seekAbortRef.current = null;
      }
      // Resolve the previous debounced promise so callers are not left hanging.
      if (seekResolveRef.current) {
        seekResolveRef.current();
        seekResolveRef.current = null;
      }

      return new Promise<void>((resolve, reject) => {
        seekResolveRef.current = resolve;
        seekDebounceRef.current = setTimeout(async () => {
          seekResolveRef.current = null;
          seekDebounceRef.current = null;
          const controller = new AbortController();
          seekAbortRef.current = controller;

          const audio = audioTrack ?? findDefaultAudioTrack(mediaInfo);
          const maxAttempts = 3;
          try {
            for (let attempt = 1; attempt <= maxAttempts; attempt += 1) {
              try {
                const result = await hlsSeek(selectedTorrent.id, selectedFileIndex, clamped, {
                  audioTrack: audio,
                  subtitleTrack,
                  signal: controller.signal,
                });
                if (controller.signal.aborted) return;
                setSeekOffset(result.seekTime);
                setSeekToken(Date.now());
                setVideoError(null);
                resolve();
                return;
              } catch (error) {
                if (controller.signal.aborted) return;
                if (attempt >= maxAttempts || !isRetryableSeekError(error)) {
                  throw error;
                }
                const delayMs = attempt === 1 ? 450 : 1200;
                await sleep(delayMs);
                if (controller.signal.aborted) return;
              }
            }
            resolve();
          } catch (error) {
            if (controller.signal.aborted) return;
            if (isApiError(error)) {
              setVideoError(`seek failed: ${error.message}`);
            } else {
              setVideoError('seek failed');
            }
            reject(error);
          }
        }, 300);
      });
    },
    [selectedTorrent, selectedFileIndex, audioTrack, subtitleTrack, mediaInfo],
  );

  const retryStreamInitialization = useCallback(() => {
    // Capture current absolute playback position so the next probe can
    // seek FFmpeg to the right place instead of re-encoding from 0:00.
    const video = videoRef.current;
    if (video && Number.isFinite(video.currentTime) && video.currentTime > 0) {
      resumePositionRef.current = seekOffset + video.currentTime;
    }
    setVideoError(null);
    setPrebufferPhase('idle');
    // Re-fetch mediaInfo so mediaDuration is available after server restart.
    setMediaInfoToken((prev) => prev + 1);
    setStreamRetryToken((prev) => prev + 1);
  }, [seekOffset]);

  return {
    availableFiles,
    selectedFileIndex,
    selectedFile,
    streamUrl,
    useHls,
    videoRef,
    videoError,
    mediaInfo,
    audioTracks,
    subtitleTracks,
    audioTrack,
    subtitleTrack,
    seekOffset,
    hlsSeekTo,
    retryStreamInitialization,
    selectFile,
    selectAudioTrack,
    selectSubtitleTrack,
    prebufferPhase,
    activeMode,
    triggerFallbackToHls,
    setPrebufferResumePosition,
    trackSwitchInProgress,
    hlsDestroyRef,
  };
}
