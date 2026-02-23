# UX Improvements Design: File Hash, Language Reorder, Watch History

**Date:** 2026-02-23
**Status:** Approved

## Feature 1: OpenSubtitles File Hash Search

**Problem:** Subtitle search currently relies on filename matching, which is unreliable. OpenSubtitles moviehash gives much better matches.

**Approach:** Compute moviehash server-side. OpenSubtitles moviehash = sum of first 64KB + last 64KB of file + file size (uint64, little-endian hex).

### Changes

- New function `ComputeMovieHash(filePath string) (string, error)` in `internal/services/subtitles/opensubtitles/`
- Update `handleSubtitleSearch`: accept `torrentId` + `fileIndex` query params. If file is available on disk via `dataDir` → compute hash → search by hash first. Fallback to filename query if hash returns no results.
- Frontend: pass `torrentId` + `fileIndex` to search endpoint. Backend resolves file path internally.

### API Changes

```
GET /torrents/subtitles/search?torrentId=X&fileIndex=Y&lang=ru,en
```

Backend flow:
1. Resolve file path from torrentId + fileIndex via engine
2. Compute moviehash from disk file
3. Search OpenSubtitles by hash
4. If no results → fallback to query (filename)
5. Return results

## Feature 2: Drag-to-Reorder Subtitle Languages

**Problem:** Languages are currently entered as comma-separated text — no visual ordering, easy to mistype.

**Approach:** Use `@dnd-kit/core` + `@dnd-kit/sortable` for drag-and-drop reorderable chips.

### Changes

- Install `@dnd-kit/core`, `@dnd-kit/sortable`, `@dnd-kit/utilities`
- Replace comma-separated input in SettingsPage subtitles section with:
  - Sortable list of language chips (drag handle + label + remove X)
  - "Add language" button with dropdown of common ISO 639-1 codes
- API payload unchanged: `languages: string[]` — order is priority

## Feature 3: Watch History Improvements

### 3a. Progress Percentage

**Problem:** No visual indication of how much of a file has been watched.

**Changes:**
- Backend: add computed `progress` field (float 0.0–1.0) to `WatchPosition` JSON response. Calculated as `position / duration`. Not stored in DB — derived on read.
- Frontend: show progress bar under each file in torrent details panel. Use existing watch history data.

### 3b. Continue Watching Section

**Problem:** No quick way to resume partially watched content.

**Changes:**
- Backend: add `status` query param to `GET /watch-history`. Value `incomplete` filters to entries where `position >= 10` and `position < duration - 15`.
- Frontend: horizontal "Continue Watching" carousel at top of CatalogPage. Cards show: torrent name, file name, progress bar, "Resume" button linking to PlayerPage with resume position.
- Only shown when there are incomplete items. Max 10 items, sorted by updatedAt DESC.

### 3c. Auto Content Type Tag

**Problem:** No distinction between movies and series in watch history.

**Changes:**
- Backend: on `WatchPosition` save (PUT), parse `filePath` for episode patterns (`S\d+E\d+`, `season`, `episode`, etc.) → set `contentType: "series" | "movie"` in MongoDB document. Default to `"movie"` if no pattern matched.
- Add `contentType` field to `WatchPosition` domain struct and MongoDB document.
- Frontend: show film/series icon badge on Continue Watching cards and torrent details.

## Implementation Order

1. Feature 1 (file hash) — independent, backend-focused
2. Feature 2 (language reorder) — independent, frontend-focused
3. Feature 3a (progress) — small backend + frontend change
4. Feature 3b (continue watching) — backend filter + new frontend component
5. Feature 3c (auto content type) — backend parsing + frontend badge
