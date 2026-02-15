import { useCallback, useEffect, useRef, useState } from 'react';
import { getTorrentState, isApiError } from '../api';
import type { SessionState } from '../types';

export function useSessionState(selectedId: string | null, wsStates?: SessionState[] | null) {
  const [sessionState, setSessionState] = useState<SessionState | null>(null);
  const [autoRefreshState, setAutoRefreshState] = useState(false);
  const inFlightRef = useRef(false);
  const abortControllerRef = useRef<AbortController | null>(null);

  const refreshSessionState = useCallback(async () => {
    if (!selectedId) return;
    if (inFlightRef.current) return;

    // Abort previous request if still pending
    if (abortControllerRef.current) {
      abortControllerRef.current.abort();
    }

    const controller = new AbortController();
    abortControllerRef.current = controller;
    inFlightRef.current = true;

    try {
      const data = await getTorrentState(selectedId, controller.signal);
      setSessionState(data);
    } catch (error) {
      // Ignore abort errors
      if (error instanceof Error && error.name === 'AbortError') {
        return;
      }
      if (isApiError(error)) {
        console.error(`${error.code}: ${error.message}`);
      }
    } finally {
      inFlightRef.current = false;
      if (abortControllerRef.current === controller) {
        abortControllerRef.current = null;
      }
    }
  }, [selectedId]);

  useEffect(() => {
    setSessionState(null);
  }, [selectedId]);

  useEffect(() => {
    if (!autoRefreshState || !selectedId) return;
    void refreshSessionState();
  }, [autoRefreshState, selectedId, refreshSessionState]);

  useEffect(() => {
    if (wsStates && selectedId) {
      const match = wsStates.find((s) => s.id === selectedId);
      if (match) setSessionState(match);
    }
  }, [wsStates, selectedId]);

  useEffect(() => {
    if (wsStates) return; // WS provides updates, no polling needed.
    if (!autoRefreshState || !selectedId) return;
    const timer = window.setInterval(refreshSessionState, 15000);
    return () => {
      window.clearInterval(timer);
      // Abort any pending request on unmount
      if (abortControllerRef.current) {
        abortControllerRef.current.abort();
        abortControllerRef.current = null;
      }
    };
  }, [autoRefreshState, selectedId, refreshSessionState, wsStates]);

  return {
    sessionState,
    autoRefreshState,
    setAutoRefreshState,
    refreshSessionState,
  };
}
