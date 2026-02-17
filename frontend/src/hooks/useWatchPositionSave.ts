import { useCallback, useEffect, useRef } from 'react';
import { saveWatchPosition } from '../api';
import { upsertTorrentWatchState } from '../watchState';

interface UseWatchPositionSaveOptions {
  torrentId: string | null;
  fileIndex: number | null;
  torrentName?: string;
  filePath?: string;
  seekOffset?: number;
  mediaDuration?: number;
  enabled: boolean;
  saveIntervalMs?: number;
}

/**
 * Autosaves watch position every 5 seconds while playing.
 * Also saves on unmount and when file changes.
 *
 * When `seekOffset` is provided the saved position is shifted to absolute
 * time (seekOffset + currentTime) and `mediaDuration` is preferred over
 * the raw video element duration.
 */
export function useWatchPositionSave(
  getCurrentTime: () => number,
  getDuration: () => number,
  options: UseWatchPositionSaveOptions,
) {
  const {
    torrentId,
    fileIndex,
    torrentName,
    filePath,
    seekOffset = 0,
    mediaDuration = 0,
    enabled,
    saveIntervalMs = 5000,
  } = options;
  const lastSaveTimeRef = useRef(0);
  const saveIntervalRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const savePosition = useCallback(
    async (position: number, duration: number) => {
      if (!torrentId || fileIndex === null) return;
      const absPosition = seekOffset + position;
      const absDuration = mediaDuration > 0 ? mediaDuration : duration;

      try {
        await saveWatchPosition(torrentId, fileIndex, absPosition, absDuration, torrentName, filePath);
        upsertTorrentWatchState({
          torrentId,
          fileIndex,
          position: absPosition,
          duration: absDuration,
          torrentName,
          filePath,
        });
      } catch {
        // Ignore save errors
      }
    },
    [torrentId, fileIndex, torrentName, filePath, seekOffset, mediaDuration],
  );

  const trySave = useCallback(() => {
    if (!enabled) return;

    const now = Date.now();
    if (now - lastSaveTimeRef.current >= saveIntervalMs) {
      lastSaveTimeRef.current = now;
      const position = getCurrentTime();
      const duration = getDuration();
      if (position > 0 && duration > 0) {
        void savePosition(position, duration);
      }
    }
  }, [enabled, saveIntervalMs, getCurrentTime, getDuration, savePosition]);

  // Autosave while playing
  useEffect(() => {
    if (!enabled) {
      if (saveIntervalRef.current) {
        clearInterval(saveIntervalRef.current);
        saveIntervalRef.current = null;
      }
      return;
    }

    saveIntervalRef.current = setInterval(trySave, 1000);

    return () => {
      if (saveIntervalRef.current) {
        clearInterval(saveIntervalRef.current);
        saveIntervalRef.current = null;
      }
    };
  }, [enabled, trySave]);

  // Save on unmount
  useEffect(() => {
    return () => {
      if (torrentId && fileIndex !== null) {
        const position = getCurrentTime();
        const duration = getDuration();
        if (position > 0 && duration > 0) {
          void savePosition(position, duration);
        }
      }
    };
  }, [torrentId, fileIndex, getCurrentTime, getDuration, savePosition]);

  return { savePosition, trySave };
}
