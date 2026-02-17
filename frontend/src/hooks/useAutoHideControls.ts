import { useCallback, useEffect, useRef, useState } from 'react';

interface UseAutoHideControlsOptions {
  playing: boolean;
  menuOpen: boolean;
}

export function useAutoHideControls(options: UseAutoHideControlsOptions): {
  showControls: boolean;
  cursorHidden: boolean;
  resetHideTimer: () => void;
} {
  const { playing, menuOpen } = options;
  const [showControls, setShowControls] = useState(true);
  const [cursorHidden, setCursorHidden] = useState(false);
  const playingRef = useRef(false);
  const hideTimerRef = useRef<ReturnType<typeof setTimeout>>();

  // Keep playingRef in sync so timeouts read current value.
  useEffect(() => { playingRef.current = playing; }, [playing]);

  const resetHideTimer = useCallback(() => {
    setShowControls(true);
    setCursorHidden(false);
    if (hideTimerRef.current) clearTimeout(hideTimerRef.current);
    if (menuOpen) return;
    hideTimerRef.current = setTimeout(() => {
      if (playingRef.current) {
        setShowControls(false);
        setCursorHidden(true);
      }
    }, 3000);
  }, [menuOpen]);

  // When playing stops, show controls.
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

  // When a menu opens, pin controls visible.
  useEffect(() => {
    if (menuOpen) {
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
  }, [menuOpen, playing, resetHideTimer]);

  // Cleanup timer on unmount.
  useEffect(() => {
    return () => {
      if (hideTimerRef.current) {
        clearTimeout(hideTimerRef.current);
      }
    };
  }, []);

  return { showControls, cursorHidden, resetHideTimer };
}
