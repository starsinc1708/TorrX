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

