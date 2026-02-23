import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Play, Film, Tv } from 'lucide-react';
import { getIncompleteWatchHistory } from '../api';
import type { WatchPosition } from '../types';
import { cn } from '../lib/cn';
import { upsertTorrentWatchState } from '../watchState';

function formatTime(seconds: number): string {
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

function ContentIcon({ type }: { type: string }) {
  if (type === 'series') return <Tv className="h-3 w-3" />;
  return <Film className="h-3 w-3" />;
}

export function ContinueWatching() {
  const [items, setItems] = useState<WatchPosition[]>([]);
  const navigate = useNavigate();

  useEffect(() => {
    let cancelled = false;
    getIncompleteWatchHistory(10)
      .then((data) => {
        if (!cancelled) setItems(data);
      })
      .catch(() => {});
    return () => { cancelled = true; };
  }, []);

  if (items.length === 0) return null;

  return (
    <div className="space-y-3">
      <h2 className="text-sm font-medium text-muted-foreground">Continue Watching</h2>
      <div className="flex gap-3 overflow-x-auto pb-2">
        {items.map((item) => {
          const displayName = item.filePath
            ? item.filePath.split('/').pop()?.replace(/\.[^.]+$/, '') ?? item.torrentName
            : item.torrentName;

          return (
            <button
              key={`${item.torrentId}:${item.fileIndex}`}
              type="button"
              className={cn(
                'group flex w-56 flex-none flex-col gap-2 rounded-lg border bg-card p-3',
                'transition-colors hover:border-primary/50 hover:bg-accent/50',
              )}
              onClick={() => {
                upsertTorrentWatchState({
                  torrentId: item.torrentId,
                  fileIndex: item.fileIndex,
                  position: item.position,
                  duration: item.duration,
                  torrentName: item.torrentName || undefined,
                  filePath: item.filePath || undefined,
                });
                navigate(`/watch/${item.torrentId}/${item.fileIndex}`, {
                  state: {
                    resume: true,
                    torrentId: item.torrentId,
                    fileIndex: item.fileIndex,
                    position: item.position,
                    duration: item.duration,
                    torrentName: item.torrentName,
                    filePath: item.filePath,
                  },
                });
              }}
            >
              <div className="flex items-start justify-between gap-2">
                <div className="min-w-0 flex-1 text-left">
                  <div className="truncate text-sm font-medium">{displayName}</div>
                  <div className="flex items-center gap-1 text-xs text-muted-foreground">
                    <ContentIcon type={item.contentType} />
                    <span>{formatTime(item.position)} / {formatTime(item.duration)}</span>
                  </div>
                </div>
                <Play className="h-4 w-4 flex-none text-muted-foreground group-hover:text-primary" />
              </div>
              <div className="h-1 w-full overflow-hidden rounded-full bg-muted">
                <div
                  className="h-full rounded-full bg-primary transition-all"
                  style={{ width: `${Math.round(item.progress * 100)}%` }}
                />
              </div>
            </button>
          );
        })}
      </div>
    </div>
  );
}
