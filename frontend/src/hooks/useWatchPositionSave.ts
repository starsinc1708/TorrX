import { useCallback, useEffect, useRef } from 'react';
import { saveWatchPosition } from '../api';
import { upsertTorrentWatchState } from '../watchState';

interface UseWatchPositionSaveOptions {
  torrentId: string | null;
  selectedFile: { path: string } | null;
  enabled: boolean;
  saveIntervalMs?: number;
}

/**
 * Autosaves watch position every 5 seconds while playing.
 * Also saves on unmount and when file changes.
 */
export function useWatchPositionSave(
  getCurrentTime: () => number,
  options: UseWatchPositionSaveOptions,
) {
  const { torrentId, selectedFile, enabled, saveIntervalMs = 5000 } = options;
  const lastSaveTimeRef = useRef(0);
  const saveIntervalRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const savePosition = useCallback(
    async (time: number) => {
      if (!torrentId || !selectedFile) return;

      try {
        await saveWatchPosition(torrentId, selectedFile.path, time);
        upsertTorrentWatchState(torrentId, selectedFile.path, time, Date.now());
      } catch {
        // Ignore save errors
      }
    },
    [torrentId, selectedFile],
  );

  const trySave = useCallback(() => {
    if (!enabled) return;

    const now = Date.now();
    if (now - lastSaveTimeRef.current >= saveIntervalMs) {
      lastSaveTimeRef.current = now;
      const time = getCurrentTime();
      if (time > 0) {
        void savePosition(time);
      }
    }
  }, [enabled, saveIntervalMs, getCurrentTime, savePosition]);

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
      if (torrentId && selectedFile) {
        const time = getCurrentTime();
        if (time > 0) {
          void savePosition(time);
        }
      }
    };
  }, [torrentId, selectedFile, getCurrentTime, savePosition]);

  return { savePosition };
}
