import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useWS } from '../app/providers/WebSocketProvider';
import {
  bulkDeleteTorrents,
  bulkStartTorrents,
  bulkStopTorrents,
  createTorrentFromFile,
  createTorrentFromMagnet,
  deleteTorrent,
  getWatchHistory,
  getPlayerSettings,
  isApiError,
  listActiveStates,
  listTorrents,
  startTorrent,
  stopTorrent,
  updateTorrentTags,
  updatePlayerSettings,
} from '../api';
import type {
  SessionState,
  SortOrder,
  TorrentRecord,
  TorrentSortBy,
  TorrentStatusFilter,
  WatchPosition,
} from '../types';

const parseTagsInput = (value: string): string[] => {
  const parts = value
    .split(',')
    .map((tag) => tag.trim())
    .filter((tag) => tag.length > 0);
  const unique = new Set<string>();
  for (const tag of parts) {
    unique.add(tag);
  }
  return Array.from(unique);
};

const mergeBulkError = (prefix: string, failures: Array<{ id: string; error?: string }>) => {
  if (failures.length === 0) return null;
  const first = failures[0];
  const suffix = failures.length > 1 ? ` (+${failures.length - 1} more)` : '';
  return `${prefix}: ${first.id}${first.error ? ` - ${first.error}` : ''}${suffix}`;
};

export function useTorrents() {
  const [statusFilter, setStatusFilter] = useState<TorrentStatusFilter>('all');
  const [searchQuery, setSearchQuery] = useState('');
  const [tagsQuery, setTagsQuery] = useState('');
  const [sortBy, setSortBy] = useState<TorrentSortBy>('updatedAt');
  const [sortOrder, setSortOrder] = useState<SortOrder>('desc');
  const [torrents, setTorrents] = useState<TorrentRecord[]>([]);
  const [selectedBulkIds, setSelectedBulkIds] = useState<string[]>([]);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [activeStates, setActiveStates] = useState<SessionState[]>([]);
  const [watchHistory, setWatchHistory] = useState<WatchPosition[]>([]);
  const [loading, setLoading] = useState(false);
  const [creating, setCreating] = useState(false);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const [currentTorrentId, setCurrentTorrentId] = useState<string | null>(null);
  const playerSettingsRequestRef = useRef(0);
  const actionInFlightRef = useRef(false);
  const refreshInFlightRef = useRef(false);
  const activeStatesInFlightRef = useRef(false);
  const watchHistoryInFlightRef = useRef(false);
  const refreshActiveStatesRef = useRef<() => Promise<void>>();
  const refreshWatchHistoryRef = useRef<() => Promise<void>>();
  const selectedIdRef = useRef(selectedId);
  selectedIdRef.current = selectedId;
  const { status: wsStatus, states: wsStates, torrents: wsTorrents, playerSettings: wsPlayerSettings } = useWS();
  const parsedTags = useMemo(() => parseTagsInput(tagsQuery), [tagsQuery]);

  const activeStateMap = useMemo(() => {
    const map = new Map<string, SessionState>();
    activeStates.forEach((s) => map.set(s.id, s));
    return map;
  }, [activeStates]);

  const selectedTorrent = useMemo(
    () => torrents.find((t) => t.id === selectedId) ?? null,
    [torrents, selectedId],
  );

  const watchHistoryByTorrent = useMemo(() => {
    const map = new Map<string, WatchPosition[]>();
    for (const entry of watchHistory) {
      const torrentId = String(entry.torrentId ?? '').trim();
      if (!torrentId) continue;
      const items = map.get(torrentId) ?? [];
      items.push(entry);
      map.set(torrentId, items);
    }
    for (const [torrentId, items] of map.entries()) {
      items.sort((a, b) => {
        const ta = new Date(a.updatedAt ?? 0).getTime();
        const tb = new Date(b.updatedAt ?? 0).getTime();
        return tb - ta;
      });
      map.set(torrentId, items);
    }
    return map;
  }, [watchHistory]);

  const counts = useMemo(() => {
    const active = torrents.filter((t) => t.status === 'active').length;
    const completed = torrents.filter((t) => t.status === 'completed').length;
    const stopped = torrents.filter((t) => t.status === 'stopped').length;
    return { active, completed, stopped, total: torrents.length };
  }, [torrents]);

  const handleError = useCallback((error: unknown) => {
    if (isApiError(error)) {
      setErrorMessage(`${error.code ?? 'error'}: ${error.message}`);
    } else if (error instanceof Error) {
      setErrorMessage(error.message);
    } else {
      setErrorMessage('Unexpected error');
    }
  }, []);

  const clearError = useCallback(() => setErrorMessage(null), []);

  const refreshActiveStates = useCallback(async () => {
    if (activeStatesInFlightRef.current) return;
    activeStatesInFlightRef.current = true;
    try {
      const data = await listActiveStates();
      setActiveStates(data.items ?? []);
    } catch (error) {
      if (isApiError(error) && error.code === 'timeout') {
        return;
      }
      handleError(error);
    } finally {
      activeStatesInFlightRef.current = false;
    }
  }, [handleError]);
  refreshActiveStatesRef.current = refreshActiveStates;

  const refreshWatchHistory = useCallback(async () => {
    if (watchHistoryInFlightRef.current) return;
    watchHistoryInFlightRef.current = true;
    try {
      const positions = await getWatchHistory(300);
      setWatchHistory(Array.isArray(positions) ? positions : []);
    } catch (error) {
      if (isApiError(error) && error.code === 'timeout') {
        return;
      }
      handleError(error);
    } finally {
      watchHistoryInFlightRef.current = false;
    }
  }, [handleError]);
  refreshWatchHistoryRef.current = refreshWatchHistory;

  const refreshTorrents = useCallback(async () => {
    if (refreshInFlightRef.current) return;
    refreshInFlightRef.current = true;
    setLoading(true);
    try {
      const data = await listTorrents({
        status: statusFilter,
        view: 'full',
        search: searchQuery.trim() || undefined,
        tags: parsedTags,
        sortBy,
        sortOrder,
      });
      const items = 'items' in data ? (data.items as TorrentRecord[]) : [];
      setTorrents(items);
      setSelectedBulkIds((prev) => prev.filter((id) => items.some((item) => item.id === id)));
      setErrorMessage(null);
      void refreshActiveStatesRef.current?.();
      void refreshWatchHistoryRef.current?.();
      const requestID = playerSettingsRequestRef.current + 1;
      playerSettingsRequestRef.current = requestID;
      getPlayerSettings()
        .then((settings) => {
          if (playerSettingsRequestRef.current !== requestID) return;
          setCurrentTorrentId(settings.currentTorrentId ?? null);
        })
        .catch(() => {});

      const currentSelectedId = selectedIdRef.current;
      if (!currentSelectedId && items.length > 0) {
        setSelectedId(items[0].id);
      } else if (currentSelectedId && !items.find((t) => t.id === currentSelectedId)) {
        setSelectedId(items[0]?.id ?? null);
      }
    } catch (error) {
      if (isApiError(error) && error.code === 'timeout') {
        return;
      }
      handleError(error);
    } finally {
      setLoading(false);
      refreshInFlightRef.current = false;
    }
  }, [statusFilter, searchQuery, parsedTags, sortBy, sortOrder, handleError]);

  useEffect(() => {
    refreshTorrents();
    const handleRefresh = () => { refreshTorrents(); };
    window.addEventListener('torrents:refresh', handleRefresh);

    // If WebSocket is connected, reduce polling to 60s since WS pushes updates.
    const interval = wsStatus === 'connected' ? 60000 : 5000;
    const timer = window.setInterval(refreshTorrents, interval);
    return () => {
      window.clearInterval(timer);
      window.removeEventListener('torrents:refresh', handleRefresh);
    };
  }, [refreshTorrents, wsStatus]);

  useEffect(() => {
    if (wsStates) {
      setActiveStates(wsStates);
    }
  }, [wsStates]);

  // When WS pushes torrent summaries, trigger a full REST refresh if the set of IDs has changed.
  useEffect(() => {
    if (!wsTorrents) return;
    const currentIds = new Set(torrents.map((t) => t.id));
    const wsIds = new Set(wsTorrents.map((t) => t.id));
    const changed = currentIds.size !== wsIds.size || [...wsIds].some((id) => !currentIds.has(id));
    if (changed) {
      void refreshTorrents();
    }
  }, [wsTorrents]); // eslint-disable-line react-hooks/exhaustive-deps

  // When WS pushes player settings, update currentTorrentId.
  useEffect(() => {
    if (!wsPlayerSettings) return;
    setCurrentTorrentId(wsPlayerSettings.currentTorrentId ?? null);
  }, [wsPlayerSettings]);

  const handleCreate = useCallback(
    async (mode: 'magnet' | 'file', magnet: string, file: File | null, name: string) => {
      setCreating(true);
      try {
        let record: TorrentRecord | null = null;
        if (mode === 'magnet') {
          if (!magnet.trim()) {
            setErrorMessage('Magnet link is required');
            return;
          }
          record = await createTorrentFromMagnet(magnet.trim(), name.trim() || undefined);
        } else {
          if (!file) {
            setErrorMessage('Torrent file is required');
            return;
          }
          record = await createTorrentFromFile(file, name.trim() || undefined);
        }
        setErrorMessage(null);
        if (record) setSelectedId(record.id);
        await refreshTorrents();
      } catch (error) {
        handleError(error);
      } finally {
        setCreating(false);
      }
    },
    [handleError, refreshTorrents],
  );

  const handleStart = useCallback(async () => {
    if (!selectedTorrent || actionInFlightRef.current) return;
    actionInFlightRef.current = true;
    try {
      await startTorrent(selectedTorrent.id);
      await refreshTorrents();
    } catch (error) {
      handleError(error);
    } finally {
      actionInFlightRef.current = false;
    }
  }, [selectedTorrent, refreshTorrents, handleError]);

  const handleStartById = useCallback(
    async (id: string) => {
      if (!id) return;
      try {
        await startTorrent(id);
        await refreshTorrents();
      } catch (error) {
        handleError(error);
      }
    },
    [refreshTorrents, handleError],
  );

  const handleStop = useCallback(async () => {
    if (!selectedTorrent || actionInFlightRef.current) return;
    actionInFlightRef.current = true;
    try {
      await stopTorrent(selectedTorrent.id);
      await refreshTorrents();
    } catch (error) {
      handleError(error);
    } finally {
      actionInFlightRef.current = false;
    }
  }, [selectedTorrent, refreshTorrents, handleError]);

  const handleStopById = useCallback(
    async (id: string) => {
      if (!id) return;
      try {
        await stopTorrent(id);
        await refreshTorrents();
      } catch (error) {
        handleError(error);
      }
    },
    [refreshTorrents, handleError],
  );

  const handleDelete = useCallback(
    async (removeFiles: boolean) => {
      if (!selectedTorrent || actionInFlightRef.current) return;
      actionInFlightRef.current = true;
      try {
        await deleteTorrent(selectedTorrent.id, removeFiles);
        if (selectedTorrent.id === currentTorrentId) {
          await updatePlayerSettings({ currentTorrentId: null }).catch(() => {});
          playerSettingsRequestRef.current += 1;
          setCurrentTorrentId(null);
          window.dispatchEvent(new Event('player:current-torrent'));
        }
        setSelectedId(null);
        await refreshTorrents();
      } catch (error) {
        handleError(error);
      } finally {
        actionInFlightRef.current = false;
      }
    },
    [selectedTorrent, currentTorrentId, refreshTorrents, handleError],
  );

  const handleUpdateTagsById = useCallback(
    async (id: string, tagsInput: string): Promise<boolean> => {
      if (!id) return false;
      try {
        const tags = parseTagsInput(tagsInput);
        await updateTorrentTags(id, tags);
        await refreshTorrents();
        return true;
      } catch (error) {
        handleError(error);
        return false;
      }
    },
    [refreshTorrents, handleError],
  );

  const handleBulkStart = useCallback(
    async (ids: string[]): Promise<boolean> => {
      const unique = Array.from(new Set(ids.filter(Boolean)));
      if (unique.length === 0) return false;
      try {
        const response = await bulkStartTorrents(unique);
        const failures = response.items.filter((item) => !item.ok);
        const bulkError = mergeBulkError('Bulk start failed', failures);
        setErrorMessage(bulkError);
        await refreshTorrents();
        return failures.length === 0;
      } catch (error) {
        handleError(error);
        return false;
      }
    },
    [refreshTorrents, handleError],
  );

  const handleBulkStop = useCallback(
    async (ids: string[]): Promise<boolean> => {
      const unique = Array.from(new Set(ids.filter(Boolean)));
      if (unique.length === 0) return false;
      try {
        const response = await bulkStopTorrents(unique);
        const failures = response.items.filter((item) => !item.ok);
        const bulkError = mergeBulkError('Bulk stop failed', failures);
        setErrorMessage(bulkError);
        await refreshTorrents();
        return failures.length === 0;
      } catch (error) {
        handleError(error);
        return false;
      }
    },
    [refreshTorrents, handleError],
  );

  const handleBulkDelete = useCallback(
    async (ids: string[], removeFiles: boolean): Promise<boolean> => {
      const unique = Array.from(new Set(ids.filter(Boolean)));
      if (unique.length === 0) return false;
      try {
        const response = await bulkDeleteTorrents(unique, removeFiles);
        const failures = response.items.filter((item) => !item.ok);
        const successIDs = response.items.filter((item) => item.ok).map((item) => item.id);

        if (currentTorrentId && successIDs.includes(currentTorrentId)) {
          await updatePlayerSettings({ currentTorrentId: null }).catch(() => {});
          playerSettingsRequestRef.current += 1;
          setCurrentTorrentId(null);
          window.dispatchEvent(new Event('player:current-torrent'));
        }

        setSelectedBulkIds((prev) => prev.filter((id) => !successIDs.includes(id)));
        const bulkError = mergeBulkError('Bulk delete failed', failures);
        setErrorMessage(bulkError);
        await refreshTorrents();
        return failures.length === 0;
      } catch (error) {
        handleError(error);
        return false;
      }
    },
    [currentTorrentId, refreshTorrents, handleError],
  );

  const toggleBulkSelection = useCallback((id: string) => {
    setSelectedBulkIds((prev) => {
      if (prev.includes(id)) return prev.filter((item) => item !== id);
      return [...prev, id];
    });
  }, []);

  const setBulkSelection = useCallback((ids: string[]) => {
    setSelectedBulkIds(Array.from(new Set(ids.filter(Boolean))));
  }, []);

  const clearBulkSelection = useCallback(() => {
    setSelectedBulkIds([]);
  }, []);

  const selectTorrent = useCallback((id: string) => {
    setSelectedId(id);
  }, []);

  const handleSetCurrentById = useCallback(
    async (id: string): Promise<boolean> => {
      if (!id) return false;
      try {
        const nextCurrentId = currentTorrentId === id ? null : id;
        const settings = await updatePlayerSettings({ currentTorrentId: nextCurrentId });
        const current = settings.currentTorrentId ?? null;
        playerSettingsRequestRef.current += 1;
        setCurrentTorrentId(current);
        if (current) {
          localStorage.setItem('lastWatch', JSON.stringify({ torrentId: current }));
          window.dispatchEvent(new Event('player:last-watch'));
        }
        window.dispatchEvent(new Event('player:current-torrent'));
        await refreshActiveStates();
        return true;
      } catch (error) {
        handleError(error);
        return false;
      }
    },
    [currentTorrentId, refreshActiveStates, handleError],
  );

  return {
    torrents,
    selectedTorrent,
    selectedId,
    activeStates,
    activeStateMap,
    wsStatus,
    wsStates,
    watchHistoryByTorrent,
    counts,
    statusFilter,
    searchQuery,
    tagsQuery,
    sortBy,
    sortOrder,
    selectedBulkIds,
    loading,
    creating,
    errorMessage,
    currentTorrentId,
    setStatusFilter,
    setSearchQuery,
    setTagsQuery,
    setSortBy,
    setSortOrder,
    selectTorrent,
    refreshTorrents,
    refreshActiveStates,
    refreshWatchHistory,
    handleCreate,
    handleStart,
    handleStartById,
    handleStop,
    handleStopById,
    handleBulkStart,
    handleBulkStop,
    handleBulkDelete,
    handleUpdateTagsById,
    toggleBulkSelection,
    setBulkSelection,
    clearBulkSelection,
    handleSetCurrentById,
    handleDelete,
    clearError,
  };
}
