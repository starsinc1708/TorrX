export type ThemeMode = 'system' | 'light' | 'dark';

export type AccentPresetId =
  | 'indigo'
  | 'blue'
  | 'teal'
  | 'green'
  | 'orange'
  | 'rose'
  | 'violet';

export type AccentPreset = {
  id: AccentPresetId;
  label: string;
  /** Space-separated HSL triplet: "H S% L%" (shadcn style). */
  hsl: string;
};

export const ACCENT_PRESETS: AccentPreset[] = [
  { id: 'indigo', label: 'Indigo', hsl: '239 84% 60%' },
  { id: 'blue', label: 'Blue', hsl: '217 91% 60%' },
  { id: 'teal', label: 'Teal', hsl: '172 66% 45%' },
  { id: 'green', label: 'Green', hsl: '142 71% 45%' },
  { id: 'orange', label: 'Orange', hsl: '24 95% 53%' },
  { id: 'rose', label: 'Rose', hsl: '346 77% 50%' },
  { id: 'violet', label: 'Violet', hsl: '262 83% 58%' },
];

export const STORAGE_KEYS = {
  theme: 'torrix.theme',
  accentPreset: 'torrix.accent.preset',
  accentCustom: 'torrix.accent.custom',
} as const;

const clamp = (n: number, min: number, max: number) => Math.max(min, Math.min(max, n));

export const parseHslTriplet = (value: string): { h: number; s: number; l: number } | null => {
  // Expect: "H S% L%"
  const parts = value.trim().split(/\s+/);
  if (parts.length !== 3) return null;
  const h = Number(parts[0]);
  const s = Number(parts[1].replace('%', ''));
  const l = Number(parts[2].replace('%', ''));
  if (![h, s, l].every((n) => Number.isFinite(n))) return null;
  return { h: clamp(h, 0, 360), s: clamp(s, 0, 100), l: clamp(l, 0, 100) };
};

export const formatHslTriplet = (h: number, s: number, l: number) =>
  `${Math.round(clamp(h, 0, 360))} ${Math.round(clamp(s, 0, 100))}% ${Math.round(clamp(l, 0, 100))}%`;

export const hexToHslTriplet = (hex: string): string | null => {
  const raw = hex.trim().replace('#', '');
  if (![3, 6].includes(raw.length)) return null;

  const full =
    raw.length === 3 ? raw.split('').map((c) => c + c).join('') : raw;
  const r = parseInt(full.slice(0, 2), 16) / 255;
  const g = parseInt(full.slice(2, 4), 16) / 255;
  const b = parseInt(full.slice(4, 6), 16) / 255;
  if (![r, g, b].every((n) => Number.isFinite(n))) return null;

  const max = Math.max(r, g, b);
  const min = Math.min(r, g, b);
  const delta = max - min;

  let h = 0;
  if (delta !== 0) {
    if (max === r) h = ((g - b) / delta) % 6;
    else if (max === g) h = (b - r) / delta + 2;
    else h = (r - g) / delta + 4;
    h *= 60;
    if (h < 0) h += 360;
  }

  const l = (max + min) / 2;
  const s = delta === 0 ? 0 : delta / (1 - Math.abs(2 * l - 1));

  return formatHslTriplet(h, s * 100, l * 100);
};

export const pickPrimaryForegroundForHsl = (hslTriplet: string): string => {
  const parsed = parseHslTriplet(hslTriplet);
  // Fallback: white text
  if (!parsed) return '0 0% 100%';
  // If accent is very light, use near-black text.
  return parsed.l >= 68 ? '240 10% 3.9%' : '0 0% 100%';
};

export const applyAccentToRoot = (hslTriplet: string) => {
  const root = document.documentElement;
  root.style.setProperty('--primary', hslTriplet);
  root.style.setProperty('--ring', hslTriplet);
  root.style.setProperty('--primary-foreground', pickPrimaryForegroundForHsl(hslTriplet));
};
