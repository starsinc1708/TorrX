const PLAYER_PREFS_KEY = 'playerPreferencesByTorrent:v1';

export type TorrentPlayerPreferences = {
  audioTrack?: number | null;
  subtitleTrack?: number | null;
  playbackRate?: number;
  preferredQualityLevel?: number;
  updatedAt: string;
};

type PreferencesMap = Record<string, TorrentPlayerPreferences>;

const isValidTrackValue = (value: unknown): value is number | null =>
  value === null || (typeof value === 'number' && Number.isInteger(value) && value >= 0);

const isValidQualityLevel = (value: unknown): value is number =>
  typeof value === 'number' && Number.isInteger(value) && value >= -1;

const clampPlaybackRate = (value: number): number => {
  if (!Number.isFinite(value)) return 1;
  if (value < 0.25) return 0.25;
  if (value > 2) return 2;
  return Number(value.toFixed(2));
};

const readMap = (): PreferencesMap => {
  const raw = window.localStorage.getItem(PLAYER_PREFS_KEY);
  if (!raw) return {};
  try {
    const parsed = JSON.parse(raw) as Record<string, unknown>;
    if (!parsed || typeof parsed !== 'object') return {};
    const next: PreferencesMap = {};
    for (const [torrentId, value] of Object.entries(parsed)) {
      if (!torrentId || !value || typeof value !== 'object') continue;
      const candidate = value as Partial<TorrentPlayerPreferences>;
      const item: TorrentPlayerPreferences = {
        updatedAt: typeof candidate.updatedAt === 'string' ? candidate.updatedAt : new Date().toISOString(),
      };
      if (Object.prototype.hasOwnProperty.call(candidate, 'audioTrack') && isValidTrackValue(candidate.audioTrack)) {
        item.audioTrack = candidate.audioTrack;
      }
      if (
        Object.prototype.hasOwnProperty.call(candidate, 'subtitleTrack') &&
        isValidTrackValue(candidate.subtitleTrack)
      ) {
        item.subtitleTrack = candidate.subtitleTrack;
      }
      if (typeof candidate.playbackRate === 'number' && Number.isFinite(candidate.playbackRate)) {
        item.playbackRate = clampPlaybackRate(candidate.playbackRate);
      }
      if (
        Object.prototype.hasOwnProperty.call(candidate, 'preferredQualityLevel') &&
        isValidQualityLevel(candidate.preferredQualityLevel)
      ) {
        item.preferredQualityLevel = candidate.preferredQualityLevel;
      }
      next[torrentId] = item;
    }
    return next;
  } catch {
    return {};
  }
};

const writeMap = (map: PreferencesMap) => {
  window.localStorage.setItem(PLAYER_PREFS_KEY, JSON.stringify(map));
};

export const getTorrentPlayerPreferences = (torrentId: string): TorrentPlayerPreferences | null => {
  if (!torrentId) return null;
  const map = readMap();
  return map[torrentId] ?? null;
};

export const patchTorrentPlayerPreferences = (
  torrentId: string,
  patch: Partial<Omit<TorrentPlayerPreferences, 'updatedAt'>>,
) => {
  if (!torrentId) return;
  const map = readMap();
  const prev = map[torrentId] ?? { updatedAt: new Date().toISOString() };
  const next: TorrentPlayerPreferences = {
    ...prev,
    updatedAt: new Date().toISOString(),
  };

  if (Object.prototype.hasOwnProperty.call(patch, 'audioTrack') && isValidTrackValue(patch.audioTrack)) {
    next.audioTrack = patch.audioTrack;
  }
  if (
    Object.prototype.hasOwnProperty.call(patch, 'subtitleTrack') &&
    isValidTrackValue(patch.subtitleTrack)
  ) {
    next.subtitleTrack = patch.subtitleTrack;
  }
  if (
    Object.prototype.hasOwnProperty.call(patch, 'playbackRate') &&
    typeof patch.playbackRate === 'number' &&
    Number.isFinite(patch.playbackRate)
  ) {
    next.playbackRate = clampPlaybackRate(patch.playbackRate);
  }
  if (
    Object.prototype.hasOwnProperty.call(patch, 'preferredQualityLevel') &&
    isValidQualityLevel(patch.preferredQualityLevel)
  ) {
    next.preferredQualityLevel = patch.preferredQualityLevel;
  }

  map[torrentId] = next;
  writeMap(map);
};

