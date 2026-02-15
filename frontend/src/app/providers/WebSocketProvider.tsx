import React, { createContext, useContext } from 'react';
import { useWebSocket } from '../../hooks/useWebSocket';
import type { SessionState, TorrentSummary, PlayerSettings, PlayerHealth } from '../../types';

type WSStatus = 'connecting' | 'connected' | 'disconnected';

type WebSocketContextValue = {
  status: WSStatus;
  states: SessionState[] | null;
  torrents: TorrentSummary[] | null;
  playerSettings: PlayerSettings | null;
  health: PlayerHealth | null;
};

const defaultValue: WebSocketContextValue = {
  status: 'disconnected',
  states: null,
  torrents: null,
  playerSettings: null,
  health: null,
};

const WebSocketContext = createContext<WebSocketContextValue>(defaultValue);

export function WebSocketProvider({ children }: { children: React.ReactNode }) {
  const ws = useWebSocket(true);
  return <WebSocketContext.Provider value={ws}>{children}</WebSocketContext.Provider>;
}

export function useWS() {
  return useContext(WebSocketContext);
}
