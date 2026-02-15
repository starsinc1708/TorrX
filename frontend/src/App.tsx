import React, { Suspense } from 'react';
import { Navigate, Route, Routes, useLocation } from 'react-router-dom';
import Header from './components/Header';
import CatalogPage from './pages/CatalogPage';
import SettingsPage from './pages/SettingsPage';
import SearchPage from './pages/SearchPage';
import ProviderDiagnosticsPage from './pages/ProviderDiagnosticsPage';
import RouteLoader from './components/RouteLoader';
import { cn } from './lib/cn';

const PlayerPage = React.lazy(() => import('./pages/PlayerPage'));

function AppShell() {
  const location = useLocation();
  const isPlayerRoute = location.pathname.startsWith('/watch');

  return (
    <div className="min-h-dvh">
      <div className="app-backdrop pointer-events-none fixed inset-0 -z-10" />
      <Header />
      <main
        className={cn(
          'w-full',
          isPlayerRoute ? 'p-0' : 'p-3 sm:p-4 md:p-5',
        )}
      >
        <Routes>
          <Route path="/" element={<CatalogPage />} />
          <Route path="/discover" element={<SearchPage />} />
          <Route
            path="/watch/:torrentId/:fileIndex?"
            element={
              <Suspense fallback={<RouteLoader label="Loading player" />}>
                <PlayerPage />
              </Suspense>
            }
          />
          <Route path="/settings" element={<SettingsPage />} />
          <Route path="/diagnostics" element={<ProviderDiagnosticsPage />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </main>
    </div>
  );
}

const App: React.FC = () => {
  return <AppShell />;
};

export default App;
