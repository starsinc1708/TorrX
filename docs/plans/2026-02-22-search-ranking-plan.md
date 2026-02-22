# Search Ranking UX Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace 5 abstract weight sliders with 4 named preset buttons, and add a "Preferred Quality" target that boosts/penalises results by proximity to the desired resolution.

**Architecture:** Add `PreferredQuality string` to the domain `SearchRankingProfile` struct. A new `preferredQualityBonus()` function in `normalize.go` adds a score delta based on tier distance. The frontend replaces the slider grid with preset buttons + quality dropdown; presets set the 5 weights as named constants.

**Tech Stack:** Go (backend scoring + HTTP parsing), React 18 + TypeScript + Tailwind (frontend), Vitest + `go test` (tests).

---

## Key files

| File | Action |
|------|--------|
| `services/torrent-search/internal/domain/search.go` | **Modify** — add `PreferredQuality string` to struct + leave `DefaultSearchRankingProfile` and `NormalizeRankingProfile` unchanged |
| `services/torrent-search/internal/search/normalize.go` | **Modify** — add `qualityTiers` map + `preferredQualityBonus()` function + one call in `relevanceScoreFromMeta` |
| `services/torrent-search/internal/search/normalize_test.go` | **Modify** — add `TestPreferredQualityBonus` table-driven test |
| `services/torrent-search/internal/api/http/server.go` | **Modify** — parse `preferred_quality` query param in `parseRankingProfile` |
| `frontend/src/types.ts` | **Modify** — add `preferredQuality: string` to `SearchRankingProfile` interface |
| `frontend/src/lib/search-utils.ts` | **Modify** — add `preferredQuality: ''` to `defaultRankingProfile` and `loadStoredProfile` |
| `frontend/src/api.ts` | **Modify** — add `preferred_quality` param to `appendRankingProfile` |
| `frontend/src/pages/SearchPage.tsx` | **Modify** — remove `updateWeight` + sliders, add presets + quality dropdown |

---

## Context for implementer

**normalize.go** (`relevanceScoreFromMeta`, line ~543):
```go
qualityComponent := qualityComponentScore(enrichment)    // line ~593
// ...
score += profile.QualityWeight * qualityComponent        // line ~596
// INSERT preferredQualityBonus call HERE, after the QualityWeight line
score += profile.LanguageWeight * languagePreferenceScore(...)
```

`enrichment` in `relevanceScoreFromMeta` is a local variable of type `domain.SearchEnrichment`. Its `Quality` field is a string like `"4K"`, `"1080p"`, `"720p"`, `"480p"`, `"360p"` or `""` (empty when unknown).

**server.go** `parseRankingProfile` ends with:
```go
    profile.TargetSizeBytes = value     // last parsing block
}
return domain.NormalizeRankingProfile(profile), nil
```
Add `preferred_quality` parsing between the TargetSizeBytes block and the `return`.

**`NormalizeRankingProfile`** only clamps the 5 float weights and `TargetSizeBytes`. It does **not** touch `PreferredQuality` — no changes needed there.

**SearchPage.tsx** sliders live in a `<div className="mt-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-3">` block (~line 611) that maps over the 5 weight fields. Replace the entire grid div with the preset buttons + quality dropdown.

**`updateWeight`** callback (~line 109) is only used by the slider inputs. After removing the sliders, verify it is unused (grep for `updateWeight` in the file), then delete it.

**`search-utils.ts`** — `loadStoredProfile()` spreads `parsed` over `defaultRankingProfile`. Add explicit `preferredQuality` handling so that stored values load correctly.

---

## Task 1 — Go backend: `PreferredQuality` field + scoring

**Files:**
- Modify: `services/torrent-search/internal/domain/search.go` (lines ~61-72)
- Modify: `services/torrent-search/internal/search/normalize.go` (lines ~543-628, ~717-766)
- Modify: `services/torrent-search/internal/search/normalize_test.go`
- Modify: `services/torrent-search/internal/api/http/server.go` (lines ~839-877)

### Step 1 — Write the failing test

Add this function to `services/torrent-search/internal/search/normalize_test.go` (at the end of the file):

```go
func TestPreferredQualityBonus(t *testing.T) {
	cases := []struct {
		target   string
		actual   string
		expected float64
	}{
		// No preference → always 0
		{"", "1080p", 0},
		{"", "", 0},
		// Exact match → +20
		{"1080p", "1080p", 20},
		{"4K", "4K", 20},
		{"720p", "720p", 20},
		// Distance 1 → +8
		{"1080p", "4K", 8},
		{"1080p", "720p", 8},
		{"4K", "1080p", 8},
		{"720p", "480p", 8},
		// Distance 2 → 0
		{"1080p", "480p", 0},
		{"4K", "720p", 0},
		// Distance 3+ → -10
		{"1080p", "360p", -10},
		{"4K", "480p", -10},
		{"4K", "360p", -10},
		// Unknown actual → 0
		{"1080p", "", 0},
		{"1080p", "Unknown", 0},
	}
	for _, tc := range cases {
		t.Run(tc.target+"/"+tc.actual, func(t *testing.T) {
			got := preferredQualityBonus(tc.target, tc.actual)
			if got != tc.expected {
				t.Errorf("preferredQualityBonus(%q, %q) = %v, want %v",
					tc.target, tc.actual, got, tc.expected)
			}
		})
	}
}
```

### Step 2 — Run test to verify it fails

```bash
cd services/torrent-search && go test ./internal/search/ -run TestPreferredQualityBonus -v
```

Expected: `FAIL — undefined: preferredQualityBonus`

### Step 3 — Add `PreferredQuality` to the domain struct

In `services/torrent-search/internal/domain/search.go`, find the `SearchRankingProfile` struct (lines ~61-72):

```go
type SearchRankingProfile struct {
	FreshnessWeight    float64  `json:"freshnessWeight"`
	SeedersWeight      float64  `json:"seedersWeight"`
	QualityWeight      float64  `json:"qualityWeight"`
	LanguageWeight     float64  `json:"languageWeight"`
	SizeWeight         float64  `json:"sizeWeight"`
	PreferSeries       bool     `json:"preferSeries"`
	PreferMovies       bool     `json:"preferMovies"`
	PreferredAudio     []string `json:"preferredAudio,omitempty"`
	PreferredSubtitles []string `json:"preferredSubtitles,omitempty"`
	TargetSizeBytes    int64    `json:"targetSizeBytes,omitempty"`
}
```

Add `PreferredQuality` after `TargetSizeBytes`:

```go
type SearchRankingProfile struct {
	FreshnessWeight    float64  `json:"freshnessWeight"`
	SeedersWeight      float64  `json:"seedersWeight"`
	QualityWeight      float64  `json:"qualityWeight"`
	LanguageWeight     float64  `json:"languageWeight"`
	SizeWeight         float64  `json:"sizeWeight"`
	PreferSeries       bool     `json:"preferSeries"`
	PreferMovies       bool     `json:"preferMovies"`
	PreferredAudio     []string `json:"preferredAudio,omitempty"`
	PreferredSubtitles []string `json:"preferredSubtitles,omitempty"`
	TargetSizeBytes    int64    `json:"targetSizeBytes,omitempty"`
	PreferredQuality   string   `json:"preferredQuality,omitempty"`
}
```

Do **not** touch `DefaultSearchRankingProfile` (empty string is the correct zero value) or `NormalizeRankingProfile` (it only clamps floats).

### Step 4 — Add `preferredQualityBonus` to normalize.go

Add these two declarations to `services/torrent-search/internal/search/normalize.go` — place them just before `qualityComponentScore` (which is around line ~717):

```go
// qualityTiers maps a quality label to a tier index.
// Higher tier = higher resolution.
var qualityTiers = map[string]int{
	"360p":  0,
	"480p":  1,
	"720p":  2,
	"1080p": 3,
	"4K":    4,
}

// preferredQualityBonus returns a score adjustment based on how close actual
// quality is to the user's preferred quality target.
// Returns 0 when target is empty (no preference) or either quality is unknown.
func preferredQualityBonus(target, actual string) float64 {
	if target == "" {
		return 0
	}
	targetTier, ok := qualityTiers[target]
	if !ok {
		return 0
	}
	actualTier, ok := qualityTiers[actual]
	if !ok {
		return 0
	}
	dist := targetTier - actualTier
	if dist < 0 {
		dist = -dist
	}
	switch dist {
	case 0:
		return 20
	case 1:
		return 8
	case 2:
		return 0
	default:
		return -10
	}
}
```

### Step 5 — Call `preferredQualityBonus` in `relevanceScoreFromMeta`

In `relevanceScoreFromMeta` (~line 543), find this block:

```go
qualityComponent := qualityComponentScore(enrichment)
// ...
score += profile.QualityWeight * qualityComponent
```

Add one line **immediately after** `score += profile.QualityWeight * qualityComponent`:

```go
score += preferredQualityBonus(profile.PreferredQuality, enrichment.Quality)
```

### Step 6 — Parse `preferred_quality` in server.go

In `parseRankingProfile` (~line 839), find the end of the function just before `return`:

```go
	if target := strings.TrimSpace(r.URL.Query().Get("targetSizeBytes")); target != "" {
		// ...
		profile.TargetSizeBytes = value
	}
	return domain.NormalizeRankingProfile(profile), nil
```

Insert between the `TargetSizeBytes` block and the `return`:

```go
	if q := strings.TrimSpace(r.URL.Query().Get("preferred_quality")); q != "" {
		profile.PreferredQuality = q
	}
	return domain.NormalizeRankingProfile(profile), nil
```

### Step 7 — Run all Go tests

```bash
cd services/torrent-search && go test ./...
```

Expected: all pass including `TestPreferredQualityBonus` (17 sub-tests).

### Step 8 — Build check

```bash
cd services/torrent-search && go build ./...
```

Expected: no errors.

### Step 9 — Commit

```bash
cd /c/1_Projects/torrent-stream
git add services/torrent-search/internal/domain/search.go \
        services/torrent-search/internal/search/normalize.go \
        services/torrent-search/internal/search/normalize_test.go \
        services/torrent-search/internal/api/http/server.go
git commit -m "feat(search): add PreferredQuality target scoring to ranking profile"
```

---

## Task 2 — Frontend: types, api, defaults

**Files:**
- Modify: `frontend/src/types.ts` (lines ~360-371)
- Modify: `frontend/src/lib/search-utils.ts` (lines ~63-74 for default, ~134-149 for loadStoredProfile)
- Modify: `frontend/src/api.ts` (lines ~397-411 `appendRankingProfile`)

### Step 1 — Add `preferredQuality` to `SearchRankingProfile` type

In `frontend/src/types.ts`, find the `SearchRankingProfile` interface (lines ~360-371):

```typescript
export interface SearchRankingProfile {
  freshnessWeight: number;
  seedersWeight: number;
  qualityWeight: number;
  languageWeight: number;
  sizeWeight: number;
  preferSeries: boolean;
  preferMovies: boolean;
  preferredAudio: string[];
  preferredSubtitles: string[];
  targetSizeBytes: number;
}
```

Add `preferredQuality: string` after `targetSizeBytes`:

```typescript
export interface SearchRankingProfile {
  freshnessWeight: number;
  seedersWeight: number;
  qualityWeight: number;
  languageWeight: number;
  sizeWeight: number;
  preferSeries: boolean;
  preferMovies: boolean;
  preferredAudio: string[];
  preferredSubtitles: string[];
  targetSizeBytes: number;
  preferredQuality: string;
}
```

### Step 2 — Add `preferredQuality` to default profile

In `frontend/src/lib/search-utils.ts`, find `defaultRankingProfile` (lines ~63-74). Add `preferredQuality: ''` after `targetSizeBytes`:

```typescript
export const defaultRankingProfile: SearchRankingProfile = {
  freshnessWeight: 1,
  seedersWeight: 1,
  qualityWeight: 1,
  languageWeight: 5,
  sizeWeight: 0.4,
  preferSeries: true,
  preferMovies: true,
  preferredAudio: ['RU'],
  preferredSubtitles: ['RU'],
  targetSizeBytes: 0,
  preferredQuality: '',
};
```

### Step 3 — Add `preferredQuality` to `loadStoredProfile`

In `frontend/src/lib/search-utils.ts`, find `loadStoredProfile` (lines ~134-149). Add `preferredQuality` handling in the return object:

```typescript
export const loadStoredProfile = (): SearchRankingProfile => {
  try {
    const raw = window.localStorage.getItem(profileStorageKey);
    if (!raw) return defaultRankingProfile;
    const parsed = JSON.parse(raw) as Partial<SearchRankingProfile>;
    return {
      ...defaultRankingProfile,
      ...parsed,
      preferredAudio: Array.isArray(parsed.preferredAudio) ? parsed.preferredAudio : [],
      preferredSubtitles: Array.isArray(parsed.preferredSubtitles) ? parsed.preferredSubtitles : [],
      targetSizeBytes: Number(parsed.targetSizeBytes) > 0 ? Number(parsed.targetSizeBytes) : 0,
      preferredQuality: typeof parsed.preferredQuality === 'string' ? parsed.preferredQuality : '',
    };
  } catch {
    return defaultRankingProfile;
  }
};
```

### Step 4 — Add `preferred_quality` to `appendRankingProfile` in api.ts

In `frontend/src/api.ts`, find `appendRankingProfile` (lines ~397-411). Add one line after the `targetSizeBytes` line:

```typescript
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
  if (profile.preferredQuality) params.set('preferred_quality', profile.preferredQuality);
};
```

### Step 5 — Type-check

```bash
cd frontend && npx tsc --noEmit
```

Expected: no errors.

### Step 6 — Commit

```bash
cd /c/1_Projects/torrent-stream
git add frontend/src/types.ts \
        frontend/src/lib/search-utils.ts \
        frontend/src/api.ts
git commit -m "feat(search): add preferredQuality field to frontend types, defaults, and API client"
```

---

## Task 3 — Frontend: SearchPage presets UI

**Files:**
- Modify: `frontend/src/pages/SearchPage.tsx`

### Step 1 — Add preset constants at the top of SearchPage component

Find the `SearchPage` component body (just after `const SearchPage: React.FC = () => {`, around line 43). Add the preset definitions before any hooks:

```tsx
const RANKING_PRESETS = {
  balanced:   { freshnessWeight: 1,   seedersWeight: 2, qualityWeight: 2, languageWeight: 1, sizeWeight: 0.5 },
  bestQuality:{ freshnessWeight: 0.5, seedersWeight: 2, qualityWeight: 5, languageWeight: 2, sizeWeight: 0   },
  mostSeeded: { freshnessWeight: 0.5, seedersWeight: 5, qualityWeight: 1, languageWeight: 1, sizeWeight: 0   },
  compact:    { freshnessWeight: 0.5, seedersWeight: 2, qualityWeight: 1, languageWeight: 1, sizeWeight: 5   },
} as const;

type PresetKey = keyof typeof RANKING_PRESETS;
```

Place them **inside** the component function body, immediately before the `const search = useSearch()` line (~line 44).

### Step 2 — Remove `updateWeight` and add `applyPreset`

Find the `updateWeight` callback (~line 109):

```tsx
const updateWeight = useCallback(
  (
    field: 'freshnessWeight' | 'seedersWeight' | 'qualityWeight' | 'languageWeight' | 'sizeWeight',
    value: number,
  ) => {
    setProfile((prev) => ({ ...prev, [field]: value }));
  },
  [setProfile],
);
```

Verify `updateWeight` is not used anywhere else in the file (search for `updateWeight` — it should only appear in the slider `onChange` handlers that we're removing). Delete the entire `updateWeight` callback.

Add `activePreset` and `applyPreset` in its place:

```tsx
const activePreset = useMemo((): PresetKey | null => {
  for (const [key, preset] of Object.entries(RANKING_PRESETS) as [PresetKey, (typeof RANKING_PRESETS)[PresetKey]][]) {
    if (
      profile.freshnessWeight === preset.freshnessWeight &&
      profile.seedersWeight === preset.seedersWeight &&
      profile.qualityWeight === preset.qualityWeight &&
      profile.languageWeight === preset.languageWeight &&
      profile.sizeWeight === preset.sizeWeight
    ) {
      return key;
    }
  }
  return null;
}, [profile]);

const applyPreset = useCallback(
  (key: PresetKey) => {
    setProfile((prev) => ({ ...prev, ...RANKING_PRESETS[key] }));
  },
  [setProfile],
);
```

### Step 3 — Replace slider grid with preset buttons + quality dropdown

Find the slider grid block. It starts with:
```tsx
<div className="mt-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
  {([
    ['freshnessWeight', 'Freshness'] as const,
```
and ends after the last slider `</div>` closing the grid.

Replace the entire `<div className="mt-4 grid gap-3 ...">` block with:

```tsx
<div className="mt-4 space-y-3">
  {/* Preset buttons */}
  <div className="flex flex-wrap gap-2">
    {(
      [
        ['balanced', 'Balanced'],
        ['bestQuality', 'Best Quality'],
        ['mostSeeded', 'Most Seeded'],
        ['compact', 'Compact'],
      ] as [PresetKey, string][]
    ).map(([key, label]) => (
      <button
        key={key}
        onClick={() => applyPreset(key)}
        className={cn(
          'rounded-lg border px-3 py-1.5 text-sm transition-colors',
          activePreset === key
            ? 'border-primary bg-primary/10 text-primary'
            : 'border-border bg-background/40 text-muted-foreground hover:text-foreground',
        )}
      >
        {label}
      </button>
    ))}
  </div>

  {/* Quality target */}
  <div className="flex items-center gap-3">
    <span className="shrink-0 text-sm text-muted-foreground">Quality target</span>
    <select
      value={profile.preferredQuality ?? ''}
      onChange={(e) => setProfile((prev) => ({ ...prev, preferredQuality: e.target.value }))}
      className="ts-select ts-dropdown-trigger rounded-lg px-2 py-1 text-sm"
    >
      <option value="">Auto</option>
      <option value="4K">4K</option>
      <option value="1080p">1080p</option>
      <option value="720p">720p</option>
      <option value="480p">480p</option>
    </select>
  </div>
</div>
```

### Step 4 — Type-check

```bash
cd frontend && npx tsc --noEmit
```

Expected: no errors.

If TypeScript complains about `Object.entries(RANKING_PRESETS)` typing, use explicit cast `as [PresetKey, (typeof RANKING_PRESETS)[PresetKey]][]` as shown above.

### Step 5 — Run all tests

```bash
cd frontend && npx vitest run
```

Expected: all tests pass (slider tests don't exist, so nothing breaks).

### Step 6 — Commit

```bash
cd /c/1_Projects/torrent-stream
git add frontend/src/pages/SearchPage.tsx
git commit -m "feat(search): replace weight sliders with preset buttons and quality target dropdown"
```

---

## Verification

After all three tasks:

```bash
# Go tests
cd services/torrent-search && go test ./...

# Frontend type-check
cd frontend && npx tsc --noEmit

# Frontend tests
cd frontend && npx vitest run

# Go build
cd services/torrent-search && go build ./...
```

**Manual checklist (dev server):**
- [ ] Search page shows 4 preset buttons (Balanced, Best Quality, Most Seeded, Compact)
- [ ] Clicking a preset tints that button with `text-primary`
- [ ] "Quality target" dropdown shows Auto / 4K / 1080p / 720p / 480p
- [ ] Selecting "1080p" and searching — 1080p results rank above 4K and 480p
- [ ] Selecting "Auto" — order reverts to previous behaviour (no quality penalty)
- [ ] Preset selection persists across page reload (`localStorage`)
- [ ] Quality target persists across page reload
