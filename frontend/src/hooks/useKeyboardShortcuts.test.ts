import { describe, it, expect, vi, afterEach } from 'vitest';
import { renderHook } from '@testing-library/react';
import { useKeyboardShortcuts } from './useKeyboardShortcuts';

function createHandlers() {
  return {
    onPlayPause: vi.fn(),
    onSeekBackward: vi.fn(),
    onSeekForward: vi.fn(),
    onToggleMute: vi.fn(),
    onToggleFullscreen: vi.fn(),
    onTakeScreenshot: vi.fn(),
    onHandled: vi.fn(),
  };
}

function pressKey(key: string, target?: EventTarget) {
  const event = new KeyboardEvent('keydown', {
    key,
    bubbles: true,
    cancelable: true,
  });
  if (target) {
    Object.defineProperty(event, 'target', { value: target });
  }
  window.dispatchEvent(event);
}

describe('useKeyboardShortcuts', () => {
  it('Space triggers onPlayPause', () => {
    const handlers = createHandlers();
    renderHook(() => useKeyboardShortcuts(handlers));

    pressKey(' ');

    expect(handlers.onPlayPause).toHaveBeenCalledTimes(1);
  });

  it('k triggers onPlayPause', () => {
    const handlers = createHandlers();
    renderHook(() => useKeyboardShortcuts(handlers));

    pressKey('k');

    expect(handlers.onPlayPause).toHaveBeenCalledTimes(1);
  });

  it('ArrowLeft triggers onSeekBackward', () => {
    const handlers = createHandlers();
    renderHook(() => useKeyboardShortcuts(handlers));

    pressKey('ArrowLeft');

    expect(handlers.onSeekBackward).toHaveBeenCalledTimes(1);
  });

  it('ArrowRight triggers onSeekForward', () => {
    const handlers = createHandlers();
    renderHook(() => useKeyboardShortcuts(handlers));

    pressKey('ArrowRight');

    expect(handlers.onSeekForward).toHaveBeenCalledTimes(1);
  });

  it('m triggers onToggleMute', () => {
    const handlers = createHandlers();
    renderHook(() => useKeyboardShortcuts(handlers));

    pressKey('m');

    expect(handlers.onToggleMute).toHaveBeenCalledTimes(1);
  });

  it('f triggers onToggleFullscreen', () => {
    const handlers = createHandlers();
    renderHook(() => useKeyboardShortcuts(handlers));

    pressKey('f');

    expect(handlers.onToggleFullscreen).toHaveBeenCalledTimes(1);
  });

  it('F (uppercase) triggers onToggleFullscreen', () => {
    const handlers = createHandlers();
    renderHook(() => useKeyboardShortcuts(handlers));

    pressKey('F');

    expect(handlers.onToggleFullscreen).toHaveBeenCalledTimes(1);
  });

  it('s triggers onTakeScreenshot', () => {
    const handlers = createHandlers();
    renderHook(() => useKeyboardShortcuts(handlers));

    pressKey('s');

    expect(handlers.onTakeScreenshot).toHaveBeenCalledTimes(1);
  });

  it('onHandled fires after a handled key', () => {
    const handlers = createHandlers();
    renderHook(() => useKeyboardShortcuts(handlers));

    pressKey(' ');

    expect(handlers.onHandled).toHaveBeenCalledTimes(1);
  });

  it('onHandled does not fire for unhandled keys', () => {
    const handlers = createHandlers();
    renderHook(() => useKeyboardShortcuts(handlers));

    pressKey('z');

    expect(handlers.onHandled).not.toHaveBeenCalled();
  });

  it('ignores keys when target is an input element', () => {
    const handlers = createHandlers();
    renderHook(() => useKeyboardShortcuts(handlers));

    const input = document.createElement('input');
    pressKey(' ', input);

    expect(handlers.onPlayPause).not.toHaveBeenCalled();
    expect(handlers.onHandled).not.toHaveBeenCalled();
  });

  it('ignores keys when target is a textarea element', () => {
    const handlers = createHandlers();
    renderHook(() => useKeyboardShortcuts(handlers));

    const textarea = document.createElement('textarea');
    pressKey(' ', textarea);

    expect(handlers.onPlayPause).not.toHaveBeenCalled();
    expect(handlers.onHandled).not.toHaveBeenCalled();
  });

  it('cleans up event listener on unmount', () => {
    const handlers = createHandlers();
    const removeSpy = vi.spyOn(window, 'removeEventListener');

    const { unmount } = renderHook(() => useKeyboardShortcuts(handlers));
    unmount();

    expect(removeSpy).toHaveBeenCalledWith('keydown', expect.any(Function));
    removeSpy.mockRestore();
  });
});
