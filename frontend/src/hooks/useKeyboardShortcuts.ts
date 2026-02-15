import { useEffect } from 'react';

interface KeyboardShortcutHandlers {
  onPlayPause: () => void;
  onSeekBackward: () => void;
  onSeekForward: () => void;
  onToggleMute: () => void;
  onToggleFullscreen: () => void;
  onTakeScreenshot: () => void;
}

/**
 * Registers keyboard shortcuts for video player controls.
 * Ignores shortcuts when user is typing in an input/textarea.
 */
export function useKeyboardShortcuts(handlers: KeyboardShortcutHandlers) {
  const {
    onPlayPause,
    onSeekBackward,
    onSeekForward,
    onToggleMute,
    onToggleFullscreen,
    onTakeScreenshot,
  } = handlers;

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      // Ignore shortcuts when user is typing
      if (e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement) {
        return;
      }

      switch (e.key) {
        case ' ':
        case 'k':
          e.preventDefault();
          onPlayPause();
          break;
        case 'ArrowLeft':
          e.preventDefault();
          onSeekBackward();
          break;
        case 'ArrowRight':
          e.preventDefault();
          onSeekForward();
          break;
        case 'm':
          e.preventDefault();
          onToggleMute();
          break;
        case 'f':
        case 'F':
          e.preventDefault();
          onToggleFullscreen();
          break;
        case 's':
          e.preventDefault();
          onTakeScreenshot();
          break;
      }
    };

    window.addEventListener('keydown', onKey);
    return () => {
      window.removeEventListener('keydown', onKey);
    };
  }, [onPlayPause, onSeekBackward, onSeekForward, onToggleMute, onToggleFullscreen, onTakeScreenshot]);
}
