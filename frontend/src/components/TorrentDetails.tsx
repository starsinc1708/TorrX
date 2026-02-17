import React, { useEffect, useMemo, useState } from 'react';
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
} from 'lucide-react';
import type { FileRef, SessionState, TorrentRecord } from '../types';
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
  const [deleteFiles, setDeleteFiles] = useState(false);
  const [tagsInput, setTagsInput] = useState((torrent.tags ?? []).join(', '));
  const [tagsSaving, setTagsSaving] = useState(false);
  const progress = Math.max(sessionState?.progress ?? 0, normalizeProgress(torrent));

  const files = useMemo(() => sessionState?.files ?? torrent.files ?? [], [sessionState?.files, torrent.files]);
  const tagsDisplay = (torrent.tags ?? []).filter((tag) => tag.trim().length > 0);

  useEffect(() => {
    setTagsInput((torrent.tags ?? []).join(', '));
  }, [torrent.id, torrent.tags]);

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
                  <span
                    key={`${torrent.id}-tag-${tag}`}
                    className="rounded-full border border-border/70 bg-muted/20 px-2 py-0.5 text-xs text-muted-foreground"
                  >
                    #{tag}
                  </span>
                ))
              ) : (
                <span className="text-sm text-muted-foreground">No tags</span>
              )}
            </div>
            {onUpdateTags ? (
              <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
                <Input
                  value={tagsInput}
                  onChange={(event) => setTagsInput(event.target.value)}
                  placeholder="movie, 4k, anime"
                />
                <Button
                  variant="outline"
                  onClick={async () => {
                    setTagsSaving(true);
                    try {
                      await onUpdateTags(tagsInput);
                    } finally {
                      setTagsSaving(false);
                    }
                  }}
                  disabled={tagsSaving}
                >
                  {tagsSaving ? 'Saving...' : 'Save tags'}
                </Button>
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
            <CardContent className="space-y-2 lg:min-h-0 lg:flex-1 lg:overflow-y-auto lg:pr-1">
              {files.map((file) => {
                const fileProg = file.length > 0 ? (file.bytesCompleted ?? 0) / file.length : 0;
                return (
                  <button
                    key={file.index}
                    type="button"
                    className="w-full rounded-lg border border-border/70 bg-muted/10 px-4 py-3 text-left transition-colors hover:bg-muted/30 focus-visible:outline-none"
                    onClick={() => onWatchFile(torrent.id, file.index)}
                  >
                    <div className="flex items-center justify-between gap-3">
                      <div className="flex min-w-0 items-center gap-3">
                        {fileIcon(file)}
                        <div className="min-w-0">
                          <div className="truncate text-sm font-medium">{file.path.split('/').pop()}</div>
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
