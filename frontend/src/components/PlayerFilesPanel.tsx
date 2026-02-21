import React, { useEffect, useMemo, useRef, useState } from 'react';
import { File, FileImage, FileMusic, FileVideo } from 'lucide-react';

import type { FileRef, MediaOrganization, MediaOrganizationItem, SessionState } from '../types';
import { cn } from '../lib/cn';
import { decodePieceBitfield, formatBytes, formatPercent, formatSpeed, isAudioFile, isImageFile, isVideoFile } from '../utils';

import PieceBar from './PieceBar';

type PlayerFilesPanelProps = {
  files: FileRef[];
  selectedFileIndex: number | null;
  sessionState: SessionState | null;
  mediaOrganization?: MediaOrganization;
  onSelectFile: (index: number) => void;
  className?: string;
};

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

const normalizeTitle = (value: string) =>
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
  const fromPath = partTokenPattern.exec(item.filePath);
  if (fromPath && fromPath[1]) return Number.parseInt(fromPath[1], 10) || fallback;
  const fromDisplay = partTokenPattern.exec(item.displayName);
  if (fromDisplay && fromDisplay[1]) return Number.parseInt(fromDisplay[1], 10) || fallback;
  return fallback;
};

function PiecesBlock({ sessionState }: { sessionState: SessionState }) {
  const { numPieces, pieceBitfield, downloadSpeed, transferPhase, verificationProgress } = sessionState;

  const downloadedPieces = useMemo(() => {
    if (!pieceBitfield || !numPieces) return 0;
    const pieces = decodePieceBitfield(pieceBitfield, numPieces);
    return pieces.filter(Boolean).length;
  }, [pieceBitfield, numPieces]);

  const progressPercent = numPieces ? (downloadedPieces / numPieces) * 100 : 0;
  const isComplete = progressPercent >= 100;

  return (
    <div className="rounded-xl border border-border/70 bg-muted/10 p-3">
      <div className="mb-2 flex items-center justify-between gap-2">
        <div className="flex items-baseline gap-2">
          <span className="text-xs font-semibold uppercase tracking-[0.12em] text-muted-foreground">Pieces</span>
          {numPieces ? (
            <span className="text-[11px] tabular-nums text-muted-foreground">
              <span className={cn('font-semibold', isComplete ? 'text-emerald-500' : 'text-foreground')}>
                {downloadedPieces}
              </span>
              {' / '}
              {numPieces}
            </span>
          ) : null}
        </div>
        <div className="flex items-center gap-2 text-[11px] tabular-nums text-muted-foreground">
          {downloadSpeed != null && downloadSpeed > 0 && !isComplete ? (
            <span className="font-medium text-sky-500 dark:text-sky-400">DL {formatSpeed(downloadSpeed)}</span>
          ) : null}
          {transferPhase === 'verifying' ? (
            <span className="font-medium text-amber-600 dark:text-amber-400">
              verifying {formatPercent(Math.max(0, Math.min(1, verificationProgress ?? 0)))}
            </span>
          ) : null}
          <span className={cn('font-semibold', isComplete ? 'text-emerald-500' : '')}>
            {progressPercent.toFixed(1)}%
          </span>
        </div>
      </div>
      <PieceBar numPieces={numPieces!} pieceBitfield={pieceBitfield!} height={14} />
    </div>
  );
}

function FilePiecesBlock({
  sessionState,
  files,
  selectedFileIndex,
}: {
  sessionState: SessionState;
  files: FileRef[];
  selectedFileIndex: number | null;
}) {
  const { numPieces, pieceBitfield } = sessionState;
  const normalizePath = (value: string) => value.replace(/\\/g, '/').toLowerCase();

  const stats = useMemo(() => {
    if (!numPieces || numPieces <= 0 || !pieceBitfield || selectedFileIndex === null) return null;

    const pieces = decodePieceBitfield(pieceBitfield, numPieces);
    if (pieces.length === 0) return null;

    const selectedFile =
      files.find((file) => file.index === selectedFileIndex)
      ?? sessionState.files?.find((file) => file.index === selectedFileIndex);
    if (!selectedFile || selectedFile.length <= 0) return null;

    const liveFiles = sessionState.files ?? [];
    const liveByIndex = new Map<number, FileRef>(liveFiles.map((file) => [file.index, file]));
    const liveByPath = new Map<string, FileRef>(liveFiles.map((file) => [normalizePath(file.path), file]));
    const live = liveByIndex.get(selectedFileIndex) ?? liveByPath.get(normalizePath(selectedFile.path));
    const fileForPieces = live ?? selectedFile;

    const startValue = fileForPieces.pieceStart;
    const endValue = fileForPieces.pieceEnd;
    if (
      typeof startValue !== 'number'
      || typeof endValue !== 'number'
      || !Number.isInteger(startValue)
      || !Number.isInteger(endValue)
    ) {
      return null;
    }

    if (startValue < 0 || endValue <= startValue) return null;

    const start = Math.max(0, Math.min(numPieces - 1, startValue));
    const end = Math.max(start + 1, Math.min(numPieces, endValue));
    const filePieces = pieces.slice(start, end);
    const totalFilePieces = filePieces.length;
    if (totalFilePieces <= 0) return null;

    let completedFilePieces = 0;
    for (const piece of filePieces) {
      if (piece) completedFilePieces += 1;
    }

    const pieceProgress = fileForPieces.progress ?? Math.max(0, Math.min(1, completedFilePieces / totalFilePieces));

    return {
      fileLabel: fileBaseName(selectedFile.path),
      filePieces,
      completedFilePieces,
      totalFilePieces,
      pieceProgress,
    };
  }, [numPieces, pieceBitfield, selectedFileIndex, files, sessionState.files]);

  if (!stats) return null;

  return (
    <div className="rounded-xl border border-border/70 bg-muted/10 p-3">
      <div className="mb-2 flex items-center justify-between gap-2">
        <div className="min-w-0">
          <div className="flex items-baseline gap-2">
            <span className="text-xs font-semibold uppercase tracking-[0.12em] text-muted-foreground">File pieces</span>
            <span className="text-[11px] tabular-nums text-muted-foreground">
              <span className="font-semibold text-foreground">{stats.completedFilePieces}</span>
              {' / '}
              {stats.totalFilePieces}
            </span>
          </div>
          <div className="mt-1 truncate text-[11px] text-muted-foreground">{stats.fileLabel}</div>
        </div>
        <div className="flex flex-col items-end text-[11px] tabular-nums text-muted-foreground">
          <span className="font-semibold text-foreground">{(stats.pieceProgress * 100).toFixed(1)}%</span>
        </div>
      </div>

      <PieceBar pieces={stats.filePieces} height={14} />
    </div>
  );
}

export default function PlayerFilesPanel({
  files,
  selectedFileIndex,
  sessionState,
  mediaOrganization,
  onSelectFile,
  className,
}: PlayerFilesPanelProps) {
  const [selectedSeasonGroupId, setSelectedSeasonGroupId] = useState<string | null>(null);
  const [seasonScrollRequest, setSeasonScrollRequest] = useState(0);
  const sectionRefs = useRef<Map<string, HTMLDivElement>>(new Map());
  const fileEntryRefs = useRef<Map<number, HTMLButtonElement>>(new Map());
  const seasonScrollTimerRef = useRef<number | null>(null);
  const pendingSeasonScrollRef = useRef<{ groupId: string; fileIndex: number | null } | null>(null);
  const selectionScrollInitializedRef = useRef(false);

  const liveFileMap = useMemo(() => {
    const map = new Map<number, FileRef>();
    sessionState?.files?.forEach((f) => map.set(f.index, f));
    return map;
  }, [sessionState?.files]);

  const fileOrder = useMemo(() => {
    const map = new Map<number, number>();
    files.forEach((file, idx) => map.set(file.index, idx));
    return map;
  }, [files]);

  const groupedFiles = useMemo(() => {
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

    for (const group of mediaOrganization?.groups ?? []) {
      const sectionEntries: Array<{ file: FileRef; item?: MediaOrganizationItem; targetFileIndex: number }> = [];
      for (const item of group.items ?? []) {
        const file = byIndex.get(item.fileIndex) ?? byPath.get(item.filePath.replace(/\\/g, '/').toLowerCase());
        if (!file) continue;
        const targetFileIndex = byIndex.has(item.fileIndex) ? item.fileIndex : file.index;
        sectionEntries.push({ file, item, targetFileIndex });
        used.add(targetFileIndex);
      }
      if (sectionEntries.length > 0) {
        sections.push({
          id: group.id,
          title: group.title,
          type: group.type,
          entries: sectionEntries,
        });
      }
    }

    const leftovers = files.filter((file) => !used.has(file.index));
    if (leftovers.length > 0) {
      const type = sections.length === 0 ? 'movie' : 'other';
      sections.push({
        id: sections.length === 0 ? 'all' : 'other',
        title: sections.length === 0 ? 'All files' : 'Other files',
        type,
        entries: leftovers.map((file) => ({ file, targetFileIndex: file.index })),
      });
    }

    return sections;
  }, [files, mediaOrganization?.groups]);

  const seasonGroups = useMemo(() => {
    return groupedFiles
      .filter((section) => section.type === 'series')
      .map((section) => {
        const season = section.entries[0]?.item?.season ?? 0;
        const episodes = section.entries
          .map((entry, idx) => ({
            fileIndex: entry.targetFileIndex,
            episode: entry.item?.episode ?? idx + 1,
          }))
          .sort((a, b) => a.episode - b.episode);
        return {
          id: section.id,
          season,
          title: season > 0 ? `Season ${season}` : section.title,
          episodes,
        };
      })
      .sort((a, b) => {
        if (a.season !== b.season) return a.season - b.season;
        return a.id.localeCompare(b.id);
      });
  }, [groupedFiles]);

  useEffect(() => {
    if (seasonGroups.length === 0) {
      setSelectedSeasonGroupId(null);
      return;
    }

    // Respect manual season selection. Auto-select only when current selection
    // is empty or no longer exists after data refresh.
    const current = seasonGroups.find((group) => group.id === selectedSeasonGroupId);
    if (current) {
      return;
    }

    const selectedSeasonByFile = seasonGroups.find((group) =>
      group.episodes.some((ep) => ep.fileIndex === selectedFileIndex),
    )?.id;
    setSelectedSeasonGroupId(selectedSeasonByFile ?? seasonGroups[0].id);
  }, [seasonGroups, selectedFileIndex, selectedSeasonGroupId]);

  const activeSeasonGroup = useMemo(() => {
    if (selectedSeasonGroupId === null) return null;
    return seasonGroups.find((group) => group.id === selectedSeasonGroupId) ?? null;
  }, [seasonGroups, selectedSeasonGroupId]);

  useEffect(() => {
    return () => {
      if (seasonScrollTimerRef.current !== null) {
        window.clearTimeout(seasonScrollTimerRef.current);
      }
    };
  }, []);

  useEffect(() => {
    const pending = pendingSeasonScrollRef.current;
    if (!pending || pending.groupId !== selectedSeasonGroupId) return;

    window.requestAnimationFrame(() => {
      const sectionEl = sectionRefs.current.get(pending.groupId);
      if (sectionEl) {
        sectionEl.scrollIntoView({ behavior: 'smooth', block: 'start' });
      }

      if (pending.fileIndex !== null) {
        if (seasonScrollTimerRef.current !== null) {
          window.clearTimeout(seasonScrollTimerRef.current);
        }
        seasonScrollTimerRef.current = window.setTimeout(() => {
          const fileEl = fileEntryRefs.current.get(pending.fileIndex!);
          if (fileEl) {
            fileEl.scrollIntoView({ behavior: 'smooth', block: 'center' });
          }
          seasonScrollTimerRef.current = null;
        }, 220);
      }
    });

    pendingSeasonScrollRef.current = null;
  }, [selectedSeasonGroupId, groupedFiles, seasonScrollRequest]);

  useEffect(() => {
    if (selectedFileIndex === null) return;
    if (!selectionScrollInitializedRef.current) {
      selectionScrollInitializedRef.current = true;
      return;
    }

    // Season click runs its own two-step scroll (season -> episode).
    if (pendingSeasonScrollRef.current) return;

    const fileEl = fileEntryRefs.current.get(selectedFileIndex);
    if (!fileEl) return;
    window.requestAnimationFrame(() => {
      fileEl.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
    });
  }, [selectedFileIndex]);

  const showSectionHeaders = groupedFiles.length > 1;

  return (
    <section
      className={cn(
        'flex min-h-0 flex-col gap-3 border-border/70 bg-card/40 p-3 sm:p-4',
        className,
      )}
      aria-label="Files"
    >
      <div className="flex items-center justify-between gap-3">
        <div className="min-w-0">
          <div className="text-xs font-semibold uppercase tracking-[0.12em] text-muted-foreground">Files</div>
          <div className="mt-1 text-sm font-semibold">
            {files.length} item{files.length === 1 ? '' : 's'}
          </div>
        </div>
        {selectedFileIndex !== null ? (
          <div className="text-xs text-muted-foreground">
            Selected: <span className="font-medium text-foreground">{selectedFileIndex + 1}</span>
          </div>
        ) : null}
      </div>

      {sessionState?.numPieces && sessionState.pieceBitfield ? (
        <PiecesBlock sessionState={sessionState} />
      ) : null}

      {sessionState?.numPieces && sessionState.pieceBitfield ? (
        <FilePiecesBlock
          sessionState={sessionState}
          files={files}
          selectedFileIndex={selectedFileIndex}
        />
      ) : null}

      {seasonGroups.length > 0 && activeSeasonGroup ? (
        <div className="rounded-xl border border-border/70 bg-muted/10 p-3">
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
                onClick={() => {
                  const selectedInSeason = group.episodes.some((episode) => episode.fileIndex === selectedFileIndex);
                  pendingSeasonScrollRef.current = {
                    groupId: group.id,
                    fileIndex: selectedInSeason ? selectedFileIndex : (group.episodes[0]?.fileIndex ?? null),
                  };
                  setSelectedSeasonGroupId(group.id);
                  setSeasonScrollRequest((value) => value + 1);
                }}
              >
                {group.title}
              </button>
            ))}
          </div>

          <div className="mt-2 flex max-h-20 flex-wrap gap-1.5 overflow-y-auto pr-1">
            {activeSeasonGroup.episodes.map((episode) => (
              <button
                key={`episode-${activeSeasonGroup.id}-${episode.fileIndex}`}
                type="button"
                className={cn(
                  'rounded-md border px-2 py-1 text-xs font-semibold tabular-nums',
                  selectedFileIndex === episode.fileIndex
                    ? 'border-primary/40 bg-primary/15 text-foreground'
                    : 'border-border/70 bg-background/60 text-muted-foreground hover:text-foreground',
                )}
                onClick={() => onSelectFile(episode.fileIndex)}
              >
                E{String(episode.episode).padStart(2, '0')}
              </button>
            ))}
          </div>
        </div>
      ) : null}

      {files.length === 0 ? (
        <div className="rounded-xl border border-dashed border-border/70 bg-muted/10 p-6 text-sm text-muted-foreground">
          No files available.
        </div>
      ) : (
        <div className="min-h-0 overflow-y-auto pr-1">
          <div className="flex flex-col gap-3">
            {groupedFiles.map((section) => (
              <div
                key={section.id}
                className="flex flex-col gap-2"
                ref={(el) => {
                  if (el) {
                    sectionRefs.current.set(section.id, el);
                  } else {
                    sectionRefs.current.delete(section.id);
                  }
                }}
              >
                {showSectionHeaders ? (
                  <div className="px-1 text-[11px] font-semibold uppercase tracking-[0.1em] text-muted-foreground">
                    {section.title}
                  </div>
                ) : null}
                {section.entries.map((entry, idx) => {
                  const file = entry.file;
                  const targetFileIndex = entry.targetFileIndex;
                  const live = liveFileMap.get(targetFileIndex) ?? liveFileMap.get(file.index);
                  const completed = Math.max(live?.bytesCompleted ?? 0, file.bytesCompleted ?? 0);
                  const total = file.length ?? 0;
                  const fileProg = total > 0 ? completed / total : 0;
                  const active = selectedFileIndex === targetFileIndex;
                  const fallbackTitle = entry.item?.displayName?.trim() || fileBaseName(file.path);
                  const cleanTitle = normalizeTitle(fallbackTitle) || fallbackTitle;
                  const partLabel =
                    section.type === 'movie' && section.entries.length > 1
                      ? `Part ${getMoviePartNumber(entry.item, idx + 1)}`
                      : null;
                  const episodeLabel =
                    section.type === 'series'
                      ? episodeCode(entry.item?.season, entry.item?.episode)
                      : '';
                  const name =
                    section.type === 'series'
                      ? `${cleanTitle}${episodeLabel ? ` - ${episodeLabel}` : ''}`
                      : `${cleanTitle}${partLabel ? ` - ${partLabel}` : ''}`;
                  const ordinal = (fileOrder.get(targetFileIndex) ?? fileOrder.get(file.index) ?? 0) + 1;
                  const filePriority = (live ?? file).priority;

                  return (
                    <button
                      key={`${section.id}-${targetFileIndex}-${idx}`}
                      ref={(el) => {
                        if (el) {
                          fileEntryRefs.current.set(targetFileIndex, el);
                        } else {
                          fileEntryRefs.current.delete(targetFileIndex);
                        }
                      }}
                      type="button"
                      aria-pressed={active}
                      className={cn(
                        'group w-full rounded-xl border px-3 py-2 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring',
                        active
                          ? 'border-primary/30 bg-primary/10'
                          : 'border-border/70 bg-muted/10 hover:bg-muted/20',
                        filePriority === 'none' && 'opacity-50',
                      )}
                      onClick={() => onSelectFile(targetFileIndex)}
                    >
                      <div className="flex items-center justify-between gap-3">
                        <div className="flex min-w-0 items-center gap-3">
                          <div className="flex h-9 w-9 items-center justify-center rounded-lg border border-border/70 bg-background/60">
                            {fileIcon(file)}
                          </div>
                          <div className="min-w-0">
                            <div className="flex items-center gap-2">
                              <span className="w-7 flex-shrink-0 text-center text-[11px] font-bold tabular-nums text-muted-foreground">
                                {ordinal}
                              </span>
                              <span className="truncate text-sm font-semibold">{name}</span>
                              {filePriority && filePriority !== 'normal' && (
                                <span className={cn(
                                  'ml-1.5 inline-flex items-center rounded-full px-1.5 py-0.5 text-[10px] font-medium leading-none',
                                  filePriority === 'high' || filePriority === 'now'
                                    ? 'bg-primary/20 text-primary'
                                    : filePriority === 'low'
                                      ? 'bg-amber-500/15 text-amber-600 dark:text-amber-400'
                                      : filePriority === 'none'
                                        ? 'bg-muted text-muted-foreground'
                                        : ''
                                )}>
                                  {filePriority}
                                </span>
                              )}
                            </div>
                            <div className="mt-1 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                              <span>{formatBytes(total)}</span>
                              <span aria-hidden="true">{'\u00B7'}</span>
                              <span className={cn('font-semibold tabular-nums', fileProg >= 1 ? 'text-emerald-600 dark:text-emerald-400' : 'text-foreground')}>
                                {formatPercent(fileProg)}
                              </span>
                            </div>
                          </div>
                        </div>
                      </div>

                      <div className="mt-2 h-2 w-full overflow-hidden rounded-full bg-muted/80">
                        <div
                          className={cn(
                            'h-full transition-[width] duration-500',
                            fileProg >= 1
                              ? 'bg-emerald-500'
                              : 'bg-gradient-to-r from-primary/80 to-primary',
                          )}
                          style={{ width: `${Math.max(0, Math.min(100, fileProg * 100))}%` }}
                        />
                      </div>
                    </button>
                  );
                })}
              </div>
            ))}
          </div>
        </div>
      )}
    </section>
  );
}
