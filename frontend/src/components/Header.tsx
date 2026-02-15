import React, { useEffect, useMemo, useState } from 'react';
import { NavLink } from 'react-router-dom';
import { Activity, Film, Library, Moon, Plus, Search, Settings, Sun } from 'lucide-react';

import { getPlayerSettings } from '../api';
import { useCatalogMeta } from '../app/providers/CatalogMetaProvider';
import { useThemeAccent } from '../app/providers/ThemeAccentProvider';
import { cn } from '../lib/cn';
import { getTorrentWatchState, WATCH_STATE_UPDATED_EVENT } from '../watchState';

import AddTorrentModal from './AddTorrentModal';
import { Button } from './ui/button';
import { Switch } from './ui/switch';

type LastWatch = {
  torrentId: string;
  fileIndex?: number;
};

const LAST_WATCH_KEY = 'lastWatch';

const readLastWatch = (): LastWatch | null => {
  const raw = localStorage.getItem(LAST_WATCH_KEY);
  if (!raw) return null;
  try {
    const data = JSON.parse(raw) as Partial<LastWatch>;
    if (!data.torrentId) return null;
    return {
      torrentId: data.torrentId,
      fileIndex: typeof data.fileIndex === 'number' ? data.fileIndex : undefined,
    };
  } catch {
    return null;
  }
};

const navBtnClass = (isActive: boolean) =>
  cn(
    'inline-flex items-center gap-2 rounded-md px-3 py-2 text-sm font-medium transition-colors',
    isActive ? 'bg-accent text-accent-foreground' : 'text-muted-foreground hover:bg-accent hover:text-accent-foreground',
  );

const Header: React.FC = () => {
  const [lastWatch, setLastWatch] = useState<LastWatch | null>(() => readLastWatch());
  const [currentTorrentId, setCurrentTorrentId] = useState<string | null>(null);
  const [isAddModalOpen, setIsAddModalOpen] = useState(false);
  const { counts, isLoading: isMetaLoading } = useCatalogMeta();
  const { resolvedTheme, setTheme } = useThemeAccent();

  useEffect(() => {
    const handleUpdate = () => setLastWatch(readLastWatch());

    const refreshCurrentTorrent = () => {
      getPlayerSettings()
        .then((settings) => setCurrentTorrentId(settings.currentTorrentId ?? null))
        .catch(() => setCurrentTorrentId(null));
    };

    // Initial fetch is handled by useTorrents; only listen for explicit events here.
    window.addEventListener('storage', handleUpdate);
    window.addEventListener('player:last-watch', handleUpdate);
    window.addEventListener(WATCH_STATE_UPDATED_EVENT, handleUpdate);
    window.addEventListener('player:current-torrent', refreshCurrentTorrent);
    return () => {
      window.removeEventListener('storage', handleUpdate);
      window.removeEventListener('player:last-watch', handleUpdate);
      window.removeEventListener(WATCH_STATE_UPDATED_EVENT, handleUpdate);
      window.removeEventListener('player:current-torrent', refreshCurrentTorrent);
    };
  }, []);

  const playerPath = useMemo(() => {
    if (currentTorrentId) {
      const currentWatch = getTorrentWatchState(currentTorrentId);
      if (typeof currentWatch?.fileIndex === 'number') {
        return `/watch/${currentTorrentId}/${currentWatch.fileIndex}`;
      }
      if (lastWatch?.torrentId === currentTorrentId && typeof lastWatch.fileIndex === 'number') {
        return `/watch/${currentTorrentId}/${lastWatch.fileIndex}`;
      }
      return `/watch/${currentTorrentId}`;
    }
    if (!lastWatch) return '/';
    if (lastWatch.fileIndex === undefined) return `/watch/${lastWatch.torrentId}`;
    return `/watch/${lastWatch.torrentId}/${lastWatch.fileIndex}`;
  }, [currentTorrentId, lastWatch]);

  const isDark = resolvedTheme === 'dark';

  return (
    <header className="sticky top-0 z-50 border-b border-border/70 bg-background/70 backdrop-blur">
      <div className="grid h-14 w-full grid-cols-[1fr_auto_1fr] items-center gap-2 px-4 md:px-6 lg:px-8">
        <div className="flex min-w-0 items-center gap-2">
          <NavLink
            to="/"
            end
            aria-label="T◎RRX"
            className={cn(
              'group inline-flex min-w-0 items-center gap-2 rounded-md px-2 py-1.5',
              'text-sm font-semibold tracking-tight text-foreground/90',
              'hover:bg-accent hover:text-accent-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring',
            )}
          >
            <picture className="shrink-0">
              <source media="(min-width: 768px)" srcSet="/logo/full_logo_v1.png" />
              <img src="/logo/only_x_logo_v1.png" alt="T◎RRX" className="h-6 w-auto" decoding="async" />
            </picture>
            <span className="hidden sm:inline-flex select-none">T◎RRX</span>
          </NavLink>

          <NavLink to="/discover" className={({ isActive }) => cn(navBtnClass(isActive), 'hidden sm:inline-flex')}>
            <Search className="h-4 w-4" />
            <span>Search</span>
          </NavLink>
          <NavLink
            to="/discover"
            aria-label="Search"
            className={({ isActive }) =>
              cn(
                'inline-flex h-10 w-10 items-center justify-center rounded-md transition-colors sm:hidden',
                isActive ? 'bg-accent text-accent-foreground' : 'text-muted-foreground hover:bg-accent hover:text-accent-foreground',
              )
            }
          >
            <Search className="h-4 w-4" />
          </NavLink>

          <Button variant="secondary" className="hidden sm:inline-flex" onClick={() => setIsAddModalOpen(true)}>
            <Plus className="h-4 w-4" />
            <span>Add torrent</span>
          </Button>
          <Button
            variant="secondary"
            size="icon"
            className="sm:hidden"
            onClick={() => setIsAddModalOpen(true)}
            aria-label="Add torrent"
          >
            <Plus className="h-4 w-4" />
          </Button>
        </div>

        <div className="flex min-w-0 items-center justify-center gap-3">
          <nav className="flex items-center justify-center gap-1">
            <NavLink to="/" end className={({ isActive }) => navBtnClass(isActive)} aria-label="Catalog">
              <Library className="h-4 w-4" />
              <span className="hidden sm:block">Catalog</span>
            </NavLink>
            <NavLink to={playerPath} className={({ isActive }) => navBtnClass(isActive)} aria-label="Player">
              <Film className="h-4 w-4" />
              <span className="hidden sm:block">Player</span>
            </NavLink>
          </nav>

          <div className="hidden min-w-0 items-center rounded-full border border-border/70 bg-card/60 px-3 py-1.5 text-xs shadow-soft md:flex">
            {isMetaLoading ? (
              <span className="text-muted-foreground">Loading…</span>
            ) : (
              <div className="flex min-w-0 items-center gap-1.5 whitespace-nowrap">
                <span className="font-medium text-foreground/85">{counts.total} total</span>
                <span className="text-muted-foreground">·</span>
                <span className="inline-flex items-center rounded-full border border-emerald-500/30 bg-emerald-500/10 px-2 py-0.5 font-medium text-emerald-700 dark:text-emerald-300">
                  {counts.active} active
                </span>
                <span className="text-muted-foreground">·</span>
                <span className="inline-flex items-center rounded-full border border-sky-500/30 bg-sky-500/10 px-2 py-0.5 font-medium text-sky-700 dark:text-sky-300">
                  {counts.completed} completed
                </span>
                <span className="text-muted-foreground">·</span>
                <span className="inline-flex items-center rounded-full border border-amber-500/30 bg-amber-500/10 px-2 py-0.5 font-medium text-amber-700 dark:text-amber-300">
                  {counts.stopped} stopped
                </span>
              </div>
            )}
          </div>
        </div>

        <div className="flex items-center justify-end gap-2">
          <NavLink to="/diagnostics" className={({ isActive }) => cn(navBtnClass(isActive), 'hidden md:inline-flex')} aria-label="Diagnostics">
            <Activity className="h-4 w-4" />
            <span>Diagnostics</span>
          </NavLink>
          <NavLink to="/settings" className={({ isActive }) => navBtnClass(isActive)} aria-label="Settings">
            <Settings className="h-4 w-4" />
            <span className="hidden sm:block">Settings</span>
          </NavLink>

          <div className="flex items-center gap-2 rounded-full border border-border/70 bg-card/60 px-3 py-2 shadow-soft">
            <Sun className={cn('h-4 w-4', isDark ? 'text-muted-foreground' : 'text-foreground')} />
            <Switch checked={isDark} onCheckedChange={(checked) => setTheme(checked ? 'dark' : 'light')} aria-label="Toggle theme" />
            <Moon className={cn('h-4 w-4', isDark ? 'text-foreground' : 'text-muted-foreground')} />
          </div>
        </div>

        <AddTorrentModal
          open={isAddModalOpen}
          onClose={() => setIsAddModalOpen(false)}
          onCreated={(torrent) => {
            localStorage.setItem('lastWatch', JSON.stringify({ torrentId: torrent.id }));
            window.dispatchEvent(new Event('player:last-watch'));
            window.dispatchEvent(new Event('torrents:refresh'));
          }}
        />
      </div>
    </header>
  );
};

export default Header;
