import React, { useMemo, useState } from 'react';
import {
  ArrowUpDown,
  Clock3,
  Focus,
  MonitorPlay,
  Play,
  Search,
  Square,
  Trash2,
} from 'lucide-react';
import type {
  SessionState,
  SortOrder,
  TorrentRecord,
  TorrentSortBy,
  TorrentStatusFilter,
  WatchPosition,
} from '../types';
import { cn } from '../lib/cn';
import { formatBytes, formatDate, formatPercent, formatSpeed, formatTime, normalizeProgress } from '../utils';
import PieceBar from './PieceBar';
import TagInput from './TagInput';
import { Badge } from './ui/badge';
import { Button } from './ui/button';
import { Card, CardContent, CardHeader, CardTitle } from './ui/card';
import { Input } from './ui/input';
import { Select } from './ui/select';
import { Switch } from './ui/switch';

const statusToBadgeVariant = (status: string): React.ComponentProps<typeof Badge>['variant'] => {
  if (status === 'active') return 'success';
  if (status === 'completed') return 'secondary';
  if (status === 'stopped') return 'outline';
  return 'default';
};

const statusFilterOptions: Array<{ value: TorrentStatusFilter; label: string }> = [
  { value: 'all', label: 'All' },
  { value: 'active', label: 'Active' },
  { value: 'completed', label: 'Completed' },
  { value: 'stopped', label: 'Stopped' },
];

const sortByOptions: Array<{ value: TorrentSortBy; label: string }> = [
  { value: 'updatedAt', label: 'Updated' },
  { value: 'createdAt', label: 'Created' },
  { value: 'name', label: 'Name' },
  { value: 'totalBytes', label: 'Size' },
  { value: 'progress', label: 'Progress' },
];

const sortOrderOptions: Array<{ value: SortOrder; label: string }> = [
  { value: 'desc', label: 'Desc' },
  { value: 'asc', label: 'Asc' },
];

const formatEpisodeCode = (season?: number, episode?: number): string | null => {
  if (!Number.isFinite(season) || !Number.isFinite(episode)) return null;
  const seasonNumber = Math.trunc(season ?? 0);
  const episodeNumber = Math.trunc(episode ?? 0);
  if (seasonNumber <= 0 || episodeNumber <= 0) return null;
  return `S${String(seasonNumber).padStart(2, '0')}E${String(episodeNumber).padStart(2, '0')}`;
};

interface TorrentListProps {
  torrents: TorrentRecord[];
  activeStateMap: Map<string, SessionState>;
  watchHistoryByTorrent: Map<string, WatchPosition[]>;
  currentTorrentId: string | null;
  prioritizeActiveFileOnly: boolean;
  allTags?: string[];
  statusFilter: TorrentStatusFilter;
  searchQuery: string;
  tagsQuery: string;
  sortBy: TorrentSortBy;
  sortOrder: SortOrder;
  selectedBulkIds: string[];
  onSelect: (id: string) => void;
  onWatch: (id: string) => void;
  onResumeWatch: (
    torrentId: string,
    fileIndex: number,
    position: number,
    duration: number,
    torrentName: string,
    filePath: string,
  ) => void;
  onStart: (id: string) => void;
  onStop: (id: string) => void;
  onBulkStart: (ids: string[]) => Promise<boolean>;
  onBulkStop: (ids: string[]) => Promise<boolean>;
  onBulkDelete: (ids: string[], removeFiles: boolean) => Promise<boolean>;
  onToggleBulkSelect: (id: string) => void;
  onSetBulkSelection: (ids: string[]) => void;
  onClearBulkSelection: () => void;
  onSetCurrent: (id: string) => void;
  onOpenDetails: (id: string) => void;
  onFilterChange: (filter: TorrentStatusFilter) => void;
  onSearchChange: (value: string) => void;
  onTagsChange: (value: string) => void;
  onSortByChange: (value: TorrentSortBy) => void;
  onSortOrderChange: (value: SortOrder) => void;
  onClearFilters: () => void;
}

const TorrentList: React.FC<TorrentListProps> = ({
  torrents,
  activeStateMap,
  watchHistoryByTorrent,
  currentTorrentId,
  prioritizeActiveFileOnly,
  allTags = [],
  statusFilter,
  searchQuery,
  tagsQuery,
  sortBy,
  sortOrder,
  selectedBulkIds,
  onSelect,
  onWatch,
  onResumeWatch,
  onStart,
  onStop,
  onBulkStart,
  onBulkStop,
  onBulkDelete,
  onToggleBulkSelect,
  onSetBulkSelection,
  onClearBulkSelection,
  onSetCurrent,
  onOpenDetails,
  onFilterChange,
  onSearchChange,
  onTagsChange,
  onSortByChange,
  onSortOrderChange,
  onClearFilters,
}) => {
  const [bulkRunning, setBulkRunning] = useState(false);
  const [bulkDeleteFiles, setBulkDeleteFiles] = useState(true);

  const currentTorrent =
    currentTorrentId !== null ? torrents.find((torrent) => torrent.id === currentTorrentId) ?? null : null;
  const regularTorrents = torrents.filter((torrent) => torrent.id !== currentTorrentId);

  const selectedSet = useMemo(() => new Set(selectedBulkIds), [selectedBulkIds]);
  const visibleIDs = useMemo(() => torrents.map((torrent) => torrent.id), [torrents]);
  const visibleSelectedCount = useMemo(
    () => visibleIDs.filter((id) => selectedSet.has(id)).length,
    [visibleIDs, selectedSet],
  );
  const allVisibleSelected = visibleIDs.length > 0 && visibleSelectedCount === visibleIDs.length;
  const hasActiveFilters =
    searchQuery.trim().length > 0 ||
    tagsQuery.trim().length > 0 ||
    statusFilter !== 'all' ||
    sortBy !== 'updatedAt' ||
    sortOrder !== 'desc';

  const runBulk = async (action: () => Promise<boolean>) => {
    if (bulkRunning) return;
    setBulkRunning(true);
    try {
      const ok = await action();
      if (ok) onClearBulkSelection();
    } finally {
      setBulkRunning(false);
    }
  };

  const toggleSelectVisible = () => {
    if (allVisibleSelected) {
      onSetBulkSelection(selectedBulkIds.filter((id) => !visibleIDs.includes(id)));
      return;
    }
    onSetBulkSelection(Array.from(new Set([...selectedBulkIds, ...visibleIDs])));
  };

  const renderTile = (torrent: TorrentRecord, options?: { currentPriority?: boolean }) => {
    const isCurrentPriority = options?.currentPriority ?? false;
    const state = activeStateMap.get(torrent.id);
    const progress = state?.progress ?? normalizeProgress(torrent);
    const doneBytes = torrent.doneBytes;
    const status = state?.status ?? torrent.status;
    const transferPhase = state?.transferPhase;
    const verificationProgress = Math.max(0, Math.min(1, state?.verificationProgress ?? 0));

    const totalBytes = torrent.totalBytes ?? 0;
    const fileCount = torrent.files?.length ?? 0;
    const tags = (torrent.tags ?? []).filter((tag) => tag.trim().length > 0);
    const isChecked = selectedSet.has(torrent.id);
    const watchEntries = watchHistoryByTorrent.get(torrent.id) ?? [];
    const latestWatch = watchEntries[0] ?? null;
    const watchEntryChips = watchEntries.slice(0, 3);
    const episodeCodeByFileIndex = new Map<number, string>();
    for (const group of torrent.mediaOrganization?.groups ?? []) {
      if (group.type !== 'series') continue;
      for (const item of group.items ?? []) {
        const code = formatEpisodeCode(item.season, item.episode);
        if (!code) continue;
        episodeCodeByFileIndex.set(item.fileIndex, code);
      }
    }

    return (
      <Card
        key={torrent.id}
        className={cn(
          'p-4 transition-colors',
          isCurrentPriority ? 'border-primary/40 bg-primary/5' : 'hover:bg-muted/30',
        )}
        role="button"
        tabIndex={0}
        onClick={() => onSelect(torrent.id)}
        onKeyDown={(event) => {
          if (event.key === 'Enter' || event.key === ' ') {
            event.preventDefault();
            onSelect(torrent.id);
          }
        }}
      >
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="flex items-start gap-3">
              <label
                className="mt-0.5 inline-flex items-center"
                onClick={(e) => e.stopPropagation()}
              >
                <input
                  type="checkbox"
                  checked={isChecked}
                  onChange={() => onToggleBulkSelect(torrent.id)}
                  className="h-4 w-4 accent-[hsl(var(--primary))]"
                />
              </label>
              <div className="min-w-0">
                <div className="truncate text-sm font-semibold">
                  {torrent.name ?? torrent.id}
                </div>
                <div className="mt-1 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                  <span>{formatBytes(doneBytes)} / {formatBytes(totalBytes)}</span>
                  <span aria-hidden="true">{'\u00B7'}</span>
                  <span className="font-medium text-foreground">{formatPercent(progress)}</span>
                  <span aria-hidden="true">{'\u00B7'}</span>
                  <span>Files {fileCount}</span>
                </div>
              </div>
            </div>

            {tags.length > 0 ? (
              <div className="mt-3 flex flex-wrap gap-2" onClick={(e) => e.stopPropagation()}>
                {tags.map((tag) => (
                  <span
                    key={`${torrent.id}-${tag}`}
                    className="rounded-full border border-border/70 bg-muted/20 px-2 py-0.5 text-xs text-muted-foreground"
                  >
                    #{tag}
                  </span>
                ))}
              </div>
            ) : null}

            {watchEntryChips.length > 0 ? (
              <div className="mt-3 rounded-lg border border-border/70 bg-muted/15 p-2.5" onClick={(e) => e.stopPropagation()}>
                <div className="mb-1.5 flex items-center gap-1.5 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
                  <Clock3 className="h-3.5 w-3.5" />
                  Continue watching
                </div>
                <div className="flex flex-wrap gap-1.5">
                  {watchEntryChips.map((entry) => {
                    const ratio = entry.duration > 0 ? Math.max(0, Math.min(1, entry.position / entry.duration)) : 0;
                    const code = episodeCodeByFileIndex.get(entry.fileIndex);
                    const fileLabel = code ?? `F${entry.fileIndex + 1}`;
                    const label = `${fileLabel} ${formatPercent(ratio)} · ${formatTime(entry.position)}`;
                    return (
                      <button
                        key={`${torrent.id}-${entry.fileIndex}-${entry.updatedAt}`}
                        type="button"
                        className="inline-flex items-center rounded-full border border-primary/25 bg-primary/10 px-2 py-0.5 text-[11px] font-medium text-primary hover:bg-primary/15"
                        onClick={() =>
                          onResumeWatch(
                            torrent.id,
                            entry.fileIndex,
                            entry.position,
                            entry.duration,
                            entry.torrentName ?? torrent.name ?? '',
                            entry.filePath ?? '',
                          )
                        }
                        title={entry.filePath || undefined}
                      >
                        {label}
                      </button>
                    );
                  })}
                </div>
              </div>
            ) : null}
          </div>

          <div className="flex items-center gap-2">
            {isCurrentPriority ? (
              <Badge className="border-transparent bg-primary text-primary-foreground">Current</Badge>
            ) : null}
            {transferPhase === 'verifying' ? (
              <Badge variant="outline" className="border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-300">
                verifying {formatPercent(verificationProgress)}
              </Badge>
            ) : null}
            <Badge variant={statusToBadgeVariant(status)}>{status}</Badge>
          </div>
        </div>

        <div className="mt-4">
          {state?.numPieces && state.pieceBitfield ? (
            <PieceBar numPieces={state.numPieces} pieceBitfield={state.pieceBitfield} height={10} />
          ) : (
            <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
              <div
                className={cn('h-full bg-primary', progress >= 1 ? 'bg-emerald-500' : '')}
                style={{ width: `${Math.max(0, Math.min(100, progress * 100))}%` }}
              />
            </div>
          )}
        </div>

        <div className="mt-4 flex flex-wrap gap-2" onClick={(e) => e.stopPropagation()}>
          <Button variant="outline" size="sm" onClick={() => onWatch(torrent.id)}>
            <MonitorPlay className="h-4 w-4" />
            Watch
          </Button>
          <Button
            variant={isCurrentPriority ? 'default' : 'outline'}
            size="sm"
            onClick={() => onSetCurrent(torrent.id)}
          >
            <Focus className="h-4 w-4" />
            {isCurrentPriority ? 'Current' : 'Set current'}
          </Button>
          {status === 'active' ? (
            <Button variant="outline" size="sm" onClick={() => onStop(torrent.id)}>
              <Square className="h-4 w-4" />
              Stop
            </Button>
          ) : (
            <Button variant="outline" size="sm" onClick={() => onStart(torrent.id)}>
              <Play className="h-4 w-4" />
              Start
            </Button>
          )}
          <Button variant="ghost" size="sm" onClick={() => onOpenDetails(torrent.id)}>
            Details
          </Button>
        </div>

        <div className="mt-3 flex flex-wrap gap-2 text-xs text-muted-foreground" onClick={(e) => e.stopPropagation()}>
          <span className="rounded-full border border-border/70 bg-muted/20 px-2 py-0.5">
            DL {formatSpeed(state?.downloadSpeed)}
          </span>
          <span className="rounded-full border border-border/70 bg-muted/20 px-2 py-0.5">
            UL {formatSpeed(state?.uploadSpeed)}
          </span>
          <span className="rounded-full border border-border/70 bg-muted/20 px-2 py-0.5">
            Peers {state?.peers ?? 0}
          </span>
          {transferPhase ? (
            <span className="rounded-full border border-border/70 bg-muted/20 px-2 py-0.5">
              Phase {transferPhase}
            </span>
          ) : null}
          {isCurrentPriority ? (
            <span className="rounded-full border border-border/70 bg-muted/20 px-2 py-0.5">
              Neighbors {prioritizeActiveFileOnly ? 'none' : 'low'}
            </span>
          ) : null}
          <span className="rounded-full border border-border/70 bg-muted/20 px-2 py-0.5">
            {formatDate(torrent.createdAt)}
          </span>
        </div>
      </Card>
    );
  };

  return (
    <div className="flex flex-col gap-4">
      <Card>
        <CardHeader className="pb-2">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <CardTitle className="flex items-center gap-2 text-base">
              <ArrowUpDown className="h-4 w-4 text-primary" />
              Filters & actions
            </CardTitle>
            <div className="flex flex-wrap items-center gap-2">
              <div className="rounded-full border border-border/70 bg-muted/15 px-3 py-1 text-xs text-muted-foreground">
                Visible: <span className="font-medium text-foreground">{visibleIDs.length}</span>
                <span className="px-1.5" aria-hidden="true">
                  {'\u00B7'}
                </span>
                Selected: <span className="font-medium text-foreground">{visibleSelectedCount}</span>
              </div>
              <Button
                variant="ghost"
                size="sm"
                className="h-8 px-2.5"
                onClick={onClearFilters}
                disabled={!hasActiveFilters}
              >
                Clear filters
              </Button>
              <Button
                variant="outline"
                size="sm"
                className="h-8"
                onClick={toggleSelectVisible}
                disabled={visibleIDs.length === 0 || bulkRunning}
              >
                {allVisibleSelected ? 'Unselect visible' : 'Select visible'}
              </Button>
            </div>
          </div>
        </CardHeader>
        <CardContent className="space-y-2 pt-0">
          <div className="grid gap-2 sm:grid-cols-2 xl:grid-cols-[minmax(240px,1.35fr)_minmax(240px,1.35fr)_minmax(140px,0.55fr)_minmax(140px,0.55fr)_minmax(120px,0.45fr)]">
            <div className="relative">
              <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
              <Input
                className="h-9 pl-9"
                value={searchQuery}
                onChange={(e) => onSearchChange(e.target.value)}
                placeholder="Search by name or keyword..."
                aria-label="Search torrents"
              />
            </div>

            <div className="min-w-0">
              <TagInput
                value={tagsQuery}
                onChange={onTagsChange}
                allTags={allTags}
                placeholder="Tags: movie, 4k, anime"
                inputClassName="h-9"
              />
            </div>

            <Select
              value={statusFilter}
              className="h-9"
              onChange={(e) => onFilterChange(e.target.value as TorrentStatusFilter)}
              aria-label="Status filter"
            >
              {statusFilterOptions.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </Select>

            <Select
              value={sortBy}
              className="h-9"
              onChange={(e) => onSortByChange(e.target.value as TorrentSortBy)}
              aria-label="Sort by"
            >
              {sortByOptions.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </Select>

            <Select
              value={sortOrder}
              className="h-9"
              onChange={(e) => onSortOrderChange(e.target.value as SortOrder)}
              aria-label="Sort order"
            >
              {sortOrderOptions.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </Select>
          </div>

          <div className="flex flex-wrap items-center justify-end gap-2 rounded-xl border border-border/70 bg-muted/10 px-2.5 py-2">
            <div className="flex flex-wrap items-center gap-2">
              <Button
                variant="outline"
                size="sm"
                className="h-8"
                onClick={() => void runBulk(() => onBulkStart(selectedBulkIds))}
                disabled={selectedBulkIds.length === 0 || bulkRunning}
              >
                <Play className="h-4 w-4" />
                Start
              </Button>
              <Button
                variant="outline"
                size="sm"
                className="h-8"
                onClick={() => void runBulk(() => onBulkStop(selectedBulkIds))}
                disabled={selectedBulkIds.length === 0 || bulkRunning}
              >
                <Square className="h-4 w-4" />
                Stop
              </Button>
              <div className="flex h-8 items-center gap-2 rounded-xl border border-border/70 bg-card/60 px-2.5 shadow-soft">
                <span className="text-xs text-muted-foreground">Delete files</span>
                <Switch checked={bulkDeleteFiles} onCheckedChange={setBulkDeleteFiles} />
              </div>
              <Button
                variant="destructive"
                size="sm"
                className="h-8"
                onClick={() => void runBulk(() => onBulkDelete(selectedBulkIds, bulkDeleteFiles))}
                disabled={selectedBulkIds.length === 0 || bulkRunning}
              >
                <Trash2 className="h-4 w-4" />
                Delete
              </Button>
            </div>
          </div>
        </CardContent>
      </Card>

      {currentTorrent ? (
        <div className="space-y-3">
          <div className="text-sm font-medium text-muted-foreground">Current priority</div>
          {renderTile(currentTorrent, { currentPriority: true })}
        </div>
      ) : null}

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
        {torrents.length === 0 ? (
          <Card className="col-span-full">
            <div className="p-6 text-sm text-muted-foreground">No torrents added yet.</div>
          </Card>
        ) : null}
        {torrents.length > 0 && regularTorrents.length === 0 ? (
          <Card className="col-span-full">
            <div className="p-6 text-sm text-muted-foreground">No other torrents in this filter.</div>
          </Card>
        ) : null}
        {regularTorrents.map((torrent) => renderTile(torrent))}
      </div>
    </div>
  );
};

export default React.memo(TorrentList);
