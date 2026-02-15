import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { Activity, ChevronDown, ChevronRight, Loader2, RefreshCw, Search, Zap } from 'lucide-react';
import { getSearchProviderDiagnostics, isApiError, testSearchProvider } from '../api';
import { Button } from '../components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '../components/ui/card';
import { Input } from '../components/ui/input';
import type { SearchProviderDiagnostics, SearchProviderTestResult } from '../types';
import { formatDate } from '../utils';

const ProviderDiagnosticsPage: React.FC = () => {
  const [items, setItems] = useState<SearchProviderDiagnostics[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [query, setQuery] = useState('spider man');
  const [testRunning, setTestRunning] = useState<Record<string, boolean>>({});
  const [testResult, setTestResult] = useState<Record<string, SearchProviderTestResult>>({});
  const [expandedIndexers, setExpandedIndexers] = useState<Record<string, boolean>>({});

  const loadDiagnostics = useCallback(async () => {
    setLoading(true);
    try {
      const next = await getSearchProviderDiagnostics();
      setItems(next);
      setError(null);
    } catch (err) {
      if (isApiError(err)) setError(`${err.code ?? 'error'}: ${err.message}`);
      else if (err instanceof Error) setError(err.message);
      else setError('Failed to load provider diagnostics');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadDiagnostics();
  }, [loadDiagnostics]);

  const runTest = useCallback(async (providerName: string) => {
    const name = providerName.trim().toLowerCase();
    if (!name) return;
    setTestRunning((prev) => ({ ...prev, [name]: true }));
    try {
      const result = await testSearchProvider(name, query);
      setTestResult((prev) => ({ ...prev, [name]: result }));
    } finally {
      setTestRunning((prev) => ({ ...prev, [name]: false }));
    }
  }, [query]);

  const runTestAll = useCallback(async () => {
    for (const item of items) {
      if (!item.enabled) continue;
      await runTest(item.name);
    }
  }, [items, runTest]);

  const rows = useMemo(() => [...items].sort((a, b) => a.name.localeCompare(b.name)), [items]);

  return (
    <div className="flex w-full flex-col gap-4">
      <Card>
        <CardHeader className="gap-4 sm:flex-row sm:items-end sm:justify-between">
          <div>
            <CardTitle className="flex items-center gap-2">
              <Activity className="h-4 w-4 text-primary" />
              Provider Diagnostics
            </CardTitle>
            <CardDescription>Health, latency and manual test for each search provider.</CardDescription>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Test query"
              className="w-[16rem]"
            />
            <Button variant="outline" onClick={() => void runTestAll()} disabled={loading || rows.length === 0}>
              <Search className="h-4 w-4" />
              Test all
            </Button>
            <Button variant="outline" onClick={() => void loadDiagnostics()} disabled={loading}>
              <RefreshCw className={`h-4 w-4 ${loading ? 'animate-spin' : ''}`} />
              Reload
            </Button>
          </div>
        </CardHeader>
        <CardContent className="space-y-3">
          {error ? (
            <div className="rounded-lg border border-destructive/30 bg-destructive/10 px-4 py-3 text-sm">{error}</div>
          ) : null}

          {rows.length === 0 && !loading ? (
            <div className="rounded-lg border border-border/70 bg-muted/10 px-4 py-6 text-sm text-muted-foreground">
              No providers available.
            </div>
          ) : null}

          <div className="grid gap-3">
            {rows.map((item) => {
              const name = item.name.toLowerCase();
              const running = Boolean(testRunning[name]);
              const latestTest = testResult[name];
              const healthTone =
                !item.enabled
                  ? 'text-muted-foreground'
                  : item.consecutiveFailures > 0 || item.lastTimeout
                    ? 'text-amber-600 dark:text-amber-300'
                    : 'text-emerald-700 dark:text-emerald-300';

              const hasSubIndexers = item.subIndexers && item.subIndexers.length > 0;
              const indexersExpanded = Boolean(expandedIndexers[name]);

              return (
                <div key={item.name} className="rounded-xl border border-border/70 bg-card/60 p-4">
                  <div className="flex flex-wrap items-center justify-between gap-3">
                    <div className="min-w-0">
                      <div className="flex items-center gap-2 truncate text-sm font-semibold">
                        {item.label || item.name}
                        {item.fanOut ? (
                          <span className="inline-flex items-center gap-1 rounded-full bg-sky-500/15 px-2 py-0.5 text-[10px] font-medium text-sky-700 dark:text-sky-300">
                            <Zap className="h-3 w-3" />
                            fan-out
                          </span>
                        ) : null}
                      </div>
                      <div className="text-xs text-muted-foreground">
                        {item.kind} · {item.enabled ? 'enabled' : 'disabled'}
                        {hasSubIndexers ? ` · ${item.subIndexers!.length} indexers` : ''}
                      </div>
                    </div>
                    <div className={`text-xs font-medium ${healthTone}`}>
                      failures: {item.consecutiveFailures} · latency: {item.lastLatencyMs ?? 0} ms
                    </div>
                  </div>

                  <div className="mt-3 grid gap-2 text-xs text-muted-foreground sm:grid-cols-2 lg:grid-cols-4">
                    <div>Last success: {item.lastSuccessAt ? formatDate(item.lastSuccessAt) : '-'}</div>
                    <div>Last failure: {item.lastFailureAt ? formatDate(item.lastFailureAt) : '-'}</div>
                    <div>Timeouts: {item.timeoutCount ?? 0}</div>
                    <div>Blocked until: {item.blockedUntil ? formatDate(item.blockedUntil) : '-'}</div>
                  </div>

                  {item.lastError ? (
                    <div className="mt-2 rounded-md border border-amber-500/25 bg-amber-500/10 px-2.5 py-1.5 text-xs text-amber-700 dark:text-amber-300">
                      {item.lastError}
                    </div>
                  ) : null}

                  {hasSubIndexers ? (
                    <div className="mt-2">
                      <button
                        type="button"
                        className="flex items-center gap-1 text-xs font-medium text-muted-foreground hover:text-foreground transition-colors"
                        onClick={() => setExpandedIndexers((prev) => ({ ...prev, [name]: !prev[name] }))}
                      >
                        {indexersExpanded
                          ? <ChevronDown className="h-3.5 w-3.5" />
                          : <ChevronRight className="h-3.5 w-3.5" />}
                        Indexers ({item.subIndexers!.length})
                      </button>
                      {indexersExpanded ? (
                        <div className="mt-1.5 flex flex-wrap gap-1.5">
                          {item.subIndexers!.map((idx) => (
                            <span
                              key={idx.id}
                              className="inline-block rounded-md border border-border/60 bg-muted/30 px-2 py-0.5 text-[11px] text-muted-foreground"
                              title={idx.id}
                            >
                              {idx.name || idx.id}
                            </span>
                          ))}
                        </div>
                      ) : null}
                    </div>
                  ) : null}

                  <div className="mt-3 flex flex-wrap items-center justify-between gap-2 border-t border-border/60 pt-3">
                    <div className="text-xs text-muted-foreground">
                      requests: {item.totalRequests ?? 0} · failures: {item.totalFailures ?? 0}
                    </div>
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => void runTest(item.name)}
                      disabled={running || !item.enabled}
                    >
                      {running ? <Loader2 className="h-4 w-4 animate-spin" /> : <Search className="h-4 w-4" />}
                      Test
                    </Button>
                  </div>

                  {latestTest ? (
                    <div className="mt-2 rounded-md border border-border/70 bg-muted/20 px-2.5 py-2 text-xs">
                      <div className="font-medium">
                        {latestTest.ok ? 'OK' : 'Failed'} · {latestTest.elapsedMs} ms · {latestTest.count ?? 0} items
                      </div>
                      {latestTest.error ? <div className="mt-1 text-destructive">{latestTest.error}</div> : null}
                      {latestTest.sample && latestTest.sample.length > 0 ? (
                        <div className="mt-1 text-muted-foreground">{latestTest.sample.join(' | ')}</div>
                      ) : null}
                    </div>
                  ) : null}
                </div>
              );
            })}
          </div>
        </CardContent>
      </Card>
    </div>
  );
};

export default ProviderDiagnosticsPage;

