import React, { useCallback, useMemo, useState } from 'react';
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
import { useSearch } from '../app/providers/SearchProvider';
import { useToast } from '../app/providers/ToastProvider';
import { Badge } from '../components/ui/badge';
import { Button } from '../components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '../components/ui/card';
import { Dialog, DialogBody, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '../components/ui/dialog';
import { Input } from '../components/ui/input';
import { MultiSelect } from '../components/ui/multi-select';
import { Select } from '../components/ui/select';
import { cn } from '../lib/cn';
import {
  type ResultFilters,
  buildResultKey,
  defaultResultFilters,
  sortOptions,
  sortOrderOptions,
  tokenizeKeywords,
} from '../lib/search-utils';
import { saveEnabledSearchProviders } from '../searchProviderSettings';
import type {
  SearchResultItem,
  SearchSortBy,
  SearchSortOrder,
} from '../types';
import { formatBytes } from '../utils';

const SearchPage: React.FC = () => {
  const search = useSearch();
  const { toast } = useToast();

  const [detailItem, setDetailItem] = useState<SearchResultItem | null>(null);

  const {
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
    addTorrent,
  } = search;

  const enabledProvidersSet = useMemo(() => new Set(enabledProviders), [enabledProviders]);

  const toggleProvider = useCallback(
    (providerName: string) => {
      const name = providerName.toLowerCase();
      const next = enabledProvidersSet.has(name)
        ? enabledProviders.filter((p) => p !== name)
        : [...enabledProviders, name];
      setEnabledProviders(next);
      saveEnabledSearchProviders(next);
    },
    [enabledProviders, enabledProvidersSet, setEnabledProviders],
  );

  const catalogNameSet = useMemo(() => {
    // Re-derive from search context's catalog awareness via addState
    // (the addTorrent function in SearchProvider already checks catalog)
    return new Set<string>();
  }, []);

  const updateWeight = useCallback(
    (
      field: 'freshnessWeight' | 'seedersWeight' | 'qualityWeight' | 'languageWeight' | 'sizeWeight',
      value: number,
    ) => {
      setProfile((prev) => ({ ...prev, [field]: value }));
    },
    [setProfile],
  );

  const targetSizeGB = profile.targetSizeBytes > 0 ? profile.targetSizeBytes / (1024 ** 3) : 0;

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

  const contentTypeFacetOptions = useMemo(() => {
    const set = new Set<string>();
    for (const item of items) {
      const ct = String(item.enrichment?.contentType ?? '').trim().toLowerCase();
      if (ct) set.add(ct);
      else if (item.enrichment?.isSeries) set.add('series');
    }
    return Array.from(set)
      .sort((a, b) => a.localeCompare(b))
      .map((v) => ({ value: v, label: v.charAt(0).toUpperCase() + v.slice(1) }));
  }, [items]);

  const dubbingTypeFacetOptions = useMemo(() => {
    const set = new Set<string>();
    for (const item of items) {
      const dt = String(item.enrichment?.dubbing?.type ?? '').trim();
      if (dt) set.add(dt);
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
    const contentTypes = new Set(resultFilters.contentTypes);
    const dubbingTypes = new Set(resultFilters.dubbingTypes);

    const yearMinVal = Number(resultFilters.yearMin);
    const yearMaxVal = Number(resultFilters.yearMax);
    const hasYearMin = Number.isFinite(yearMinVal) && yearMinVal > 0;
    const hasYearMax = Number.isFinite(yearMaxVal) && yearMaxVal > 0;

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

      if (contentTypes.size > 0) {
        const ct = String(item.enrichment?.contentType ?? '').trim().toLowerCase()
          || (item.enrichment?.isSeries ? 'series' : '');
        if (!ct || !contentTypes.has(ct)) return false;
      }

      if (dubbingTypes.size > 0) {
        const dt = String(item.enrichment?.dubbing?.type ?? '').trim();
        if (!dt || !dubbingTypes.has(dt)) return false;
      }

      if (hasYearMin || hasYearMax) {
        const year = Number(item.enrichment?.year ?? 0);
        if (!Number.isFinite(year) || year <= 0) return false;
        if (hasYearMin && year < yearMinVal) return false;
        if (hasYearMax && year > yearMaxVal) return false;
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
    if (resultFilters.contentTypes.length > 0) return true;
    if (resultFilters.dubbingTypes.length > 0) return true;
    if (String(resultFilters.yearMin).trim()) return true;
    if (String(resultFilters.yearMax).trim()) return true;
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
    if (resultFilters.contentTypes.length > 0) count += 1;
    if (resultFilters.dubbingTypes.length > 0) count += 1;
    if (String(resultFilters.yearMin).trim() || String(resultFilters.yearMax).trim()) count += 1;
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
    summarize('Type', resultFilters.contentTypes);
    summarize('Dubbing', resultFilters.dubbingTypes);

    const yearMin = String(resultFilters.yearMin).trim();
    const yearMax = String(resultFilters.yearMax).trim();
    if (yearMin || yearMax) {
      badges.push({ key: 'Year', label: `Year: ${yearMin || '...'}-${yearMax || '...'}` });
    }

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
  const detailStatus = detailItem ? addState[detailKey] : undefined;
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
      Type: 'contentTypes',
      Dubbing: 'dubbingTypes',
    };
    const filterKey = keyMap[badgeKey];
    if (filterKey) {
      setResultFilters((prev) => ({ ...prev, [filterKey]: [] }));
      return;
    }
    if (badgeKey === 'Year') {
      setResultFilters((prev) => ({ ...prev, yearMin: '', yearMax: '' }));
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
  }, [setResultFilters]);

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
      const next = {
        id: `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
        name,
        filters: { ...resultFilters },
        updatedAt: new Date().toISOString(),
      };
      return [next, ...prev].slice(0, 20);
    });
    toast({ title: 'Filters', description: `Preset "${name}" saved.`, variant: 'success' });
  }, [hasActiveFilters, resultFilters, savedFilterPresets.length, selectedFilterPresetId, toast, setSavedFilterPresets]);

  const handleApplyFilterPreset = useCallback(
    (presetId: string) => {
      setSelectedFilterPresetId(presetId);
      if (!presetId) return;
      const preset = savedFilterPresets.find((item) => item.id === presetId);
      if (!preset) return;
      setResultFilters({ ...defaultResultFilters, ...preset.filters });
      toast({ title: 'Filters', description: `Applied preset "${preset.name}".` });
    },
    [savedFilterPresets, toast, setSelectedFilterPresetId, setResultFilters],
  );

  const handleDeleteFilterPreset = useCallback(() => {
    if (!selectedFilterPresetId) return;
    const preset = savedFilterPresets.find((item) => item.id === selectedFilterPresetId);
    setSavedFilterPresets((prev) => prev.filter((item) => item.id !== selectedFilterPresetId));
    setSelectedFilterPresetId('');
    if (preset) {
      toast({ title: 'Filters', description: `Preset "${preset.name}" removed.` });
    }
  }, [savedFilterPresets, selectedFilterPresetId, toast, setSavedFilterPresets, setSelectedFilterPresetId]);

  const handleAddTorrent = useCallback(
    (item: SearchResultItem) => {
      void addTorrent(item);
    },
    [addTorrent],
  );

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
          <form className="space-y-3" onSubmit={submitSearch}>
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
                  onClick={forceRefresh}
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
                <button
                  key={provider.name}
                  type="button"
                  onClick={() => toggleProvider(provider.name)}
                  className={cn(
                    'inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-medium transition-colors',
                    enabled
                      ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300'
                      : 'border-border/70 bg-muted/20 text-muted-foreground opacity-60',
                  )}
                  title={`${enabled ? 'Disable' : 'Enable'} ${provider.name}`}
                >
                  <span className={cn('h-2 w-2 rounded-full', enabled ? 'bg-emerald-500' : 'bg-muted-foreground/40')} />
                  {provider.label}
                </button>
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
                <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
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

                  {/* Block: Type & Dubbing */}
                  <fieldset className="space-y-2">
                    <legend className="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">Type &amp; Year</legend>
                    <MultiSelect
                      label="Content type"
                      value={resultFilters.contentTypes}
                      options={contentTypeFacetOptions}
                      onChange={(next) => setResultFilters((prev) => ({ ...prev, contentTypes: next }))}
                      placeholder="Any type"
                    />
                    <MultiSelect
                      label="Dubbing"
                      value={resultFilters.dubbingTypes}
                      options={dubbingTypeFacetOptions}
                      onChange={(next) => setResultFilters((prev) => ({ ...prev, dubbingTypes: next }))}
                      placeholder="Any dubbing"
                    />
                    <div className="grid grid-cols-2 gap-2">
                      <Input
                        inputMode="numeric"
                        placeholder="Year from"
                        value={resultFilters.yearMin}
                        onChange={(e) => setResultFilters((prev) => ({ ...prev, yearMin: e.target.value }))}
                        aria-label="Year from"
                      />
                      <Input
                        inputMode="numeric"
                        placeholder="Year to"
                        value={resultFilters.yearMax}
                        onChange={(e) => setResultFilters((prev) => ({ ...prev, yearMax: e.target.value }))}
                        aria-label="Year to"
                      />
                    </div>
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
                const isAdding = status === 'adding';
                const isAdded = status === 'added';

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

                    <div className="flex min-h-0 flex-1 gap-3 p-3.5">
                      {item.enrichment?.tmdbPoster ? (
                        <img
                          src={item.enrichment.tmdbPoster}
                          alt=""
                          loading="lazy"
                          className="h-[72px] w-[48px] shrink-0 rounded object-cover"
                        />
                      ) : null}
                      <div className="flex min-w-0 flex-1 flex-col gap-2.5">
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
                            handleAddTorrent(item);
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
                    </div>
                  </article>
                );
              })}
            </div>
          )}

          {hasMore ? (
            <div className="flex justify-center pt-2">
              <Button variant="outline" onClick={loadMore} disabled={isLoadingMore}>
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
                    <div className="flex gap-4">
                      {detailItem.enrichment?.tmdbPoster ? (
                        <img
                          src={detailItem.enrichment.tmdbPoster}
                          alt={detailItem.name}
                          className="h-[180px] w-[120px] shrink-0 rounded-lg object-cover shadow-sm"
                        />
                      ) : null}
                    <div className="min-w-0 flex-1 space-y-3">
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
                      onClick={() => handleAddTorrent(detailItem)}
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
