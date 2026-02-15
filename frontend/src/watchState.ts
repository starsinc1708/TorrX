const WATCH_STATE_KEY = 'watchStateByTorrent';
export const WATCH_STATE_UPDATED_EVENT = 'player:watch-state-updated';

export interface TorrentWatchState {
  torrentId: string;
  fileIndex: number;
  position: number;
  duration: number;
  torrentName?: string;
  filePath?: string;
  updatedAt: string;
}

type WatchStateMap = Record<string, TorrentWatchState>;

const safeNumber = (value: unknown): number | null => {
  if (typeof value !== 'number' || !Number.isFinite(value)) {
    return null;
  }
  return value;
};

const readMap = (): WatchStateMap => {
  const raw = localStorage.getItem(WATCH_STATE_KEY);
  if (!raw) return {};
  try {
    const parsed = JSON.parse(raw) as Record<string, unknown>;
    if (!parsed || typeof parsed !== 'object') return {};
    const result: WatchStateMap = {};
    for (const [torrentId, value] of Object.entries(parsed)) {
      if (!value || typeof value !== 'object') continue;
      const entry = value as Partial<TorrentWatchState>;
      const fileIndex = safeNumber(entry.fileIndex);
      const position = safeNumber(entry.position);
      const duration = safeNumber(entry.duration);
      if (!torrentId || fileIndex === null || position === null || duration === null) {
        continue;
      }
      result[torrentId] = {
        torrentId,
        fileIndex,
        position,
        duration,
        torrentName: typeof entry.torrentName === 'string' ? entry.torrentName : undefined,
        filePath: typeof entry.filePath === 'string' ? entry.filePath : undefined,
        updatedAt: typeof entry.updatedAt === 'string' ? entry.updatedAt : new Date().toISOString(),
      };
    }
    return result;
  } catch {
    return {};
  }
};

const writeMap = (map: WatchStateMap) => {
  localStorage.setItem(WATCH_STATE_KEY, JSON.stringify(map));
  window.dispatchEvent(new Event(WATCH_STATE_UPDATED_EVENT));
};

export const getTorrentWatchState = (torrentId: string): TorrentWatchState | null => {
  if (!torrentId) return null;
  const map = readMap();
  return map[torrentId] ?? null;
};

export const upsertTorrentWatchState = (input: Omit<TorrentWatchState, 'updatedAt'>) => {
  if (!input.torrentId) return;
  if (!Number.isFinite(input.position) || input.position < 0) return;
  if (!Number.isFinite(input.duration) || input.duration <= 0) return;

  const map = readMap();
  map[input.torrentId] = {
    ...input,
    updatedAt: new Date().toISOString(),
  };
  writeMap(map);
};
