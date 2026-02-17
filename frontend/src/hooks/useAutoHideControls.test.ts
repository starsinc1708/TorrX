import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { useAutoHideControls } from './useAutoHideControls';

describe('useAutoHideControls', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('controls are visible by default', () => {
    const { result } = renderHook(() =>
      useAutoHideControls({ playing: false, menuOpen: false }),
    );
    expect(result.current.showControls).toBe(true);
    expect(result.current.cursorHidden).toBe(false);
  });

  it('auto-hides after 3s when playing and menu closed', () => {
    const { result } = renderHook(() =>
      useAutoHideControls({ playing: true, menuOpen: false }),
    );

    expect(result.current.showControls).toBe(true);

    act(() => {
      vi.advanceTimersByTime(3000);
    });

    expect(result.current.showControls).toBe(false);
    expect(result.current.cursorHidden).toBe(true);
  });

  it('controls stay pinned when menuOpen is true', () => {
    const { result } = renderHook(() =>
      useAutoHideControls({ playing: true, menuOpen: true }),
    );

    act(() => {
      vi.advanceTimersByTime(5000);
    });

    expect(result.current.showControls).toBe(true);
    expect(result.current.cursorHidden).toBe(false);
  });

  it('resetHideTimer resets the 3s countdown', () => {
    const { result } = renderHook(() =>
      useAutoHideControls({ playing: true, menuOpen: false }),
    );

    // Advance 2s (not enough to hide)
    act(() => {
      vi.advanceTimersByTime(2000);
    });
    expect(result.current.showControls).toBe(true);

    // Reset the timer
    act(() => {
      result.current.resetHideTimer();
    });
    expect(result.current.showControls).toBe(true);

    // Advance another 2s — should still be visible since timer was reset
    act(() => {
      vi.advanceTimersByTime(2000);
    });
    expect(result.current.showControls).toBe(true);

    // Advance the remaining 1s — now should hide
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(result.current.showControls).toBe(false);
    expect(result.current.cursorHidden).toBe(true);
  });

  it('controls reappear when playing transitions to paused', () => {
    const { result, rerender } = renderHook(
      ({ playing, menuOpen }) => useAutoHideControls({ playing, menuOpen }),
      { initialProps: { playing: true, menuOpen: false } },
    );

    // Let controls auto-hide
    act(() => {
      vi.advanceTimersByTime(3000);
    });
    expect(result.current.showControls).toBe(false);

    // Pause playback
    rerender({ playing: false, menuOpen: false });

    expect(result.current.showControls).toBe(true);
    expect(result.current.cursorHidden).toBe(false);
  });

  it('does not hide controls when not playing', () => {
    const { result } = renderHook(() =>
      useAutoHideControls({ playing: false, menuOpen: false }),
    );

    act(() => {
      vi.advanceTimersByTime(5000);
    });

    expect(result.current.showControls).toBe(true);
    expect(result.current.cursorHidden).toBe(false);
  });
});
