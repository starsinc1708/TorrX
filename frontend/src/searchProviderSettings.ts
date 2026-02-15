const SEARCH_ENABLED_PROVIDERS_KEY = 'search-enabled-providers:v1';
const SEARCH_PROVIDERS_CHANGED_EVENT = 'search:providers-changed';

const normalizeProviderNames = (items: string[]): string[] => {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const item of items) {
    const value = item.trim().toLowerCase();
    if (!value || seen.has(value)) continue;
    seen.add(value);
    out.push(value);
  }
  return out;
};

export const loadStoredEnabledSearchProviders = (): string[] | null => {
  try {
    const raw = window.localStorage.getItem(SEARCH_ENABLED_PROVIDERS_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) return null;
    return normalizeProviderNames(parsed.map((item) => String(item)));
  } catch {
    return null;
  }
};

export const resolveEnabledSearchProviders = (availableProviderNames: string[]): string[] => {
  const available = normalizeProviderNames(availableProviderNames);
  const stored = loadStoredEnabledSearchProviders();
  if (!stored) return available;
  return stored.filter((name) => available.includes(name));
};

export const saveEnabledSearchProviders = (providerNames: string[]): void => {
  const normalized = normalizeProviderNames(providerNames);
  window.localStorage.setItem(SEARCH_ENABLED_PROVIDERS_KEY, JSON.stringify(normalized));
  window.dispatchEvent(
    new CustomEvent<string[]>(SEARCH_PROVIDERS_CHANGED_EVENT, { detail: normalized }),
  );
};

export const onSearchProvidersChanged = (
  handler: (providerNames: string[]) => void,
): (() => void) => {
  const wrapped = (event: Event) => {
    const custom = event as CustomEvent<string[]>;
    if (!Array.isArray(custom.detail)) return;
    handler(normalizeProviderNames(custom.detail));
  };
  window.addEventListener(SEARCH_PROVIDERS_CHANGED_EVENT, wrapped);
  return () => window.removeEventListener(SEARCH_PROVIDERS_CHANGED_EVENT, wrapped);
};

