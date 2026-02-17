import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { useWatchPositionSave } from './useWatchPositionSave';

vi.mock('../api', () => ({
  saveWatchPosition: vi.fn().mockResolvedValue(undefined),
}));

vi.mock('../watchState', () => ({
  upsertTorrentWatchState: vi.fn(),
}));

import { saveWatchPosition } from '../api';
import { upsertTorrentWatchState } from '../watchState';

const mockedSaveWatchPosition = vi.mocked(saveWatchPosition);
const mockedUpsertTorrentWatchState = vi.mocked(upsertTorrentWatchState);

describe('useWatchPositionSave', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('does not save when enabled is false', () => {
    const getCurrentTime = vi.fn(() => 30);
    const getDuration = vi.fn(() => 600);

    renderHook(() =>
      useWatchPositionSave(getCurrentTime, getDuration, {
        torrentId: 'abc',
        fileIndex: 0,
        enabled: false,
      }),
    );

    act(() => {
      vi.advanceTimersByTime(10000);
    });

    expect(mockedSaveWatchPosition).not.toHaveBeenCalled();
    expect(mockedUpsertTorrentWatchState).not.toHaveBeenCalled();
  });

  it('saves periodically when enabled', async () => {
    const getCurrentTime = vi.fn(() => 30);
    const getDuration = vi.fn(() => 600);

    renderHook(() =>
      useWatchPositionSave(getCurrentTime, getDuration, {
        torrentId: 'abc',
        fileIndex: 0,
        enabled: true,
        saveIntervalMs: 5000,
      }),
    );

    // The interval fires every 1s but only actually saves every saveIntervalMs (5s)
    await act(async () => {
      vi.advanceTimersByTime(5000);
    });

    expect(mockedSaveWatchPosition).toHaveBeenCalledWith(
      'abc', 0, 30, 600, undefined, undefined,
    );
    expect(mockedUpsertTorrentWatchState).toHaveBeenCalledWith({
      torrentId: 'abc',
      fileIndex: 0,
      position: 30,
      duration: 600,
      torrentName: undefined,
      filePath: undefined,
    });
  });

  it('applies seekOffset to saved position', () => {
    const getCurrentTime = vi.fn(() => 10);
    const getDuration = vi.fn(() => 600);

    renderHook(() =>
      useWatchPositionSave(getCurrentTime, getDuration, {
        torrentId: 'abc',
        fileIndex: 0,
        seekOffset: 100,
        enabled: true,
        saveIntervalMs: 5000,
      }),
    );

    act(() => {
      vi.advanceTimersByTime(5000);
    });

    // absPosition = seekOffset(100) + position(10) = 110
    expect(mockedSaveWatchPosition).toHaveBeenCalledWith(
      'abc', 0, 110, 600, undefined, undefined,
    );
  });

  it('uses mediaDuration when greater than 0', () => {
    const getCurrentTime = vi.fn(() => 20);
    const getDuration = vi.fn(() => 300);

    renderHook(() =>
      useWatchPositionSave(getCurrentTime, getDuration, {
        torrentId: 'abc',
        fileIndex: 0,
        mediaDuration: 7200,
        enabled: true,
        saveIntervalMs: 5000,
      }),
    );

    act(() => {
      vi.advanceTimersByTime(5000);
    });

    // absDuration = mediaDuration (7200) since > 0
    expect(mockedSaveWatchPosition).toHaveBeenCalledWith(
      'abc', 0, 20, 7200, undefined, undefined,
    );
  });

  it('saves on unmount when torrentId and fileIndex are set', () => {
    const getCurrentTime = vi.fn(() => 45);
    const getDuration = vi.fn(() => 900);

    const { unmount } = renderHook(() =>
      useWatchPositionSave(getCurrentTime, getDuration, {
        torrentId: 'xyz',
        fileIndex: 2,
        torrentName: 'Movie',
        filePath: 'movie.mp4',
        enabled: true,
      }),
    );

    vi.clearAllMocks();
    unmount();

    expect(mockedSaveWatchPosition).toHaveBeenCalledWith(
      'xyz', 2, 45, 900, 'Movie', 'movie.mp4',
    );
  });

  it('does not save on unmount when position is 0', () => {
    const getCurrentTime = vi.fn(() => 0);
    const getDuration = vi.fn(() => 900);

    const { unmount } = renderHook(() =>
      useWatchPositionSave(getCurrentTime, getDuration, {
        torrentId: 'xyz',
        fileIndex: 0,
        enabled: true,
      }),
    );

    vi.clearAllMocks();
    unmount();

    expect(mockedSaveWatchPosition).not.toHaveBeenCalled();
  });

  it('does not save when torrentId is null', () => {
    const getCurrentTime = vi.fn(() => 30);
    const getDuration = vi.fn(() => 600);

    renderHook(() =>
      useWatchPositionSave(getCurrentTime, getDuration, {
        torrentId: null,
        fileIndex: 0,
        enabled: true,
        saveIntervalMs: 5000,
      }),
    );

    act(() => {
      vi.advanceTimersByTime(5000);
    });

    expect(mockedSaveWatchPosition).not.toHaveBeenCalled();
  });
});
