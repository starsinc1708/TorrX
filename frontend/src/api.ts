import type {
  ApiErrorPayload,
  EncodingSettings,
  FlareSolverrApplyResponse,
  FlareSolverrSettings,
  MediaInfo,
  SessionState,
  SessionStateList,
  StorageSettings,
  PlayerSettings,
  TorrentListFull,
  TorrentListSummary,
  TorrentRecord,
  BulkResponse,
  SortOrder,
  TorrentSortBy,
  TorrentStatusFilter,
  TorrentView,
  UpdateStorageSettingsInput,
  WatchPosition,
  PlayerHealth,
  SearchProviderInfo,
  SearchProviderDiagnostics,
  SearchProviderTestResult,
  SearchProviderAutodetectResult,
  SearchProviderRuntimeConfig,
  SearchProviderRuntimePatch,
  SearchRankingProfile,
  SearchResponse,
  SearchSortBy,
  SearchSortOrder,
} from './types';

const rawBase = (import.meta as any).env?.VITE_API_BASE_URL ?? '';
const API_BASE = typeof rawBase === 'string' ? rawBase.replace(/\/$/, '') : '';

export const buildUrl = (path: string) => (API_BASE ? `${API_BASE}${path}` : path);
const DEFAULT_REQUEST_TIMEOUT_MS = 15000;
const POLL_REQUEST_TIMEOUT_MS = 7000;
const LONG_REQUEST_TIMEOUT_MS = 90000;

// ---- GET request deduplication ----
// If an identical GET is already in-flight, return the same promise instead of
// creating a new HTTP request. This collapses bursts of duplicate polls into a
// single network round-trip.
const inflightGets = new Map<string, Promise<Response>>();

const deduplicatedFetch = async (
  url: string,
  init?: RequestInit,
  timeoutMs = DEFAULT_REQUEST_TIMEOUT_MS,
): Promise<Response> => {
  const method = init?.method?.toUpperCase() ?? 'GET';

  // Don't deduplicate mutations (POST, PUT, DELETE, etc.) because different
  // request bodies should not share the same response. Mutations are idempotent
  // by design and should execute independently.
  if (method !== 'GET') {
    return fetchWithTimeout(url, init, timeoutMs);
  }

  const existing = inflightGets.get(url);
  if (existing) return existing.then((r) => r.clone());

  const promise = fetchWithTimeout(url, init, timeoutMs).finally(() => {
    inflightGets.delete(url);
  });
  inflightGets.set(url, promise);
  return promise;
};

class ApiRequestError extends Error {
  code?: string;
  status?: number;

  constructor(message: string, code?: string, status?: number) {
    super(message);
    this.name = 'ApiRequestError';
    this.code = code;
    this.status = status;
  }
}

const parseErrorPayload = async (response: Response): Promise<ApiErrorPayload | null> => {
  try {
    const data = (await response.json()) as ApiErrorPayload;
    if (data && typeof data === 'object') {
      return data;
    }
  } catch (error) {
    return null;
  }
  return null;
};

const handleResponse = async <T>(response: Response): Promise<T> => {
  if (response.ok) {
    if (response.status === 204) {
      return undefined as T;
    }
    return (await response.json()) as T;
  }

  const payload = await parseErrorPayload(response);
  const code = payload?.error?.code ?? 'request_failed';
  const message = payload?.error?.message ?? response.statusText;
  throw new ApiRequestError(message, code, response.status);
};

const fetchWithTimeout = async (
  url: string,
  init?: RequestInit,
  timeoutMs = DEFAULT_REQUEST_TIMEOUT_MS,
): Promise<Response> => {
  const controller = new AbortController();
  const timeout = window.setTimeout(() => controller.abort(), timeoutMs);
  try {
    return await fetch(url, { ...init, signal: controller.signal });
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') {
      throw new ApiRequestError('request timeout', 'timeout');
    }
    throw error;
  } finally {
    window.clearTimeout(timeout);
  }
};

export const listTorrents = async (options?: {
  status?: TorrentStatusFilter;
  view?: TorrentView;
  search?: string;
  tags?: string[];
  sortBy?: TorrentSortBy;
  sortOrder?: SortOrder;
  limit?: number;
  offset?: number;
}): Promise<TorrentListFull | TorrentListSummary> => {
  const params = new URLSearchParams();
  params.set('status', options?.status ?? 'all');
  params.set('view', options?.view ?? 'full');
  if (options?.search?.trim()) params.set('search', options.search.trim());
  if (options?.tags && options.tags.length > 0) params.set('tags', options.tags.join(','));
  if (options?.sortBy) params.set('sortBy', options.sortBy);
  if (options?.sortOrder) params.set('sortOrder', options.sortOrder);
  if (options?.limit) params.set('limit', String(options.limit));
  if (options?.offset) params.set('offset', String(options.offset));
  const response = await deduplicatedFetch(
    buildUrl(`/torrents?${params.toString()}`),
    undefined,
    POLL_REQUEST_TIMEOUT_MS,
  );
  return handleResponse(response);
};

export const listSearchProviders = async (): Promise<SearchProviderInfo[]> => {
  const response = await fetch(buildUrl('/search/providers'));
  const payload = await handleResponse<{ items: SearchProviderInfo[] }>(response);
  return payload.items ?? [];
};

export const getSearchProviderRuntimeConfigs = async (): Promise<SearchProviderRuntimeConfig[]> => {
  const response = await fetch(buildUrl('/search/settings/providers'));
  const payload = await handleResponse<{ items: SearchProviderRuntimeConfig[] }>(response);
  return payload.items ?? [];
};

export const updateSearchProviderRuntimeConfig = async (
  input: SearchProviderRuntimePatch,
): Promise<SearchProviderRuntimeConfig> => {
  const response = await fetch(buildUrl('/search/settings/providers'), {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
  return handleResponse<SearchProviderRuntimeConfig>(response);
};

export const autodetectSearchProviderRuntimeConfig = async (
  provider?: string,
): Promise<{
  items: SearchProviderRuntimeConfig[];
  results?: SearchProviderAutodetectResult[];
  errors?: { provider: string; error: string }[];
}> => {
  const response = await fetch(buildUrl('/search/settings/providers/autodetect'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(provider ? { provider } : {}),
  });
  return handleResponse<{
    items: SearchProviderRuntimeConfig[];
    results?: SearchProviderAutodetectResult[];
    errors?: { provider: string; error: string }[];
  }>(response);
};

export const getFlareSolverrSettings = async (): Promise<FlareSolverrSettings> => {
  const response = await fetch(buildUrl('/search/settings/flaresolverr'));
  return handleResponse<FlareSolverrSettings>(response);
};

export const applyFlareSolverrSettings = async (
  input: { url: string; provider?: string },
): Promise<FlareSolverrApplyResponse> => {
  const response = await fetch(buildUrl('/search/settings/flaresolverr'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
  return handleResponse<FlareSolverrApplyResponse>(response);
};

export const getSearchProviderDiagnostics = async (): Promise<SearchProviderDiagnostics[]> => {
  const response = await fetch(buildUrl('/search/providers/health'), { cache: 'no-store' });
  const payload = await handleResponse<{ items: SearchProviderDiagnostics[] }>(response);
  return payload.items ?? [];
};

export const testSearchProvider = async (provider: string, query: string): Promise<SearchProviderTestResult> => {
  const params = new URLSearchParams();
  params.set('provider', provider.trim().toLowerCase());
  params.set('q', query.trim() || 'spider man');
  params.set('limit', '10');
  params.set('nocache', '1');
  const response = await fetch(buildUrl(`/search/providers/test?${params.toString()}`), { cache: 'no-store' });
  return handleResponse<SearchProviderTestResult>(response);
};

export const searchTorrents = async (options: {
  query: string;
  limit?: number;
  offset?: number;
  sortBy?: SearchSortBy;
  sortOrder?: SearchSortOrder;
  providers?: string[];
  profile?: SearchRankingProfile;
  noCache?: boolean;
}): Promise<SearchResponse> => {
  const params = buildSearchParams(options);
  // Avoid browser/proxy caching for search results (but allow server-side Redis cache).
  params.set('_ts', String(Date.now()));
  const response = await fetch(buildUrl(`/search?${params.toString()}`), {
    cache: 'no-store',
    headers: { 'Cache-Control': 'no-store' },
  });
  return handleResponse(response);
};

export const searchTorrentsStream = (
  options: {
    query: string;
    limit?: number;
    offset?: number;
    sortBy?: SearchSortBy;
    sortOrder?: SearchSortOrder;
    providers?: string[];
    profile?: SearchRankingProfile;
    noCache?: boolean;
  },
  handlers: {
    onPhase: (response: SearchResponse) => void;
    onDone?: () => void;
    onError?: (message: string) => void;
  },
): (() => void) => {
  const params = buildSearchParams(options);
  // Avoid browser/proxy caching for SSE streams.
  params.set('_ts', String(Date.now()));
  const source = new EventSource(buildUrl(`/search/stream?${params.toString()}`));
  let closed = false;

  const closeStream = () => {
    if (closed) return;
    closed = true;
    source.close();
  };

  const handlePhase = (event: MessageEvent<string>) => {
    try {
      const payload = JSON.parse(event.data) as SearchResponse;
      handlers.onPhase(payload);
    } catch {
      handlers.onError?.('invalid stream payload');
    }
  };

  const handleDone = () => {
    handlers.onDone?.();
    closeStream();
  };

  const handleError = (event: MessageEvent<string> | Event) => {
    if (event instanceof MessageEvent) {
      try {
        const payload = JSON.parse(event.data) as { message?: string };
        handlers.onError?.(payload.message || 'search stream failed');
      } catch {
        handlers.onError?.('search stream failed');
      }
    } else {
      handlers.onError?.('search stream failed');
    }
  };

  source.addEventListener('phase', handlePhase as EventListener);
  source.addEventListener('done', handleDone as EventListener);
  source.addEventListener('error', handleError as EventListener);
  source.onerror = () => {
    if (closed) return;
    handlers.onError?.('search stream disconnected');
    closeStream();
  };

  return () => {
    source.removeEventListener('phase', handlePhase as EventListener);
    source.removeEventListener('done', handleDone as EventListener);
    source.removeEventListener('error', handleError as EventListener);
    closeStream();
  };
};

const buildSearchParams = (options: {
  query: string;
  limit?: number;
  offset?: number;
  sortBy?: SearchSortBy;
  sortOrder?: SearchSortOrder;
  providers?: string[];
  profile?: SearchRankingProfile;
  noCache?: boolean;
}) => {
  const params = new URLSearchParams();
  params.set('q', options.query.trim());
  // Only bypass cache when explicitly requested (e.g., force refresh button).
  if (options.noCache) {
    params.set('nocache', '1');
  }
  if (options.limit && options.limit > 0) params.set('limit', String(options.limit));
  if (options.offset && options.offset >= 0) params.set('offset', String(options.offset));
  if (options.sortBy) params.set('sortBy', options.sortBy);
  if (options.sortOrder) params.set('sortOrder', options.sortOrder);
  if (options.providers && options.providers.length > 0) params.set('providers', options.providers.join(','));
  appendRankingProfile(params, options.profile);
  return params;
};

const appendRankingProfile = (params: URLSearchParams, profile?: SearchRankingProfile) => {
  if (!profile) return;
  params.set('freshnessWeight', String(profile.freshnessWeight));
  params.set('seedersWeight', String(profile.seedersWeight));
  params.set('qualityWeight', String(profile.qualityWeight));
  params.set('languageWeight', String(profile.languageWeight));
  params.set('sizeWeight', String(profile.sizeWeight));
  if (profile.preferSeries) params.set('preferSeries', '1');
  if (profile.preferMovies) params.set('preferMovies', '1');
  if (profile.preferredAudio.length > 0) params.set('preferredAudio', profile.preferredAudio.join(','));
  if (profile.preferredSubtitles.length > 0) {
    params.set('preferredSubtitles', profile.preferredSubtitles.join(','));
  }
  if (profile.targetSizeBytes > 0) params.set('targetSizeBytes', String(profile.targetSizeBytes));
};

export const getTorrent = async (id: string): Promise<TorrentRecord> => {
  const response = await fetch(buildUrl(`/torrents/${id}`));
  return handleResponse(response);
};

export const createTorrentFromMagnet = async (magnet: string, name?: string): Promise<TorrentRecord> => {
  const response = await fetchWithTimeout(
    buildUrl('/torrents'),
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ magnet, name: name || undefined }),
    },
    LONG_REQUEST_TIMEOUT_MS,
  );
  return handleResponse(response);
};

export const createTorrentFromFile = async (file: File, name?: string): Promise<TorrentRecord> => {
  const form = new FormData();
  form.append('torrent', file);
  if (name) {
    form.append('name', name);
  }

  const response = await fetch(buildUrl('/torrents'), {
    method: 'POST',
    body: form,
  });
  return handleResponse(response);
};

export const startTorrent = async (id: string): Promise<TorrentRecord> => {
  const response = await deduplicatedFetch(buildUrl(`/torrents/${id}/start`), { method: 'POST' });
  return handleResponse(response);
};

export const stopTorrent = async (id: string): Promise<TorrentRecord> => {
  const response = await deduplicatedFetch(buildUrl(`/torrents/${id}/stop`), { method: 'POST' });
  return handleResponse(response);
};

export const deleteTorrent = async (id: string, deleteFiles: boolean): Promise<void> => {
  const params = new URLSearchParams();
  if (deleteFiles) {
    params.set('deleteFiles', 'true');
  }
  const url = params.toString() ? `/torrents/${id}?${params.toString()}` : `/torrents/${id}`;
  const response = await deduplicatedFetch(buildUrl(url), { method: 'DELETE' });
  return handleResponse(response);
};

export const updateTorrentTags = async (id: string, tags: string[]): Promise<TorrentRecord> => {
  const response = await fetch(buildUrl(`/torrents/${id}/tags`), {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ tags }),
  });
  return handleResponse(response);
};

export const bulkStartTorrents = async (ids: string[]): Promise<BulkResponse> => {
  const response = await fetch(buildUrl('/torrents/bulk/start'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ids }),
  });
  return handleResponse(response);
};

export const bulkStopTorrents = async (ids: string[]): Promise<BulkResponse> => {
  const response = await fetch(buildUrl('/torrents/bulk/stop'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ids }),
  });
  return handleResponse(response);
};

export const bulkDeleteTorrents = async (ids: string[], deleteFiles: boolean): Promise<BulkResponse> => {
  const response = await fetch(buildUrl('/torrents/bulk/delete'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ids, deleteFiles }),
  });
  return handleResponse(response);
};

export const getTorrentState = async (id: string, signal?: AbortSignal): Promise<SessionState> => {
  const response = await deduplicatedFetch(
    buildUrl(`/torrents/${id}/state`),
    { signal },
    POLL_REQUEST_TIMEOUT_MS,
  );
  return handleResponse(response);
};

export const listActiveStates = async (): Promise<SessionStateList> => {
  const response = await deduplicatedFetch(
    buildUrl('/torrents/state?status=active'),
    undefined,
    POLL_REQUEST_TIMEOUT_MS,
  );
  return handleResponse(response);
};

export const buildStreamUrl = (id: string, fileIndex: number) =>
  buildUrl(`/torrents/${id}/stream?fileIndex=${fileIndex}`);

/** HEAD request to direct stream URL. Resolves true if 200/206, false otherwise. */
export const probeDirectStream = async (
  id: string,
  fileIndex: number,
  signal?: AbortSignal,
): Promise<boolean> => {
  try {
    const res = await fetch(buildStreamUrl(id, fileIndex), { method: 'HEAD', signal });
    return res.ok;
  } catch {
    return false;
  }
};

/** Fetches HLS manifest. Returns true if it contains #EXTINF (segments ready). */
export const probeHlsManifest = async (
  url: string,
  signal?: AbortSignal,
): Promise<boolean> => {
  try {
    const res = await fetch(url, { signal });
    if (!res.ok) return false;
    const text = await res.text();
    return text.includes('#EXTINF');
  } catch {
    return false;
  }
};

export const buildHlsUrl = (
  id: string,
  fileIndex: number,
  options?: { audioTrack?: number | null; subtitleTrack?: number | null },
) => {
  const params = new URLSearchParams();
  if (options?.audioTrack !== undefined && options.audioTrack !== null) {
    params.set('audioTrack', String(options.audioTrack));
  }
  if (options?.subtitleTrack !== undefined && options.subtitleTrack !== null) {
    params.set('subtitleTrack', String(options.subtitleTrack));
  }
  const query = params.toString();
  const suffix = query ? `?${query}` : '';
  return buildUrl(`/torrents/${id}/hls/${fileIndex}/index.m3u8${suffix}`);
};

export const getMediaInfo = async (id: string, fileIndex: number): Promise<MediaInfo> => {
  const response = await deduplicatedFetch(
    buildUrl(`/torrents/${id}/media/${fileIndex}`),
    undefined,
    POLL_REQUEST_TIMEOUT_MS,
  );
  return handleResponse(response);
};

export const getStorageSettings = async (): Promise<StorageSettings> => {
  const response = await fetch(buildUrl('/settings/storage'));
  return handleResponse(response);
};

export const updateStorageSettings = async (
  input: UpdateStorageSettingsInput,
): Promise<StorageSettings> => {
  const response = await fetch(buildUrl('/settings/storage'), {
    method: 'PATCH',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(input),
  });
  return handleResponse(response);
};

export const saveWatchPosition = async (
  torrentId: string,
  fileIndex: number,
  position: number,
  duration: number,
  torrentName?: string,
  filePath?: string,
): Promise<void> => {
  const response = await fetch(buildUrl(`/watch-history/${torrentId}/${fileIndex}`), {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ position, duration, torrentName: torrentName ?? '', filePath: filePath ?? '' }),
  });
  return handleResponse(response);
};

export const getWatchPosition = async (
  torrentId: string,
  fileIndex: number,
): Promise<WatchPosition | null> => {
  const response = await fetch(buildUrl(`/watch-history/${torrentId}/${fileIndex}`));
  if (response.status === 404) return null;
  return handleResponse(response);
};

export const getWatchHistory = async (limit = 20): Promise<WatchPosition[]> => {
  const response = await deduplicatedFetch(
    buildUrl(`/watch-history?limit=${limit}`),
    undefined,
    POLL_REQUEST_TIMEOUT_MS,
  );
  return handleResponse(response);
};

export const getEncodingSettings = async (): Promise<EncodingSettings> => {
  const response = await fetch(buildUrl('/settings/encoding'));
  return handleResponse(response);
};

export const getPlayerSettings = async (): Promise<PlayerSettings> => {
  const response = await deduplicatedFetch(buildUrl('/settings/player'), undefined, POLL_REQUEST_TIMEOUT_MS);
  return handleResponse(response);
};

export const getPlayerHealth = async (): Promise<PlayerHealth> => {
  const response = await deduplicatedFetch(
    buildUrl('/internal/health/player'),
    undefined,
    POLL_REQUEST_TIMEOUT_MS,
  );
  return handleResponse(response);
};

export const updatePlayerSettings = async (
  input: { currentTorrentId: string | null },
): Promise<PlayerSettings> => {
  const response = await fetch(buildUrl('/settings/player'), {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ currentTorrentId: input.currentTorrentId ?? '' }),
  });
  return handleResponse(response);
};

export const updateEncodingSettings = async (
  input: Partial<EncodingSettings>,
): Promise<EncodingSettings> => {
  const response = await fetch(buildUrl('/settings/encoding'), {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
  return handleResponse(response);
};

export const hlsSeek = async (
  id: string,
  fileIndex: number,
  time: number,
  options?: { audioTrack?: number | null; subtitleTrack?: number | null },
): Promise<{ seekTime: number }> => {
  const params = new URLSearchParams();
  params.set('time', String(time));
  if (options?.audioTrack !== undefined && options.audioTrack !== null) {
    params.set('audioTrack', String(options.audioTrack));
  }
  if (options?.subtitleTrack !== undefined && options.subtitleTrack !== null) {
    params.set('subtitleTrack', String(options.subtitleTrack));
  }
  const response = await fetch(
    buildUrl(`/torrents/${id}/hls/${fileIndex}/seek?${params.toString()}`),
    { method: 'POST' },
  );
  return handleResponse(response);
};

export const focusTorrent = async (id: string): Promise<void> => {
  const response = await fetch(buildUrl(`/torrents/${id}/focus`), { method: 'POST' });
  return handleResponse(response);
};

export const unfocusTorrents = async (): Promise<void> => {
  const response = await fetch(buildUrl('/torrents/unfocus'), { method: 'POST' });
  return handleResponse(response);
};

export const isApiError = (error: unknown): error is ApiRequestError =>
  error instanceof ApiRequestError;
