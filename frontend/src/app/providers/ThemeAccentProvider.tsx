import React, { createContext, useCallback, useEffect, useMemo, useState } from 'react';
import {
  ACCENT_PRESETS,
  STORAGE_KEYS,
  type AccentPresetId,
  type ThemeMode,
  applyAccentToRoot,
  hexToHslTriplet,
} from '../../lib/design-system';

type ResolvedTheme = 'light' | 'dark';

type ThemeAccentContextValue = {
  theme: ThemeMode;
  resolvedTheme: ResolvedTheme;
  setTheme: (theme: ThemeMode) => void;

  accentPreset: AccentPresetId;
  accentCustomHex: string | null;
  setAccentPreset: (preset: AccentPresetId) => void;
  setAccentCustomHex: (hex: string) => void;
  clearAccentCustom: () => void;
};

const ThemeAccentContext = createContext<ThemeAccentContextValue | null>(null);

const resolveTheme = (theme: ThemeMode): ResolvedTheme => {
  if (theme === 'light') return 'light';
  if (theme === 'dark') return 'dark';
  return window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
};

const readStoredTheme = (): ThemeMode => {
  const raw = window.localStorage.getItem(STORAGE_KEYS.theme);
  if (raw === 'light' || raw === 'dark' || raw === 'system') return raw;
  return 'system';
};

const readStoredAccentPreset = (): AccentPresetId => {
  const raw = window.localStorage.getItem(STORAGE_KEYS.accentPreset);
  const found = ACCENT_PRESETS.find((p) => p.id === raw);
  return found?.id ?? 'indigo';
};

const readStoredAccentCustomHex = (): string | null => {
  const raw = window.localStorage.getItem(STORAGE_KEYS.accentCustom);
  if (!raw) return null;
  const hex = raw.trim();
  if (!/^#?[0-9a-fA-F]{6}$/.test(hex)) return null;
  return hex.startsWith('#') ? hex : `#${hex}`;
};

export function ThemeAccentProvider({ children }: { children: React.ReactNode }) {
  const [theme, setThemeState] = useState<ThemeMode>(() => readStoredTheme());
  const [resolvedTheme, setResolvedTheme] = useState<ResolvedTheme>(() => resolveTheme(readStoredTheme()));

  const [accentPreset, setAccentPresetState] = useState<AccentPresetId>(() => readStoredAccentPreset());
  const [accentCustomHex, setAccentCustomHexState] = useState<string | null>(() => readStoredAccentCustomHex());

  const applyThemeClass = useCallback((nextResolved: ResolvedTheme) => {
    const root = document.documentElement;
    root.classList.toggle('dark', nextResolved === 'dark');
  }, []);

  useEffect(() => {
    const nextResolved = resolveTheme(theme);
    setResolvedTheme(nextResolved);
    applyThemeClass(nextResolved);
    window.localStorage.setItem(STORAGE_KEYS.theme, theme);

    if (theme !== 'system') return;
    const mq = window.matchMedia?.('(prefers-color-scheme: dark)');
    if (!mq) return;
    const handler = () => {
      const resolved = resolveTheme('system');
      setResolvedTheme(resolved);
      applyThemeClass(resolved);
    };
    mq.addEventListener?.('change', handler);
    return () => mq.removeEventListener?.('change', handler);
  }, [theme, applyThemeClass]);

  const applyAccent = useCallback(
    (preset: AccentPresetId, customHex: string | null) => {
      if (customHex) {
        const triplet = hexToHslTriplet(customHex);
        if (triplet) {
          applyAccentToRoot(triplet);
          return;
        }
      }
      const presetValue = ACCENT_PRESETS.find((p) => p.id === preset)?.hsl ?? ACCENT_PRESETS[0].hsl;
      applyAccentToRoot(presetValue);
    },
    [],
  );

  useEffect(() => {
    applyAccent(accentPreset, accentCustomHex);
    window.localStorage.setItem(STORAGE_KEYS.accentPreset, accentPreset);
    if (accentCustomHex) {
      window.localStorage.setItem(STORAGE_KEYS.accentCustom, accentCustomHex);
    } else {
      window.localStorage.removeItem(STORAGE_KEYS.accentCustom);
    }
  }, [accentPreset, accentCustomHex, applyAccent]);

  const setTheme = useCallback((next: ThemeMode) => setThemeState(next), []);
  const setAccentPreset = useCallback((next: AccentPresetId) => setAccentPresetState(next), []);

  const setAccentCustomHex = useCallback((hex: string) => {
    const normalized = hex.trim();
    if (!normalized) {
      setAccentCustomHexState(null);
      return;
    }
    const withHash = normalized.startsWith('#') ? normalized : `#${normalized}`;
    setAccentCustomHexState(withHash);
  }, []);

  const clearAccentCustom = useCallback(() => setAccentCustomHexState(null), []);

  const value = useMemo<ThemeAccentContextValue>(
    () => ({
      theme,
      resolvedTheme,
      setTheme,
      accentPreset,
      accentCustomHex,
      setAccentPreset,
      setAccentCustomHex,
      clearAccentCustom,
    }),
    [theme, resolvedTheme, setTheme, accentPreset, accentCustomHex, setAccentPreset, setAccentCustomHex, clearAccentCustom],
  );

  return <ThemeAccentContext.Provider value={value}>{children}</ThemeAccentContext.Provider>;
}

export function useThemeAccent() {
  const ctx = React.useContext(ThemeAccentContext);
  if (!ctx) throw new Error('useThemeAccent must be used within ThemeAccentProvider');
  return ctx;
}

