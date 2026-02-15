import React, { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import { useLocation } from 'react-router-dom';

import { listTorrents } from '../../api';
import { useWS } from './WebSocketProvider';
import type { TorrentSummary } from '../../types';

type CatalogCounts = {
  total: number;
  active: number;
  completed: number;
  stopped: number;
};

type CatalogMetaValue = {
  items: TorrentSummary[];
  tags: string[];
  counts: CatalogCounts;
  isLoading: boolean;
  refresh: () => void;
};

const CatalogMetaContext = createContext<CatalogMetaValue | null>(null);

const computeCounts = (items: TorrentSummary[]): CatalogCounts => {
  let active = 0;
  let completed = 0;
  let stopped = 0;
  for (const t of items) {
    if (t.status === 'active') active++;
    else if (t.status === 'completed') completed++;
    else if (t.status === 'stopped') stopped++;
  }
  return { total: items.length, active, completed, stopped };
};

const computeTags = (items: TorrentSummary[]) => {
  const unique = new Set<string>();
  for (const t of items) {
    for (const tag of t.tags ?? []) {
      const clean = String(tag).trim();
      if (clean) unique.add(clean);
    }
  }
  return Array.from(unique).sort((a, b) => a.localeCompare(b));
};

export function CatalogMetaProvider({ children }: { children: React.ReactNode }) {
  const [items, setItems] = useState<TorrentSummary[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const inFlightRef = useRef(false);
  const location = useLocation();
  const isPlayerRoute = location.pathname.startsWith('/watch');
  const { status: wsStatus, torrents: wsTorrents } = useWS();

  const refresh = useCallback(() => {
    if (inFlightRef.current) return;
    inFlightRef.current = true;
    setIsLoading(true);

    const limit = 500;
    const load = async () => {
      const first = await listTorrents({ status: 'all', view: 'summary', limit, offset: 0 });
      const total = 'count' in first ? Number(first.count) : 0;
      const firstItems = 'items' in first ? (first.items as TorrentSummary[]) : [];
      const acc = [...firstItems];

      // Best-effort pagination without assuming the backend supports arbitrary sizes.
      let offset = acc.length;
      let safety = 0;
      while (offset < total && safety < 20) {
        safety++;
        const page = await listTorrents({ status: 'all', view: 'summary', limit, offset });
        const pageItems = 'items' in page ? (page.items as TorrentSummary[]) : [];
        if (pageItems.length === 0) break;
        acc.push(...pageItems);
        offset = acc.length;
      }

      return acc;
    };

    load()
      .then((next) => setItems(next))
      .catch(() => {
        // Keep previous state; meta should never block the app.
      })
      .finally(() => {
        inFlightRef.current = false;
        setIsLoading(false);
      });
  }, []);

  // Use WS torrent summaries when available.
  useEffect(() => {
    if (!wsTorrents) return;
    setItems(wsTorrents);
    setIsLoading(false);
  }, [wsTorrents]);

  useEffect(() => {
    // Pause polling during video playback to free browser connections for HLS.
    if (isPlayerRoute) return;
    refresh();
    const onRefresh = () => refresh();
    // When WS connected, reduce polling from 15s to 60s since WS pushes updates.
    const interval = wsStatus === 'connected' ? 60000 : 15000;
    const timer = window.setInterval(refresh, interval);
    window.addEventListener('torrents:refresh', onRefresh);
    return () => {
      window.removeEventListener('torrents:refresh', onRefresh);
      window.clearInterval(timer);
    };
  }, [refresh, isPlayerRoute, wsStatus]);

  const value = useMemo<CatalogMetaValue>(() => {
    const tags = computeTags(items);
    const counts = computeCounts(items);
    return { items, tags, counts, isLoading, refresh };
  }, [items, isLoading, refresh]);

  return <CatalogMetaContext.Provider value={value}>{children}</CatalogMetaContext.Provider>;
}

export function useCatalogMeta() {
  const ctx = useContext(CatalogMetaContext);
  if (!ctx) throw new Error('useCatalogMeta must be used within CatalogMetaProvider');
  return ctx;
}

