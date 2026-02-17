import { useCallback, useEffect, useState } from 'react';

export function useFullscreen(containerRef: React.RefObject<HTMLElement | null>): {
  isFullscreen: boolean;
  toggleFullscreen: () => void;
  getFullscreenElement: () => Element | null;
} {
  const [isFullscreen, setIsFullscreen] = useState(false);

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
  }, [containerRef, getFullscreenElement]);

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

  return { isFullscreen, toggleFullscreen, getFullscreenElement };
}
