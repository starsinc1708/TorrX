import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';
import { useSessionState } from './useSessionState';

vi.mock('../api', () => ({
  getTorrentState: vi.fn(),
  isApiError: vi.fn(() => false),
}));

import { getTorrentState } from '../api';
const mockedGetTorrentState = vi.mocked(getTorrentState);

describe('useSessionState', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('sessionState starts as null', () => {
    const { result } = renderHook(() => useSessionState(null));
    expect(result.current.sessionState).toBeNull();
  });

  it('autoRefreshState starts as false', () => {
    const { result } = renderHook(() => useSessionState('abc'));
    expect(result.current.autoRefreshState).toBe(false);
  });

  it('resets sessionState when selectedId changes', () => {
    const { result, rerender } = renderHook(
      ({ id }) => useSessionState(id),
      { initialProps: { id: 'abc' as string | null } },
    );

    // Manually set a state
    act(() => {
      result.current.setAutoRefreshState(true);
    });

    rerender({ id: 'xyz' });
    expect(result.current.sessionState).toBeNull();
  });

  it('refreshSessionState fetches data for selected torrent', async () => {
    const mockState = { id: 'abc', mode: 'idle', files: [] };
    mockedGetTorrentState.mockResolvedValue(mockState as any);

    const { result } = renderHook(() => useSessionState('abc'));

    await act(async () => {
      await result.current.refreshSessionState();
    });

    expect(mockedGetTorrentState).toHaveBeenCalledWith('abc', expect.any(AbortSignal));
    expect(result.current.sessionState).toEqual(mockState);
  });

  it('does not fetch when selectedId is null', async () => {
    const { result } = renderHook(() => useSessionState(null));

    await act(async () => {
      await result.current.refreshSessionState();
    });

    expect(mockedGetTorrentState).not.toHaveBeenCalled();
  });

  it('uses wsStates when provided', () => {
    const wsState = { id: 'abc', mode: 'downloading', files: [] };
    const { result } = renderHook(() => useSessionState('abc', [wsState as any]));

    expect(result.current.sessionState).toEqual(wsState);
  });

  it('ignores wsStates that do not match selectedId', () => {
    const wsState = { id: 'xyz', mode: 'downloading', files: [] };
    const { result } = renderHook(() => useSessionState('abc', [wsState as any]));

    expect(result.current.sessionState).toBeNull();
  });

  it('setAutoRefreshState toggles auto-refresh', () => {
    const { result } = renderHook(() => useSessionState('abc'));
    expect(result.current.autoRefreshState).toBe(false);

    act(() => {
      result.current.setAutoRefreshState(true);
    });

    expect(result.current.autoRefreshState).toBe(true);
  });
});
