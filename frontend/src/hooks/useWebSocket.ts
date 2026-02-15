import { useCallback, useEffect, useRef, useState } from 'react';
import type { PlayerHealth, PlayerSettings, SessionState, TorrentSummary } from '../types';
import { buildUrl } from '../api';

type WSStatus = 'connecting' | 'connected' | 'disconnected';

interface WSMessage {
  type: string;
  data: unknown;
}

export function useWebSocket(enabled: boolean) {
  const [status, setStatus] = useState<WSStatus>('disconnected');
  const [states, setStates] = useState<SessionState[] | null>(null);
  const [torrents, setTorrents] = useState<TorrentSummary[] | null>(null);
  const [playerSettings, setPlayerSettings] = useState<PlayerSettings | null>(null);
  const [health, setHealth] = useState<PlayerHealth | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const reconnectTimerRef = useRef<number | null>(null);
  const reconnectAttemptsRef = useRef(0);
  const mountedRef = useRef(false);

  const connect = useCallback(() => {
    if (!enabled || !mountedRef.current) return;

    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${protocol}//${window.location.host}${buildUrl('/ws')}`;

    setStatus('connecting');
    const ws = new WebSocket(wsUrl);
    wsRef.current = ws;

    ws.onopen = () => {
      setStatus('connected');
      reconnectAttemptsRef.current = 0;
    };

    ws.onmessage = (event) => {
      try {
        const msg: WSMessage = JSON.parse(event.data as string);
        switch (msg.type) {
          case 'states':
            setStates(msg.data as SessionState[]);
            break;
          case 'torrents':
            setTorrents(msg.data as TorrentSummary[]);
            break;
          case 'player_settings':
            setPlayerSettings(msg.data as PlayerSettings);
            break;
          case 'health':
            setHealth(msg.data as PlayerHealth);
            break;
        }
      } catch {
        // Ignore malformed messages.
      }
    };

    ws.onclose = () => {
      setStatus('disconnected');
      wsRef.current = null;
      // Only attempt reconnect if the component is still mounted.
      if (!mountedRef.current) return;

      // Clear any existing reconnect timer before scheduling new one
      if (reconnectTimerRef.current !== null) {
        clearTimeout(reconnectTimerRef.current);
      }

      const delay = Math.min(1000 * Math.pow(2, reconnectAttemptsRef.current), 30000);
      reconnectAttemptsRef.current += 1;
      reconnectTimerRef.current = window.setTimeout(connect, delay);
    };

    ws.onerror = () => {
      ws.close();
    };
  }, [enabled]);

  useEffect(() => {
    mountedRef.current = true;
    if (enabled) {
      connect();
    }
    return () => {
      // Mark as unmounted FIRST to prevent any reconnect attempts.
      mountedRef.current = false;
      if (reconnectTimerRef.current) {
        clearTimeout(reconnectTimerRef.current);
        reconnectTimerRef.current = null;
      }
      if (wsRef.current) {
        wsRef.current.close();
        wsRef.current = null;
      }
    };
  }, [enabled, connect]);

  return { status, states, torrents, playerSettings, health };
}
