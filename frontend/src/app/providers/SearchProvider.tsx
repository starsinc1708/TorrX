import React, { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import { useLocation } from 'react-router-dom';

import {
  createTorrentFromMagnet,
  isApiError,
  listSearchProviders,
  searchTorrents,
  searchTorrentsStream,
} from '../../api';
import { useCatalogMeta } from './CatalogMetaProvider';
import { useToast } from './ToastProvider';
import { onSearchProvidersChanged, resolveEnabledSearchProviders } from '../../searchProviderSettings';
import type {
  SearchProviderInfo,
  SearchProviderStatus,
  SearchRankingProfile,
  SearchResponse,
  SearchResultItem,
  SearchSortBy,
  SearchSortOrder,
} from '../../types';
import {
  type ResultFilters,
  type SavedFilterPreset,
  buildResultKey,
  defaultResultFilters,
  loadSavedFilterPresets,
  loadStoredProfile,
  phaseLabels,
  profileStorageKey,
  saveFilterPresets,
  simplifyProviderError,
} from '../../lib/search-utils';

type AddState = Record<string, 'adding' | 'added' | 'error'>;

type SearchContextValue = {
  // Query
  query: string;
  setQuery: (value: string) => void;
  activeQuery: string;

  // Results
  items: SearchResultItem[];
  totalItems: number;
  elapsedMs: number;
  hasMore: boolean;
  providerStatus: SearchProviderStatus[];

  // Stream lifecycle
  isLoading: boolean;
  isLoadingMore: boolean;
  streamActive: boolean;
  phaseMessage: string;

  // Providers
  providers: SearchProviderInfo[];
  enabledProviders: string[];
  setEnabledProviders: (value: string[]) => void;
  isLoadingProviders: boolean;

  // Profile
  profile: SearchRankingProfile;
  setProfile: React.Dispatch<React.SetStateAction<SearchRankingProfile>>;

  // Sort / limit
  sortBy: SearchSortBy;
  setSortBy: (value: SearchSortBy) => void;
  sortOrder: SearchSortOrder;
  setSortOrder: (value: SearchSortOrder) => void;
  limit: number;
  setLimit: (value: number) => void;

  // Filters
  resultFilters: ResultFilters;
  setResultFilters: React.Dispatch<React.SetStateAction<ResultFilters>>;
  filtersOpen: boolean;
  setFiltersOpen: React.Dispatch<React.SetStateAction<boolean>>;
  savedFilterPresets: SavedFilterPreset[];
  setSavedFilterPresets: React.Dispatch<React.SetStateAction<SavedFilterPreset[]>>;
  selectedFilterPresetId: string;
  setSelectedFilterPresetId: (value: string) => void;

  // Add state
  addState: AddState;

  // Actions
  submitSearch: (event: React.FormEvent) => void;
  forceRefresh: () => void;
  loadMore: () => void;
  stopStream: () => void;
  addTorrent: (item: SearchResultItem) => void;
};

const SearchContext = createContext<SearchContextValue | null>(null);

export function SearchProvider({ children }: { children: React.ReactNode }) {
  const { items: catalogItems } = useCatalogMeta();
  const { toast } = useToast();
  const location = useLocation();

  // ---- State ----
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
  const [addState, setAddState] = useState<AddState>({});
  const [resultFilters, setResultFilters] = useState<ResultFilters>(defaultResultFilters);
  const [filtersOpen, setFiltersOpen] = useState(false);
  const [savedFilterPresets, setSavedFilterPresets] = useState<SavedFilterPreset[]>(() => loadSavedFilterPresets());
  const [selectedFilterPresetId, setSelectedFilterPresetId] = useState('');

  // ---- Refs ----
  const streamCleanupRef = useRef<(() => void) | null>(null);
  const streamTokenRef = useRef(0);
  const lastPhaseRef = useRef<string>('');
  const providerErrorToastRef = useRef<Map<string, string>>(new Map());
  const sortByRef = useRef(sortBy);
  sortByRef.current = sortBy;
  const sortOrderRef = useRef(sortOrder);
  sortOrderRef.current = sortOrder;

  // ---- Catalog name set (for dedup "already added") ----
  const catalogNameSet = useMemo(() => {
    const set = new Set<string>();
    for (const t of catalogItems) {
      const name = (t.name ?? '').trim().toLowerCase();
      if (name) set.add(name);
    }
    return set;
  }, [catalogItems]);

  // ---- Persist profile / presets ----
  useEffect(() => {
    window.localStorage.setItem(profileStorageKey, JSON.stringify(profile));
  }, [profile]);

  useEffect(() => {
    saveFilterPresets(savedFilterPresets);
  }, [savedFilterPresets]);

  // ---- Error toast ----
  useEffect(() => {
    if (!errorMessage) return;
    toast({ title: 'Search', description: errorMessage, variant: 'danger' });
    setErrorMessage(null);
  }, [errorMessage, toast]);

  // ---- Provider error toasts ----
  useEffect(() => {
    if (providerStatus.length === 0) return;
    for (const status of providerStatus) {
      if (status.ok) continue;
      const name = String(status.name ?? '').trim();
      if (!name) continue;
      const key = name.toLowerCase();
      const raw = String(status.error ?? 'failed');
      const prev = providerErrorToastRef.current.get(key);
      if (prev === raw) continue;
      providerErrorToastRef.current.set(key, raw);
      toast({
        title: name,
        description: simplifyProviderError(raw),
        variant: 'danger',
      });
    }
  }, [providerStatus, toast]);

  // ---- Load providers ----
  const stopStream = useCallback(() => {
    if (streamCleanupRef.current) {
      streamCleanupRef.current();
      streamCleanupRef.current = null;
    }
    setStreamActive(false);
  }, []);

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
    const interval = setInterval(() => {
      void listSearchProviders()
        .then((list) => {
          if (cancelled) return;
          setProviders((prev) => {
            const changed =
              prev.length !== list.length ||
              prev.some((p, i) => p.name !== list[i]?.name || p.enabled !== list[i]?.enabled);
            if (!changed) return prev;
            const configured = list.filter((p) => p.enabled).map((p) => p.name);
            setEnabledProviders(resolveEnabledSearchProviders(configured));
            return list;
          });
        })
        .catch(() => {});
    }, 30_000);
    return () => {
      cancelled = true;
      clearInterval(interval);
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

  // ---- Apply response ----
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
      setItems(safeItems);
    }
  }, []);

  // ---- Run search ----
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
      const isOnSearchPage = () => window.location.pathname === '/discover';

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
              // Toast when user is NOT on search page
              if (!isOnSearchPage() && providerName) {
                const count = Array.isArray(response.items) ? response.items.length : 0;
                toast({
                  title: 'Search',
                  description: `${providerName}: ${count} results`,
                  variant: 'default',
                });
              }
              return;
            }
            // Legacy: fast/full phases
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
    [activeQuery, enabledProviders, limit, profile, stopStream, applyResponse, toast],
  );

  // ---- Submit search ----
  const submitSearch = useCallback(
    (event: React.FormEvent) => {
      event.preventDefault();
      const normalized = query.trim();
      if (!normalized) {
        setErrorMessage('Enter search query');
        return;
      }
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
      void runSearch(0, false);
    },
    [activeQuery, query, runSearch, stopStream],
  );

  // ---- Force refresh ----
  const forceRefresh = useCallback(() => {
    if (!activeQuery.trim()) return;
    stopStream();
    providerErrorToastRef.current.clear();
    setItems([]);
    setProviderStatus([]);
    setTotalItems(0);
    setHasMore(false);
    setElapsedMs(0);
    setPhaseMessage('');
    setStreamActive(false);
    void runSearch(0, false, true);
  }, [activeQuery, runSearch, stopStream]);

  // ---- Trigger search when activeQuery changes ----
  useEffect(() => {
    if (!activeQuery.trim()) return;
    void runSearch(0, false);
  }, [activeQuery, runSearch]);

  // ---- Load more ----
  const loadMore = useCallback(() => {
    void runSearch(items.length, true);
  }, [items.length, runSearch]);

  // ---- Add torrent ----
  const addTorrent = useCallback(
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

  // ---- Context value ----
  const value = useMemo<SearchContextValue>(
    () => ({
      query,
      setQuery,
      activeQuery,
      items,
      totalItems,
      elapsedMs,
      hasMore,
      providerStatus,
      isLoading,
      isLoadingMore,
      streamActive,
      phaseMessage,
      providers,
      enabledProviders,
      setEnabledProviders,
      isLoadingProviders,
      profile,
      setProfile,
      sortBy,
      setSortBy,
      sortOrder,
      setSortOrder,
      limit,
      setLimit,
      resultFilters,
      setResultFilters,
      filtersOpen,
      setFiltersOpen,
      savedFilterPresets,
      setSavedFilterPresets,
      selectedFilterPresetId,
      setSelectedFilterPresetId,
      addState,
      submitSearch,
      forceRefresh,
      loadMore,
      stopStream,
      addTorrent,
    }),
    [
      query,
      activeQuery,
      items,
      totalItems,
      elapsedMs,
      hasMore,
      providerStatus,
      isLoading,
      isLoadingMore,
      streamActive,
      phaseMessage,
      providers,
      enabledProviders,
      isLoadingProviders,
      profile,
      sortBy,
      sortOrder,
      limit,
      resultFilters,
      filtersOpen,
      savedFilterPresets,
      selectedFilterPresetId,
      addState,
      submitSearch,
      forceRefresh,
      loadMore,
      stopStream,
      addTorrent,
    ],
  );

  return <SearchContext.Provider value={value}>{children}</SearchContext.Provider>;
}

export function useSearch() {
  const ctx = useContext(SearchContext);
  if (!ctx) throw new Error('useSearch must be used within SearchProvider');
  return ctx;
}
