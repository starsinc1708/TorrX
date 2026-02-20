import type {
  SearchRankingProfile,
  SearchResultItem,
  SearchSortBy,
  SearchSortOrder,
} from '../types';

export type ResultFilters = {
  sources: string[];
  qualities: string[];
  audio: string[];
  subtitles: string[];
  contentTypes: string[];
  dubbingTypes: string[];
  yearMin: string;
  yearMax: string;
  minSizeGB: string;
  maxSizeGB: string;
  minSeeders: string;
  includeKeywords: string;
  excludeKeywords: string;
  excludeCam: boolean;
};

export const defaultResultFilters: ResultFilters = {
  sources: [],
  qualities: [],
  audio: [],
  subtitles: [],
  contentTypes: [],
  dubbingTypes: [],
  yearMin: '',
  yearMax: '',
  minSizeGB: '',
  maxSizeGB: '',
  minSeeders: '',
  includeKeywords: '',
  excludeKeywords: '',
  excludeCam: false,
};

export type SavedFilterPreset = {
  id: string;
  name: string;
  filters: ResultFilters;
  updatedAt: string;
};

export const sortOptions: Array<{ value: SearchSortBy; label: string }> = [
  { value: 'relevance', label: 'Relevance' },
  { value: 'seeders', label: 'Seeders' },
  { value: 'sizeBytes', label: 'Size' },
  { value: 'publishedAt', label: 'Published' },
];

export const sortOrderOptions: Array<{ value: SearchSortOrder; label: string }> = [
  { value: 'desc', label: 'Desc' },
  { value: 'asc', label: 'Asc' },
];

export const profileStorageKey = 'search-ranking-profile:v2';

export const defaultRankingProfile: SearchRankingProfile = {
  freshnessWeight: 1,
  seedersWeight: 1,
  qualityWeight: 1,
  languageWeight: 5,
  sizeWeight: 0.4,
  preferSeries: true,
  preferMovies: true,
  preferredAudio: ['RU'],
  preferredSubtitles: ['RU'],
  targetSizeBytes: 0,
};

export const phaseLabels: Record<string, string> = {
  bootstrap: 'Searching providers...',
  update: 'Results incoming...',
  fast: 'Fast providers loaded. Fetching slow sources...',
  full: 'All providers loaded.',
};

export const simplifyProviderError = (raw: string): string => {
  const trimmed = String(raw ?? '').trim();
  if (!trimmed) return 'Failed';
  const m = trimmed.match(/:\s*(.+)$/);
  const tail = (m?.[1] ?? trimmed).trim();
  const lower = tail.toLowerCase();
  if (lower.includes('context deadline exceeded')) return 'Timeout';
  if (lower.includes('i/o timeout')) return 'Timeout';
  if (lower.includes('connection refused')) return 'Connection refused';
  if (lower.includes('no such host')) return 'DNS error';
  return tail.length > 120 ? `${tail.slice(0, 117)}...` : tail;
};

export const buildResultKey = (item: SearchResultItem) =>
  item.infoHash ||
  item.magnet ||
  item.pageUrl ||
  `${String(item.source ?? 'torrent')}:${String(item.tracker ?? '')}:${String(item.name ?? '').trim()}`;

export const tokenizeKeywords = (raw: string): string[] =>
  raw
    .split(',')
    .map((v) => v.trim().toLowerCase())
    .filter(Boolean);

const filterPresetsStorageKey = 'search-result-filter-presets:v1';

export const loadSavedFilterPresets = (): SavedFilterPreset[] => {
  try {
    const raw = window.localStorage.getItem(filterPresetsStorageKey);
    if (!raw) return [];
    const parsed = JSON.parse(raw) as SavedFilterPreset[];
    if (!Array.isArray(parsed)) return [];
    return parsed
      .filter((item) => item && typeof item === 'object')
      .map((item) => ({
        id: String(item.id ?? ''),
        name: String(item.name ?? '').trim(),
        filters: { ...defaultResultFilters, ...(item.filters ?? {}) },
        updatedAt: String(item.updatedAt ?? new Date().toISOString()),
      }))
      .filter((item) => item.id && item.name);
  } catch {
    return [];
  }
};

export const saveFilterPresets = (presets: SavedFilterPreset[]) => {
  window.localStorage.setItem(filterPresetsStorageKey, JSON.stringify(presets.slice(0, 20)));
};

export const loadStoredProfile = (): SearchRankingProfile => {
  try {
    const raw = window.localStorage.getItem(profileStorageKey);
    if (!raw) return defaultRankingProfile;
    const parsed = JSON.parse(raw) as Partial<SearchRankingProfile>;
    return {
      ...defaultRankingProfile,
      ...parsed,
      preferredAudio: Array.isArray(parsed.preferredAudio) ? parsed.preferredAudio : [],
      preferredSubtitles: Array.isArray(parsed.preferredSubtitles) ? parsed.preferredSubtitles : [],
      targetSizeBytes: Number(parsed.targetSizeBytes) > 0 ? Number(parsed.targetSizeBytes) : 0,
    };
  } catch {
    return defaultRankingProfile;
  }
};
