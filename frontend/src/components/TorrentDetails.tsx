import React, { useCallback, useEffect, useMemo, useState } from 'react';
import {
  ArrowDown,
  ArrowUp,
  ChevronLeft,
  File,
  FileImage,
  FileMusic,
  FileVideo,
  Play,
  Square,
  Trash2,
  Users,
  X,
} from 'lucide-react';
import type { FileRef, MediaOrganizationItem, SessionState, TorrentRecord } from '../types';
import { cn } from '../lib/cn';
import {
  formatBytes,
  formatDate,
  formatPercent,
  formatSpeed,
  isAudioFile,
  isImageFile,
  isVideoFile,
  normalizeProgress,
} from '../utils';
import PieceBar from './PieceBar';
import { Badge } from './ui/badge';
import { Button } from './ui/button';
import { Card, CardContent, CardHeader, CardTitle } from './ui/card';
import { Input } from './ui/input';
import { Switch } from './ui/switch';

interface TorrentDetailsProps {
  torrent: TorrentRecord;
  sessionState: SessionState | null;
  onBack: () => void;
  onStart: () => void;
  onStop: () => void;
  onDelete: (removeFiles: boolean) => void;
  onWatchFile?: (torrentId: string, fileIndex: number) => void;
  onUpdateTags?: (tagsInput: string) => Promise<boolean> | boolean;
}

const fileIcon = (file: FileRef) => {
  if (isVideoFile(file.path)) return <FileVideo className="h-4 w-4 text-primary" />;
  if (isAudioFile(file.path)) return <FileMusic className="h-4 w-4 text-primary" />;
  if (isImageFile(file.path)) return <FileImage className="h-4 w-4 text-primary" />;
  return <File className="h-4 w-4 text-muted-foreground" />;
};

const seriesTokenPattern = /\bS\d{1,2}\s*E\d{1,3}\b|\b\d{1,2}x\d{1,3}\b|\bE(?:P)?\s*\d{1,3}\b/gi;
const partTokenPattern = /\b(?:part|pt|cd|disc|disk)\s*([0-9]{1,2})\b/i;
const cleanupSpacesPattern = /\s+/g;

const fileBaseName = (path: string) => {
  const normalized = path.replace(/\\/g, '/');
  const base = normalized.split('/').pop() ?? normalized;
  return base.replace(/\.[a-z0-9]{1,5}$/i, '');
};

const normalizeDisplayTitle = (value: string) =>
  value
    .replace(seriesTokenPattern, ' ')
    .replace(partTokenPattern, ' ')
    .replace(cleanupSpacesPattern, ' ')
    .trim();

const episodeCode = (season?: number, episode?: number) => {
  if (!episode || episode <= 0) return '';
  const e = String(episode).padStart(2, '0');
  if (season && season > 0) return `S${String(season).padStart(2, '0')}E${e}`;
  return `E${e}`;
};

const getMoviePartNumber = (item: MediaOrganizationItem | undefined, fallback: number) => {
  if (!item) return fallback;
  const pathMatch = partTokenPattern.exec(item.filePath);
  if (pathMatch?.[1]) return Number.parseInt(pathMatch[1], 10) || fallback;
  const nameMatch = partTokenPattern.exec(item.displayName);
  if (nameMatch?.[1]) return Number.parseInt(nameMatch[1], 10) || fallback;
  return fallback;
};

const normalizeTagsList = (tags: string[]): string[] => {
  const seen = new Set<string>();
  const result: string[] = [];
  for (const rawTag of tags) {
    const tag = rawTag.trim();
    if (!tag) continue;
    const key = tag.toLowerCase();
    if (seen.has(key)) continue;
    seen.add(key);
    result.push(tag);
  }
  return result;
};

const areTagsEqual = (a: string[], b: string[]) => {
  if (a.length !== b.length) return false;
  const normalizedA = normalizeTagsList(a).map((t) => t.toLowerCase()).sort();
  const normalizedB = normalizeTagsList(b).map((t) => t.toLowerCase()).sort();
  if (normalizedA.length !== normalizedB.length) return false;
  for (let i = 0; i < normalizedA.length; i += 1) {
    if (normalizedA[i] !== normalizedB[i]) return false;
  }
  return true;
};

const TorrentDetails: React.FC<TorrentDetailsProps> = ({
  torrent,
  sessionState,
  onBack,
  onStart,
  onStop,
  onDelete,
  onWatchFile,
  onUpdateTags,
}) => {
  const [deleteFiles, setDeleteFiles] = useState(true);
  const [tagDraft, setTagDraft] = useState('');
  const [editableTags, setEditableTags] = useState<string[]>(() => normalizeTagsList(torrent.tags ?? []));
  const [tagsSaving, setTagsSaving] = useState(false);
  const [selectedSeasonGroupId, setSelectedSeasonGroupId] = useState<string | null>(null);
  const progress = sessionState?.progress ?? normalizeProgress(torrent);

  const files = useMemo(() => sessionState?.files ?? torrent.files ?? [], [sessionState?.files, torrent.files]);
  const fileOrder = useMemo(() => {
    const map = new Map<number, number>();
    files.forEach((file, idx) => map.set(file.index, idx));
    return map;
  }, [files]);
  const tagsDisplay = editableTags;

  const organizedSections = useMemo(() => {
    const byIndex = new Map<number, FileRef>();
    const byPath = new Map<string, FileRef>();
    files.forEach((file) => byIndex.set(file.index, file));
    files.forEach((file) => byPath.set(file.path.replace(/\\/g, '/').toLowerCase(), file));

    const sections: Array<{
      id: string;
      title: string;
      type: 'series' | 'movie' | 'other';
      entries: Array<{ file: FileRef; item?: MediaOrganizationItem; targetFileIndex: number }>;
    }> = [];
    const used = new Set<number>();

    for (const group of torrent.mediaOrganization?.groups ?? []) {
      const entries: Array<{ file: FileRef; item?: MediaOrganizationItem; targetFileIndex: number }> = [];
      for (const item of group.items ?? []) {
        const file = byIndex.get(item.fileIndex) ?? byPath.get(item.filePath.replace(/\\/g, '/').toLowerCase());
        if (!file) continue;
        const targetFileIndex = byIndex.has(item.fileIndex) ? item.fileIndex : file.index;
        entries.push({ file, item, targetFileIndex });
        used.add(targetFileIndex);
      }
      if (entries.length === 0) continue;
      sections.push({
        id: group.id,
        title: group.title,
        type: group.type,
        entries,
      });
    }

    const leftovers = files.filter((file) => !used.has(file.index));
    if (leftovers.length > 0) {
      sections.push({
        id: sections.length === 0 ? 'all' : 'other',
        title: sections.length === 0 ? 'All files' : 'Other files',
        type: sections.length === 0 ? 'movie' : 'other',
        entries: leftovers.map((file) => ({ file, targetFileIndex: file.index })),
      });
    }

    return sections;
  }, [files, torrent.mediaOrganization?.groups]);

  const seasonGroups = useMemo(() => {
    return organizedSections
      .filter((section) => section.type === 'series')
      .map((section) => ({
        id: section.id,
        title: section.title,
        season: section.entries[0]?.item?.season ?? 0,
        episodes: section.entries
          .map((entry, idx) => ({
            fileIndex: entry.targetFileIndex,
            episode: entry.item?.episode ?? idx + 1,
          }))
          .sort((a, b) => a.episode - b.episode),
      }))
      .sort((a, b) => {
        if (a.season !== b.season) return a.season - b.season;
        return a.id.localeCompare(b.id);
      });
  }, [organizedSections]);

  useEffect(() => {
    if (seasonGroups.length === 0) {
      setSelectedSeasonGroupId(null);
      return;
    }
    const exists = seasonGroups.some((group) => group.id === selectedSeasonGroupId);
    if (exists) return;
    setSelectedSeasonGroupId(seasonGroups[0].id);
  }, [seasonGroups, selectedSeasonGroupId]);

  const activeSeasonGroup = useMemo(() => {
    if (!selectedSeasonGroupId) return null;
    return seasonGroups.find((group) => group.id === selectedSeasonGroupId) ?? null;
  }, [seasonGroups, selectedSeasonGroupId]);

  useEffect(() => {
    setEditableTags(normalizeTagsList(torrent.tags ?? []));
    setTagDraft('');
  }, [torrent.id, torrent.tags]);

  const addTag = useCallback(() => {
    const nextTag = tagDraft.trim();
    if (!nextTag) return;
    setEditableTags((prev) => normalizeTagsList([...prev, nextTag]));
    setTagDraft('');
  }, [tagDraft]);

  const removeTag = useCallback((tag: string) => {
    const target = tag.trim().toLowerCase();
    setEditableTags((prev) => prev.filter((item) => item.trim().toLowerCase() !== target));
  }, []);

  const tagsChanged = useMemo(() => {
    return !areTagsEqual(editableTags, torrent.tags ?? []);
  }, [editableTags, torrent.tags]);

  return (
    <div className="grid gap-4 lg:h-[calc(100dvh-3.5rem-2*theme(spacing.5))] lg:grid-cols-[minmax(420px,1fr)_minmax(0,1.35fr)] lg:overflow-hidden">
      <div className="space-y-4 lg:flex lg:min-h-0 lg:flex-col">
        <div className="flex items-center justify-between gap-3">
          <Button variant="outline" onClick={onBack}>
            <ChevronLeft className="h-4 w-4" />
            Back
          </Button>
        </div>

        <Card className="lg:flex lg:min-h-0 lg:flex-1 lg:flex-col">
        <CardHeader className="pb-3">
          <div className="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
            <div className="min-w-0">
              <CardTitle className="truncate">{torrent.name ?? torrent.id}</CardTitle>
              <div className="mt-1 text-sm text-muted-foreground">
                {torrent.infoHash ? (
                  <span className="font-mono text-xs">{torrent.infoHash}</span>
                ) : (
                  <span className="text-xs">InfoHash not available</span>
                )}
              </div>
            </div>
            <Badge className="w-fit" variant={torrent.status === 'active' ? 'success' : torrent.status === 'stopped' ? 'outline' : 'secondary'}>
              {torrent.status}
            </Badge>
          </div>
        </CardHeader>
          <CardContent className="space-y-4 lg:flex-1">
          <div className="space-y-2">
            <div className="flex items-center justify-between text-sm">
              <span className="text-muted-foreground">
                {formatBytes(torrent.doneBytes)} / {formatBytes(torrent.totalBytes)}
              </span>
              <span className="font-medium">{formatPercent(progress)}</span>
            </div>
            <div className="h-2 w-full overflow-hidden rounded-full bg-muted">
              <div
                className={cn('h-full bg-primary', progress >= 1 ? 'bg-emerald-500' : '')}
                style={{ width: `${Math.max(0, Math.min(100, progress * 100))}%` }}
              />
            </div>
          </div>

          {sessionState?.numPieces && sessionState.pieceBitfield ? (
            <PieceBar numPieces={sessionState.numPieces} pieceBitfield={sessionState.pieceBitfield} height={12} />
          ) : null}

          {sessionState ? (
            <div className="flex flex-wrap gap-2">
              <span className="inline-flex items-center gap-2 rounded-full border border-border/70 bg-muted/20 px-3 py-1 text-xs text-muted-foreground">
                <ArrowDown className="h-4 w-4" />
                {formatSpeed(sessionState.downloadSpeed)}
              </span>
              <span className="inline-flex items-center gap-2 rounded-full border border-border/70 bg-muted/20 px-3 py-1 text-xs text-muted-foreground">
                <ArrowUp className="h-4 w-4" />
                {formatSpeed(sessionState.uploadSpeed)}
              </span>
              <span className="inline-flex items-center gap-2 rounded-full border border-border/70 bg-muted/20 px-3 py-1 text-xs text-muted-foreground">
                <Users className="h-4 w-4" />
                {sessionState.peers ?? 0} peers
              </span>
              {sessionState.transferPhase ? (
                <span className="inline-flex items-center gap-2 rounded-full border border-border/70 bg-muted/20 px-3 py-1 text-xs text-muted-foreground">
                  phase {sessionState.transferPhase}
                  {sessionState.transferPhase === 'verifying'
                    ? ` ${formatPercent(Math.max(0, Math.min(1, sessionState.verificationProgress ?? 0)))}`
                    : ''}
                </span>
              ) : null}
            </div>
          ) : null}

          <div className="flex flex-wrap items-center gap-2">
            {torrent.status === 'active' ? (
              <Button variant="outline" size="sm" onClick={onStop}>
                <Square className="h-4 w-4" />
                Stop
              </Button>
            ) : (
              <Button variant="outline" size="sm" onClick={onStart}>
                <Play className="h-4 w-4" />
                Start
              </Button>
            )}
            <Button variant="destructive" size="sm" onClick={() => onDelete(deleteFiles)}>
              <Trash2 className="h-4 w-4" />
              Delete
            </Button>
            <div className="flex items-center gap-2 rounded-md border border-border/70 bg-muted/20 px-3 py-2">
              <span className="text-xs text-muted-foreground">Delete files</span>
              <Switch checked={deleteFiles} onCheckedChange={setDeleteFiles} />
            </div>
          </div>

          <div className="grid gap-3 sm:grid-cols-2">
            <div className="rounded-lg border border-border/70 bg-muted/10 p-4">
              <div className="text-xs font-medium text-muted-foreground">Created</div>
              <div className="mt-1 text-sm font-semibold">{formatDate(torrent.createdAt)}</div>
            </div>
            <div className="rounded-lg border border-border/70 bg-muted/10 p-4">
              <div className="text-xs font-medium text-muted-foreground">Updated</div>
              <div className="mt-1 text-sm font-semibold">{formatDate(torrent.updatedAt)}</div>
            </div>
          </div>

          <div className="space-y-2">
            <div className="text-sm font-medium">Tags</div>
            <div className="flex flex-wrap gap-2">
              {tagsDisplay.length > 0 ? (
                tagsDisplay.map((tag) => (
                  onUpdateTags ? (
                    <button
                      key={`${torrent.id}-editable-tag-${tag}`}
                      type="button"
                      className="inline-flex items-center gap-1 rounded-full border border-border/70 bg-muted/20 px-2 py-0.5 text-xs text-muted-foreground transition-colors hover:bg-muted/35"
                      onClick={() => removeTag(tag)}
                      title={`Remove #${tag}`}
                    >
                      <span>#{tag}</span>
                      <X className="h-3 w-3" />
                    </button>
                  ) : (
                    <span
                      key={`${torrent.id}-tag-${tag}`}
                      className="rounded-full border border-border/70 bg-muted/20 px-2 py-0.5 text-xs text-muted-foreground"
                    >
                      #{tag}
                    </span>
                  )
                ))
              ) : (
                <span className="text-sm text-muted-foreground">No tags</span>
              )}
            </div>
            {onUpdateTags ? (
              <div className="space-y-2">
                <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
                  <Input
                    value={tagDraft}
                    onChange={(event) => setTagDraft(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key !== 'Enter') return;
                      event.preventDefault();
                      addTag();
                    }}
                    placeholder="Add one tag and press Enter"
                  />
                  <Button
                    type="button"
                    variant="outline"
                    className="whitespace-nowrap"
                    onClick={addTag}
                    disabled={!tagDraft.trim()}
                  >
                    Add tag
                  </Button>
                  <Button
                    type="button"
                    variant="outline"
                    className="min-w-[110px] whitespace-nowrap"
                    onClick={async () => {
                      setTagsSaving(true);
                      try {
                        await onUpdateTags(editableTags.join(', '));
                      } finally {
                        setTagsSaving(false);
                      }
                    }}
                    disabled={tagsSaving || !tagsChanged}
                  >
                    {tagsSaving ? 'Saving...' : 'Save tags'}
                  </Button>
                </div>
              </div>
            ) : null}
          </div>
          </CardContent>
        </Card>
      </div>

      <div className="min-w-0 space-y-4 lg:flex lg:min-h-0 lg:flex-col">
        {files.length > 0 && onWatchFile ? (
          <Card className="lg:flex lg:min-h-0 lg:flex-1 lg:flex-col">
            <CardHeader className="pb-3">
              <CardTitle className="text-base">Files ({files.length})</CardTitle>
            </CardHeader>
            <CardContent className="space-y-3 lg:min-h-0 lg:flex-1 lg:overflow-y-auto lg:pr-1">
              {seasonGroups.length > 0 && activeSeasonGroup ? (
                <div className="rounded-lg border border-border/70 bg-muted/10 p-3">
                  <div className="text-[11px] font-semibold uppercase tracking-[0.1em] text-muted-foreground">
                    Episode selector
                  </div>
                  <div className="mt-2 flex flex-wrap gap-1.5">
                    {seasonGroups.map((group) => (
                      <button
                        key={`season-${group.id}`}
                        type="button"
                        className={cn(
                          'rounded-md border px-2 py-1 text-xs font-medium',
                          selectedSeasonGroupId === group.id
                            ? 'border-primary/40 bg-primary/15 text-foreground'
                            : 'border-border/70 bg-background/60 text-muted-foreground hover:text-foreground',
                        )}
                        onClick={() => setSelectedSeasonGroupId(group.id)}
                      >
                        {group.title}
                      </button>
                    ))}
                  </div>
                  <div className="mt-2 flex max-h-24 flex-wrap gap-1.5 overflow-y-auto pr-1">
                    {activeSeasonGroup.episodes.map((episode) => (
                      <button
                        key={`episode-${activeSeasonGroup.id}-${episode.fileIndex}`}
                        type="button"
                        className="rounded-md border border-border/70 bg-background/60 px-2 py-1 text-xs font-semibold tabular-nums text-foreground hover:bg-accent/60"
                        onClick={() => onWatchFile(torrent.id, episode.fileIndex)}
                      >
                        E{String(episode.episode).padStart(2, '0')}
                      </button>
                    ))}
                  </div>
                </div>
              ) : null}

              {organizedSections.map((section) => (
                <div key={section.id} className="space-y-2">
                  {organizedSections.length > 1 ? (
                    <div className="px-1 text-[11px] font-semibold uppercase tracking-[0.1em] text-muted-foreground">
                      {section.title}
                    </div>
                  ) : null}
                  {section.entries.map((entry, idx) => {
                    const file = entry.file;
                    const targetFileIndex = entry.targetFileIndex;
                    const fileProg = file.length > 0 ? (file.bytesCompleted ?? 0) / file.length : 0;
                    const fallbackTitle = entry.item?.displayName?.trim() || fileBaseName(file.path);
                    const cleanTitle = normalizeDisplayTitle(fallbackTitle) || fallbackTitle;
                    const partLabel =
                      section.type === 'movie' && section.entries.length > 1
                        ? `Part ${getMoviePartNumber(entry.item, idx + 1)}`
                        : null;
                    const epCode =
                      section.type === 'series'
                        ? episodeCode(entry.item?.season, entry.item?.episode)
                        : '';
                    const displayName =
                      section.type === 'series'
                        ? `${cleanTitle}${epCode ? ` - ${epCode}` : ''}`
                        : `${cleanTitle}${partLabel ? ` - ${partLabel}` : ''}`;
                    const ordinal = (fileOrder.get(targetFileIndex) ?? fileOrder.get(file.index) ?? 0) + 1;

                    return (
                      <button
                        key={`${section.id}-${targetFileIndex}-${idx}`}
                        type="button"
                        className={cn(
                          'w-full rounded-lg border border-border/70 bg-muted/10 px-4 py-3 text-left transition-colors hover:bg-muted/30 focus-visible:outline-none',
                        )}
                        onClick={() => onWatchFile(torrent.id, targetFileIndex)}
                      >
                        <div className="flex items-center justify-between gap-3">
                          <div className="flex min-w-0 items-center gap-3">
                            {fileIcon(file)}
                            <div className="min-w-0">
                              <div className="flex items-center gap-2">
                                <span className="w-7 flex-shrink-0 text-center text-[11px] font-bold tabular-nums text-muted-foreground">
                                  {ordinal}
                                </span>
                                <span className="truncate text-sm font-medium">{displayName}</span>
                                {file.priority && file.priority !== 'normal' && (
                                  <span className={cn(
                                    'ml-1.5 inline-flex items-center rounded-full px-1.5 py-0.5 text-[10px] font-medium leading-none',
                                    file.priority === 'high' || file.priority === 'now'
                                      ? 'bg-primary/20 text-primary'
                                      : file.priority === 'low'
                                        ? 'bg-amber-500/15 text-amber-600 dark:text-amber-400'
                                        : ''
                                  )}>
                                    {file.priority}
                                  </span>
                                )}
                              </div>
                              <div className="mt-1 text-xs text-muted-foreground">{formatBytes(file.length)}</div>
                            </div>
                          </div>
                          <div className={cn('text-xs font-semibold tabular-nums', fileProg >= 1 ? 'text-emerald-500' : 'text-primary')}>
                            {formatPercent(fileProg)}
                          </div>
                        </div>
                        <div className="mt-3 h-1.5 w-full overflow-hidden rounded-full bg-muted">
                          <div
                            className={cn('h-full bg-primary', fileProg >= 1 ? 'bg-emerald-500' : '')}
                            style={{ width: `${Math.max(0, Math.min(100, fileProg * 100))}%` }}
                          />
                        </div>
                      </button>
                    );
                  })}
                </div>
              ))}
            </CardContent>
          </Card>
        ) : (
          <Card>
            <CardContent className="p-6 text-sm text-muted-foreground">No files available.</CardContent>
          </Card>
        )}
      </div>
    </div>
  );
};

export default React.memo(TorrentDetails);
