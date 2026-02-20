import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { useVideoPlayer } from './useVideoPlayer';
import type { TorrentRecord, SessionState } from '../types';

vi.mock('../api', () => ({
  buildStreamUrl: vi.fn((_id: string, fi: number) => `/torrents/t1/stream?fileIndex=${fi}`),
  buildDirectPlaybackUrl: vi.fn((_id: string, fi: number) => `/torrents/t1/direct/${fi}`),
  buildHlsUrl: vi.fn((_id: string, fi: number) => `/torrents/t1/hls/${fi}/index.m3u8`),
  getMediaInfo: vi.fn().mockResolvedValue({ tracks: [], duration: 0, subtitlesReady: false }),
  hlsSeek: vi.fn(),
  isApiError: vi.fn(() => false),
  probeDirectPlayback: vi.fn().mockResolvedValue(false),
  probeDirectStream: vi.fn().mockResolvedValue(true),
  probeHlsManifest: vi.fn().mockResolvedValue(true),
}));

const makeTorrent = (overrides?: Partial<TorrentRecord>): TorrentRecord => ({
  id: 't1',
  name: 'Test Torrent',
  status: 'active',
  totalBytes: 1000,
  doneBytes: 1000,
  createdAt: '2026-01-01T00:00:00Z',
  updatedAt: '2026-01-01T00:00:00Z',
  files: [
    { index: 0, path: 'movie.mp4', length: 800, bytesCompleted: 800 },
    { index: 1, path: 'subs.srt', length: 200, bytesCompleted: 200 },
  ],
  tags: [],
  ...overrides,
} as TorrentRecord);

describe('useVideoPlayer', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('initializes with null file selection', () => {
    const { result } = renderHook(() => useVideoPlayer(null, null));
    expect(result.current.selectedFileIndex).toBeNull();
    expect(result.current.selectedFile).toBeNull();
    expect(result.current.streamUrl).toBe('');
    expect(result.current.videoError).toBeNull();
  });

  it('availableFiles comes from torrent record', () => {
    const torrent = makeTorrent();
    const { result } = renderHook(() => useVideoPlayer(torrent, null));
    expect(result.current.availableFiles).toHaveLength(2);
    expect(result.current.availableFiles[0].path).toBe('movie.mp4');
  });

  it('selectFile updates selectedFileIndex', () => {
    const torrent = makeTorrent();
    const { result } = renderHook(() => useVideoPlayer(torrent, null));

    act(() => {
      result.current.selectFile(0);
    });

    expect(result.current.selectedFileIndex).toBe(0);
    expect(result.current.selectedFile?.path).toBe('movie.mp4');
  });

  it('resets state when torrent changes', () => {
    const torrent1 = makeTorrent({ id: 't1' });
    const torrent2 = makeTorrent({ id: 't2' });

    const { result, rerender } = renderHook(
      ({ torrent }) => useVideoPlayer(torrent, null),
      { initialProps: { torrent: torrent1 as TorrentRecord | null } },
    );

    act(() => {
      result.current.selectFile(0);
    });
    expect(result.current.selectedFileIndex).toBe(0);

    rerender({ torrent: torrent2 });
    expect(result.current.selectedFileIndex).toBeNull();
    expect(result.current.videoError).toBeNull();
    expect(result.current.seekOffset).toBe(0);
  });

  it('audioTracks and subtitleTracks are empty when no mediaInfo', () => {
    const torrent = makeTorrent();
    const { result } = renderHook(() => useVideoPlayer(torrent, null));
    expect(result.current.audioTracks).toEqual([]);
    expect(result.current.subtitleTracks).toEqual([]);
  });

  it('prebufferPhase is idle when no torrent selected', () => {
    const { result } = renderHook(() => useVideoPlayer(null, null));
    expect(result.current.prebufferPhase).toBe('idle');
  });

  it('activeMode defaults to direct for .mp4', () => {
    const torrent = makeTorrent();
    const { result } = renderHook(() => useVideoPlayer(torrent, null));

    act(() => {
      result.current.selectFile(0);
    });

    expect(result.current.activeMode).toBe('direct');
  });

  it('selectAudioTrack updates audioTrack', () => {
    const torrent = makeTorrent();
    const { result } = renderHook(() => useVideoPlayer(torrent, null));

    act(() => {
      result.current.selectAudioTrack(1);
    });

    expect(result.current.audioTrack).toBe(1);
  });

  it('selectSubtitleTrack updates subtitleTrack', () => {
    const torrent = makeTorrent();
    const { result } = renderHook(() => useVideoPlayer(torrent, null));

    act(() => {
      result.current.selectSubtitleTrack(2);
    });

    expect(result.current.subtitleTrack).toBe(2);
  });

  it('retryStreamInitialization resets error and increments retry token', () => {
    const torrent = makeTorrent();
    const { result } = renderHook(() => useVideoPlayer(torrent, null));

    act(() => {
      result.current.retryStreamInitialization();
    });

    expect(result.current.videoError).toBeNull();
    expect(result.current.prebufferPhase).toBe('idle');
  });

  it('availableFiles falls back to session state files', () => {
    const torrent = makeTorrent({ files: [] });
    const session = {
      id: 't1',
      mode: 'idle',
      files: [{ index: 0, path: 'session-file.mkv', length: 500, bytesCompleted: 500 }],
    } as unknown as SessionState;

    const { result } = renderHook(() => useVideoPlayer(torrent, session));
    expect(result.current.availableFiles).toHaveLength(1);
    expect(result.current.availableFiles[0].path).toBe('session-file.mkv');
  });
});
