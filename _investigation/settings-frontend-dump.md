# Frontend Settings + PWA Exploration Dump

## Manifest / Service Worker Search

```text
<none>
```

## frontend/public/ listing

```text
DIR  C:\1_Projects\torrent-stream\frontend\public\logo
FILE C:\1_Projects\torrent-stream\frontend\public\logo\full_logo_v1.png | size=2244125 | sha256=B68891E4569A1D749D772E74E51CE9CC68182780BCD90D55FD3BC25A785D6316
FILE C:\1_Projects\torrent-stream\frontend\public\logo\only_x_logo_v1.png | size=1614961 | sha256=4EA2AFA8079393AF5E1AF4B98E3A500F91041254FCE0A36B9FA70B2F1D3266AD
```

Note: `frontend/public` currently contains only binary PNG assets (no text files to inline).

## frontend/src/pages/SettingsPage.tsx

```tsx
import React, { useCallback, useEffect, useState } from 'react';
import { Check, KeyRound, Palette, RefreshCw } from 'lucide-react';
import {
  autodetectSearchProviderRuntimeConfig,
  applyFlareSolverrSettings,
  getFlareSolverrSettings,
  getEncodingSettings,
  getHLSSettings,
  getSearchProviderRuntimeConfigs,
  isApiError,
  listSearchProviders,
  updateSearchProviderRuntimeConfig,
  updateEncodingSettings,
  updateHLSSettings,
} from '../api';
import { useToast } from '../app/providers/ToastProvider';
import { useThemeAccent } from '../app/providers/ThemeAccentProvider';
import { ACCENT_PRESETS } from '../lib/design-system';
import { cn } from '../lib/cn';
import { Button } from '../components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '../components/ui/card';
import { Input } from '../components/ui/input';
import { Select } from '../components/ui/select';
import { Switch } from '../components/ui/switch';
import type {
  EncodingSettings,
  HLSSettings,
  FlareSolverrProviderStatus,
  SearchProviderInfo,
  SearchProviderRuntimeConfig,
} from '../types';
import { resolveEnabledSearchProviders, saveEnabledSearchProviders } from '../searchProviderSettings';

const qualityPresets = {
  fast: { preset: 'ultrafast', crf: 28 },
  balanced: { preset: 'veryfast', crf: 23 },
  quality: { preset: 'fast', crf: 20 },
  best: { preset: 'medium', crf: 18 },
} as const;

const qualityLevel = (s: EncodingSettings): string => {
  for (const [key, val] of Object.entries(qualityPresets)) {
    if (s.preset === val.preset && s.crf === val.crf) return key;
  }
  if (s.crf <= 20) return 'quality';
  if (s.crf <= 24) return 'balanced';
  return 'fast';
};

type FlareApplyTarget = 'all' | 'jackett' | 'prowlarr';

const SettingsPage: React.FC = () => {
  const { toast } = useToast();
  const {
    theme,
    resolvedTheme,
    setTheme,
    accentPreset,
    accentCustomHex,
    setAccentPreset,
    setAccentCustomHex,
    clearAccentCustom,
  } = useThemeAccent();

  // Encoding
  const [encodingSettings, setEncodingSettings] = useState<EncodingSettings | null>(null);
  const [encodingLoading, setEncodingLoading] = useState(false);
  const [encodingSaving, setEncodingSaving] = useState(false);
  const [encodingError, setEncodingError] = useState<string | null>(null);

  // Streaming (HLS)
  const [hlsSettings, setHlsSettings] = useState<HLSSettings | null>(null);
  const [hlsLoading, setHlsLoading] = useState(false);
  const [hlsSaving, setHlsSaving] = useState(false);
  const [hlsError, setHlsError] = useState<string | null>(null);
  const [hlsForm, setHlsForm] = useState({ ramBufSizeMB: '', prebufferMB: '', windowBeforeMB: '', windowAfterMB: '', segmentDuration: 4 });
  const [hlsMaxBuffer, setHlsMaxBuffer] = useState(() => Number(localStorage.getItem('hlsMaxBufferLength')) || 60);

  // Search sources
  const [searchProviders, setSearchProviders] = useState<SearchProviderInfo[]>([]);
  const [searchEnabledProviders, setSearchEnabledProviders] = useState<string[]>([]);
  const [searchLoading, setSearchLoading] = useState(false);
  const [searchError, setSearchError] = useState<string | null>(null);
  const [runtimeConfigs, setRuntimeConfigs] = useState<SearchProviderRuntimeConfig[]>([]);
  const [runtimeForm, setRuntimeForm] = useState<
    Record<string, { endpoint: string; proxyUrl: string; apiKey: string }>
  >({});
  const [runtimeLoading, setRuntimeLoading] = useState(false);
  const [runtimeSaving, setRuntimeSaving] = useState<string | null>(null);
  const [runtimeDetecting, setRuntimeDetecting] = useState<string | null>(null);
  const [runtimeError, setRuntimeError] = useState<string | null>(null);
  const [flareSolverrDefaultUrl, setFlareSolverrDefaultUrl] = useState('http://flaresolverr:8191/');
  const [flareSolverrUrl, setFlareSolverrUrl] = useState('http://flaresolverr:8191/');
  const [flareSolverrProviders, setFlareSolverrProviders] = useState<FlareSolverrProviderStatus[]>([]);
  const [flareSolverrLoading, setFlareSolverrLoading] = useState(false);
  const [flareSolverrApplyingTarget, setFlareSolverrApplyingTarget] = useState<FlareApplyTarget | null>(null);
  const [flareSolverrError, setFlareSolverrError] = useState<string | null>(null);

  const loadEncoding = useCallback(async () => {
    setEncodingLoading(true);
    try {
      const settings = await getEncodingSettings();
      setEncodingSettings(settings);
      setEncodingError(null);
    } catch (error) {
      if (isApiError(error)) setEncodingError(`${error.code ?? 'error'}: ${error.message}`);
      else if (error instanceof Error) setEncodingError(error.message);
    } finally {
      setEncodingLoading(false);
    }
  }, []);

  const loadHLSSettings = useCallback(async () => {
    setHlsLoading(true);
    try {
      const settings = await getHLSSettings();
      setHlsSettings(settings);
      setHlsForm({
        ramBufSizeMB: String(settings.ramBufSizeMB),
        prebufferMB: String(settings.prebufferMB),
        windowBeforeMB: String(settings.windowBeforeMB),
        windowAfterMB: String(settings.windowAfterMB),
        segmentDuration: settings.segmentDuration,
      });
      setHlsError(null);
    } catch (error) {
      if (isApiError(error)) setHlsError(`${error.code ?? 'error'}: ${error.message}`);
      else if (error instanceof Error) setHlsError(error.message);
    } finally {
      setHlsLoading(false);
    }
  }, []);

  const loadSearchProviders = useCallback(async () => {
    setSearchLoading(true);
    try {
      const providers = await listSearchProviders();
      setSearchProviders(providers);
      const configured = providers.filter((p) => p.enabled).map((p) => p.name);
      setSearchEnabledProviders(resolveEnabledSearchProviders(configured));
      setSearchError(null);
    } catch (error) {
      if (isApiError(error)) setSearchError(`${error.code ?? 'error'}: ${error.message}`);
      else if (error instanceof Error) setSearchError(error.message);
      else setSearchError('Failed to load providers');
    } finally {
      setSearchLoading(false);
    }
  }, []);

  const syncRuntimeForm = useCallback((items: SearchProviderRuntimeConfig[]) => {
    const next: Record<string, { endpoint: string; proxyUrl: string; apiKey: string }> = {};
    items.forEach((item) => {
      const key = item.name.toLowerCase();
      next[key] = {
        endpoint: item.endpoint ?? '',
        proxyUrl: item.proxyUrl ?? '',
        apiKey: '',
      };
    });
    setRuntimeForm(next);
  }, []);

  const loadRuntimeConfigs = useCallback(async () => {
    setRuntimeLoading(true);
    try {
      const items = await getSearchProviderRuntimeConfigs();
      setRuntimeConfigs(items);
      syncRuntimeForm(items);
      setRuntimeError(null);
    } catch (error) {
      if (isApiError(error)) setRuntimeError(`${error.code ?? 'error'}: ${error.message}`);
      else if (error instanceof Error) setRuntimeError(error.message);
      else setRuntimeError('Failed to load runtime settings');
    } finally {
      setRuntimeLoading(false);
    }
  }, [syncRuntimeForm]);

  const loadFlareSolverrSettings = useCallback(async () => {
    setFlareSolverrLoading(true);
    try {
      const settings = await getFlareSolverrSettings();
      const defaultUrl = settings.defaultUrl?.trim() || 'http://flaresolverr:8191/';
      setFlareSolverrDefaultUrl(defaultUrl);
      setFlareSolverrUrl(settings.url?.trim() || defaultUrl);
      setFlareSolverrProviders(settings.providers ?? []);
      setFlareSolverrError(null);
    } catch (error) {
      if (isApiError(error)) setFlareSolverrError(`${error.code ?? 'error'}: ${error.message}`);
      else if (error instanceof Error) setFlareSolverrError(error.message);
      else setFlareSolverrError('Failed to load FlareSolverr settings');
    } finally {
      setFlareSolverrLoading(false);
    }
  }, []);

  useEffect(() => {
    loadEncoding();
    loadHLSSettings();
    loadSearchProviders();
    loadRuntimeConfigs();
    loadFlareSolverrSettings();
  }, [loadEncoding, loadHLSSettings, loadSearchProviders, loadRuntimeConfigs, loadFlareSolverrSettings]);

  const flareConfiguredCount = flareSolverrProviders.filter((item) => item.configured).length;

  const handleUpdateEncoding = useCallback(async (patch: Partial<EncodingSettings>) => {
    setEncodingSaving(true);
    try {
      const updated = await updateEncodingSettings(patch);
      setEncodingSettings(updated);
      setEncodingError(null);
    } catch (error) {
      if (isApiError(error)) setEncodingError(`${error.code ?? 'error'}: ${error.message}`);
      else if (error instanceof Error) setEncodingError(error.message);
    } finally {
      setEncodingSaving(false);
    }
  }, []);

  const handleSaveHLSSettings = async () => {
    const ramBuf = Number(hlsForm.ramBufSizeMB);
    const prebuf = Number(hlsForm.prebufferMB);
    const winBefore = Number(hlsForm.windowBeforeMB);
    const winAfter = Number(hlsForm.windowAfterMB);
    const segDur = hlsForm.segmentDuration;
    if (!Number.isFinite(ramBuf) || !Number.isFinite(prebuf) || !Number.isFinite(winBefore) || !Number.isFinite(winAfter)) return;
    setHlsSaving(true);
    try {
      const updated = await updateHLSSettings({
        ramBufSizeMB: Math.floor(ramBuf),
        prebufferMB: Math.floor(prebuf),
        windowBeforeMB: Math.floor(winBefore),
        windowAfterMB: Math.floor(winAfter),
        segmentDuration: segDur,
      });
      setHlsSettings(updated);
      setHlsForm({
        ramBufSizeMB: String(updated.ramBufSizeMB),
        prebufferMB: String(updated.prebufferMB),
        windowBeforeMB: String(updated.windowBeforeMB),
        windowAfterMB: String(updated.windowAfterMB),
        segmentDuration: updated.segmentDuration,
      });
      localStorage.setItem('hlsMaxBufferLength', String(hlsMaxBuffer));
      setHlsError(null);
    } catch (error) {
      if (isApiError(error)) setHlsError(`${error.code ?? 'error'}: ${error.message}`);
      else if (error instanceof Error) setHlsError(error.message);
    } finally {
      setHlsSaving(false);
    }
  };

  const handleToggleSearchProvider = (providerName: string, enabled: boolean) => {
    setSearchEnabledProviders((prev) => {
      const current = new Set(prev);
      const normalizedName = providerName.trim().toLowerCase();
      if (enabled) current.add(normalizedName);
      else current.delete(normalizedName);
      const next = Array.from(current);
      saveEnabledSearchProviders(next);
      return next;
    });
  };

  const handleEnableAllSearchProviders = () => {
    const all = searchProviders.filter((p) => p.enabled).map((p) => p.name.toLowerCase());
    setSearchEnabledProviders(all);
    saveEnabledSearchProviders(all);
  };

  const handleDisableAllSearchProviders = () => {
    setSearchEnabledProviders([]);
    saveEnabledSearchProviders([]);
  };

  const updateRuntimeField = (
    provider: string,
    field: 'endpoint' | 'proxyUrl' | 'apiKey',
    value: string,
  ) => {
    const key = provider.toLowerCase();
    setRuntimeForm((prev) => ({
      ...prev,
      [key]: {
        endpoint: prev[key]?.endpoint ?? '',
        proxyUrl: prev[key]?.proxyUrl ?? '',
        apiKey: prev[key]?.apiKey ?? '',
        [field]: value,
      },
    }));
  };

  const handleSaveRuntimeConfig = async (provider: string) => {
    const key = provider.toLowerCase();
    const draft = runtimeForm[key];
    if (!draft) return;
    setRuntimeSaving(key);
    try {
      const patch: {
        provider: string;
        endpoint?: string;
        proxyUrl?: string;
        apiKey?: string;
      } = {
        provider: key,
        endpoint: draft.endpoint.trim(),
        proxyUrl: draft.proxyUrl.trim(),
      };
      if (draft.apiKey.trim()) {
        patch.apiKey = draft.apiKey.trim();
      }
      const updated = await updateSearchProviderRuntimeConfig(patch);
      const nextItems = runtimeConfigs.map((item) => (item.name.toLowerCase() === key ? updated : item));
      setRuntimeConfigs(nextItems);
      syncRuntimeForm(nextItems);
      setRuntimeError(null);
      toast({ title: `${updated.label}: settings saved`, variant: 'success' });
      await loadSearchProviders();
    } catch (error) {
      if (isApiError(error)) setRuntimeError(`${error.code ?? 'error'}: ${error.message}`);
      else if (error instanceof Error) setRuntimeError(error.message);
      else setRuntimeError('Failed to update provider settings');
    } finally {
      setRuntimeSaving(null);
    }
  };

  const handleAutodetectRuntimeConfig = async (provider?: string) => {
    const target = provider ? provider.toLowerCase() : 'all';
    setRuntimeDetecting(target);
    try {
      const payload = await autodetectSearchProviderRuntimeConfig(provider);
      const itemsByName = new Map(runtimeConfigs.map((item) => [item.name.toLowerCase(), item]));
      payload.items.forEach((item) => itemsByName.set(item.name.toLowerCase(), item));
      const nextItems = Array.from(itemsByName.values()).sort((a, b) => a.name.localeCompare(b.name));
      setRuntimeConfigs(nextItems);
      syncRuntimeForm(nextItems);
      setRuntimeError(null);
      if (provider && payload.results && payload.results.length > 0) {
        const first = payload.results[0];
        if (first.status === 'already_configured') {
          toast({
            title: `${first.provider}: auto-detect completed`,
            description: first.method ? `Method: ${first.method}` : undefined,
            variant: 'success',
          });
        } else {
          toast({
            title: `${first.provider}: auto-detect completed`,
            description: `${first.message}${first.method ? ` (${first.method})` : ''}`,
            variant: first.ok ? 'success' : 'warning',
          });
        }
      } else {
        const detected = payload.results?.filter((item) => item.status === 'detected').length ?? 0;
        const failed = payload.results?.filter((item) => item.status === 'error').length ?? 0;
        toast({
          title: 'Auto-detect completed',
          description: `${detected} updated, ${failed} errors`,
          variant: failed > 0 ? 'warning' : 'success',
        });
      }
      await loadSearchProviders();
    } catch (error) {
      if (isApiError(error)) setRuntimeError(`${error.code ?? 'error'}: ${error.message}`);
      else if (error instanceof Error) setRuntimeError(error.message);
      else setRuntimeError('Autodetect failed');
    } finally {
      setRuntimeDetecting(null);
    }
  };

  const handleApplyFlareSolverr = async (target: FlareApplyTarget = 'all') => {
    const value = flareSolverrUrl.trim() || flareSolverrDefaultUrl.trim();
    if (!value) return;
    const provider = target === 'all' ? undefined : target;
    setFlareSolverrApplyingTarget(target);
    try {
      const response = await applyFlareSolverrSettings({ url: value, provider });
      const okCount = response.results.filter((item) => item.ok).length;
      const failed = response.results.filter((item) => !item.ok);
      if (failed.length === 0) {
        const scopeLabel =
          target === 'all'
            ? 'Jackett & Prowlarr'
            : target === 'jackett'
              ? 'Jackett'
              : 'Prowlarr';
        toast({
          title: `FlareSolverr linked successfully: ${scopeLabel}`,
          description: `${okCount}/${response.results.length} provider(s) updated.`,
          variant: 'success',
          durationMs: 4500,
        });
      } else {
        const failedNames = failed.map((item) => item.provider).join(', ');
        toast({
          title: `FlareSolverr apply failed for: ${failedNames}`,
          description: failed.map((item) => `${item.provider}: ${item.message}`).join(' | '),
          variant: 'danger',
          durationMs: 5500,
        });
      }
      setFlareSolverrError(null);
      await loadFlareSolverrSettings();
    } catch (error) {
      const message = isApiError(error)
        ? `${error.code ?? 'error'}: ${error.message}`
        : error instanceof Error
          ? error.message
          : 'Failed to apply FlareSolverr';
      setFlareSolverrError(message);
      toast({
        title: 'FlareSolverr apply failed',
        description: message,
        variant: 'danger',
        durationMs: 5500,
      });
    } finally {
      setFlareSolverrApplyingTarget(null);
    }
  };

  return (
    <div className="grid w-full grid-cols-1 items-start gap-4 lg:grid-cols-2 lg:grid-rows-[auto_1fr]">
      <Card className="order-2 lg:col-start-1 lg:row-start-2">
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Palette className="h-4 w-4 text-primary" />
            Interface
          </CardTitle>
          <CardDescription>Theme and accent color.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-5">
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <div className="text-sm font-medium">Theme</div>
              <div className="flex flex-wrap items-center gap-2">
                {(['system', 'light', 'dark'] as const).map((modeOption) => (
                  <Button
                    key={modeOption}
                    variant={theme === modeOption ? 'default' : 'outline'}
                    size="sm"
                    onClick={() => setTheme(modeOption)}
                  >
                    {modeOption === 'system' ? 'System' : modeOption === 'light' ? 'Light' : 'Dark'}
                    {theme === modeOption ? <Check className="h-4 w-4" /> : null}
                  </Button>
                ))}
                <div className="text-xs text-muted-foreground">
                  Resolved: <span className="font-medium text-foreground">{resolvedTheme}</span>
                </div>
              </div>
            </div>

            <div className="space-y-2">
              <div className="text-sm font-medium">Accent</div>
              <div className="flex flex-wrap gap-2">
                {ACCENT_PRESETS.map((preset) => (
                  <Button
                    key={preset.id}
                    variant={!accentCustomHex && accentPreset === preset.id ? 'default' : 'outline'}
                    size="sm"
                    className={cn(!accentCustomHex && accentPreset === preset.id ? 'ring-1 ring-ring' : '')}
                    onClick={() => {
                      clearAccentCustom();
                      setAccentPreset(preset.id);
                    }}
                    title={preset.hsl}
                  >
                    {preset.label}
                  </Button>
                ))}
              </div>

              <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
                <div className="flex flex-wrap items-center gap-2">
                  <input
                    aria-label="Custom accent"
                    type="color"
                    value={accentCustomHex ?? '#6366f1'}
                    onChange={(e) => setAccentCustomHex(e.target.value)}
                    className="h-10 w-10 cursor-pointer rounded-md border border-border bg-background p-1"
                  />
                  <Input
                    value={accentCustomHex ?? ''}
                    onChange={(e) => setAccentCustomHex(e.target.value)}
                    placeholder="#6366f1"
                    className="w-36 max-w-full flex-1 font-mono text-xs sm:flex-none"
                  />
                  <Button variant="ghost" size="sm" onClick={clearAccentCustom} disabled={!accentCustomHex}>
                    Clear
                  </Button>
                </div>
                <div className="sm:ml-auto flex items-center gap-2">
                  <span className="text-xs text-muted-foreground">Preview</span>
                  <span className="inline-flex h-9 items-center rounded-md bg-primary px-3 text-sm font-medium text-primary-foreground shadow-soft">
                    Primary
                  </span>
                </div>
              </div>
            </div>
          </div>
        </CardContent>
      </Card>

      <Card className="order-3 lg:col-start-2 lg:row-span-2">
        <CardHeader className="gap-4 sm:flex-row sm:items-start sm:justify-between">
          <div className="space-y-1">
            <CardTitle className="flex items-center gap-2">
              <KeyRound className="h-4 w-4 text-primary" />
              Jackett / Prowlarr Integration
            </CardTitle>
            <CardDescription>
              Configure providers, API keys, proxy and active search sources from one place.
            </CardDescription>
          </div>
          <div className="flex w-full flex-wrap items-center gap-2 sm:w-auto sm:flex-nowrap">
            <Button
              variant="outline"
              size="sm"
              className="h-9 min-w-[9.5rem] justify-center whitespace-nowrap px-4"
              onClick={() => void handleAutodetectRuntimeConfig()}
              disabled={runtimeDetecting === 'all' || runtimeLoading}
            >
              {runtimeDetecting === 'all' ? 'Detecting...' : 'Auto-detect all'}
            </Button>
            <Button
              variant="outline"
              size="sm"
              className="h-9 whitespace-nowrap px-4"
              onClick={() => {
                void loadRuntimeConfigs();
                void loadFlareSolverrSettings();
                void loadSearchProviders();
              }}
              disabled={runtimeLoading || flareSolverrLoading || searchLoading}
            >
              <RefreshCw
                className={cn('h-4 w-4', runtimeLoading || flareSolverrLoading || searchLoading ? 'animate-spin' : '')}
              />
              Reload
            </Button>
          </div>
        </CardHeader>
        <CardContent className="space-y-3">
          {runtimeLoading ? <div className="text-sm text-muted-foreground">Loading runtime settings...</div> : null}
          <div className="rounded-xl border border-border/70 bg-muted/20 p-4">
            <div className="space-y-1">
              <div className="text-sm font-semibold">FlareSolverr</div>
            </div>
            <div className="mt-3 grid gap-2">
              <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
                <Input
                  value={flareSolverrUrl}
                  onChange={(e) => setFlareSolverrUrl(e.target.value)}
                  placeholder={flareSolverrDefaultUrl}
                  className="font-mono text-xs sm:flex-1"
                />
                <Button
                  variant="outline"
                  size="sm"
                  className="h-9 whitespace-nowrap"
                  onClick={() => setFlareSolverrUrl(flareSolverrDefaultUrl)}
                  disabled={Boolean(flareSolverrApplyingTarget) || flareSolverrLoading}
                >
                  Use Docker URL
                </Button>
              </div>
              <div className="flex flex-wrap items-center gap-2">
                <Button
                  size="sm"
                  className="h-9 whitespace-nowrap px-4"
                  onClick={() => void handleApplyFlareSolverr('all')}
                  disabled={Boolean(flareSolverrApplyingTarget) || flareSolverrLoading}
                >
                  {flareSolverrApplyingTarget === 'all' ? 'Applying...' : 'Apply to Jackett & Prowlarr'}
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  className="h-9 whitespace-nowrap px-4"
                  onClick={() => void handleApplyFlareSolverr('jackett')}
                  disabled={Boolean(flareSolverrApplyingTarget) || flareSolverrLoading}
                >
                  {flareSolverrApplyingTarget === 'jackett' ? 'Applying...' : 'Apply to Jackett'}
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  className="h-9 whitespace-nowrap px-4"
                  onClick={() => void handleApplyFlareSolverr('prowlarr')}
                  disabled={Boolean(flareSolverrApplyingTarget) || flareSolverrLoading}
                >
                  {flareSolverrApplyingTarget === 'prowlarr' ? 'Applying...' : 'Apply to Prowlarr'}
                </Button>
              </div>
            </div>

            <div className="mt-3 grid gap-2 sm:grid-cols-2">
              {flareSolverrProviders.map((provider) => (
                <div key={provider.provider} className="rounded-lg border border-border/70 bg-card/40 px-3 py-2">
                  <div className="flex items-center justify-between gap-2">
                    <div className="text-xs font-semibold uppercase tracking-wide">{provider.provider}</div>
                    <div
                      className={cn(
                        'inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[11px] font-medium',
                        provider.configured
                          ? 'border-emerald-500/50 bg-emerald-500/10 text-emerald-500'
                          : 'border-amber-500/50 bg-amber-500/10 text-amber-500',
                      )}
                    >
                      <Check className="h-3 w-3" />
                      {provider.configured ? 'linked' : 'not linked'}
                    </div>
                  </div>
                  <div className="mt-1 truncate font-mono text-[11px] text-muted-foreground">
                    {provider.url || provider.message || 'No FlareSolverr configured'}
                  </div>
                </div>
              ))}
            </div>
            {flareSolverrError ? <div className="mt-3 text-xs text-destructive">{flareSolverrError}</div> : null}
          </div>
          <div className="grid gap-3">
            {runtimeConfigs.map((item) => {
              const providerName = item.name.toLowerCase();
              const form = runtimeForm[providerName] ?? {
                endpoint: item.endpoint ?? '',
                proxyUrl: item.proxyUrl ?? '',
                apiKey: '',
              };
              const isSaving = runtimeSaving === providerName;
              const isDetecting = runtimeDetecting === providerName;
              const hasStoredApiKey = item.hasApiKey && !form.apiKey.trim();
              const apiKeyVisualValue = hasStoredApiKey ? '••••••••••••' : form.apiKey;
              return (
                <div key={item.name} className="rounded-xl border border-border/70 bg-muted/20 p-4">
                  <div className="flex flex-wrap items-center justify-between gap-3">
                    <div className="space-y-0.5">
                      <div className="text-sm font-semibold">{item.label || item.name}</div>
                      <div className="text-xs text-muted-foreground">{item.name}</div>
                    </div>
                    <div className="flex flex-wrap items-center gap-2">
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => void handleAutodetectRuntimeConfig(providerName)}
                        disabled={isSaving || isDetecting}
                      >
                        {isDetecting ? 'Detecting...' : 'Auto-detect'}
                      </Button>
                      <Button
                        size="sm"
                        onClick={() => void handleSaveRuntimeConfig(providerName)}
                        disabled={isSaving || isDetecting}
                      >
                        {isSaving ? 'Saving...' : 'Save'}
                      </Button>
                    </div>
                  </div>
                  <div className="mt-3 grid gap-3 md:grid-cols-3">
                    <div className="space-y-1.5">
                      <div className="text-xs font-medium text-muted-foreground">Endpoint</div>
                      <Input
                        value={form.endpoint}
                        onChange={(e) => updateRuntimeField(providerName, 'endpoint', e.target.value)}
                        placeholder="http://jackett:9117/api/v2.0/indexers/all/results/torznab/api"
                      />
                    </div>
                    <div className="space-y-1.5">
                      <div className="text-xs font-medium text-muted-foreground">API key</div>
                      <Input
                        type="password"
                        value={apiKeyVisualValue}
                        className={cn(
                          item.hasApiKey
                            ? 'border-emerald-500/40 bg-emerald-500/10 text-emerald-300 placeholder:text-emerald-300/70'
                            : '',
                        )}
                        onFocus={() => {
                          if (hasStoredApiKey) {
                            updateRuntimeField(providerName, 'apiKey', '');
                          }
                        }}
                        onChange={(e) => updateRuntimeField(providerName, 'apiKey', e.target.value)}
                        placeholder={item.hasApiKey ? '' : 'Enter API key'}
                        autoComplete="off"
                      />
                    </div>
                    <div className="space-y-1.5">
                      <div className="text-xs font-medium text-muted-foreground">Proxy URL</div>
                      <Input
                        value={form.proxyUrl}
                        onChange={(e) => updateRuntimeField(providerName, 'proxyUrl', e.target.value)}
                        placeholder="http://user:pass@host:port"
                      />
                    </div>
                  </div>
                </div>
              );
            })}
          </div>

          {runtimeError ? (
            <div className="rounded-lg border border-destructive/30 bg-destructive/10 px-4 py-3 text-sm">
              {runtimeError}
            </div>
          ) : null}

          <div className="border-t border-border/70 pt-5">
            <div className="space-y-3">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="text-sm font-semibold">Search Sources</div>
                <Button variant="outline" size="sm" onClick={() => void loadSearchProviders()} disabled={searchLoading}>
                  <RefreshCw className={cn('h-4 w-4', searchLoading ? 'animate-spin' : '')} />
                  Reload sources
                </Button>
              </div>

              <div className="flex flex-wrap items-center gap-2">
                <Button variant="outline" size="sm" onClick={handleEnableAllSearchProviders}>
                  Enable all
                </Button>
                <Button variant="outline" size="sm" onClick={handleDisableAllSearchProviders}>
                  Disable all
                </Button>
                <div className="text-sm text-muted-foreground">
                  Enabled:{' '}
                  <span className="font-medium text-foreground">{searchEnabledProviders.length}</span>/{searchProviders.length}
                </div>
              </div>

              <div className="grid gap-2">
                {searchLoading ? <div className="text-sm text-muted-foreground">Loading providers...</div> : null}
                {!searchLoading && searchProviders.length === 0 ? (
                  <div className="text-sm text-muted-foreground">No providers available.</div>
                ) : null}
                {searchProviders.map((provider) => {
                  const providerName = provider.name.toLowerCase();
                  const isEnabled = searchEnabledProviders.includes(providerName);
                  const isConfigured = provider.enabled;
                  return (
                    <div
                      key={provider.name}
                      className={cn(
                        'flex items-center justify-between gap-4 rounded-lg border border-border/70 bg-muted/20 px-4 py-3',
                        !isConfigured ? 'opacity-60' : '',
                      )}
                    >
                      <div className="min-w-0">
                        <div className="truncate text-sm font-medium">{provider.label}</div>
                        <div className="text-xs text-muted-foreground">
                          {provider.kind}
                          {!isConfigured ? ' - needs config' : ''}
                        </div>
                      </div>
                      <Switch
                        checked={isEnabled}
                        disabled={!isConfigured}
                        onCheckedChange={(checked) => handleToggleSearchProvider(provider.name, checked)}
                      />
                    </div>
                  );
                })}
              </div>

              {searchError ? (
                <div className="rounded-lg border border-destructive/30 bg-destructive/10 px-4 py-3 text-sm">
                  {searchError}
                </div>
              ) : null}
            </div>
          </div>
        </CardContent>
      </Card>

      <Card className="order-1 lg:col-start-1 lg:row-start-1">
        <CardHeader className="gap-4 sm:flex-row sm:items-start sm:justify-between">
          <div className="space-y-1">
            <CardTitle>Engine</CardTitle>
            <CardDescription>Streaming window, transcoding presets and playback settings.</CardDescription>
          </div>
          <div className="flex items-center gap-2">
            <Button variant="outline" size="sm" onClick={() => void loadEncoding()} disabled={encodingLoading}>
              <RefreshCw className={cn('h-4 w-4', encodingLoading ? 'animate-spin' : '')} />
              Encoding
            </Button>
            <Button variant="outline" size="sm" onClick={() => void loadHLSSettings()} disabled={hlsLoading}>
              <RefreshCw className={cn('h-4 w-4', hlsLoading ? 'animate-spin' : '')} />
              Streaming
            </Button>
          </div>
        </CardHeader>
        <CardContent className="space-y-5">
          <div className="space-y-3">
            <div className="text-sm font-semibold">Streaming</div>
            {hlsLoading ? <div className="text-sm text-muted-foreground">Loading...</div> : null}

            {!hlsLoading && hlsSettings ? (
              <>
                <div className="grid gap-4 sm:grid-cols-2">
                  <div className="space-y-2">
                    <div className="text-sm font-medium">RAM buffer (MB)</div>
                    <Input
                      type="number"
                      min={4}
                      max={4096}
                      step={16}
                      value={hlsForm.ramBufSizeMB}
                      onChange={(e) => setHlsForm((prev) => ({ ...prev, ramBufSizeMB: e.target.value }))}
                      disabled={hlsSaving}
                    />
                    <div className="text-xs text-muted-foreground">Pipe buffer for FFmpeg input</div>
                  </div>
                  <div className="space-y-2">
                    <div className="text-sm font-medium">Prebuffer (MB)</div>
                    <Input
                      type="number"
                      min={1}
                      max={1024}
                      step={1}
                      value={hlsForm.prebufferMB}
                      onChange={(e) => setHlsForm((prev) => ({ ...prev, prebufferMB: e.target.value }))}
                      disabled={hlsSaving}
                    />
                    <div className="text-xs text-muted-foreground">Data to buffer before starting FFmpeg</div>
                  </div>
                </div>
                <div className="grid gap-4 sm:grid-cols-2">
                  <div className="space-y-2">
                    <div className="text-sm font-medium">Window before (MB)</div>
                    <Input
                      type="number"
                      min={1}
                      max={1024}
                      step={4}
                      value={hlsForm.windowBeforeMB}
                      onChange={(e) => setHlsForm((prev) => ({ ...prev, windowBeforeMB: e.target.value }))}
                      disabled={hlsSaving}
                    />
                    <div className="text-xs text-muted-foreground">Priority window behind playback</div>
                  </div>
                  <div className="space-y-2">
                    <div className="text-sm font-medium">Window after (MB)</div>
                    <Input
                      type="number"
                      min={4}
                      max={4096}
                      step={16}
                      value={hlsForm.windowAfterMB}
                      onChange={(e) => setHlsForm((prev) => ({ ...prev, windowAfterMB: e.target.value }))}
                      disabled={hlsSaving}
                    />
                    <div className="text-xs text-muted-foreground">Priority window ahead of playback</div>
                  </div>
                </div>
                <div className="grid gap-4 sm:grid-cols-2">
                  <div className="space-y-2">
                    <div className="text-sm font-medium">Segment duration (s)</div>
                    <div className="flex flex-wrap items-center gap-2">
                      {[2, 4, 6, 8, 10].map((dur) => (
                        <Button
                          key={dur}
                          variant={hlsForm.segmentDuration === dur ? 'default' : 'outline'}
                          size="sm"
                          onClick={() => setHlsForm((prev) => ({ ...prev, segmentDuration: dur }))}
                          disabled={hlsSaving}
                        >
                          {dur}
                        </Button>
                      ))}
                    </div>
                  </div>
                  <div className="space-y-2">
                    <div className="text-sm font-medium">Player buffer (s)</div>
                    <div className="flex flex-wrap items-center gap-2">
                      {[15, 30, 60, 120, 300].map((buf) => (
                        <Button
                          key={buf}
                          variant={hlsMaxBuffer === buf ? 'default' : 'outline'}
                          size="sm"
                          onClick={() => setHlsMaxBuffer(buf)}
                          disabled={hlsSaving}
                        >
                          {buf}
                        </Button>
                      ))}
                    </div>
                  </div>
                </div>
                <div className="flex justify-end">
                  <Button onClick={() => void handleSaveHLSSettings()} disabled={hlsSaving}>
                    {hlsSaving ? 'Saving...' : 'Save'}
                  </Button>
                </div>
              </>
            ) : null}

            {hlsError ? (
              <div className="rounded-lg border border-destructive/30 bg-destructive/10 px-4 py-3 text-sm">
                {hlsError}
              </div>
            ) : null}
          </div>

          <div className="border-t border-border/70 pt-5">
            <div className="space-y-3">
              <div className="text-sm font-semibold">Encoding</div>
              {encodingLoading ? <div className="text-sm text-muted-foreground">Loading...</div> : null}

              {!encodingLoading && encodingSettings ? (
                <div className="grid gap-4 sm:grid-cols-2">
                  <div className="space-y-2">
                    <div className="text-sm font-medium">Quality</div>
                    <Select
                      value={qualityLevel(encodingSettings)}
                      onChange={(e) => {
                        const q = qualityPresets[e.target.value as keyof typeof qualityPresets];
                        if (q) void handleUpdateEncoding({ preset: q.preset, crf: q.crf });
                      }}
                      disabled={encodingSaving}
                    >
                      <option value="fast">Fast encoding</option>
                      <option value="balanced">Balanced</option>
                      <option value="quality">High quality</option>
                      <option value="best">Best quality</option>
                    </Select>
                  </div>

                  <div className="space-y-2">
                    <div className="text-sm font-medium">Audio bitrate</div>
                    <Select
                      value={encodingSettings.audioBitrate}
                      onChange={(e) => void handleUpdateEncoding({ audioBitrate: e.target.value })}
                      disabled={encodingSaving}
                    >
                      <option value="96k">96k</option>
                      <option value="128k">128k</option>
                      <option value="192k">192k</option>
                      <option value="256k">256k</option>
                    </Select>
                  </div>
                </div>
              ) : null}

              {encodingError ? (
                <div className="rounded-lg border border-destructive/30 bg-destructive/10 px-4 py-3 text-sm">
                  {encodingError}
                </div>
              ) : null}
            </div>
          </div>
        </CardContent>
      </Card>
    </div>
  );
};

export default SettingsPage;

```

## frontend/src/api.ts

```ts
import type {
  ApiErrorPayload,
  EncodingSettings,
  HLSSettings,
  FlareSolverrApplyResponse,
  FlareSolverrSettings,
  MediaInfo,
  SessionState,
  SessionStateList,
  PlayerSettings,
  TorrentListFull,
  TorrentListSummary,
  TorrentRecord,
  BulkResponse,
  SortOrder,
  TorrentSortBy,
  TorrentStatusFilter,
  TorrentView,
  WatchPosition,
  PlayerHealth,
  SearchProviderInfo,
  SearchProviderDiagnostics,
  SearchProviderTestResult,
  SearchProviderAutodetectResult,
  SearchProviderRuntimeConfig,
  SearchProviderRuntimePatch,
  SearchRankingProfile,
  SearchResponse,
  SearchSortBy,
  SearchSortOrder,
} from './types';

const rawBase = (import.meta as any).env?.VITE_API_BASE_URL ?? '';
const API_BASE = typeof rawBase === 'string' ? rawBase.replace(/\/$/, '') : '';

export const buildUrl = (path: string) => (API_BASE ? `${API_BASE}${path}` : path);
const DEFAULT_REQUEST_TIMEOUT_MS = 15000;
const POLL_REQUEST_TIMEOUT_MS = 7000;
const LONG_REQUEST_TIMEOUT_MS = 90000;

// ---- GET request deduplication ----
// If an identical GET is already in-flight, return the same promise instead of
// creating a new HTTP request. This collapses bursts of duplicate polls into a
// single network round-trip.
const inflightGets = new Map<string, Promise<Response>>();

const deduplicatedFetch = async (
  url: string,
  init?: RequestInit,
  timeoutMs = DEFAULT_REQUEST_TIMEOUT_MS,
): Promise<Response> => {
  const method = init?.method?.toUpperCase() ?? 'GET';

  // Don't deduplicate mutations (POST, PUT, DELETE, etc.) because different
  // request bodies should not share the same response. Mutations are idempotent
  // by design and should execute independently.
  if (method !== 'GET') {
    return fetchWithTimeout(url, init, timeoutMs);
  }

  const existing = inflightGets.get(url);
  if (existing) return existing.then((r) => r.clone());

  const promise = fetchWithTimeout(url, init, timeoutMs).finally(() => {
    inflightGets.delete(url);
  });
  inflightGets.set(url, promise);
  return promise;
};

class ApiRequestError extends Error {
  code?: string;
  status?: number;

  constructor(message: string, code?: string, status?: number) {
    super(message);
    this.name = 'ApiRequestError';
    this.code = code;
    this.status = status;
  }
}

const parseErrorPayload = async (response: Response): Promise<ApiErrorPayload | null> => {
  try {
    const data = (await response.json()) as ApiErrorPayload;
    if (data && typeof data === 'object') {
      return data;
    }
  } catch (error) {
    return null;
  }
  return null;
};

const handleResponse = async <T>(response: Response): Promise<T> => {
  if (response.ok) {
    if (response.status === 204) {
      return undefined as T;
    }
    return (await response.json()) as T;
  }

  const payload = await parseErrorPayload(response);
  const code = payload?.error?.code ?? 'request_failed';
  const message = payload?.error?.message ?? response.statusText;
  throw new ApiRequestError(message, code, response.status);
};

const fetchWithTimeout = async (
  url: string,
  init?: RequestInit,
  timeoutMs = DEFAULT_REQUEST_TIMEOUT_MS,
): Promise<Response> => {
  const controller = new AbortController();
  const timeout = window.setTimeout(() => controller.abort(), timeoutMs);
  try {
    return await fetch(url, { ...init, signal: controller.signal });
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') {
      throw new ApiRequestError('request timeout', 'timeout');
    }
    throw error;
  } finally {
    window.clearTimeout(timeout);
  }
};

export const listTorrents = async (options?: {
  status?: TorrentStatusFilter;
  view?: TorrentView;
  search?: string;
  tags?: string[];
  sortBy?: TorrentSortBy;
  sortOrder?: SortOrder;
  limit?: number;
  offset?: number;
}): Promise<TorrentListFull | TorrentListSummary> => {
  const params = new URLSearchParams();
  params.set('status', options?.status ?? 'all');
  params.set('view', options?.view ?? 'full');
  if (options?.search?.trim()) params.set('search', options.search.trim());
  if (options?.tags && options.tags.length > 0) params.set('tags', options.tags.join(','));
  if (options?.sortBy) params.set('sortBy', options.sortBy);
  if (options?.sortOrder) params.set('sortOrder', options.sortOrder);
  if (options?.limit) params.set('limit', String(options.limit));
  if (options?.offset) params.set('offset', String(options.offset));
  const response = await deduplicatedFetch(
    buildUrl(`/torrents?${params.toString()}`),
    undefined,
    POLL_REQUEST_TIMEOUT_MS,
  );
  return handleResponse(response);
};

export const listSearchProviders = async (): Promise<SearchProviderInfo[]> => {
  const response = await fetch(buildUrl('/search/providers'));
  const payload = await handleResponse<{ items: SearchProviderInfo[] }>(response);
  return payload.items ?? [];
};

export const getSearchProviderRuntimeConfigs = async (): Promise<SearchProviderRuntimeConfig[]> => {
  const response = await fetch(buildUrl('/search/settings/providers'));
  const payload = await handleResponse<{ items: SearchProviderRuntimeConfig[] }>(response);
  return payload.items ?? [];
};

export const updateSearchProviderRuntimeConfig = async (
  input: SearchProviderRuntimePatch,
): Promise<SearchProviderRuntimeConfig> => {
  const response = await fetch(buildUrl('/search/settings/providers'), {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
  return handleResponse<SearchProviderRuntimeConfig>(response);
};

export const autodetectSearchProviderRuntimeConfig = async (
  provider?: string,
): Promise<{
  items: SearchProviderRuntimeConfig[];
  results?: SearchProviderAutodetectResult[];
  errors?: { provider: string; error: string }[];
}> => {
  const response = await fetch(buildUrl('/search/settings/providers/autodetect'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(provider ? { provider } : {}),
  });
  return handleResponse<{
    items: SearchProviderRuntimeConfig[];
    results?: SearchProviderAutodetectResult[];
    errors?: { provider: string; error: string }[];
  }>(response);
};

export const getFlareSolverrSettings = async (): Promise<FlareSolverrSettings> => {
  const response = await fetch(buildUrl('/search/settings/flaresolverr'));
  return handleResponse<FlareSolverrSettings>(response);
};

export const applyFlareSolverrSettings = async (
  input: { url: string; provider?: string },
): Promise<FlareSolverrApplyResponse> => {
  const response = await fetch(buildUrl('/search/settings/flaresolverr'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
  return handleResponse<FlareSolverrApplyResponse>(response);
};

export const getSearchProviderDiagnostics = async (): Promise<SearchProviderDiagnostics[]> => {
  const response = await fetch(buildUrl('/search/providers/health'), { cache: 'no-store' });
  const payload = await handleResponse<{ items: SearchProviderDiagnostics[] }>(response);
  return payload.items ?? [];
};

export const testSearchProvider = async (provider: string, query: string): Promise<SearchProviderTestResult> => {
  const params = new URLSearchParams();
  params.set('provider', provider.trim().toLowerCase());
  params.set('q', query.trim() || 'spider man');
  params.set('limit', '10');
  params.set('nocache', '1');
  const response = await fetch(buildUrl(`/search/providers/test?${params.toString()}`), { cache: 'no-store' });
  return handleResponse<SearchProviderTestResult>(response);
};

export const searchTorrents = async (options: {
  query: string;
  limit?: number;
  offset?: number;
  sortBy?: SearchSortBy;
  sortOrder?: SearchSortOrder;
  providers?: string[];
  profile?: SearchRankingProfile;
  noCache?: boolean;
}): Promise<SearchResponse> => {
  const params = buildSearchParams(options);
  // Avoid browser/proxy caching for search results (but allow server-side Redis cache).
  params.set('_ts', String(Date.now()));
  const response = await fetch(buildUrl(`/search?${params.toString()}`), {
    cache: 'no-store',
    headers: { 'Cache-Control': 'no-store' },
  });
  return handleResponse(response);
};

export const searchTorrentsStream = (
  options: {
    query: string;
    limit?: number;
    offset?: number;
    sortBy?: SearchSortBy;
    sortOrder?: SearchSortOrder;
    providers?: string[];
    profile?: SearchRankingProfile;
    noCache?: boolean;
  },
  handlers: {
    onPhase: (response: SearchResponse) => void;
    onDone?: () => void;
    onError?: (message: string) => void;
  },
): (() => void) => {
  const params = buildSearchParams(options);
  // Avoid browser/proxy caching for SSE streams.
  params.set('_ts', String(Date.now()));
  const source = new EventSource(buildUrl(`/search/stream?${params.toString()}`));
  let closed = false;

  const closeStream = () => {
    if (closed) return;
    closed = true;
    source.close();
  };

  const handlePhase = (event: MessageEvent<string>) => {
    try {
      const payload = JSON.parse(event.data) as SearchResponse;
      handlers.onPhase(payload);
    } catch {
      handlers.onError?.('invalid stream payload');
    }
  };

  const handleDone = () => {
    handlers.onDone?.();
    closeStream();
  };

  const handleError = (event: MessageEvent<string> | Event) => {
    if (event instanceof MessageEvent) {
      try {
        const payload = JSON.parse(event.data) as { message?: string };
        handlers.onError?.(payload.message || 'search stream failed');
      } catch {
        handlers.onError?.('search stream failed');
      }
    } else {
      handlers.onError?.('search stream failed');
    }
  };

  source.addEventListener('phase', handlePhase as EventListener);
  source.addEventListener('update', handlePhase as EventListener);
  source.addEventListener('bootstrap', handlePhase as EventListener);
  source.addEventListener('done', handleDone as EventListener);
  source.addEventListener('error', handleError as EventListener);
  source.onerror = () => {
    if (closed) return;
    handlers.onError?.('search stream disconnected');
    closeStream();
  };

  return () => {
    source.removeEventListener('phase', handlePhase as EventListener);
    source.removeEventListener('update', handlePhase as EventListener);
    source.removeEventListener('bootstrap', handlePhase as EventListener);
    source.removeEventListener('done', handleDone as EventListener);
    source.removeEventListener('error', handleError as EventListener);
    closeStream();
  };
};

const buildSearchParams = (options: {
  query: string;
  limit?: number;
  offset?: number;
  sortBy?: SearchSortBy;
  sortOrder?: SearchSortOrder;
  providers?: string[];
  profile?: SearchRankingProfile;
  noCache?: boolean;
}) => {
  const params = new URLSearchParams();
  params.set('q', options.query.trim());
  // Only bypass cache when explicitly requested (e.g., force refresh button).
  if (options.noCache) {
    params.set('nocache', '1');
  }
  if (options.limit && options.limit > 0) params.set('limit', String(options.limit));
  if (options.offset && options.offset >= 0) params.set('offset', String(options.offset));
  if (options.sortBy) params.set('sortBy', options.sortBy);
  if (options.sortOrder) params.set('sortOrder', options.sortOrder);
  if (options.providers && options.providers.length > 0) params.set('providers', options.providers.join(','));
  appendRankingProfile(params, options.profile);
  return params;
};

const appendRankingProfile = (params: URLSearchParams, profile?: SearchRankingProfile) => {
  if (!profile) return;
  params.set('freshnessWeight', String(profile.freshnessWeight));
  params.set('seedersWeight', String(profile.seedersWeight));
  params.set('qualityWeight', String(profile.qualityWeight));
  params.set('languageWeight', String(profile.languageWeight));
  params.set('sizeWeight', String(profile.sizeWeight));
  if (profile.preferSeries) params.set('preferSeries', '1');
  if (profile.preferMovies) params.set('preferMovies', '1');
  if (profile.preferredAudio.length > 0) params.set('preferredAudio', profile.preferredAudio.join(','));
  if (profile.preferredSubtitles.length > 0) {
    params.set('preferredSubtitles', profile.preferredSubtitles.join(','));
  }
  if (profile.targetSizeBytes > 0) params.set('targetSizeBytes', String(profile.targetSizeBytes));
};

export const getTorrent = async (id: string): Promise<TorrentRecord> => {
  const response = await fetch(buildUrl(`/torrents/${id}`));
  return handleResponse(response);
};

export const createTorrentFromMagnet = async (magnet: string, name?: string): Promise<TorrentRecord> => {
  const response = await fetchWithTimeout(
    buildUrl('/torrents'),
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ magnet, name: name || undefined }),
    },
    LONG_REQUEST_TIMEOUT_MS,
  );
  return handleResponse(response);
};

export const createTorrentFromFile = async (file: File, name?: string): Promise<TorrentRecord> => {
  const form = new FormData();
  form.append('torrent', file);
  if (name) {
    form.append('name', name);
  }

  const response = await fetch(buildUrl('/torrents'), {
    method: 'POST',
    body: form,
  });
  return handleResponse(response);
};

export const startTorrent = async (id: string): Promise<TorrentRecord> => {
  const response = await deduplicatedFetch(buildUrl(`/torrents/${id}/start`), { method: 'POST' });
  return handleResponse(response);
};

export const stopTorrent = async (id: string): Promise<TorrentRecord> => {
  const response = await deduplicatedFetch(buildUrl(`/torrents/${id}/stop`), { method: 'POST' });
  return handleResponse(response);
};

export const deleteTorrent = async (id: string, deleteFiles: boolean): Promise<void> => {
  const params = new URLSearchParams();
  if (deleteFiles) {
    params.set('deleteFiles', 'true');
  }
  const url = params.toString() ? `/torrents/${id}?${params.toString()}` : `/torrents/${id}`;
  const response = await deduplicatedFetch(buildUrl(url), { method: 'DELETE' });
  return handleResponse(response);
};

export const updateTorrentTags = async (id: string, tags: string[]): Promise<TorrentRecord> => {
  const response = await fetch(buildUrl(`/torrents/${id}/tags`), {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ tags }),
  });
  return handleResponse(response);
};

export const bulkStartTorrents = async (ids: string[]): Promise<BulkResponse> => {
  const response = await fetch(buildUrl('/torrents/bulk/start'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ids }),
  });
  return handleResponse(response);
};

export const bulkStopTorrents = async (ids: string[]): Promise<BulkResponse> => {
  const response = await fetch(buildUrl('/torrents/bulk/stop'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ids }),
  });
  return handleResponse(response);
};

export const bulkDeleteTorrents = async (ids: string[], deleteFiles: boolean): Promise<BulkResponse> => {
  const response = await fetch(buildUrl('/torrents/bulk/delete'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ids, deleteFiles }),
  });
  return handleResponse(response);
};

export const getTorrentState = async (id: string, signal?: AbortSignal): Promise<SessionState> => {
  const response = await deduplicatedFetch(
    buildUrl(`/torrents/${id}/state`),
    { signal },
    POLL_REQUEST_TIMEOUT_MS,
  );
  return handleResponse(response);
};

export const listActiveStates = async (): Promise<SessionStateList> => {
  const response = await deduplicatedFetch(
    buildUrl('/torrents/state?status=active'),
    undefined,
    POLL_REQUEST_TIMEOUT_MS,
  );
  return handleResponse(response);
};

export const buildStreamUrl = (id: string, fileIndex: number) =>
  buildUrl(`/torrents/${id}/stream?fileIndex=${fileIndex}`);

export const buildDirectPlaybackUrl = (id: string, fileIndex: number) =>
  buildUrl(`/torrents/${id}/direct/${fileIndex}`);

/** HEAD probe for the /direct/ endpoint. Returns 'ready', 'remuxing', or false. */
export const probeDirectPlayback = async (
  id: string,
  fileIndex: number,
  signal?: AbortSignal,
): Promise<'ready' | 'remuxing' | false> => {
  try {
    const res = await fetch(buildDirectPlaybackUrl(id, fileIndex), { method: 'HEAD', signal });
    if (res.status === 200) return 'ready';
    if (res.status === 202) return 'remuxing';
    return false;
  } catch {
    return false;
  }
};

/** HEAD request to direct stream URL. Resolves true if 200/206, false otherwise. */
export const probeDirectStream = async (
  id: string,
  fileIndex: number,
  signal?: AbortSignal,
): Promise<boolean> => {
  try {
    const res = await fetch(buildStreamUrl(id, fileIndex), { method: 'HEAD', signal });
    return res.ok;
  } catch {
    return false;
  }
};

/** Fetches HLS manifest. Returns true if it contains segments or variant streams. */
export const probeHlsManifest = async (
  url: string,
  signal?: AbortSignal,
): Promise<boolean> => {
  try {
    const res = await fetch(url, { signal });
    if (!res.ok) return false;
    const text = await res.text();
    // Single-variant: has #EXTINF (segment entries).
    // Multi-variant (master): has #EXT-X-STREAM-INF (variant references).
    return text.includes('#EXTINF') || text.includes('#EXT-X-STREAM-INF');
  } catch {
    return false;
  }
};

export const buildHlsUrl = (
  id: string,
  fileIndex: number,
  options?: { audioTrack?: number | null; subtitleTrack?: number | null },
) => {
  const params = new URLSearchParams();
  if (options?.audioTrack !== undefined && options.audioTrack !== null) {
    params.set('audioTrack', String(options.audioTrack));
  }
  if (options?.subtitleTrack !== undefined && options.subtitleTrack !== null) {
    params.set('subtitleTrack', String(options.subtitleTrack));
  }
  const query = params.toString();
  const suffix = query ? `?${query}` : '';
  return buildUrl(`/torrents/${id}/hls/${fileIndex}/index.m3u8${suffix}`);
};

export const getMediaInfo = async (id: string, fileIndex: number): Promise<MediaInfo> => {
  const response = await deduplicatedFetch(
    buildUrl(`/torrents/${id}/media/${fileIndex}`),
    undefined,
    POLL_REQUEST_TIMEOUT_MS,
  );
  return handleResponse(response);
};

export const saveWatchPosition = async (
  torrentId: string,
  fileIndex: number,
  position: number,
  duration: number,
  torrentName?: string,
  filePath?: string,
): Promise<void> => {
  const response = await fetch(buildUrl(`/watch-history/${torrentId}/${fileIndex}`), {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ position, duration, torrentName: torrentName ?? '', filePath: filePath ?? '' }),
  });
  return handleResponse(response);
};

export const getWatchPosition = async (
  torrentId: string,
  fileIndex: number,
): Promise<WatchPosition | null> => {
  const response = await fetch(buildUrl(`/watch-history/${torrentId}/${fileIndex}`));
  if (response.status === 404) return null;
  return handleResponse(response);
};

export const getWatchHistory = async (limit = 20): Promise<WatchPosition[]> => {
  const response = await deduplicatedFetch(
    buildUrl(`/watch-history?limit=${limit}`),
    undefined,
    POLL_REQUEST_TIMEOUT_MS,
  );
  return handleResponse(response);
};

export const getEncodingSettings = async (): Promise<EncodingSettings> => {
  const response = await fetch(buildUrl('/settings/encoding'));
  return handleResponse(response);
};

export const getPlayerSettings = async (): Promise<PlayerSettings> => {
  const response = await deduplicatedFetch(buildUrl('/settings/player'), undefined, POLL_REQUEST_TIMEOUT_MS);
  return handleResponse(response);
};

export const getPlayerHealth = async (): Promise<PlayerHealth> => {
  const response = await deduplicatedFetch(
    buildUrl('/internal/health/player'),
    undefined,
    POLL_REQUEST_TIMEOUT_MS,
  );
  return handleResponse(response);
};

export const updatePlayerSettings = async (
  input: { currentTorrentId: string | null },
): Promise<PlayerSettings> => {
  const response = await fetch(buildUrl('/settings/player'), {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ currentTorrentId: input.currentTorrentId ?? '' }),
  });
  return handleResponse(response);
};

export const updateEncodingSettings = async (
  input: Partial<EncodingSettings>,
): Promise<EncodingSettings> => {
  const response = await fetch(buildUrl('/settings/encoding'), {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
  return handleResponse(response);
};

export const getHLSSettings = async (): Promise<HLSSettings> => {
  const response = await fetch(buildUrl('/settings/hls'));
  return handleResponse(response);
};

export const updateHLSSettings = async (
  input: Partial<HLSSettings>,
): Promise<HLSSettings> => {
  const response = await fetch(buildUrl('/settings/hls'), {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
  return handleResponse(response);
};

export const hlsSeek = async (
  id: string,
  fileIndex: number,
  time: number,
  options?: { audioTrack?: number | null; subtitleTrack?: number | null; signal?: AbortSignal },
): Promise<{ seekTime: number; seekMode?: string }> => {
  const params = new URLSearchParams();
  params.set('time', String(time));
  if (options?.audioTrack !== undefined && options.audioTrack !== null) {
    params.set('audioTrack', String(options.audioTrack));
  }
  if (options?.subtitleTrack !== undefined && options.subtitleTrack !== null) {
    params.set('subtitleTrack', String(options.subtitleTrack));
  }
  const response = await fetch(
    buildUrl(`/torrents/${id}/hls/${fileIndex}/seek?${params.toString()}`),
    { method: 'POST', signal: options?.signal },
  );
  return handleResponse(response);
};

export const focusTorrent = async (id: string): Promise<void> => {
  const response = await fetch(buildUrl(`/torrents/${id}/focus`), { method: 'POST' });
  return handleResponse(response);
};

export const unfocusTorrents = async (): Promise<void> => {
  const response = await fetch(buildUrl('/torrents/unfocus'), { method: 'POST' });
  return handleResponse(response);
};

export const isApiError = (error: unknown): error is ApiRequestError =>
  error instanceof ApiRequestError;
```

## frontend/index.html

```html
<!doctype html>
<html lang="ru">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>T?RRX</title>
    <link rel="icon" type="image/png" href="/logo/only_x_logo_v1.png" />
    <link rel="apple-touch-icon" href="/logo/only_x_logo_v1.png" />
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>

```

## frontend/vite.config.ts

```ts
import { defineConfig, loadEnv } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '');
  const proxyTarget = env.VITE_API_PROXY_TARGET || 'http://localhost:8080';
  const searchProxyTarget = env.VITE_SEARCH_PROXY_TARGET || 'http://localhost:8090';

  return {
    plugins: [react()],
    server: {
      proxy: {
        '/torrents': {
          target: proxyTarget,
          changeOrigin: true,
        },
        '/settings/storage': {
          target: proxyTarget,
          changeOrigin: true,
        },
        '/settings/player': {
          target: proxyTarget,
          changeOrigin: true,
        },
        '/settings/encoding': {
          target: proxyTarget,
          changeOrigin: true,
        },
        '/swagger': {
          target: proxyTarget,
          changeOrigin: true,
        },
        '/watch-history': {
          target: proxyTarget,
          changeOrigin: true,
        },
        '/ws': {
          target: proxyTarget,
          changeOrigin: true,
          ws: true,
        },
        '/search': {
          target: searchProxyTarget,
          changeOrigin: true,
        },
      },
    },
    build: {
      outDir: 'dist',
    },
    define: {
      __APP_MODE__: JSON.stringify(mode),
    },
  };
});
```

## frontend/package.json

```json
{
  "name": "torrent-stream-web-client",
  "private": true,
  "version": "0.1.0",
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "vite build",
    "preview": "vite preview",
    "lint": "eslint src/",
    "lint:fix": "eslint src/ --fix",
    "format": "prettier --write src/",
    "format:check": "prettier --check src/",
    "typecheck": "tsc --noEmit",
    "test": "vitest run",
    "test:watch": "vitest"
  },
  "dependencies": {
    "@fontsource-variable/inter": "^5.2.6",
    "@fontsource-variable/jetbrains-mono": "^5.2.6",
    "@radix-ui/react-dialog": "^1.1.15",
    "@radix-ui/react-dropdown-menu": "^2.1.16",
    "@radix-ui/react-slot": "^1.2.3",
    "@radix-ui/react-switch": "^1.2.6",
    "@radix-ui/react-tabs": "^1.1.13",
    "class-variance-authority": "^0.7.1",
    "clsx": "^2.1.1",
    "hls.js": "^1.5.15",
    "lucide-react": "^0.563.0",
    "react": "^18.3.1",
    "react-dom": "^18.3.1",
    "react-router-dom": "^7.13.0",
    "tailwind-merge": "^3.3.1"
  },
  "devDependencies": {
    "@eslint/js": "^9.39.0",
    "@testing-library/dom": "^10.4.1",
    "@testing-library/jest-dom": "^6.9.1",
    "@testing-library/react": "^16.3.2",
    "@testing-library/user-event": "^14.6.1",
    "@types/react": "^18.3.3",
    "@types/react-dom": "^18.3.0",
    "@vitejs/plugin-react": "^4.3.1",
    "autoprefixer": "^10.4.21",
    "eslint": "^9.39.0",
    "eslint-config-prettier": "^10.1.8",
    "eslint-plugin-react-hooks": "^7.0.1",
    "eslint-plugin-react-refresh": "^0.5.0",
    "jsdom": "^28.1.0",
    "postcss": "^8.5.3",
    "prettier": "^3.8.1",
    "tailwindcss": "^3.4.17",
    "typescript": "^5.6.3",
    "typescript-eslint": "^8.55.0",
    "vite": "^5.4.10",
    "vitest": "^4.0.18"
  }
}
```


