import React from 'react';
import { CatalogMetaProvider } from './CatalogMetaProvider';
import { SearchProvider } from './SearchProvider';
import { ThemeAccentProvider } from './ThemeAccentProvider';
import { ToastProvider } from './ToastProvider';
import { WebSocketProvider } from './WebSocketProvider';

export function AppProviders({ children }: { children: React.ReactNode }) {
  return (
    <ThemeAccentProvider>
      <WebSocketProvider>
        <CatalogMetaProvider>
          <ToastProvider>
            <SearchProvider>{children}</SearchProvider>
          </ToastProvider>
        </CatalogMetaProvider>
      </WebSocketProvider>
    </ThemeAccentProvider>
  );
}
