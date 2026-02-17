import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { useFullscreen } from './useFullscreen';
import { createRef } from 'react';

describe('useFullscreen', () => {
  let requestFullscreenMock: ReturnType<typeof vi.fn>;
  let exitFullscreenMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    requestFullscreenMock = vi.fn();
    exitFullscreenMock = vi.fn();
    Object.defineProperty(document, 'fullscreenElement', {
      value: null,
      writable: true,
      configurable: true,
    });
    document.exitFullscreen = exitFullscreenMock as unknown as typeof document.exitFullscreen;
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('isFullscreen starts false', () => {
    const ref = createRef<HTMLDivElement>();
    const { result } = renderHook(() => useFullscreen(ref));
    expect(result.current.isFullscreen).toBe(false);
  });

  it('toggleFullscreen calls requestFullscreen on container', () => {
    const container = document.createElement('div');
    container.requestFullscreen = requestFullscreenMock as unknown as typeof container.requestFullscreen;
    const ref = { current: container };

    const { result } = renderHook(() => useFullscreen(ref));

    act(() => {
      result.current.toggleFullscreen();
    });

    expect(requestFullscreenMock).toHaveBeenCalled();
  });

  it('toggleFullscreen calls exitFullscreen when already fullscreen', () => {
    const container = document.createElement('div');
    container.requestFullscreen = requestFullscreenMock as unknown as typeof container.requestFullscreen;
    const ref = { current: container };

    // Set fullscreenElement to simulate being in fullscreen
    Object.defineProperty(document, 'fullscreenElement', {
      value: container,
      writable: true,
      configurable: true,
    });

    const { result } = renderHook(() => useFullscreen(ref));

    act(() => {
      result.current.toggleFullscreen();
    });

    expect(exitFullscreenMock).toHaveBeenCalled();
    expect(requestFullscreenMock).not.toHaveBeenCalled();
  });

  it('fullscreenchange event updates state', () => {
    const container = document.createElement('div');
    container.requestFullscreen = requestFullscreenMock as unknown as typeof container.requestFullscreen;
    const ref = { current: container };

    const { result } = renderHook(() => useFullscreen(ref));
    expect(result.current.isFullscreen).toBe(false);

    // Simulate entering fullscreen
    act(() => {
      Object.defineProperty(document, 'fullscreenElement', {
        value: container,
        writable: true,
        configurable: true,
      });
      document.dispatchEvent(new Event('fullscreenchange'));
    });

    expect(result.current.isFullscreen).toBe(true);

    // Simulate exiting fullscreen
    act(() => {
      Object.defineProperty(document, 'fullscreenElement', {
        value: null,
        writable: true,
        configurable: true,
      });
      document.dispatchEvent(new Event('fullscreenchange'));
    });

    expect(result.current.isFullscreen).toBe(false);
  });

  it('cleans up event listeners on unmount', () => {
    const container = document.createElement('div');
    const ref = { current: container };
    const removeSpy = vi.spyOn(document, 'removeEventListener');

    const { unmount } = renderHook(() => useFullscreen(ref));
    unmount();

    expect(removeSpy).toHaveBeenCalledWith('fullscreenchange', expect.any(Function));
  });
});
