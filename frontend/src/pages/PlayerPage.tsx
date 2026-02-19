import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { useLocation, useNavigate, useParams } from 'react-router-dom';
import { AlertTriangle, X } from 'lucide-react';
import VideoPlayer from '../components/VideoPlayer';
import PlayerFilesPanel from '../components/PlayerFilesPanel';
import { Alert } from '../components/ui/alert';
import { Button } from '../components/ui/button';
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '../components/ui/dialog';
import { focusTorrent, getPlayerHealth, getTorrent, getWatchHistory, isApiError, startTorrent } from '../api';
import { useSessionState } from '../hooks/useSessionState';
import { useWS } from '../app/providers/WebSocketProvider';
import { useVideoPlayer } from '../hooks/useVideoPlayer';
import { getTorrentPlayerPreferences, patchTorrentPlayerPreferences } from '../playerPreferences';
import type { PlayerHealth, TorrentRecord, WatchPosition } from '../types';
import { formatTime, isVideoFile } from '../utils';
import { cn } from '../lib/cn';
import { getTorrentWatchState, upsertTorrentWatchState, type TorrentWatchState } from '../watchState';
import { useToast } from '../app/providers/ToastProvider';

type ResumeNavigationState = {
  resume?: boolean;
  torrentId?: string;
  fileIndex?: number;
  position?: number;
  duration?: number;
  torrentName?: string;
  filePath?: string;
};

const PlayerPage: React.FC = () => {
  const { torrentId, fileIndex } = useParams<{ torrentId: string; fileIndex?: string }>();
  const location = useLocation();
  const navigate = useNavigate();

  const [torrent, setTorrent] = useState<TorrentRecord | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [resumeHint, setResumeHint] = useState<TorrentWatchState | null>(null);
  const [resumeRequest, setResumeRequest] = useState<{ requestId: number; fileIndex: number; position: number } | null>(null);
  const [resumeHintDismissed, setResumeHintDismissed] = useState(false);
  const [playerHealth, setPlayerHealth] = useState<PlayerHealth | null>(null);
  const [infoOpen, setInfoOpen] = useState(false);
  const [filesPanelOpen, setFilesPanelOpen] = useState(true);
  const { toast } = useToast();

  const lastWatchKey = 'lastWatch';
  const autoStartRef = React.useRef<string | null>(null);
  const healthRequestInFlightRef = React.useRef(false);
  const pendingResumeFromNavigationRef = React.useRef<{ fileIndex: number; position: number } | null>(null);
  const autoRecoveredResumeKeyRef = React.useRef<string | null>(null);

  const isValidResumePoint = React.useCallback((value: TorrentWatchState | null): value is TorrentWatchState => {
    if (!value) return false;
    if (!Number.isFinite(value.position) || value.position < 10) return false;
    if (!Number.isFinite(value.duration) || value.duration <= 0) return false;
    if (value.position >= value.duration - 15) return false;
    return Number.isInteger(value.fileIndex) && value.fileIndex >= 0;
  }, []);

  const toWatchState = React.useCallback((item: WatchPosition): TorrentWatchState => {
    return {
      torrentId: item.torrentId,
      fileIndex: item.fileIndex,
      position: item.position,
      duration: item.duration,
      torrentName: item.torrentName || undefined,
      filePath: item.filePath || undefined,
      updatedAt: item.updatedAt,
    };
  }, []);

  useEffect(() => {
    if (!torrentId) {
      setTorrent(null);
      return;
    }
    setTorrent(null);
    let cancelled = false;
    getTorrent(torrentId)
      .then((record) => {
        if (!cancelled) {
          setTorrent(record);
          setLoadError(null);
        }
      })
      .catch((error) => {
        if (cancelled) return;
        if (isApiError(error)) setLoadError(`${error.code ?? 'error'}: ${error.message}`);
        else if (error instanceof Error) setLoadError(error.message);
      });
    return () => {
      cancelled = true;
    };
  }, [torrentId]);

  const { states: wsStates, health: wsHealth } = useWS();
  const { sessionState, setAutoRefreshState } = useSessionState(torrentId ?? null, wsStates);
  useEffect(() => {
    if (torrentId) setAutoRefreshState(true);
  }, [torrentId, setAutoRefreshState]);

  // Auto-start torrent when entering player to ensure stream availability.
  useEffect(() => {
    if (!torrentId) return;
    if (autoStartRef.current === torrentId) return;
    autoStartRef.current = torrentId;
    startTorrent(torrentId).catch(() => {});
  }, [torrentId]);

  // Focus the torrent being played to give it maximum download bandwidth.
  useEffect(() => {
    if (!torrentId) return;
    focusTorrent(torrentId).catch(() => {});
  }, [torrentId]);

  const effectiveSessionState = useMemo(() => sessionState ?? null, [sessionState]);

  const {
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
  } = useVideoPlayer(torrent, effectiveSessionState);

  const torrentPreferences = useMemo(
    () => (torrentId ? getTorrentPlayerPreferences(torrentId) : null),
    [torrentId],
  );

  const initialPlaybackRate = useMemo(() => {
    const value = torrentPreferences?.playbackRate;
    if (typeof value !== 'number' || !Number.isFinite(value)) return 1;
    return Math.max(0.25, Math.min(2, value));
  }, [torrentPreferences?.playbackRate]);

  const initialQualityLevel = useMemo(() => {
    const value = torrentPreferences?.preferredQualityLevel;
    if (typeof value !== 'number' || !Number.isInteger(value) || value < -1) return -1;
    return value;
  }, [torrentPreferences?.preferredQualityLevel]);

  // Keep selected file in sync with URL param.
  useEffect(() => {
    if (fileIndex === undefined) return;
    if (availableFiles.length === 0) return;
    const idx = Number.parseInt(fileIndex, 10);
    if (Number.isNaN(idx)) return;
    if (!availableFiles.some((f) => f.index === idx)) return;
    if (selectedFileIndex === idx) return;
    selectFile(idx);
  }, [fileIndex, availableFiles, selectedFileIndex, selectFile]);

  const handleSelectFile = useCallback(
    (index: number) => {
      selectFile(index);
      if (torrentId) navigate(`/watch/${torrentId}/${index}`, { replace: true });
    },
    [selectFile, torrentId, navigate],
  );

  useEffect(() => {
    if (!torrentId) return;
    const state = (location.state as ResumeNavigationState | null) ?? null;
    if (!state?.resume) return;

    const stateTorrentId = String(state.torrentId ?? '').trim();
    if (stateTorrentId && stateTorrentId !== torrentId) return;

    const targetFileIndex = Number(state.fileIndex);
    const targetPosition = Number(state.position);
    if (!Number.isInteger(targetFileIndex) || targetFileIndex < 0) return;
    if (!Number.isFinite(targetPosition) || targetPosition <= 0) return;

    pendingResumeFromNavigationRef.current = { fileIndex: targetFileIndex, position: targetPosition };
    setResumeHintDismissed(true);
    // Pre-seed resume position so the prebuffer probe can call hlsSeek during probe.
    setPrebufferResumePosition(targetPosition);

    if (selectedFileIndex === targetFileIndex) {
      setResumeRequest({
        requestId: Date.now(),
        fileIndex: targetFileIndex,
        position: targetPosition,
      });
      pendingResumeFromNavigationRef.current = null;
    } else {
      handleSelectFile(targetFileIndex);
    }

    navigate(location.pathname, { replace: true, state: null });
  }, [torrentId, location.state, location.pathname, selectedFileIndex, handleSelectFile, navigate, setPrebufferResumePosition]);

  useEffect(() => {
    const pending = pendingResumeFromNavigationRef.current;
    if (!pending) return;
    if (selectedFileIndex === null || selectedFileIndex !== pending.fileIndex) return;
    setResumeHintDismissed(true);
    setResumeRequest({
      requestId: Date.now(),
      fileIndex: pending.fileIndex,
      position: pending.position,
    });
    pendingResumeFromNavigationRef.current = null;
  }, [selectedFileIndex]);

  const handleSelectAudioTrack = useCallback(
    (index: number | null) => {
      selectAudioTrack(index);
      if (torrentId) {
        patchTorrentPlayerPreferences(torrentId, { audioTrack: index });
      }
    },
    [selectAudioTrack, torrentId],
  );

  const handleSelectSubtitleTrack = useCallback(
    (index: number | null) => {
      selectSubtitleTrack(index);
      if (torrentId) {
        patchTorrentPlayerPreferences(torrentId, { subtitleTrack: index });
      }
    },
    [selectSubtitleTrack, torrentId],
  );

  const handlePlaybackRateChange = useCallback(
    (rate: number) => {
      if (!torrentId) return;
      patchTorrentPlayerPreferences(torrentId, { playbackRate: rate });
    },
    [torrentId],
  );

  const handleQualityLevelChange = useCallback(
    (level: number) => {
      if (!torrentId) return;
      patchTorrentPlayerPreferences(torrentId, { preferredQualityLevel: level });
    },
    [torrentId],
  );

  const appliedPreferencesRef = React.useRef<string | null>(null);
  useEffect(() => {
    if (!torrentId) return;
    if (!torrent) return;
    if (appliedPreferencesRef.current === torrentId) return;
    if (torrentPreferences) {
      if (Object.prototype.hasOwnProperty.call(torrentPreferences, 'audioTrack')) {
        selectAudioTrack(torrentPreferences.audioTrack ?? null);
      }
      if (Object.prototype.hasOwnProperty.call(torrentPreferences, 'subtitleTrack')) {
        selectSubtitleTrack(torrentPreferences.subtitleTrack ?? null);
      }
    }
    appliedPreferencesRef.current = torrentId;
  }, [torrentId, torrent, torrentPreferences, selectAudioTrack, selectSubtitleTrack]);

  useEffect(() => {
    appliedPreferencesRef.current = null;
  }, [torrentId]);

  useEffect(() => {
    if (!torrentId) return;
    if (!torrent) return;
    if (fileIndex !== undefined) return;
    if (selectedFileIndex !== null) return;
    if (availableFiles.length === 0) return;
    // Auto-select the largest video file, or the single file if only one.
    if (availableFiles.length === 1) {
      handleSelectFile(availableFiles[0].index);
      return;
    }
    const videoFiles = availableFiles.filter((f) => isVideoFile(f.path));
    if (videoFiles.length > 0) {
      const largest = videoFiles.reduce((a, b) => (b.length > a.length ? b : a));
      handleSelectFile(largest.index);
    }
  }, [torrentId, torrent, fileIndex, selectedFileIndex, availableFiles, handleSelectFile]);

  useEffect(() => {
    if (!torrentId) {
      setResumeHint(null);
      setResumeHintDismissed(false);
      autoRecoveredResumeKeyRef.current = null;
      return;
    }
    const local = getTorrentWatchState(torrentId);
    setResumeHint(local);
    setResumeHintDismissed(false);
    autoRecoveredResumeKeyRef.current = null;

    let cancelled = false;
    void getWatchHistory(300)
      .then((entries) => {
        if (cancelled || !Array.isArray(entries)) return;
        const latestServer = entries
          .filter((entry) => entry.torrentId === torrentId)
          .reduce<TorrentWatchState | null>((acc, entry) => {
            const next = toWatchState(entry);
            if (!acc) return next;
            const accTs = new Date(acc.updatedAt || 0).getTime();
            const nextTs = new Date(next.updatedAt || 0).getTime();
            return nextTs > accTs ? next : acc;
          }, null);
        if (!latestServer) return;

        const localTs = local ? new Date(local.updatedAt || 0).getTime() : 0;
        const serverTs = new Date(latestServer.updatedAt || 0).getTime();
        const preferred = serverTs > localTs ? latestServer : local ?? latestServer;
        setResumeHint(preferred);
        upsertTorrentWatchState({
          torrentId: preferred.torrentId,
          fileIndex: preferred.fileIndex,
          position: preferred.position,
          duration: preferred.duration,
          torrentName: preferred.torrentName,
          filePath: preferred.filePath,
        });
      })
      .catch(() => {});

    return () => {
      cancelled = true;
    };
  }, [torrentId, toWatchState]);

  useEffect(() => {
    if (!torrentId) return;
    const fallbackIndex = fileIndex ? Number.parseInt(fileIndex, 10) : null;
    const targetIndex = selectedFileIndex ?? fallbackIndex;
    if (targetIndex === null || Number.isNaN(targetIndex)) return;
    localStorage.setItem(lastWatchKey, JSON.stringify({ torrentId, fileIndex: targetIndex }));
    window.dispatchEvent(new Event('player:last-watch'));
  }, [torrentId, selectedFileIndex, fileIndex, lastWatchKey]);

  // Use WS health data when available, fall back to REST polling.
  useEffect(() => {
    if (wsHealth) setPlayerHealth(wsHealth);
  }, [wsHealth]);

  useEffect(() => {
    // Skip REST polling when WS is providing health data.
    if (wsHealth) return;

    let cancelled = false;
    let timer: number | null = null;

    const refreshHealth = async () => {
      if (healthRequestInFlightRef.current) return;
      healthRequestInFlightRef.current = true;
      try {
        const health = await getPlayerHealth();
        if (!cancelled) setPlayerHealth(health);
      } catch {
        // Keep last known health data on transient errors.
      } finally {
        healthRequestInFlightRef.current = false;
      }
    };

    void refreshHealth();
    timer = window.setInterval(() => void refreshHealth(), 15000);
    return () => {
      cancelled = true;
      if (timer !== null) window.clearInterval(timer);
    };
  }, [wsHealth]);

  const isResumeHintVisible = useMemo(() => {
    if (resumeHintDismissed) return false;
    if (!isValidResumePoint(resumeHint)) return false;
    if (!availableFiles.some((file) => file.index === resumeHint.fileIndex)) return false;
    return true;
  }, [resumeHintDismissed, resumeHint, availableFiles, isValidResumePoint]);

  useEffect(() => {
    if (fileIndex !== undefined) return;
    if (!torrentId) return;
    if (!isValidResumePoint(resumeHint)) return;
    if (!availableFiles.some((file) => file.index === resumeHint.fileIndex)) return;
    const navState = (location.state as ResumeNavigationState | null) ?? null;
    if (navState?.resume) return;

    const key = `${torrentId}:${resumeHint.fileIndex}:${Math.round(resumeHint.position)}`;
    if (autoRecoveredResumeKeyRef.current === key) return;
    autoRecoveredResumeKeyRef.current = key;
    setResumeHintDismissed(true);
    setPrebufferResumePosition(resumeHint.position);

    if (selectedFileIndex !== resumeHint.fileIndex) {
      pendingResumeFromNavigationRef.current = {
        fileIndex: resumeHint.fileIndex,
        position: resumeHint.position,
      };
      handleSelectFile(resumeHint.fileIndex);
      return;
    }

    setResumeRequest({
      requestId: Date.now(),
      fileIndex: resumeHint.fileIndex,
      position: resumeHint.position,
    });
  }, [fileIndex, torrentId, resumeHint, selectedFileIndex, availableFiles, handleSelectFile, location.state, isValidResumePoint, setPrebufferResumePosition]);

  const handleResumeFromHint = useCallback(() => {
    if (!resumeHint) return;
    setResumeHintDismissed(true);
    setPrebufferResumePosition(resumeHint.position);
    const requestId = Date.now();
    setResumeRequest({ requestId, fileIndex: resumeHint.fileIndex, position: resumeHint.position });
    if (resumeHint.fileIndex !== selectedFileIndex) {
      handleSelectFile(resumeHint.fileIndex);
    }
  }, [resumeHint, selectedFileIndex, handleSelectFile, setPrebufferResumePosition]);

  const handleShowHealth = useCallback(() => {
    if (!playerHealth) {
      toast({ title: 'Playback health', description: 'Health data not available.', variant: 'warning' });
      return;
    }
    const descriptionParts: string[] = [];
    descriptionParts.push(`Active sessions: ${playerHealth.activeSessions}`);
    descriptionParts.push(`HLS jobs: ${playerHealth.hls.activeJobs}`);
    if (playerHealth.issues?.length) {
      descriptionParts.push(`Issues: ${playerHealth.issues.join(' | ')}`);
    }
    if (playerHealth.hls.lastJobError) {
      descriptionParts.push(`Last error: ${playerHealth.hls.lastJobError}`);
    }

    toast({
      title: `Playback health: ${playerHealth.status}`,
      description: descriptionParts.join(' · '),
      variant: playerHealth.status === 'ok' ? 'success' : 'warning',
    });
  }, [playerHealth, toast]);

  if (loadError) {
    return (
      <div className="mx-auto flex min-h-[50vh] max-w-xl flex-col items-center justify-center gap-4 text-center">
        <Alert className="w-full border-destructive/30 bg-destructive/10">
          <div className="flex items-center justify-center gap-2">
            <AlertTriangle className="h-4 w-4" />
            <span>{loadError}</span>
          </div>
        </Alert>
        <Button variant="outline" onClick={() => navigate('/')}>
          Back to catalog
        </Button>
      </div>
    );
  }

  return (
    <div className="h-[calc(100dvh-56px)] overflow-hidden">
      <Dialog open={infoOpen} onOpenChange={setInfoOpen}>
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle>Torrent info</DialogTitle>
          </DialogHeader>
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="rounded-lg border border-border/70 bg-muted/10 p-4">
              <div className="text-xs font-medium text-muted-foreground">ID</div>
              <div className="mt-1 font-mono text-xs">{torrent?.id ?? '-'}</div>
            </div>
            <div className="rounded-lg border border-border/70 bg-muted/10 p-4">
              <div className="text-xs font-medium text-muted-foreground">InfoHash</div>
              <div className="mt-1 font-mono text-xs">{torrent?.infoHash ?? '-'}</div>
            </div>
            <div className="rounded-lg border border-border/70 bg-muted/10 p-4">
              <div className="text-xs font-medium text-muted-foreground">Status</div>
              <div className="mt-1 text-sm font-semibold">{torrent?.status ?? '-'}</div>
            </div>
            <div className="rounded-lg border border-border/70 bg-muted/10 p-4">
              <div className="text-xs font-medium text-muted-foreground">Files</div>
              <div className="mt-1 text-sm font-semibold">{availableFiles.length}</div>
            </div>
          </div>
        </DialogContent>
      </Dialog>

      {/* Two independent blocks: player takes remaining space, files panel is fixed-size. */}
      <div className="flex h-full flex-col overflow-hidden lg:flex-row">

        {/* ── Player block ── explicit calc height on mobile so it never pushes into the files panel */}
        <div className={cn(
          'relative flex-none overflow-hidden lg:h-full lg:flex-1',
          filesPanelOpen ? 'h-[calc(100%-35dvh)]' : 'h-full',
        )}>
          <VideoPlayer
            videoRef={videoRef}
            streamUrl={streamUrl}
            useHls={useHls}
            files={availableFiles}
            selectedFile={selectedFile}
            selectedFileIndex={selectedFileIndex}
            torrentId={torrent?.id ?? null}
            torrentName={torrent?.name ?? null}
            videoError={videoError}
            audioTracks={audioTracks}
            subtitleTracks={subtitleTracks}
            selectedAudioTrack={audioTrack}
            selectedSubtitleTrack={subtitleTrack}
            subtitlesReady={mediaInfo?.subtitlesReady ?? false}
            mediaDuration={mediaInfo?.duration ?? 0}
            seekOffset={seekOffset}
            onHlsSeek={hlsSeekTo}
            onRetryInitialize={retryStreamInitialization}
            initialPlaybackRate={initialPlaybackRate}
            onPlaybackRateChange={handlePlaybackRateChange}
            initialQualityLevel={initialQualityLevel}
            onQualityLevelChange={handleQualityLevelChange}
            sessionState={effectiveSessionState}
            onSelectFile={handleSelectFile}
            onSelectAudioTrack={handleSelectAudioTrack}
            onSelectSubtitleTrack={handleSelectSubtitleTrack}
            onOpenInfo={() => setInfoOpen(true)}
            playerHealth={playerHealth}
            onShowHealth={handleShowHealth}
            filesPanelOpen={filesPanelOpen}
            onToggleFilesPanel={() => setFilesPanelOpen((p) => !p)}
            resumeRequest={resumeRequest}
            onResumeHandled={(requestId) => {
              setResumeRequest((prev) => (prev && prev.requestId === requestId ? null : prev));
            }}
            prebufferPhase={prebufferPhase}
            activeMode={activeMode}
            onFallbackToHls={triggerFallbackToHls}
            trackSwitchInProgress={trackSwitchInProgress}
            hlsDestroyRef={hlsDestroyRef}
          />

          {isResumeHintVisible && resumeHint ? (
            <div className="absolute bottom-4 left-4 right-4 z-30 animate-[ts-fade-in_300ms_ease-out]">
              <div className="flex items-center justify-between gap-3 rounded-xl border border-border/70 bg-popover/95 px-4 py-3 shadow-elevated backdrop-blur-md">
                <div className="min-w-0 text-sm text-muted-foreground">
                  Continue from{' '}
                  <span className="font-semibold text-foreground">{formatTime(resumeHint.position)}</span>
                  {resumeHint.filePath ? (
                    <span className="ml-1.5 text-xs text-muted-foreground">
                      ({resumeHint.filePath.split('/').pop() ?? ''})
                    </span>
                  ) : null}
                </div>
                <div className="flex shrink-0 items-center gap-2">
                  <Button size="sm" onClick={handleResumeFromHint}>
                    Continue
                  </Button>
                  <button
                    type="button"
                    className="inline-flex h-8 w-8 items-center justify-center rounded-lg text-muted-foreground hover:bg-accent hover:text-foreground"
                    onClick={() => setResumeHintDismissed(true)}
                    title="Dismiss"
                  >
                    <X className="h-4 w-4" />
                  </button>
                </div>
              </div>
            </div>
          ) : null}
        </div>

        {/* ── Files panel block ── fixed height on mobile, fixed width on desktop */}
        {filesPanelOpen && (
          <div className="h-[35dvh] flex-none overflow-hidden border-t border-border/70 lg:h-full lg:w-[clamp(300px,34vw,440px)] lg:border-l lg:border-t-0">
            <PlayerFilesPanel
              files={availableFiles}
              selectedFileIndex={selectedFileIndex}
              sessionState={effectiveSessionState}
              onSelectFile={handleSelectFile}
              className="h-full"
            />
          </div>
        )}

      </div>
    </div>
  );
};

export default PlayerPage;
