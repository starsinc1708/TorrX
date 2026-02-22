import React, { useCallback, useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { AlertTriangle } from 'lucide-react';
import TorrentDetails from '../components/TorrentDetails';
import TorrentList from '../components/TorrentList';
import { Alert } from '../components/ui/alert';
import { Button } from '../components/ui/button';
import { useSessionState } from '../hooks/useSessionState';
import { useTorrents } from '../hooks/useTorrents';
import { useCatalogMeta } from '../app/providers/CatalogMetaProvider';
import type { SessionState } from '../types';
import { upsertTorrentWatchState } from '../watchState';

const CatalogPage: React.FC = () => {
  const navigate = useNavigate();
  const [showDetails, setShowDetails] = useState(false);
  const { tags: allTags } = useCatalogMeta();

  const {
    torrents,
    selectedTorrent,
    selectedId,
    activeStateMap,
    watchHistoryByTorrent,
    statusFilter,
    searchQuery,
    tagsQuery,
    sortBy,
    sortOrder,
    selectedBulkIds,
    errorMessage,
    currentTorrentId,
    prioritizeActiveFileOnly,
    setStatusFilter,
    setSearchQuery,
    setTagsQuery,
    setSortBy,
    setSortOrder,
    selectTorrent,
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
    wsStates,
  } = useTorrents();

  const { sessionState, setAutoRefreshState } = useSessionState(selectedId, wsStates);
  const effectiveSessionState = useMemo<SessionState | null>(() => {
    if (sessionState) return sessionState;
    if (!selectedId) return null;
    return activeStateMap.get(selectedId) ?? null;
  }, [sessionState, selectedId, activeStateMap]);

  const handleSelectTorrent = useCallback(
    (id: string) => {
      selectTorrent(id);
      setAutoRefreshState(true);
    },
    [selectTorrent, setAutoRefreshState],
  );

  const handleOpenDetails = useCallback(
    (id: string) => {
      selectTorrent(id);
      setShowDetails(true);
      setAutoRefreshState(true);
    },
    [selectTorrent, setAutoRefreshState],
  );

  const handleBack = useCallback(() => {
    setShowDetails(false);
  }, []);

  const handleWatchFile = useCallback(
    (torrentId: string, fileIndex: number) => {
      navigate(`/watch/${torrentId}/${fileIndex}`);
    },
    [navigate],
  );

  const handleWatchTorrent = useCallback(
    (torrentId: string) => {
      navigate(`/watch/${torrentId}`);
    },
    [navigate],
  );

  const handleResumeWatch = useCallback(
    (torrentId: string, fileIndex: number, position: number, duration: number, torrentName: string, filePath: string) => {
      upsertTorrentWatchState({
        torrentId,
        fileIndex,
        position,
        duration,
        torrentName: torrentName || undefined,
        filePath: filePath || undefined,
      });
      navigate(`/watch/${torrentId}/${fileIndex}`, {
        state: {
          resume: true,
          torrentId,
          fileIndex,
          position,
          duration,
          torrentName,
          filePath,
        },
      });
    },
    [navigate],
  );

  const handleSetCurrent = useCallback(
    async (torrentId: string) => {
      await handleSetCurrentById(torrentId);
    },
    [handleSetCurrentById],
  );

  const handleClearFilters = useCallback(() => {
    setSearchQuery('');
    setTagsQuery('');
    setStatusFilter('all');
    setSortBy('updatedAt');
    setSortOrder('desc');
  }, [setSearchQuery, setTagsQuery, setStatusFilter, setSortBy, setSortOrder]);

  return (
    <div className="flex flex-col gap-4">
      {errorMessage ? (
        <Alert className="border-destructive/30 bg-destructive/10">
          <div className="flex items-center justify-between gap-4">
            <div className="flex items-center gap-2">
              <AlertTriangle className="h-4 w-4" />
              <div className="text-sm">{errorMessage}</div>
            </div>
            <Button variant="outline" size="sm" onClick={clearError}>
              Dismiss
            </Button>
          </div>
        </Alert>
      ) : null}

      {showDetails && selectedTorrent ? (
        <TorrentDetails
          torrent={selectedTorrent}
          sessionState={effectiveSessionState}
          onBack={handleBack}
          onStart={handleStart}
          onStop={handleStop}
          onDelete={handleDelete}
          onWatchFile={handleWatchFile}
          onUpdateTags={(tagsInput) => handleUpdateTagsById(selectedTorrent.id, tagsInput)}
        />
      ) : (
        <TorrentList
          torrents={torrents}
          activeStateMap={activeStateMap}
          watchHistoryByTorrent={watchHistoryByTorrent}
          currentTorrentId={currentTorrentId}
          prioritizeActiveFileOnly={prioritizeActiveFileOnly}
          allTags={allTags}
          statusFilter={statusFilter}
          searchQuery={searchQuery}
          tagsQuery={tagsQuery}
          sortBy={sortBy}
          sortOrder={sortOrder}
          selectedBulkIds={selectedBulkIds}
          onSelect={handleSelectTorrent}
          onWatch={handleWatchTorrent}
          onResumeWatch={handleResumeWatch}
          onStart={handleStartById}
          onStop={handleStopById}
          onBulkStart={handleBulkStart}
          onBulkStop={handleBulkStop}
          onBulkDelete={handleBulkDelete}
          onToggleBulkSelect={toggleBulkSelection}
          onSetBulkSelection={setBulkSelection}
          onClearBulkSelection={clearBulkSelection}
          onSetCurrent={handleSetCurrent}
          onOpenDetails={handleOpenDetails}
          onFilterChange={setStatusFilter}
          onSearchChange={setSearchQuery}
          onTagsChange={setTagsQuery}
          onSortByChange={setSortBy}
          onSortOrderChange={setSortOrder}
          onClearFilters={handleClearFilters}
        />
      )}
    </div>
  );
};

export default CatalogPage;
