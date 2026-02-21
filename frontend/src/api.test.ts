import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
  buildUrl,
  listTorrents,
  getTorrent,
  createTorrentFromMagnet,
  startTorrent,
  stopTorrent,
  deleteTorrent,
  getEncodingSettings,
  saveWatchPosition,
  getWatchPosition,
  buildHlsUrl,
  buildSubtitleTrackUrl,
  buildStreamUrl,
  buildDirectPlaybackUrl,
  isApiError,
} from './api';

const mockFetch = vi.fn();

beforeEach(() => {
  vi.stubGlobal('fetch', mockFetch);
  mockFetch.mockReset();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

const jsonResponse = (data: unknown, status = 200) =>
  Promise.resolve({
    ok: status >= 200 && status < 300,
    status,
    statusText: 'OK',
    json: () => Promise.resolve(data),
    clone() {
      return {
        ok: this.ok,
        status: this.status,
        statusText: this.statusText,
        json: () => Promise.resolve(data),
        clone() { return this; },
      };
    },
  });

const noContentResponse = () =>
  Promise.resolve({
    ok: true,
    status: 204,
    statusText: 'No Content',
    json: () => Promise.resolve(undefined),
    clone() {
      return {
        ok: true,
        status: 204,
        statusText: 'No Content',
        json: () => Promise.resolve(undefined),
        clone() { return this; },
      };
    },
  });

describe('buildUrl', () => {
  it('returns path as-is when no API_BASE is set', () => {
    expect(buildUrl('/torrents')).toBe('/torrents');
  });

  it('returns path with leading slash', () => {
    expect(buildUrl('/search')).toBe('/search');
  });

  it('handles settings paths', () => {
    expect(buildUrl('/settings/storage')).toBe('/settings/storage');
  });
});

describe('listTorrents', () => {
  it('fetches torrents with default params', async () => {
    const data = { items: [{ id: 'abc' }] };
    mockFetch.mockReturnValue(jsonResponse(data));

    const result = await listTorrents();
    expect(mockFetch).toHaveBeenCalledTimes(1);
    const url = mockFetch.mock.calls[0][0] as string;
    expect(url).toContain('/torrents?');
    expect(url).toContain('status=all');
    expect(url).toContain('view=full');
    expect(result).toEqual(data);
  });

  it('passes status and search params', async () => {
    mockFetch.mockReturnValue(jsonResponse({ items: [] }));

    await listTorrents({ status: 'active', search: 'test' });
    const url = mockFetch.mock.calls[0][0] as string;
    expect(url).toContain('status=active');
    expect(url).toContain('search=test');
  });

  it('passes tags as comma-separated', async () => {
    mockFetch.mockReturnValue(jsonResponse({ items: [] }));

    await listTorrents({ tags: ['a', 'b'] });
    const url = mockFetch.mock.calls[0][0] as string;
    expect(url).toContain('tags=a%2Cb');
  });
});

describe('getTorrent', () => {
  it('fetches single torrent by id', async () => {
    const torrent = { id: 'abc', name: 'Test' };
    mockFetch.mockReturnValue(jsonResponse(torrent));

    const result = await getTorrent('abc');
    expect(result).toEqual(torrent);
    const url = mockFetch.mock.calls[0][0] as string;
    expect(url).toContain('/torrents/abc');
  });
});

describe('createTorrentFromMagnet', () => {
  it('sends POST with magnet link', async () => {
    const torrent = { id: 'new', name: 'Created' };
    mockFetch.mockReturnValue(jsonResponse(torrent));

    const result = await createTorrentFromMagnet('magnet:?xt=abc', 'My Torrent');
    expect(result).toEqual(torrent);
    expect(mockFetch).toHaveBeenCalledTimes(1);
    const [, init] = mockFetch.mock.calls[0];
    expect(init.method).toBe('POST');
    expect(JSON.parse(init.body)).toEqual({ magnet: 'magnet:?xt=abc', name: 'My Torrent' });
  });
});

describe('startTorrent', () => {
  it('sends POST to start endpoint', async () => {
    const torrent = { id: 'abc', status: 'active' };
    mockFetch.mockReturnValue(jsonResponse(torrent));

    const result = await startTorrent('abc');
    expect(result).toEqual(torrent);
    const url = mockFetch.mock.calls[0][0] as string;
    expect(url).toContain('/torrents/abc/start');
    expect(mockFetch.mock.calls[0][1].method).toBe('POST');
  });
});

describe('stopTorrent', () => {
  it('sends POST to stop endpoint', async () => {
    const torrent = { id: 'abc', status: 'stopped' };
    mockFetch.mockReturnValue(jsonResponse(torrent));

    const result = await stopTorrent('abc');
    expect(result).toEqual(torrent);
    const url = mockFetch.mock.calls[0][0] as string;
    expect(url).toContain('/torrents/abc/stop');
  });
});

describe('deleteTorrent', () => {
  it('sends DELETE request', async () => {
    mockFetch.mockReturnValue(noContentResponse());

    await deleteTorrent('abc', false);
    const url = mockFetch.mock.calls[0][0] as string;
    expect(url).toContain('/torrents/abc');
    expect(mockFetch.mock.calls[0][1].method).toBe('DELETE');
  });

  it('includes deleteFiles param when true', async () => {
    mockFetch.mockReturnValue(noContentResponse());

    await deleteTorrent('abc', true);
    const url = mockFetch.mock.calls[0][0] as string;
    expect(url).toContain('deleteFiles=true');
  });
});

describe('getEncodingSettings', () => {
  it('fetches encoding settings', async () => {
    const settings = { preset: 'fast', crf: 23 };
    mockFetch.mockReturnValue(jsonResponse(settings));

    const result = await getEncodingSettings();
    expect(result).toEqual(settings);
    const url = mockFetch.mock.calls[0][0] as string;
    expect(url).toContain('/settings/encoding');
  });
});

describe('saveWatchPosition', () => {
  it('sends PUT with position data', async () => {
    mockFetch.mockReturnValue(noContentResponse());

    await saveWatchPosition('abc', 0, 120, 600, 'Movie', 'movie.mp4');
    const [url, init] = mockFetch.mock.calls[0];
    expect(url).toContain('/watch-history/abc/0');
    expect(init.method).toBe('PUT');
    expect(JSON.parse(init.body)).toEqual({
      position: 120,
      duration: 600,
      torrentName: 'Movie',
      filePath: 'movie.mp4',
    });
  });
});

describe('getWatchPosition', () => {
  it('returns position data', async () => {
    const pos = { position: 120, duration: 600 };
    mockFetch.mockReturnValue(jsonResponse(pos));

    const result = await getWatchPosition('abc', 0);
    expect(result).toEqual(pos);
  });

  it('returns null on 404', async () => {
    mockFetch.mockReturnValue(
      Promise.resolve({ ok: false, status: 404, statusText: 'Not Found' }),
    );

    const result = await getWatchPosition('abc', 0);
    expect(result).toBeNull();
  });
});

describe('URL builders', () => {
  it('buildStreamUrl builds correct path', () => {
    const url = buildStreamUrl('abc', 2);
    expect(url).toContain('/torrents/abc/stream');
    expect(url).toContain('fileIndex=2');
  });

  it('buildDirectPlaybackUrl builds correct path', () => {
    const url = buildDirectPlaybackUrl('abc', 1);
    expect(url).toContain('/torrents/abc/direct/1');
  });

  it('buildHlsUrl builds correct path', () => {
    const url = buildHlsUrl('abc', 0);
    expect(url).toContain('/torrents/abc/hls/0/index.m3u8');
  });

  it('buildHlsUrl includes audio params', () => {
    const url = buildHlsUrl('abc', 0, { audioTrack: 1 });
    expect(url).toContain('audioTrack=1');
  });

  it('buildSubtitleTrackUrl builds correct path', () => {
    const url = buildSubtitleTrackUrl('abc', 0, 2);
    expect(url).toContain('/torrents/abc/subtitles/0/2.vtt');
  });
});

describe('error handling', () => {
  it('throws ApiRequestError on non-ok response', async () => {
    mockFetch.mockReturnValue(
      Promise.resolve({
        ok: false,
        status: 500,
        statusText: 'Internal Server Error',
        json: () => Promise.resolve({ error: { code: 'server_error', message: 'Failed' } }),
        clone() { return this; },
      }),
    );

    await expect(getTorrent('abc')).rejects.toThrow('Failed');
  });

  it('isApiError returns false for generic errors', () => {
    expect(isApiError(new Error('test'))).toBe(false);
  });
});
