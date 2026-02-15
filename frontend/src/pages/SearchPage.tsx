import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  BookmarkCheck,
  BookmarkPlus,
  Check,
  ChevronDown,
  ExternalLink,
  Filter,
  Loader2,
  Plus,
  RefreshCw,
  Search as SearchIcon,
  SlidersHorizontal,
  Trash2,
  X,
} from 'lucide-react';
import {
  createTorrentFromMagnet,
  isApiError,
  listSearchProviders,
  searchTorrents,
  searchTorrentsStream,
} from '../api';
import { useCatalogMeta } from '../app/providers/CatalogMetaProvider';
import { useToast } from '../app/providers/ToastProvider';
import { onSearchProvidersChanged, resolveEnabledSearchProviders } from '../searchProviderSettings';
import { Badge } from '../components/ui/badge';
import { Button } from '../components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '../components/ui/card';
import { Dialog, DialogBody, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '../components/ui/dialog';
import { Input } from '../components/ui/input';
import { MultiSelect } from '../components/ui/multi-select';
import { Select } from '../components/ui/select';
import { cn } from '../lib/cn';
import type {
  SearchProviderInfo,
  SearchProviderStatus,
  SearchRankingProfile,
  SearchResponse,
  SearchResultItem,
  SearchSortBy,
  SearchSortOrder,
} from '../types';
import { formatBytes } from '../utils';

const sortOptions: Array<{ value: SearchSortBy; label: string }> = [
  { value: 'relevance', label: 'Relevance' },
  { value: 'seeders', label: 'Seeders' },
  { value: 'sizeBytes', label: 'Size' },
  { value: 'publishedAt', label: 'Published' },
];

const sortOrderOptions: Array<{ value: SearchSortOrder; label: string }> = [
  { value: 'desc', label: 'Desc' },
  { value: 'asc', label: 'Asc' },
];

const profileStorageKey = 'search-ranking-profile:v2';

const defaultRankingProfile: SearchRankingProfile = {
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

const phaseLabels: Record<string, string> = {
  bootstrap: 'Searching providers...',
  update: 'Results incoming...',
  fast: 'Fast providers loaded. Fetching slow sources...',
  full: 'All providers loaded.',
};

const simplifyProviderError = (raw: string): string => {
  const trimmed = String(raw ?? '').trim();
  if (!trimmed) return 'Failed';
  // Common format: `Get "URL": message`
  const m = trimmed.match(/:\s*(.+)$/);
  const tail = (m?.[1] ?? trimmed).trim();
  const lower = tail.toLowerCase();
  if (lower.includes('context deadline exceeded')) return 'Timeout';
  if (lower.includes('i/o timeout')) return 'Timeout';
  if (lower.includes('connection refused')) return 'Connection refused';
  if (lower.includes('no such host')) return 'DNS error';
  return tail.length > 120 ? `${tail.slice(0, 117)}...` : tail;
};

const buildResultKey = (item: SearchResultItem) =>
  item.infoHash ||
  item.magnet ||
  item.pageUrl ||
  `${String(item.source ?? 'torrent')}:${String(item.tracker ?? '')}:${String(item.name ?? '').trim()}`;

type ResultFilters = {
  sources: string[];
  qualities: string[];
  audio: string[];
  subtitles: string[];
  minSizeGB: string;
  maxSizeGB: string;
  minSeeders: string;
  includeKeywords: string;
  excludeKeywords: string;
  excludeCam: boolean;
};

const defaultResultFilters: ResultFilters = {
  sources: [],
  qualities: [],
  audio: [],
  subtitles: [],
  minSizeGB: '',
  maxSizeGB: '',
  minSeeders: '',
  includeKeywords: '',
  excludeKeywords: '',
  excludeCam: false,
};

type SavedFilterPreset = {
  id: string;
  name: string;
  filters: ResultFilters;
  updatedAt: string;
};

const filterPresetsStorageKey = 'search-result-filter-presets:v1';

const tokenizeKeywords = (raw: string): string[] =>
  raw
    .split(',')
    .map((v) => v.trim().toLowerCase())
    .filter(Boolean);

const loadSavedFilterPresets = (): SavedFilterPreset[] => {
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

const saveFilterPresets = (presets: SavedFilterPreset[]) => {
  window.localStorage.setItem(filterPresetsStorageKey, JSON.stringify(presets.slice(0, 20)));
};

const loadStoredProfile = (): SearchRankingProfile => {
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

const SearchPage: React.FC = () => {
  const { items: catalogItems } = useCatalogMeta();
  const { toast } = useToast();

  const [query, setQuery] = useState('');
  const [activeQuery, setActiveQuery] = useState('');
  const [providers, setProviders] = useState<SearchProviderInfo[]>([]);
  const [enabledProviders, setEnabledProviders] = useState<string[]>([]);
  const [providerStatus, setProviderStatus] = useState<SearchProviderStatus[]>([]);
  const [items, setItems] = useState<SearchResultItem[]>([]);
  const [totalItems, setTotalItems] = useState(0);
  const [elapsedMs, setElapsedMs] = useState(0);
  const [hasMore, setHasMore] = useState(false);
  const [sortBy, setSortBy] = useState<SearchSortBy>('relevance');
  const [sortOrder, setSortOrder] = useState<SearchSortOrder>('desc');
  const [limit, setLimit] = useState(30);
  const [isLoading, setIsLoading] = useState(false);
  const [isLoadingMore, setIsLoadingMore] = useState(false);
  const [isLoadingProviders, setIsLoadingProviders] = useState(true);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const [phaseMessage, setPhaseMessage] = useState('');
  const [streamActive, setStreamActive] = useState(false);
  const [profile, setProfile] = useState<SearchRankingProfile>(() => loadStoredProfile());
  const [addState, setAddState] = useState<Record<string, 'adding' | 'added' | 'error'>>({});
  const [resultFilters, setResultFilters] = useState<ResultFilters>(defaultResultFilters);
  const [filtersOpen, setFiltersOpen] = useState(false);
  const [savedFilterPresets, setSavedFilterPresets] = useState<SavedFilterPreset[]>(() => loadSavedFilterPresets());
  const [selectedFilterPresetId, setSelectedFilterPresetId] = useState('');
  const [detailItem, setDetailItem] = useState<SearchResultItem | null>(null);

  const streamCleanupRef = useRef<(() => void) | null>(null);
  const streamTokenRef = useRef(0);
  const lastPhaseRef = useRef<string>('');
  const providerErrorToastRef = useRef<Map<string, string>>(new Map());
  const sortByRef = useRef(sortBy);
  sortByRef.current = sortBy;
  const sortOrderRef = useRef(sortOrder);
  sortOrderRef.current = sortOrder;

  const catalogNameSet = useMemo(() => {
    const set = new Set<string>();
    for (const t of catalogItems) {
      const name = (t.name ?? '').trim().toLowerCase();
      if (name) set.add(name);
    }
    return set;
  }, [catalogItems]);

  const stopStream = useCallback(() => {
    if (streamCleanupRef.current) {
      streamCleanupRef.current();
      streamCleanupRef.current = null;
    }
    setStreamActive(false);
  }, []);

  useEffect(() => {
    window.localStorage.setItem(profileStorageKey, JSON.stringify(profile));
  }, [profile]);

  useEffect(() => {
    saveFilterPresets(savedFilterPresets);
  }, [savedFilterPresets]);

  useEffect(() => {
    if (!errorMessage) return;
    toast({ title: 'Search', description: errorMessage, variant: 'danger' });
    setErrorMessage(null);
  }, [errorMessage, toast]);

  useEffect(() => {
    if (providerStatus.length === 0) return;
    for (const status of providerStatus) {
      if (status.ok) continue;
      const name = String(status.name ?? '').trim();
      if (!name) continue;
      const key = name.toLowerCase();
      const raw = String(status.error ?? 'failed');
      const signature = raw;
      const prev = providerErrorToastRef.current.get(key);
      if (prev === signature) continue;
      providerErrorToastRef.current.set(key, signature);
      toast({
        title: name,
        description: simplifyProviderError(raw),
        variant: 'danger',
      });
    }
  }, [providerStatus, toast]);

  useEffect(() => {
    let cancelled = false;
    const loadProviders = async () => {
      setIsLoadingProviders(true);
      try {
        const list = await listSearchProviders();
        if (cancelled) return;
        setProviders(list);
        const configured = list.filter((p) => p.enabled).map((p) => p.name);
        setEnabledProviders(resolveEnabledSearchProviders(configured));
      } catch {
        if (cancelled) return;
        setErrorMessage('Failed to load search providers');
      } finally {
        if (!cancelled) setIsLoadingProviders(false);
      }
    };
    void loadProviders();
    return () => {
      cancelled = true;
      stopStream();
    };
  }, [stopStream]);

  useEffect(() => {
    const off = onSearchProvidersChanged((next) => {
      const available = providers.filter((p) => p.enabled).map((p) => p.name.toLowerCase());
      setEnabledProviders(next.filter((name) => available.includes(name)));
    });
    return off;
  }, [providers]);

  const applyResponse = useCallback((response: SearchResponse, mode: 'replace' | 'append' | 'merge') => {
    const safeItems = Array.isArray(response.items) ? response.items : [];
    const safeProviders = Array.isArray(response.providers) ? response.providers : [];
    setProviderStatus(safeProviders);
    setElapsedMs(response.elapsedMs ?? 0);
    setTotalItems(response.totalItems ?? 0);
    setHasMore(Boolean(response.hasMore));
    if (mode === 'append') {
      setItems((prev) => [...prev, ...safeItems]);
    } else {
      // Both 'replace' and 'merge' use the full sorted snapshot from the server
      setItems(safeItems);
    }
  }, []);

  const runSearch = useCallback(
    async (nextOffset: number, append: boolean, noCache = false) => {
      if (!activeQuery.trim()) return;
      if (enabledProviders.length === 0) {
        setErrorMessage('Enable at least one source in Settings');
        return;
      }

      setErrorMessage(null);
      setPhaseMessage('');

      if (append) {
        setIsLoadingMore(true);
        try {
          const response = await searchTorrents({
            query: activeQuery,
            limit,
            offset: nextOffset,
            sortBy: sortByRef.current,
            sortOrder: sortOrderRef.current,
            providers: enabledProviders,
            profile,
            noCache,
          });
          applyResponse(response, 'append');
        } catch (error) {
          if (isApiError(error)) setErrorMessage(error.message || 'Search failed');
          else setErrorMessage('Search failed');
        } finally {
          setIsLoadingMore(false);
        }
        return;
      }

      stopStream();
      providerErrorToastRef.current.clear();
      setIsLoading(true);
      setItems([]);
      setProviderStatus([]);

      const token = streamTokenRef.current + 1;
      streamTokenRef.current = token;

      streamCleanupRef.current = searchTorrentsStream(
        {
          query: activeQuery,
          limit,
          offset: nextOffset,
          sortBy: sortByRef.current,
          sortOrder: sortOrderRef.current,
          providers: enabledProviders,
          profile,
          noCache,
        },
          {
            onPhase: (response) => {
              if (streamTokenRef.current !== token) return;
            if (response.phase === 'bootstrap') {
              setPhaseMessage(phaseLabels.bootstrap);
              setStreamActive(true);
              return;
            }
            if (response.phase === 'update') {
              const providerName = response.provider ?? '';
              const label = providerName ? `${providerName} loaded` : 'Results updated';
              setPhaseMessage(label);
              applyResponse(response, 'merge');
              setIsLoading(false);
              setStreamActive(!response.final);
              return;
            }
            // Legacy: fast/full phases for backward compat
            if (response.phase) {
              const nextPhase = response.phase;
              setPhaseMessage(phaseLabels[nextPhase] ?? nextPhase);
              if (lastPhaseRef.current !== nextPhase && (nextPhase === 'fast' || nextPhase === 'full')) {
                toast({ title: 'Search', description: phaseLabels[nextPhase] ?? nextPhase });
              }
              lastPhaseRef.current = nextPhase;
            }
            applyResponse(response, 'replace');
            setIsLoading(false);
            setStreamActive(!response.final);
          },
          onDone: () => {
            if (streamTokenRef.current !== token) return;
            setStreamActive(false);
            setIsLoading(false);
          },
          onError: (message) => {
            if (streamTokenRef.current !== token) return;
            setErrorMessage(message || 'Search stream failed');
            setStreamActive(false);
            setIsLoading(false);
          },
        },
      );
    },
    [activeQuery, enabledProviders, limit, profile, stopStream, applyResponse],
  );

  const handleSearchSubmit = useCallback(
    async (event: React.FormEvent) => {
      event.preventDefault();
      const normalized = query.trim();
      if (!normalized) {
        setErrorMessage('Enter search query');
        return;
      }
      // Treat every submit as a fresh search: clear stale UI immediately.
      stopStream();
      providerErrorToastRef.current.clear();
      setItems([]);
      setProviderStatus([]);
      setTotalItems(0);
      setHasMore(false);
      setElapsedMs(0);
      setPhaseMessage('');
      setStreamActive(false);

      if (normalized !== activeQuery) {
        setResultFilters(defaultResultFilters);
        setSelectedFilterPresetId('');
        setActiveQuery(normalized);
        return;
      }
      await runSearch(0, false);
    },
    [activeQuery, query, runSearch, stopStream],
  );

  const handleForceRefresh = useCallback(
    async () => {
      if (!activeQuery.trim()) return;
      // Force refresh bypasses server-side cache (useful for debugging or latest results).
      stopStream();
      providerErrorToastRef.current.clear();
      setItems([]);
      setProviderStatus([]);
      setTotalItems(0);
      setHasMore(false);
      setElapsedMs(0);
      setPhaseMessage('');
      setStreamActive(false);
      await runSearch(0, false, true);
    },
    [activeQuery, runSearch, stopStream],
  );

  useEffect(() => {
    if (!activeQuery.trim()) return;
    void runSearch(0, false);
  }, [activeQuery, runSearch]);

  const handleLoadMore = useCallback(async () => {
    await runSearch(items.length, true);
  }, [items.length, runSearch]);

  const handleAddTorrent = useCallback(
    async (item: SearchResultItem) => {
      const key = buildResultKey(item);
      const normalizedName = (item.name ?? '').trim().toLowerCase();
      if (normalizedName && catalogNameSet.has(normalizedName)) {
        setAddState((prev) => ({ ...prev, [key]: 'added' }));
        toast({ title: 'Already added', description: item.name });
        return;
      }
      if (!item.magnet) {
        setAddState((prev) => ({ ...prev, [key]: 'error' }));
        toast({ title: 'Cannot add torrent', description: 'Magnet link is missing', variant: 'danger' });
        return;
      }
      setAddState((prev) => ({ ...prev, [key]: 'adding' }));
      try {
        await createTorrentFromMagnet(item.magnet, item.name);
        setAddState((prev) => ({ ...prev, [key]: 'added' }));
        toast({ title: 'Added to catalog', description: item.name, variant: 'success' });
        window.dispatchEvent(new Event('torrents:refresh'));
      } catch (error) {
        setAddState((prev) => ({ ...prev, [key]: 'error' }));
        const message = isApiError(error) ? error.message : 'Failed to add torrent';
        toast({ title: 'Add failed', description: message, variant: 'danger' });
      }
    },
    [catalogNameSet, toast],
  );

  const updateWeight = useCallback(
    (
      field: 'freshnessWeight' | 'seedersWeight' | 'qualityWeight' | 'languageWeight' | 'sizeWeight',
      value: number,
    ) => {
      setProfile((prev) => ({ ...prev, [field]: value }));
    },
    [],
  );

  const targetSizeGB = profile.targetSizeBytes > 0 ? profile.targetSizeBytes / (1024 ** 3) : 0;
  const enabledProvidersSet = useMemo(() => new Set(enabledProviders), [enabledProviders]);

  const preferredAudioOptions = useMemo(() => {
    const set = new Set<string>(['RU', 'EN']);
    for (const v of profile.preferredAudio ?? []) set.add(String(v).trim().toUpperCase());
    for (const item of items) {
      for (const v of item.enrichment?.audio ?? []) set.add(String(v).trim().toUpperCase());
    }
    return Array.from(set)
      .filter(Boolean)
      .sort((a, b) => a.localeCompare(b))
      .map((v) => ({ value: v, label: v }));
  }, [items, profile.preferredAudio]);

  const preferredSubtitleOptions = useMemo(() => {
    const set = new Set<string>(['RU', 'EN']);
    for (const v of profile.preferredSubtitles ?? []) set.add(String(v).trim().toUpperCase());
    for (const item of items) {
      for (const v of item.enrichment?.subtitles ?? []) set.add(String(v).trim().toUpperCase());
    }
    return Array.from(set)
      .filter(Boolean)
      .sort((a, b) => a.localeCompare(b))
      .map((v) => ({ value: v, label: v }));
  }, [items, profile.preferredSubtitles]);

  const sourceFacetOptions = useMemo(() => {
    const set = new Set<string>();
    for (const item of items) {
      if (item.sources?.length) {
        for (const src of item.sources) {
          const v = String(src.name ?? '').trim();
          if (v) set.add(v);
        }
      } else {
        const v = String(item.source ?? '').trim();
        if (v) set.add(v);
      }
    }
    return Array.from(set)
      .sort((a, b) => a.localeCompare(b))
      .map((v) => ({ value: v, label: v }));
  }, [items]);

  const qualityFacetOptions = useMemo(() => {
    const set = new Set<string>();
    for (const item of items) {
      const v = String(item.enrichment?.quality ?? '').trim();
      if (v) set.add(v);
    }
    return Array.from(set)
      .sort((a, b) => a.localeCompare(b))
      .map((v) => ({ value: v, label: v }));
  }, [items]);

  const audioFacetOptions = useMemo(() => {
    const set = new Set<string>();
    for (const item of items) {
      for (const v of item.enrichment?.audio ?? []) {
        const norm = String(v).trim().toUpperCase();
        if (norm) set.add(norm);
      }
    }
    return Array.from(set)
      .sort((a, b) => a.localeCompare(b))
      .map((v) => ({ value: v, label: v }));
  }, [items]);

  const subtitleFacetOptions = useMemo(() => {
    const set = new Set<string>();
    for (const item of items) {
      for (const v of item.enrichment?.subtitles ?? []) {
        const norm = String(v).trim().toUpperCase();
        if (norm) set.add(norm);
      }
    }
    return Array.from(set)
      .sort((a, b) => a.localeCompare(b))
      .map((v) => ({ value: v, label: v }));
  }, [items]);

  const filteredItems = useMemo(() => {
    const sources = new Set(resultFilters.sources);
    const qualities = new Set(resultFilters.qualities);
    const audio = new Set(resultFilters.audio.map((v) => String(v).trim().toUpperCase()).filter(Boolean));
    const subtitles = new Set(resultFilters.subtitles.map((v) => String(v).trim().toUpperCase()).filter(Boolean));

    const minSizeGB = Number(resultFilters.minSizeGB);
    const maxSizeGB = Number(resultFilters.maxSizeGB);
    const minSizeBytes = Number.isFinite(minSizeGB) && minSizeGB > 0 ? Math.round(minSizeGB * 1024 ** 3) : 0;
    const maxSizeBytes =
      Number.isFinite(maxSizeGB) && maxSizeGB > 0 ? Math.round(maxSizeGB * 1024 ** 3) : Number.POSITIVE_INFINITY;

    const minSeeders = Number(resultFilters.minSeeders);
    const minSeeds = Number.isFinite(minSeeders) && minSeeders > 0 ? Math.floor(minSeeders) : 0;
    const includeKeywords = tokenizeKeywords(resultFilters.includeKeywords);
    const excludeKeywords = tokenizeKeywords(resultFilters.excludeKeywords);
    const excludeCam = resultFilters.excludeCam;

    const filtered = items.filter((item) => {
      if (sources.size > 0) {
        const itemSources = item.sources?.length
          ? item.sources.map((s) => String(s.name ?? '').trim()).filter(Boolean)
          : [String(item.source ?? '').trim()].filter(Boolean);
        if (!itemSources.some((v) => sources.has(v))) return false;
      }

      const quality = String(item.enrichment?.quality ?? '').trim();
      if (qualities.size > 0 && !qualities.has(quality)) return false;

      if (audio.size > 0) {
        const itemAudio = (item.enrichment?.audio ?? []).map((v) => String(v).trim().toUpperCase()).filter(Boolean);
        if (!itemAudio.some((v) => audio.has(v))) return false;
      }

      if (subtitles.size > 0) {
        const itemSubs = (item.enrichment?.subtitles ?? []).map((v) => String(v).trim().toUpperCase()).filter(Boolean);
        if (!itemSubs.some((v) => subtitles.has(v))) return false;
      }

      const sizeBytes = Number(item.sizeBytes ?? 0);
      if (minSizeBytes > 0 && (!Number.isFinite(sizeBytes) || sizeBytes <= 0 || sizeBytes < minSizeBytes)) return false;
      if (Number.isFinite(maxSizeBytes) && maxSizeBytes > 0 && sizeBytes > maxSizeBytes) return false;

      const seeds = Number(item.seeders ?? 0);
      if (minSeeds > 0 && (!Number.isFinite(seeds) || seeds < minSeeds)) return false;

      const haystack = [
        String(item.name ?? ''),
        String(item.enrichment?.quality ?? ''),
        String(item.enrichment?.sourceType ?? ''),
        String(item.enrichment?.description ?? ''),
      ]
        .join(' ')
        .toLowerCase();

      if (includeKeywords.length > 0 && !includeKeywords.every((token) => haystack.includes(token))) {
        return false;
      }
      if (excludeKeywords.length > 0 && excludeKeywords.some((token) => haystack.includes(token))) {
        return false;
      }
      if (excludeCam) {
        const camTokens = ['cam', 'ts', 'telesync', 'hdts', 'telecine', 'tc'];
        if (camTokens.some((token) => haystack.includes(token))) {
          return false;
        }
      }

      return true;
    });

    if (sortBy === 'relevance') return filtered;

    const dir = sortOrder === 'asc' ? 1 : -1;
    return [...filtered].sort((a, b) => {
      let va: number;
      let vb: number;
      switch (sortBy) {
        case 'seeders':
          va = Number(a.seeders ?? 0);
          vb = Number(b.seeders ?? 0);
          break;
        case 'sizeBytes':
          va = Number(a.sizeBytes ?? 0);
          vb = Number(b.sizeBytes ?? 0);
          break;
        case 'publishedAt':
          va = a.publishedAt ? new Date(a.publishedAt).getTime() : 0;
          vb = b.publishedAt ? new Date(b.publishedAt).getTime() : 0;
          break;
        default:
          return 0;
      }
      return (va - vb) * dir;
    });
  }, [items, resultFilters, sortBy, sortOrder]);

  const hasActiveFilters = useMemo(() => {
    if (resultFilters.sources.length > 0) return true;
    if (resultFilters.qualities.length > 0) return true;
    if (resultFilters.audio.length > 0) return true;
    if (resultFilters.subtitles.length > 0) return true;
    if (String(resultFilters.minSizeGB).trim()) return true;
    if (String(resultFilters.maxSizeGB).trim()) return true;
    if (String(resultFilters.minSeeders).trim()) return true;
    if (String(resultFilters.includeKeywords).trim()) return true;
    if (String(resultFilters.excludeKeywords).trim()) return true;
    if (resultFilters.excludeCam) return true;
    return false;
  }, [resultFilters]);

  const activeFilterCount = useMemo(() => {
    let count = 0;
    if (resultFilters.sources.length > 0) count += 1;
    if (resultFilters.qualities.length > 0) count += 1;
    if (resultFilters.audio.length > 0) count += 1;
    if (resultFilters.subtitles.length > 0) count += 1;
    if (String(resultFilters.minSizeGB).trim() || String(resultFilters.maxSizeGB).trim()) count += 1;
    if (String(resultFilters.minSeeders).trim()) count += 1;
    if (String(resultFilters.includeKeywords).trim()) count += 1;
    if (String(resultFilters.excludeKeywords).trim()) count += 1;
    if (resultFilters.excludeCam) count += 1;
    return count;
  }, [resultFilters]);

  const activeFilterBadges = useMemo(() => {
    const badges: Array<{ key: string; label: string }> = [];

    const summarize = (prefix: string, values: string[]) => {
      if (values.length === 0) return;
      const trimmed = values.map((v) => String(v).trim()).filter(Boolean);
      if (trimmed.length === 0) return;
      if (trimmed.length <= 2) badges.push({ key: prefix, label: `${prefix}: ${trimmed.join(", ")}` });
      else badges.push({ key: prefix, label: `${prefix}: ${trimmed[0]} +${trimmed.length - 1}` });
    };

    summarize('Source', resultFilters.sources);
    summarize('Quality', resultFilters.qualities);
    summarize('Audio', resultFilters.audio);
    summarize('Subs', resultFilters.subtitles);

    const minGB = String(resultFilters.minSizeGB).trim();
    const maxGB = String(resultFilters.maxSizeGB).trim();
    if (minGB || maxGB) {
      badges.push({ key: 'Size', label: `Size: ${minGB || '0'}-${maxGB || 'inf'} GB` });
    }

    const minSeeds = String(resultFilters.minSeeders).trim();
    if (minSeeds) badges.push({ key: 'Seeds', label: `Seeds >= ${minSeeds}` });

    const include = String(resultFilters.includeKeywords).trim();
    if (include) badges.push({ key: 'Include', label: `Include: ${include}` });

    const exclude = String(resultFilters.excludeKeywords).trim();
    if (exclude) badges.push({ key: 'Exclude', label: `Exclude: ${exclude}` });

    if (resultFilters.excludeCam) badges.push({ key: 'No CAM', label: 'Exclude CAM/TS' });

    return badges;
  }, [resultFilters]);

  const detailKey = detailItem ? buildResultKey(detailItem) : '';
  const detailAlreadyAdded = useMemo(() => {
    if (!detailItem) return false;
    const normalized = (detailItem.name ?? '').trim().toLowerCase();
    return normalized.length > 0 && catalogNameSet.has(normalized);
  }, [detailItem, catalogNameSet]);
  const detailStatus = detailItem ? (detailAlreadyAdded ? 'added' : addState[detailKey]) : undefined;
  const detailIsAdding = detailStatus === 'adding';
  const detailIsAdded = detailStatus === 'added';

  const redOnZeroProviders = useMemo(
    () =>
      new Set(
        [
          '1337x',
'jackett (torznab)',
          'the pirate bay',
          'prowlarr (torznab)',
          'rutracker',
        ].map((p) => p.toLowerCase()),
      ),
    [],
  );

  const clearFilterBadge = useCallback((badgeKey: string) => {
    const keyMap: Record<string, keyof ResultFilters> = {
      Source: 'sources',
      Quality: 'qualities',
      Audio: 'audio',
      Subs: 'subtitles',
    };
    const filterKey = keyMap[badgeKey];
    if (filterKey) {
      setResultFilters((prev) => ({ ...prev, [filterKey]: [] }));
      return;
    }
    if (badgeKey === 'Size') {
      setResultFilters((prev) => ({ ...prev, minSizeGB: '', maxSizeGB: '' }));
      return;
    }
    if (badgeKey === 'Seeds') {
      setResultFilters((prev) => ({ ...prev, minSeeders: '' }));
      return;
    }
    if (badgeKey === 'Include') {
      setResultFilters((prev) => ({ ...prev, includeKeywords: '' }));
      return;
    }
    if (badgeKey === 'Exclude') {
      setResultFilters((prev) => ({ ...prev, excludeKeywords: '' }));
      return;
    }
    if (badgeKey === 'No CAM') {
      setResultFilters((prev) => ({ ...prev, excludeCam: false }));
    }
  }, []);

  const handleSaveFilterPreset = useCallback(() => {
    if (!hasActiveFilters) {
      toast({ title: 'Filters', description: 'Set at least one filter before saving.', variant: 'warning' });
      return;
    }
    const nameRaw = window.prompt('Preset name', selectedFilterPresetId ? '' : `Preset ${savedFilterPresets.length + 1}`);
    const name = String(nameRaw ?? '').trim();
    if (!name) return;

    setSavedFilterPresets((prev) => {
      const existing = prev.find((item) => item.name.toLowerCase() === name.toLowerCase());
      if (existing) {
        return prev.map((item) =>
          item.id === existing.id ? { ...item, filters: { ...resultFilters }, updatedAt: new Date().toISOString() } : item,
        );
      }
      const next: SavedFilterPreset = {
        id: `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
        name,
        filters: { ...resultFilters },
        updatedAt: new Date().toISOString(),
      };
      return [next, ...prev].slice(0, 20);
    });
    toast({ title: 'Filters', description: `Preset "${name}" saved.`, variant: 'success' });
  }, [hasActiveFilters, resultFilters, savedFilterPresets.length, selectedFilterPresetId, toast]);

  const handleApplyFilterPreset = useCallback(
    (presetId: string) => {
      setSelectedFilterPresetId(presetId);
      if (!presetId) return;
      const preset = savedFilterPresets.find((item) => item.id === presetId);
      if (!preset) return;
      setResultFilters({ ...defaultResultFilters, ...preset.filters });
      toast({ title: 'Filters', description: `Applied preset "${preset.name}".` });
    },
    [savedFilterPresets, toast],
  );

  const handleDeleteFilterPreset = useCallback(() => {
    if (!selectedFilterPresetId) return;
    const preset = savedFilterPresets.find((item) => item.id === selectedFilterPresetId);
    setSavedFilterPresets((prev) => prev.filter((item) => item.id !== selectedFilterPresetId));
    setSelectedFilterPresetId('');
    if (preset) {
      toast({ title: 'Filters', description: `Preset "${preset.name}" removed.` });
    }
  }, [savedFilterPresets, selectedFilterPresetId, toast]);

  return (
    <div className="grid h-[calc(100dvh-3.5rem-2*theme(spacing.3))] gap-6 sm:h-[calc(100dvh-3.5rem-2*theme(spacing.4))] md:h-[calc(100dvh-3.5rem-2*theme(spacing.5))] lg:grid-cols-[minmax(320px,1fr)_minmax(0,2fr)]">
      <Card className="flex flex-col overflow-hidden lg:self-stretch">
        <CardHeader className="shrink-0 pb-3">
          <CardTitle className="flex items-center gap-2">
            <SearchIcon className="h-4 w-4 text-primary" />
            Torrent Search
          </CardTitle>
          <CardDescription>
            {isLoadingProviders ? 'Loading providers...' : 'Phased search across your configured sources.'}
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-5 overflow-y-auto">
          <form className="space-y-3" onSubmit={handleSearchSubmit}>
            <div className="space-y-2">
              <div className="text-sm font-medium">Query</div>
              <div className="grid gap-3 sm:grid-cols-[1fr_auto_auto] sm:items-end">
                <Input
                  value={query}
                  onChange={(event) => setQuery(event.target.value)}
                  placeholder="Movie, series, anime, linux iso..."
                />
                <Button type="submit" className="w-full sm:w-auto" disabled={isLoading || isLoadingProviders}>
                  {isLoading ? <Loader2 className="h-4 w-4 animate-spin" /> : <SearchIcon className="h-4 w-4" />}
                  Search
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  className="w-full sm:w-auto"
                  onClick={handleForceRefresh}
                  disabled={isLoading || isLoadingProviders || !activeQuery.trim()}
                  title="Force refresh (bypass cache)"
                >
                  <RefreshCw className="h-4 w-4" />
                </Button>
              </div>
            </div>
          </form>

          <div className="flex flex-wrap gap-2">
            {providers.map((provider) => {
              const name = provider.name.toLowerCase();
              const enabled = enabledProvidersSet.has(name);
              return (
                <Badge
                  key={provider.name}
                  className={cn(enabled ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300' : 'opacity-60')}
                  title={provider.name}
                >
                  {provider.label}
                </Badge>
              );
            })}
          </div>

          <div className="rounded-lg border border-border/70 bg-muted/20 p-4">
            <div className="flex flex-col gap-1 sm:flex-row sm:items-center sm:justify-between">
              <div className="flex items-center gap-2 text-sm font-medium">
                <SlidersHorizontal className="h-4 w-4 text-primary" />
                Personal ranking profile
              </div>
              <div className="text-xs text-muted-foreground">Tune scoring by freshness, quality, language and size.</div>
            </div>

            <div className="mt-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
              {([
                ['freshnessWeight', 'Freshness'] as const,
                ['seedersWeight', 'Seeders'] as const,
                ['qualityWeight', 'Quality'] as const,
                ['languageWeight', 'Language'] as const,
                ['sizeWeight', 'Size'] as const,
              ]).map(([field, label]) => (
                <div key={field} className="rounded-xl border border-border/70 bg-background/40 p-3">
                  <div className="flex items-center justify-between text-sm">
                    <span className="font-medium">{label}</span>
                    <span className="font-mono text-xs text-muted-foreground">{profile[field].toFixed(2)}</span>
                  </div>
                  <input
                    type="range"
                    min={0}
                    max={5}
                    step={0.25}
                    value={profile[field]}
                    onChange={(event) => updateWeight(field, Number(event.target.value))}
                    className="mt-2 w-full accent-[hsl(var(--primary))]"
                  />
                </div>
              ))}

              <div className="rounded-xl border border-border/70 bg-background/40 p-3">
                <div className="text-sm font-medium">Target size (GB)</div>
                <Input
                  type="number"
                  min={0}
                  max={100}
                  step={0.5}
                  value={targetSizeGB || ''}
                  onChange={(event) => {
                    const gb = Math.max(0, Number(event.target.value) || 0);
                    setProfile((prev) => ({ ...prev, targetSizeBytes: Math.round(gb * (1024 ** 3)) }));
                  }}
                  placeholder="Auto"
                  className="mt-2"
                />
              </div>

              <div className="rounded-xl border border-border/70 bg-background/40 p-3">
                <div className="text-sm font-medium">Preferred audio</div>
                <MultiSelect
                  label="Preferred audio"
                  value={(profile.preferredAudio ?? []).map((v) => String(v).trim().toUpperCase()).filter(Boolean)}
                  options={preferredAudioOptions}
                  onChange={(next) => setProfile((prev) => ({ ...prev, preferredAudio: next }))}
                  placeholder="Any"
                  className="mt-2"
                />
              </div>

              <div className="rounded-xl border border-border/70 bg-background/40 p-3">
                <div className="text-sm font-medium">Preferred subtitles</div>
                <MultiSelect
                  label="Preferred subtitles"
                  value={(profile.preferredSubtitles ?? []).map((v) => String(v).trim().toUpperCase()).filter(Boolean)}
                  options={preferredSubtitleOptions}
                  onChange={(next) => setProfile((prev) => ({ ...prev, preferredSubtitles: next }))}
                  placeholder="Any"
                  className="mt-2"
                />
              </div>
            </div>

            <div className="mt-4 flex flex-wrap gap-2">
              <label className="inline-flex items-center gap-2 rounded-full border border-border/70 bg-background px-3 py-2 text-sm">
                <input
                  type="checkbox"
                  checked={profile.preferSeries}
                  onChange={(event) => setProfile((prev) => ({ ...prev, preferSeries: event.target.checked }))}
                  className="h-4 w-4 accent-[hsl(var(--primary))]"
                />
                Prefer series
              </label>
              <label className="inline-flex items-center gap-2 rounded-full border border-border/70 bg-background px-3 py-2 text-sm">
                <input
                  type="checkbox"
                  checked={profile.preferMovies}
                  onChange={(event) => setProfile((prev) => ({ ...prev, preferMovies: event.target.checked }))}
                  className="h-4 w-4 accent-[hsl(var(--primary))]"
                />
                Prefer movies
              </label>
            </div>
          </div>

          {providerStatus.length > 0 ? (
            <div className="flex flex-wrap gap-2">
              {providerStatus.map((status) => {
                const name = (status.name ?? '').trim();
                const normalized = name.toLowerCase();
                const redOnZero = status.ok && status.count === 0 && redOnZeroProviders.has(normalized);
                const variant = !status.ok ? 'danger' : redOnZero ? 'danger' : 'success';
                return (
                  <Badge key={status.name} variant={variant} className="gap-2">
                    <span className={cn('font-mono text-[11px] uppercase tracking-wide', redOnZero ? 'text-destructive' : '')}>
                      {name}
                    </span>
                    {status.ok ? <span className="text-[11px] opacity-90">{`${status.count} items`}</span> : null}
                  </Badge>
                );
              })}
            </div>
          ) : null}
        </CardContent>
      </Card>

      <Card className="flex min-w-0 flex-col overflow-hidden">
        <CardHeader className="shrink-0 pb-3">
          <div className="space-y-2">
            {/* Row 1: Title + stats | Sort + Filter toggle */}
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div className="flex items-baseline gap-2">
                <CardTitle className="shrink-0">Results</CardTitle>
                <span className="text-sm text-muted-foreground">
                  {activeQuery
                    ? `${filteredItems.length}/${items.length} · ${elapsedMs} ms`
                    : 'Run search to see results'}
                </span>
                {phaseMessage ? <span className="text-xs text-muted-foreground">{phaseMessage}</span> : null}
                {streamActive ? <span className="text-xs text-muted-foreground">(streaming)</span> : null}
              </div>

              <div className="flex items-center gap-2">
                <Select
                  value={sortBy}
                  onChange={(e) => setSortBy(e.target.value as SearchSortBy)}
                  wrapperClassName="w-[8.5rem]"
                  aria-label="Sort by"
                  title="Sort by"
                >
                  {sortOptions.map((o) => (
                    <option key={o.value} value={o.value}>{o.label}</option>
                  ))}
                </Select>
                <Select
                  value={sortOrder}
                  onChange={(e) => setSortOrder(e.target.value as SearchSortOrder)}
                  wrapperClassName="w-[5.5rem]"
                  aria-label="Order"
                  title="Order"
                >
                  {sortOrderOptions.map((o) => (
                    <option key={o.value} value={o.value}>{o.label}</option>
                  ))}
                </Select>
                <Input
                  type="number"
                  min={10}
                  max={100}
                  step={10}
                  value={limit}
                  onChange={(event) => setLimit(Math.max(10, Math.min(100, Number(event.target.value) || 30)))}
                  className="h-10 w-[5rem]"
                  aria-label="Limit"
                  title="Limit"
                />
                <Button
                  variant={hasActiveFilters ? 'default' : 'outline'}
                  size="sm"
                  className="h-10 gap-1.5"
                  disabled={items.length === 0}
                  onClick={() => setFiltersOpen((prev) => !prev)}
                >
                  <Filter className="h-4 w-4" />
                  Filters
                  {activeFilterCount > 0 ? (
                    <span className="inline-flex h-5 min-w-5 items-center justify-center rounded-full bg-primary-foreground/20 px-1.5 text-[11px] font-bold">
                      {activeFilterCount}
                    </span>
                  ) : null}
                  <ChevronDown className={cn('h-3.5 w-3.5 transition-transform', filtersOpen && 'rotate-180')} />
                </Button>
              </div>
            </div>

            <div className="flex flex-wrap items-center gap-2">
              <Select
                value={selectedFilterPresetId}
                onChange={(e) => handleApplyFilterPreset(e.target.value)}
                wrapperClassName="w-[14rem]"
                aria-label="Filter preset"
              >
                <option value="">Filter presets</option>
                {savedFilterPresets.map((preset) => (
                  <option key={preset.id} value={preset.id}>
                    {preset.name}
                  </option>
                ))}
              </Select>
              <Button
                variant="outline"
                size="sm"
                className="h-9"
                onClick={handleSaveFilterPreset}
                disabled={!hasActiveFilters}
              >
                <BookmarkPlus className="h-3.5 w-3.5" />
                Save preset
              </Button>
              <Button
                variant="outline"
                size="sm"
                className="h-9"
                onClick={handleDeleteFilterPreset}
                disabled={!selectedFilterPresetId}
              >
                <Trash2 className="h-3.5 w-3.5" />
                Remove
              </Button>
              {selectedFilterPresetId ? (
                <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
                  <BookmarkCheck className="h-3.5 w-3.5" />
                  preset active
                </span>
              ) : null}
            </div>

            {/* Active filter chips (always visible when filters are set) */}
            {!filtersOpen && activeFilterBadges.length > 0 ? (
              <div className="flex flex-wrap items-center gap-1.5">
                {activeFilterBadges.map((badge) => (
                  <span
                    key={badge.key}
                    className="inline-flex items-center gap-1 rounded-full border border-primary/20 bg-primary/5 px-2.5 py-0.5 text-xs font-medium text-primary"
                  >
                    {badge.label}
                    <button
                      type="button"
                      className="ml-0.5 inline-flex h-3.5 w-3.5 items-center justify-center rounded-full hover:bg-primary/20"
                      onClick={() => clearFilterBadge(badge.key)}
                    >
                      <X className="h-2.5 w-2.5" />
                    </button>
                  </span>
                ))}
                <button
                  type="button"
                  className="text-xs text-muted-foreground hover:text-foreground"
                  onClick={() => setResultFilters(defaultResultFilters)}
                >
                  Clear all
                </button>
              </div>
            ) : null}

            {/* Inline expandable filters */}
            {filtersOpen ? (
              <div className="rounded-xl border border-border/70 bg-muted/10 p-4">
                <div className="grid gap-4 sm:grid-cols-[1fr_1fr_1fr]">
                  {/* Block: Content */}
                  <fieldset className="space-y-2">
                    <legend className="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">Content</legend>
                    <MultiSelect
                      label="Source"
                      value={resultFilters.sources}
                      options={sourceFacetOptions}
                      onChange={(next) => setResultFilters((prev) => ({ ...prev, sources: next }))}
                      placeholder="All sources"
                    />
                    <MultiSelect
                      label="Quality"
                      value={resultFilters.qualities}
                      options={qualityFacetOptions}
                      onChange={(next) => setResultFilters((prev) => ({ ...prev, qualities: next }))}
                      placeholder="All quality"
                    />
                  </fieldset>

                  {/* Block: Language */}
                  <fieldset className="space-y-2">
                    <legend className="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">Language</legend>
                    <MultiSelect
                      label="Audio"
                      value={resultFilters.audio}
                      options={audioFacetOptions}
                      onChange={(next) => setResultFilters((prev) => ({ ...prev, audio: next }))}
                      placeholder="Any audio"
                    />
                    <MultiSelect
                      label="Subtitles"
                      value={resultFilters.subtitles}
                      options={subtitleFacetOptions}
                      onChange={(next) => setResultFilters((prev) => ({ ...prev, subtitles: next }))}
                      placeholder="Any subs"
                    />
                  </fieldset>

                  {/* Block: Limits */}
                  <fieldset className="space-y-2">
                    <legend className="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">Limits</legend>
                    <div className="grid grid-cols-2 gap-2">
                      <Input
                        inputMode="decimal"
                        placeholder="Min GB"
                        value={resultFilters.minSizeGB}
                        onChange={(e) => setResultFilters((prev) => ({ ...prev, minSizeGB: e.target.value }))}
                        aria-label="Min size (GB)"
                      />
                      <Input
                        inputMode="decimal"
                        placeholder="Max GB"
                        value={resultFilters.maxSizeGB}
                        onChange={(e) => setResultFilters((prev) => ({ ...prev, maxSizeGB: e.target.value }))}
                        aria-label="Max size (GB)"
                      />
                    </div>
                    <Input
                      inputMode="numeric"
                      placeholder="Min seeders"
                      value={resultFilters.minSeeders}
                      onChange={(e) => setResultFilters((prev) => ({ ...prev, minSeeders: e.target.value }))}
                      aria-label="Min seeders"
                    />
                    <Input
                      placeholder="Include keywords (comma)"
                      value={resultFilters.includeKeywords}
                      onChange={(e) => setResultFilters((prev) => ({ ...prev, includeKeywords: e.target.value }))}
                      aria-label="Include keywords"
                    />
                    <Input
                      placeholder="Exclude keywords (comma)"
                      value={resultFilters.excludeKeywords}
                      onChange={(e) => setResultFilters((prev) => ({ ...prev, excludeKeywords: e.target.value }))}
                      aria-label="Exclude keywords"
                    />
                    <label className="inline-flex items-center gap-2 rounded-md border border-border/70 bg-background/60 px-3 py-2 text-xs text-muted-foreground">
                      <input
                        type="checkbox"
                        checked={resultFilters.excludeCam}
                        onChange={(e) => setResultFilters((prev) => ({ ...prev, excludeCam: e.target.checked }))}
                        className="h-4 w-4 accent-[hsl(var(--primary))]"
                      />
                      Exclude CAM/TS
                    </label>
                  </fieldset>
                </div>

                {hasActiveFilters ? (
                  <div className="mt-3 flex items-center justify-between border-t border-border/50 pt-3">
                    <div className="flex flex-wrap gap-1.5">
                      {activeFilterBadges.map((badge) => (
                        <span
                          key={badge.key}
                          className="inline-flex items-center gap-1 rounded-full border border-primary/20 bg-primary/5 px-2 py-0.5 text-[11px] font-medium text-primary"
                        >
                          {badge.label}
                          <button
                            type="button"
                            className="ml-0.5 inline-flex h-3.5 w-3.5 items-center justify-center rounded-full hover:bg-primary/20"
                            onClick={() => clearFilterBadge(badge.key)}
                          >
                            <X className="h-2.5 w-2.5" />
                          </button>
                        </span>
                      ))}
                    </div>
                    <Button
                      variant="ghost"
                      size="sm"
                      className="h-7 px-2 text-xs"
                      onClick={() => setResultFilters(defaultResultFilters)}
                    >
                      Clear all
                    </Button>
                  </div>
                ) : null}
              </div>
            ) : null}
          </div>
        </CardHeader>

        <CardContent className="min-h-0 flex-1 space-y-3 overflow-y-auto">
          {filteredItems.length === 0 ? (
            <div className="rounded-lg border border-dashed border-border/70 bg-muted/10 p-6 text-center text-sm text-muted-foreground">
              {items.length === 0 ? 'No results yet.' : 'No results match the current filters.'}
            </div>
          ) : (
            <div className="grid gap-3 grid-cols-[repeat(auto-fill,minmax(320px,1fr))]">
              {filteredItems.map((item) => {
                const key = buildResultKey(item);
                const status = addState[key];
                const normalizedName = (item.name ?? '').trim().toLowerCase();
                const alreadyAdded = normalizedName.length > 0 && catalogNameSet.has(normalizedName);
                const effectiveStatus = alreadyAdded ? 'added' : status;
                const isAdding = effectiveStatus === 'adding';
                const isAdded = effectiveStatus === 'added';

                const meta = [
                  item.enrichment?.quality ? String(item.enrichment.quality) : null,
                  item.enrichment?.sourceType ? String(item.enrichment.sourceType) : null,
                  item.enrichment?.year ? String(item.enrichment.year) : null,
                  item.enrichment?.audioChannels ? String(item.enrichment.audioChannels) : null,
                  item.sizeBytes ? formatBytes(item.sizeBytes) : null,
                ].filter(Boolean) as string[];
                const signalBadges = [
                  item.enrichment?.hdr ? 'HDR' : null,
                  item.enrichment?.dolbyVision ? 'Dolby Vision' : null,
                ].filter(Boolean) as string[];
                const tmdbRating =
                  typeof item.enrichment?.tmdbRating === 'number' && Number.isFinite(item.enrichment.tmdbRating)
                    ? item.enrichment.tmdbRating
                    : null;

                return (
                  <article
                    key={key}
                    role="button"
                    tabIndex={0}
                    onClick={() => setDetailItem(item)}
                    onKeyDown={(event) => {
                      if (event.key === 'Enter' || event.key === ' ') {
                        event.preventDefault();
                        setDetailItem(item);
                      }
                    }}
                    className="group flex h-full cursor-pointer flex-col overflow-hidden rounded-lg border border-border/70 bg-card shadow-soft transition-colors hover:border-primary/30 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
                  >
                    <div className="flex items-center justify-between gap-1 border-b border-border/70 bg-muted/10 px-3 py-2">
                      <div className="flex min-w-0 flex-wrap gap-1">
                        {(item.sources?.length ? item.sources : [{ name: item.source || 'torrent' }]).map((src, idx) => (
                          <span
                            key={`${key}-src-${idx}`}
                            className="truncate rounded-sm bg-muted/40 px-1.5 py-0.5 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground"
                            title={src.name + (src.tracker ? ` (${src.tracker})` : '')}
                          >
                            {src.name}
                          </span>
                        ))}
                      </div>
                      {tmdbRating && tmdbRating > 0 ? (
                        <span className="rounded-full border border-border/70 bg-background px-2 py-0.5 text-[11px] font-semibold text-muted-foreground">
                          TMDB {tmdbRating.toFixed(1)}
                        </span>
                      ) : null}
                    </div>

                    <div className="flex min-h-0 flex-1 flex-col gap-2.5 p-3.5">
                      <div className="min-w-0">
                        <div className="truncate text-sm font-semibold leading-tight" title={item.name}>
                          {item.name}
                        </div>
                        {meta.length > 0 ? (
                          <div className="mt-1 truncate text-xs text-muted-foreground">
                            {meta.join(' · ')}
                          </div>
                        ) : null}
                      </div>

                      {signalBadges.length > 0 || (item.enrichment?.audio?.length ?? 0) > 0 || (item.enrichment?.subtitles?.length ?? 0) > 0 ? (
                        <div className="flex min-h-[1.5rem] flex-wrap gap-1.5">
                          {signalBadges.map((t) => (
                            <Badge key={`${key}-sig-${t}`} variant="outline" className="text-[10px]">
                              {t}
                            </Badge>
                          ))}
                          {item.enrichment?.audio?.slice(0, 3).map((audio) => (
                            <Badge key={`${key}-audio-${audio}`} variant="outline" className="text-[10px]">
                              A: {String(audio).trim().toUpperCase()}
                            </Badge>
                          ))}
                          {item.enrichment?.subtitles?.slice(0, 3).map((sub) => (
                            <Badge key={`${key}-sub-${sub}`} variant="outline" className="text-[10px]">
                              Sub: {String(sub).trim().toUpperCase()}
                            </Badge>
                          ))}
                        </div>
                      ) : null}

                      <div className="mt-auto flex items-center justify-between gap-2 border-t border-border/60 pt-2">
                        <div className="flex min-w-0 items-center gap-2 text-xs">
                          <span className="font-mono text-emerald-700 dark:text-emerald-300">S {item.seeders ?? 0}</span>
                          <span className="font-mono text-muted-foreground">P {item.leechers ?? 0}</span>
                        </div>
                        <Button
                          variant="outline"
                          size="sm"
                          disabled={!item.pageUrl}
                          onClick={(event) => {
                            event.stopPropagation();
                            if (!item.pageUrl) return;
                            window.open(item.pageUrl, '_blank', 'noopener,noreferrer');
                          }}
                          title={item.pageUrl ? 'Open on source site' : 'No source link available'}
                          className="h-8 px-2.5 text-xs"
                        >
                          <ExternalLink className="h-3.5 w-3.5" />
                          Open
                        </Button>
                        <Button
                          variant={isAdded ? 'secondary' : 'outline'}
                          disabled={!item.magnet || isAdding || isAdded}
                          onClick={(event) => {
                            event.stopPropagation();
                            void handleAddTorrent(item);
                          }}
                          className="h-8 px-2.5 text-xs"
                        >
                          {isAdding ? (
                            <Loader2 className="h-3.5 w-3.5 animate-spin" />
                          ) : isAdded ? (
                            <Check className="h-3.5 w-3.5" />
                          ) : (
                            <Plus className="h-3.5 w-3.5" />
                          )}
                          {isAdded ? 'Added' : 'Add'}
                        </Button>
                      </div>
                    </div>
                  </article>
                );
              })}
            </div>
          )}

          {hasMore ? (
            <div className="flex justify-center pt-2">
              <Button variant="outline" onClick={() => void handleLoadMore()} disabled={isLoadingMore}>
                {isLoadingMore ? <Loader2 className="h-4 w-4 animate-spin" /> : null}
                Load more
              </Button>
            </div>
          ) : null}

          <Dialog open={Boolean(detailItem)} onOpenChange={(open) => (!open ? setDetailItem(null) : null)}>
            <DialogContent className="max-w-4xl">
              {detailItem ? (
                <>
                  <DialogHeader>
                    <DialogTitle className="pr-10">{detailItem.name}</DialogTitle>
                    <DialogDescription>
                      {detailItem.enrichment?.tmdbOverview || detailItem.enrichment?.description || 'No overview available.'}
                    </DialogDescription>
                  </DialogHeader>

                  <DialogBody className="space-y-4">
                    <div className="space-y-3">
                      <div className="flex flex-wrap items-center gap-2">
                        {typeof detailItem.enrichment?.tmdbRating === 'number' && Number.isFinite(detailItem.enrichment.tmdbRating) ? (
                          <Badge>TMDB {detailItem.enrichment.tmdbRating.toFixed(1)}</Badge>
                        ) : null}
                        {detailItem.enrichment?.quality ? <Badge variant="outline">{detailItem.enrichment.quality}</Badge> : null}
                        {detailItem.enrichment?.sourceType ? <Badge variant="outline">{detailItem.enrichment.sourceType}</Badge> : null}
                        {detailItem.enrichment?.hdr ? <Badge variant="outline">HDR</Badge> : null}
                        {detailItem.enrichment?.dolbyVision ? <Badge variant="outline">Dolby Vision</Badge> : null}
                        {detailItem.enrichment?.audioChannels ? <Badge variant="outline">{detailItem.enrichment.audioChannels}</Badge> : null}
                      </div>

                      <div className="grid gap-2 rounded-xl border border-border/70 bg-muted/10 p-3 text-sm sm:grid-cols-2">
                        <div className="flex items-center justify-between gap-2">
                          <span className="text-muted-foreground">Size</span>
                          <span className="font-mono">{detailItem.sizeBytes ? formatBytes(detailItem.sizeBytes) : 'n/a'}</span>
                        </div>
                        <div className="flex items-center justify-between gap-2">
                          <span className="text-muted-foreground">Year</span>
                          <span>{detailItem.enrichment?.year ?? 'n/a'}</span>
                        </div>
                        <div className="flex items-center justify-between gap-2">
                          <span className="text-muted-foreground">Seeders</span>
                          <span className="font-mono text-emerald-700 dark:text-emerald-300">{detailItem.seeders ?? 0}</span>
                        </div>
                        <div className="flex items-center justify-between gap-2">
                          <span className="text-muted-foreground">Peers</span>
                          <span className="font-mono">{detailItem.leechers ?? 0}</span>
                        </div>
                        <div className="flex items-center justify-between gap-2 sm:col-span-2">
                          <span className="text-muted-foreground">Sources</span>
                          <div className="flex flex-wrap justify-end gap-1">
                            {(detailItem.sources?.length ? detailItem.sources : [{ name: detailItem.source || 'unknown', tracker: detailItem.tracker }]).map((src, idx) => (
                              <span
                                key={`detail-src-${idx}`}
                                className="rounded-sm bg-muted/40 px-1.5 py-0.5 text-xs font-medium"
                                title={src.tracker ? `${src.name} (${src.tracker})` : src.name}
                              >
                                {src.name}{src.tracker ? ` · ${src.tracker}` : ''}
                              </span>
                            ))}
                          </div>
                        </div>
                        <div className="flex items-center justify-between gap-2">
                          <span className="text-muted-foreground">Season/Episode</span>
                          <span>
                            {detailItem.enrichment?.season || detailItem.enrichment?.episode
                              ? `S${String(detailItem.enrichment?.season ?? 0).padStart(2, '0')} · E${String(detailItem.enrichment?.episode ?? 0).padStart(2, '0')}`
                              : 'n/a'}
                          </span>
                        </div>
                        <div className="flex items-center justify-between gap-2">
                          <span className="text-muted-foreground">Type</span>
                          <span>{detailItem.enrichment?.contentType || (detailItem.enrichment?.isSeries ? 'series' : 'movie')}</span>
                        </div>
                      </div>

                      {(detailItem.enrichment?.audio?.length ?? 0) > 0 ? (
                        <div className="space-y-1">
                          <div className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">Audio tracks</div>
                          <div className="flex flex-wrap gap-1.5">
                            {detailItem.enrichment?.audio?.map((audio) => (
                              <Badge key={`d-audio-${audio}`} variant="outline" className="text-[11px]">
                                {String(audio).trim().toUpperCase()}
                              </Badge>
                            ))}
                          </div>
                        </div>
                      ) : null}

                      {(detailItem.enrichment?.subtitles?.length ?? 0) > 0 ? (
                        <div className="space-y-1">
                          <div className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">Subtitles</div>
                          <div className="flex flex-wrap gap-1.5">
                            {detailItem.enrichment?.subtitles?.map((sub) => (
                              <Badge key={`d-sub-${sub}`} variant="outline" className="text-[11px]">
                                {String(sub).trim().toUpperCase()}
                              </Badge>
                            ))}
                          </div>
                        </div>
                      ) : null}

                      {detailItem.enrichment?.dubbing ? (
                        <div className="space-y-1">
                          <div className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">Dubbing</div>
                          <div className="rounded-lg border border-border/70 bg-muted/10 p-2 text-sm">
                            <div>Type: {detailItem.enrichment.dubbing.type || 'n/a'}</div>
                            <div>Group: {detailItem.enrichment.dubbing.group || 'n/a'}</div>
                            {detailItem.enrichment.dubbing.groups?.length ? (
                              <div>Groups: {detailItem.enrichment.dubbing.groups.join(', ')}</div>
                            ) : null}
                          </div>
                        </div>
                      ) : null}
                    </div>

                    {detailItem.enrichment?.nfo ? (
                      <div className="space-y-1">
                        <div className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">NFO</div>
                        <pre className="max-h-56 overflow-auto rounded-xl border border-border/70 bg-muted/10 p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap">
                          {detailItem.enrichment.nfo}
                        </pre>
                      </div>
                    ) : null}

                  </DialogBody>

                  <DialogFooter>
                    <Button
                      variant="outline"
                      disabled={!detailItem.pageUrl}
                      onClick={() => {
                        if (!detailItem.pageUrl) return;
                        window.open(detailItem.pageUrl, '_blank', 'noopener,noreferrer');
                      }}
                    >
                      <ExternalLink className="h-4 w-4" />
                      Open
                    </Button>
                    <Button
                      variant={detailIsAdded ? 'secondary' : 'default'}
                      disabled={!detailItem.magnet || detailIsAdding || detailIsAdded}
                      onClick={() => void handleAddTorrent(detailItem)}
                    >
                      {detailIsAdding ? <Loader2 className="h-4 w-4 animate-spin" /> : detailIsAdded ? <Check className="h-4 w-4" /> : <Plus className="h-4 w-4" />}
                      {detailIsAdded ? 'Added' : 'Add to catalog'}
                    </Button>
                  </DialogFooter>
                </>
              ) : null}
            </DialogContent>
          </Dialog>
        </CardContent>
      </Card>
    </div>
  );
};

export default SearchPage;
