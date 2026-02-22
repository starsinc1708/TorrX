# Search Ranking UX Design

**Date:** 2026-02-22

## Problem

Two pain points in the current search:

1. **UI complexity** — the ranking profile exposes 5 raw numeric weight sliders (seeders, quality, freshness, language, size). Users cannot intuit how changing a weight from 2 to 4 affects results.
2. **Quality mismatch** — there is no way to say "I want 1080p". The existing `qualityComponentScore` assigns fixed bonuses (4K=+8, 1080p=+7, 720p=+6, ...) regardless of user preference, so 4K releases always rank above 1080p even when the user doesn't want 4K.

## Solution: Presets + Quality Target

Replace the 5 sliders with 4 named preset buttons. Add a `PreferredQuality` dropdown that applies a proximity-based bonus to quality-matched results.

---

## Architecture

| Layer | Change |
|-------|--------|
| `services/torrent-search/internal/domain/` | Add `PreferredQuality string` to `SearchRankingProfile` |
| `services/torrent-search/internal/search/normalize.go` | Add `preferredQualityBonus(target, actual string) float64` |
| `services/torrent-search/internal/api/http/server.go` | Parse `preferred_quality` query param, pass to profile |
| `frontend/src/pages/SearchPage.tsx` | Replace 5 weight sliders with 4 preset buttons + quality dropdown |
| `frontend/src/api.ts` | Add `preferredQuality` to search request params |

No changes to other services. Profile persistence unchanged (localStorage via SearchProvider).

---

## Backend: Quality Target Scoring

### New field

```go
// in domain.SearchRankingProfile
PreferredQuality string // "", "4K", "1080p", "720p", "480p", "360p"
```

### Quality tier mapping

| Label | Tier |
|-------|------|
| 360p  | 0 |
| 480p  | 1 |
| 720p  | 2 |
| 1080p | 3 |
| 4K    | 4 |

### Bonus formula

```
preferredQualityBonus(target, actual):
  if target == "" → return 0   (no preference, no effect)
  distance = |tier(target) - tier(actual)|
  0  → +20
  1  → +8
  2  →  0
  3+ → -10
```

This bonus is **additive** to the existing `qualityComponentScore * QualityWeight` — the existing score is unchanged. Unknown quality strings (neither target nor actual parse to a known tier) → return 0.

### Example: target = "1080p" (tier 3)

| Result quality | Distance | Bonus |
|----------------|----------|-------|
| 4K             | 1        | +8    |
| 1080p          | 0        | +20   |
| 720p           | 1        | +8    |
| 480p           | 2        | 0     |
| 360p           | 3        | −10   |

---

## Frontend: Presets + Quality Dropdown

### Preset weight values

| Preset | seeders | quality | freshness | language | size |
|--------|---------|---------|-----------|----------|------|
| Balanced | 2 | 2 | 1 | 1 | 0.5 |
| Best Quality | 2 | 5 | 0.5 | 2 | 0 |
| Most Seeded | 5 | 1 | 0.5 | 1 | 0 |
| Compact | 2 | 1 | 0.5 | 1 | 5 |

### UI layout (in the ranking profile section of the filter panel)

```
Preset:
[Balanced] [Best Quality] [Most Seeded] [Compact]

Quality target:
[Auto ▾]  ← dropdown: Auto / 4K / 1080p / 720p / 480p
```

Active preset button is tinted with `text-primary`. Selecting a preset immediately sets the 5 weights. Quality target is independent of the preset (persisted separately).

The 5 numeric sliders are removed entirely.

All other filters (audio, subtitles, quality facets, size range, series/movies preference) are unchanged.

---

## Data Flow

1. User selects "Best Quality" preset → frontend sets `{qualityWeight:5, seedersWeight:2, freshnessWeight:0.5, languageWeight:2, sizeWeight:0}` in the ranking profile state.
2. User selects "1080p" from quality target dropdown → frontend sets `profile.preferredQuality = "1080p"`.
3. On search submit → `api.ts` sends `preferred_quality=1080p` in query params.
4. `handleSearchStream` in `server.go` reads `preferred_quality`, sets `profile.PreferredQuality = "1080p"`.
5. `relevanceScoreFromMeta` calls `preferredQualityBonus("1080p", enrichment.Quality)` and adds result to score.
6. Results sorted by score descending — 1080p releases float to top.

---

## Testing

### Go (normalize_test.go)

Table-driven test for `preferredQualityBonus`:

| target | actual | expected |
|--------|--------|----------|
| ""     | "1080p"| 0        |
| "1080p"| "1080p"| +20      |
| "1080p"| "4K"   | +8       |
| "1080p"| "720p" | +8       |
| "1080p"| "480p" | 0        |
| "1080p"| "360p" | −10      |
| "4K"   | "1080p"| +8       |
| "4K"   | "720p" | 0        |
| "4K"   | "480p" | −10      |
| "1080p"| ""     | 0 (unknown actual) |

### Frontend

No new unit tests — preset logic is pure constants + state assignment, fully covered by type-check.

---

## Out of Scope

- Showing score breakdown per result
- Learning from user click-through
- Log-scale seeder normalization
- Removing the 5-slider profile from the SearchProvider type (kept for future extensibility)
