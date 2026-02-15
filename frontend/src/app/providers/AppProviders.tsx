import React from 'react';
import { CatalogMetaProvider } from './CatalogMetaProvider';
import { ThemeAccentProvider } from './ThemeAccentProvider';
import { ToastProvider } from './ToastProvider';

export function AppProviders({ children }: { children: React.ReactNode }) {
  return (
    <ThemeAccentProvider>
      <CatalogMetaProvider>
        <ToastProvider>{children}</ToastProvider>
      </CatalogMetaProvider>
    </ThemeAccentProvider>
  );
}
