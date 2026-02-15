import React, { useMemo } from 'react';
import { File, FileImage, FileMusic, FileVideo } from 'lucide-react';

import type { FileRef, SessionState } from '../types';
import { cn } from '../lib/cn';
import { formatBytes, formatPercent, isAudioFile, isImageFile, isVideoFile } from '../utils';

import PieceBar from './PieceBar';

type PlayerFilesPanelProps = {
  files: FileRef[];
  selectedFileIndex: number | null;
  sessionState: SessionState | null;
  onSelectFile: (index: number) => void;
  className?: string;
};

const fileIcon = (file: FileRef) => {
  if (isVideoFile(file.path)) return <FileVideo className="h-4 w-4 text-primary" />;
  if (isAudioFile(file.path)) return <FileMusic className="h-4 w-4 text-primary" />;
  if (isImageFile(file.path)) return <FileImage className="h-4 w-4 text-primary" />;
  return <File className="h-4 w-4 text-muted-foreground" />;
};

export default function PlayerFilesPanel({
  files,
  selectedFileIndex,
  sessionState,
  onSelectFile,
  className,
}: PlayerFilesPanelProps) {
  const liveFileMap = useMemo(() => {
    const map = new Map<number, FileRef>();
    sessionState?.files?.forEach((f) => map.set(f.index, f));
    return map;
  }, [sessionState?.files]);

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
        <div className="rounded-xl border border-border/70 bg-muted/10 p-3">
          <div className="mb-2 text-xs font-medium text-muted-foreground">Pieces</div>
          <PieceBar numPieces={sessionState.numPieces} pieceBitfield={sessionState.pieceBitfield} height={8} />
        </div>
      ) : null}

      {files.length === 0 ? (
        <div className="rounded-xl border border-dashed border-border/70 bg-muted/10 p-6 text-sm text-muted-foreground">
          No files available.
        </div>
      ) : (
        <div className="min-h-0 overflow-y-auto pr-1">
          <div className="flex flex-col gap-2">
            {files.map((file, i) => {
              const live = liveFileMap.get(file.index);
              const completed = live?.bytesCompleted ?? file.bytesCompleted ?? 0;
              const total = file.length ?? 0;
              const fileProg = total > 0 ? completed / total : 0;
              const active = selectedFileIndex === file.index;
              const name = file.path.split('/').pop() ?? file.path;

              return (
                <button
                  key={file.index}
                  type="button"
                  aria-pressed={active}
                  className={cn(
                    'group w-full rounded-xl border px-3 py-2 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring',
                    active
                      ? 'border-primary/30 bg-primary/10'
                      : 'border-border/70 bg-muted/10 hover:bg-muted/20',
                  )}
                  onClick={() => onSelectFile(file.index)}
                >
                  <div className="flex items-center justify-between gap-3">
                    <div className="flex min-w-0 items-center gap-3">
                      <div className="flex h-9 w-9 items-center justify-center rounded-lg border border-border/70 bg-background/60">
                        {fileIcon(file)}
                      </div>
                      <div className="min-w-0">
                        <div className="flex items-center gap-2">
                          <span className="w-7 flex-shrink-0 text-center text-[11px] font-bold tabular-nums text-muted-foreground">
                            {i + 1}
                          </span>
                          <span className="truncate text-sm font-semibold">{name}</span>
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

                  <div className="mt-2 h-1.5 w-full overflow-hidden rounded-full bg-muted">
                    <div
                      className={cn('h-full bg-primary', fileProg >= 1 ? 'bg-emerald-500' : '')}
                      style={{ width: `${Math.max(0, Math.min(100, fileProg * 100))}%` }}
                    />
                  </div>
                </button>
              );
            })}
          </div>
        </div>
      )}
    </section>
  );
}

