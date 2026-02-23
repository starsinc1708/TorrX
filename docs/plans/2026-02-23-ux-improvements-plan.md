# UX Improvements Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add OpenSubtitles file hash search, drag-to-reorder subtitle languages, and watch-history improvements (progress, continue watching, auto content type).

**Architecture:** Three independent feature tracks. Feature 1 adds server-side moviehash computation to the existing subtitle search handler. Feature 2 replaces a text input with a dnd-kit sortable list in SettingsPage. Feature 3 extends WatchPosition domain model with computed `progress` and stored `contentType`, adds a "Continue Watching" carousel to CatalogPage.

**Tech Stack:** Go (torrent-engine), React + TypeScript, @dnd-kit/core + @dnd-kit/sortable, MongoDB, Tailwind CSS

---

## Task 1: OpenSubtitles MovieHash — Implementation

**Files:**
- Create: `services/torrent-engine/internal/services/subtitles/opensubtitles/hash.go`
- Create: `services/torrent-engine/internal/services/subtitles/opensubtitles/hash_test.go`
- Modify: `services/torrent-engine/internal/api/http/handlers_subtitles.go`
- Modify: `frontend/src/api.ts`
- Modify: `frontend/src/pages/PlayerPage.tsx`

### Context

OpenSubtitles moviehash algorithm:
1. Take file size as uint64
2. Read first 65536 bytes, interpret as 8192 uint64 little-endian values, sum them
3. Read last 65536 bytes, interpret as 8192 uint64 little-endian values, sum them
4. Add file size to the sum
5. Return as 16-char lowercase hex string

The server already has access to files on disk via `s.mediaDataDir` and `resolveDataFilePath()` (defined in `server_utils.go:118`). The server has `s.engine` (`ports.Engine`) which provides `GetSessionState(ctx, torrentID)` returning `domain.SessionState` with `Files []FileRef` (each has `Path string`).

### Step 1: Write hash function and test

Create `services/torrent-engine/internal/services/subtitles/opensubtitles/hash.go`:

```go
package opensubtitles

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

const hashChunkSize = 65536

// ComputeMovieHash computes the OpenSubtitles movie hash for the given file.
// Algorithm: sum of first and last 64KB as uint64 little-endian + file size.
func ComputeMovieHash(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("stat file: %w", err)
	}
	size := info.Size()
	if size < hashChunkSize*2 {
		return "", fmt.Errorf("file too small for hash: %d bytes", size)
	}

	hash := uint64(size)

	buf := make([]byte, hashChunkSize)

	// Read first 64KB.
	if _, err := io.ReadFull(f, buf); err != nil {
		return "", fmt.Errorf("read head: %w", err)
	}
	for i := 0; i < hashChunkSize/8; i++ {
		hash += binary.LittleEndian.Uint64(buf[i*8 : i*8+8])
	}

	// Read last 64KB.
	if _, err := f.Seek(-hashChunkSize, io.SeekEnd); err != nil {
		return "", fmt.Errorf("seek tail: %w", err)
	}
	if _, err := io.ReadFull(f, buf); err != nil {
		return "", fmt.Errorf("read tail: %w", err)
	}
	for i := 0; i < hashChunkSize/8; i++ {
		hash += binary.LittleEndian.Uint64(buf[i*8 : i*8+8])
	}

	return fmt.Sprintf("%016x", hash), nil
}
```

Create `services/torrent-engine/internal/services/subtitles/opensubtitles/hash_test.go`:

```go
package opensubtitles

import (
	"os"
	"path/filepath"
	"testing"
)

func TestComputeMovieHash(t *testing.T) {
	// Create a temp file >= 128KB with deterministic content.
	dir := t.TempDir()
	path := filepath.Join(dir, "test.mkv")

	data := make([]byte, 256*1024) // 256KB
	for i := range data {
		data[i] = byte(i % 256)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	hash, err := ComputeMovieHash(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hash) != 16 {
		t.Errorf("hash length = %d, want 16", len(hash))
	}

	// Same file should produce same hash.
	hash2, err := ComputeMovieHash(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hash != hash2 {
		t.Errorf("hash mismatch: %s != %s", hash, hash2)
	}
}

func TestComputeMovieHash_TooSmall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tiny.mkv")
	if err := os.WriteFile(path, make([]byte, 1000), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := ComputeMovieHash(path)
	if err == nil {
		t.Error("expected error for small file")
	}
}
```

**Verify:** `cd services/torrent-engine && go test ./internal/services/subtitles/opensubtitles/ -run TestComputeMovieHash -v`

### Step 2: Update subtitle search handler

Modify `services/torrent-engine/internal/api/http/handlers_subtitles.go` — update `handleSubtitleSearch`:

Replace the current function with one that accepts optional `torrentId` + `fileIndex` params. If present and file is on disk, compute moviehash and search by it first. Keep existing `query`/`hash`/`lang` params as fallback.

```go
func (s *Server) handleSubtitleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.subtitles == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "subtitle settings not configured")
		return
	}

	settings := s.subtitles.Get()
	if settings.APIKey == "" {
		writeError(w, http.StatusBadRequest, "no_api_key", "OpenSubtitles API key not configured")
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("query"))
	hash := strings.TrimSpace(r.URL.Query().Get("hash"))
	langParam := strings.TrimSpace(r.URL.Query().Get("lang"))
	torrentID := strings.TrimSpace(r.URL.Query().Get("torrentId"))
	fileIndexStr := strings.TrimSpace(r.URL.Query().Get("fileIndex"))

	// If torrentId + fileIndex provided, try to compute moviehash from disk file.
	if torrentID != "" && fileIndexStr != "" && hash == "" && s.engine != nil && s.mediaDataDir != "" {
		fileIndex, parseErr := strconv.Atoi(fileIndexStr)
		if parseErr == nil && fileIndex >= 0 {
			state, stateErr := s.engine.GetSessionState(r.Context(), domain.TorrentID(torrentID))
			if stateErr == nil && fileIndex < len(state.Files) {
				filePath, pathErr := resolveDataFilePath(s.mediaDataDir, state.Files[fileIndex].Path)
				if pathErr == nil {
					if computed, hashErr := opensubtitles.ComputeMovieHash(filePath); hashErr == nil {
						hash = computed
					}
					// Use filename as fallback query if none provided.
					if query == "" {
						query = cleanFilenameForQuery(state.Files[fileIndex].Path)
					}
				}
			}
		}
	}

	if query == "" && hash == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "query or hash required")
		return
	}

	var langs []string
	if langParam != "" {
		langs = strings.Split(langParam, ",")
	} else {
		langs = settings.Languages
	}

	client := opensubtitles.NewClient(settings.APIKey)

	var results []opensubtitles.SubtitleResult
	var err error

	if hash != "" {
		results, err = client.Search(r.Context(), opensubtitles.SearchParams{
			MovieHash: hash,
			Languages: langs,
		})
		if err != nil {
			slog.Error("subtitle search failed", "error", err)
			writeError(w, http.StatusBadGateway, "search_failed", "subtitle search failed")
			return
		}
	}

	if len(results) == 0 && query != "" {
		results, err = client.Search(r.Context(), opensubtitles.SearchParams{
			Query:     query,
			Languages: langs,
		})
		if err != nil {
			slog.Error("subtitle search failed", "error", err)
			writeError(w, http.StatusBadGateway, "search_failed", "subtitle search failed")
			return
		}
	}

	writeJSON(w, http.StatusOK, subtitleSearchResponse{Results: results})
}

// cleanFilenameForQuery extracts a human-readable search query from a file path.
func cleanFilenameForQuery(filePath string) string {
	base := filepath.Base(filePath)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)
	// Replace dots/underscores with spaces.
	name = strings.NewReplacer(".", " ", "_", " ").Replace(name)
	return strings.TrimSpace(name)
}
```

Add import for `strconv` and `path/filepath` to the import block. The `domain` and `opensubtitles` imports are already present.

**Verify:** `cd services/torrent-engine && go build ./... && go test ./internal/api/http/ -v`

### Step 3: Update frontend API client

Modify `frontend/src/api.ts` — update `searchSubtitles` (around line 732):

```typescript
export const searchSubtitles = async (
  options: {
    query?: string;
    torrentId?: string;
    fileIndex?: number;
    lang?: string[];
  },
): Promise<SubtitleSearchResponse> => {
  const params = new URLSearchParams();
  if (options.query) params.set('query', options.query);
  if (options.torrentId) params.set('torrentId', options.torrentId);
  if (options.fileIndex !== undefined) params.set('fileIndex', String(options.fileIndex));
  if (options.lang && options.lang.length > 0) params.set('lang', options.lang.join(','));
  const response = await deduplicatedFetch(
    buildUrl(`/torrents/subtitles/search?${params}`),
  );
  return handleResponse(response);
};
```

### Step 4: Update PlayerPage to pass torrentId/fileIndex

Modify `frontend/src/pages/PlayerPage.tsx` — find where `searchSubtitles` is called and update to pass torrentId + fileIndex instead of (or in addition to) the filename query. The call site is in `handleSearchSubtitles`. Change:

```typescript
const res = await searchSubtitles({
  torrentId: id,
  fileIndex: selectedFile,
  query: mediaInfo?.name || '',
  lang: subtitleSettings?.languages,
});
```

**Verify:** `cd frontend && npx tsc --noEmit`

**Commit:** `git commit -m "feat(subtitles): add server-side moviehash computation for subtitle search"`

---

## Task 2: Drag-to-Reorder Subtitle Languages

**Files:**
- Modify: `frontend/package.json` (new deps)
- Create: `frontend/src/components/SortableLanguageList.tsx`
- Modify: `frontend/src/pages/SettingsPage.tsx` (lines ~1379-1397)

### Step 1: Install dnd-kit dependencies

Run: `cd frontend && npm install @dnd-kit/core @dnd-kit/sortable @dnd-kit/utilities`

### Step 2: Create SortableLanguageList component

Create `frontend/src/components/SortableLanguageList.tsx`:

```tsx
import { useState } from 'react';
import {
  DndContext,
  closestCenter,
  KeyboardSensor,
  PointerSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
} from '@dnd-kit/core';
import {
  arrayMove,
  SortableContext,
  sortableKeyboardCoordinates,
  useSortable,
  horizontalListSortingStrategy,
} from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import { GripVertical, X, Plus } from 'lucide-react';
import { cn } from '@/lib/cn';
import { Button } from '@/components/ui/button';

const COMMON_LANGUAGES = [
  { code: 'en', label: 'English' },
  { code: 'ru', label: 'Russian' },
  { code: 'es', label: 'Spanish' },
  { code: 'fr', label: 'French' },
  { code: 'de', label: 'German' },
  { code: 'pt', label: 'Portuguese' },
  { code: 'it', label: 'Italian' },
  { code: 'zh', label: 'Chinese' },
  { code: 'ja', label: 'Japanese' },
  { code: 'ko', label: 'Korean' },
  { code: 'ar', label: 'Arabic' },
  { code: 'pl', label: 'Polish' },
  { code: 'nl', label: 'Dutch' },
  { code: 'tr', label: 'Turkish' },
  { code: 'uk', label: 'Ukrainian' },
];

interface SortableItemProps {
  id: string;
  onRemove: () => void;
  disabled?: boolean;
}

function SortableItem({ id, onRemove, disabled }: SortableItemProps) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id });
  const style = {
    transform: CSS.Transform.toString(transform),
    transition,
  };
  const label = COMMON_LANGUAGES.find((l) => l.code === id)?.label ?? id;

  return (
    <div
      ref={setNodeRef}
      style={style}
      className={cn(
        'flex items-center gap-1 rounded-md border bg-muted/50 px-2 py-1 text-sm',
        isDragging && 'opacity-50',
      )}
    >
      <button type="button" className="cursor-grab touch-none text-muted-foreground" {...attributes} {...listeners}>
        <GripVertical className="h-3 w-3" />
      </button>
      <span>{label}</span>
      <button
        type="button"
        onClick={onRemove}
        disabled={disabled}
        className="ml-1 text-muted-foreground hover:text-foreground"
      >
        <X className="h-3 w-3" />
      </button>
    </div>
  );
}

interface SortableLanguageListProps {
  languages: string[];
  onChange: (languages: string[]) => void;
  disabled?: boolean;
}

export function SortableLanguageList({ languages, onChange, disabled }: SortableLanguageListProps) {
  const [showDropdown, setShowDropdown] = useState(false);
  const sensors = useSensors(
    useSensor(PointerSensor),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );

  const handleDragEnd = (event: DragEndEvent) => {
    const { active, over } = event;
    if (over && active.id !== over.id) {
      const oldIndex = languages.indexOf(String(active.id));
      const newIndex = languages.indexOf(String(over.id));
      onChange(arrayMove(languages, oldIndex, newIndex));
    }
  };

  const handleRemove = (code: string) => {
    onChange(languages.filter((l) => l !== code));
  };

  const handleAdd = (code: string) => {
    if (!languages.includes(code)) {
      onChange([...languages, code]);
    }
    setShowDropdown(false);
  };

  const available = COMMON_LANGUAGES.filter((l) => !languages.includes(l.code));

  return (
    <div className="space-y-2">
      <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={handleDragEnd}>
        <SortableContext items={languages} strategy={horizontalListSortingStrategy}>
          <div className="flex flex-wrap gap-2">
            {languages.map((lang) => (
              <SortableItem key={lang} id={lang} onRemove={() => handleRemove(lang)} disabled={disabled} />
            ))}
          </div>
        </SortableContext>
      </DndContext>
      <div className="relative">
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => setShowDropdown(!showDropdown)}
          disabled={disabled || available.length === 0}
        >
          <Plus className="mr-1 h-3 w-3" />
          Add language
        </Button>
        {showDropdown && (
          <div className="ts-dropdown-panel absolute left-0 top-full z-50 mt-1 max-h-48 overflow-y-auto">
            {available.map((lang) => (
              <button
                key={lang.code}
                type="button"
                className="ts-dropdown-item w-full text-left"
                onClick={() => handleAdd(lang.code)}
              >
                {lang.label} ({lang.code})
              </button>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
```

### Step 3: Replace language input in SettingsPage

Modify `frontend/src/pages/SettingsPage.tsx`:

Add import at top:
```typescript
import { SortableLanguageList } from '@/components/SortableLanguageList';
```

Replace the languages `<div className="space-y-2">` block (around lines 1379-1397) — the one containing the `<Input>` for comma-separated languages — with:

```tsx
<div className="space-y-2">
  <div className="text-sm font-medium">Languages</div>
  <SortableLanguageList
    languages={subtitleSettings?.languages ?? []}
    onChange={(languages) => void handleUpdateSubtitleSettings({ languages })}
    disabled={subtitleSaving}
  />
  <div className="text-xs text-muted-foreground">
    Drag to reorder priority. First language is preferred.
  </div>
</div>
```

Remove `subtitleLanguagesInput` state variable and `setSubtitleLanguagesInput` calls (lines 152 and 635).

**Verify:** `cd frontend && npx tsc --noEmit`

**Commit:** `git commit -m "feat(subtitles): drag-to-reorder language priority list"`

---

## Task 3: Watch-History — Progress Percentage

**Files:**
- Modify: `services/torrent-engine/internal/domain/watch_history.go`
- Modify: `services/torrent-engine/internal/api/http/handlers_history.go`
- Modify: `frontend/src/types.ts`

### Step 1: Add Progress field to domain model

Modify `services/torrent-engine/internal/domain/watch_history.go`:

```go
type WatchPosition struct {
	TorrentID   TorrentID `json:"torrentId"`
	FileIndex   int       `json:"fileIndex"`
	Position    float64   `json:"position"`
	Duration    float64   `json:"duration"`
	Progress    float64   `json:"progress"`
	TorrentName string    `json:"torrentName"`
	FilePath    string    `json:"filePath"`
	UpdatedAt   time.Time `json:"updatedAt"`
}
```

### Step 2: Compute progress in MongoDB adapter

Modify `services/torrent-engine/internal/services/session/repository/mongo/watch_history.go` — update `watchDocToPosition`:

```go
func watchDocToPosition(doc watchPositionDoc) domain.WatchPosition {
	var progress float64
	if doc.Duration > 0 {
		progress = doc.Position / doc.Duration
		if progress > 1 {
			progress = 1
		}
		if progress < 0 {
			progress = 0
		}
	}
	return domain.WatchPosition{
		TorrentID:   domain.TorrentID(doc.TorrentID),
		FileIndex:   doc.FileIndex,
		Position:    doc.Position,
		Duration:    doc.Duration,
		Progress:    progress,
		TorrentName: doc.TorrentName,
		FilePath:    doc.FilePath,
		UpdatedAt:   time.Unix(doc.UpdatedAt, 0).UTC(),
	}
}
```

### Step 3: Add Progress to frontend type

Modify `frontend/src/types.ts` — add `progress` to `WatchPosition`:

```typescript
export interface WatchPosition {
  torrentId: string;
  fileIndex: number;
  position: number;
  duration: number;
  progress: number;
  torrentName: string;
  filePath: string;
  updatedAt: string;
}
```

**Verify:**
```bash
cd services/torrent-engine && go build ./... && go test ./...
cd frontend && npx tsc --noEmit
```

**Commit:** `git commit -m "feat(watch-history): add computed progress field"`

---

## Task 4: Watch-History — Content Type Auto-Tag

**Files:**
- Modify: `services/torrent-engine/internal/domain/watch_history.go`
- Modify: `services/torrent-engine/internal/services/session/repository/mongo/watch_history.go`
- Modify: `services/torrent-engine/internal/api/http/handlers_history.go`
- Modify: `frontend/src/types.ts`

### Step 1: Add ContentType to domain + MongoDB

Add to `domain/watch_history.go`:

```go
type WatchPosition struct {
	TorrentID   TorrentID `json:"torrentId"`
	FileIndex   int       `json:"fileIndex"`
	Position    float64   `json:"position"`
	Duration    float64   `json:"duration"`
	Progress    float64   `json:"progress"`
	ContentType string    `json:"contentType"`
	TorrentName string    `json:"torrentName"`
	FilePath    string    `json:"filePath"`
	UpdatedAt   time.Time `json:"updatedAt"`
}
```

Add to MongoDB `watchPositionDoc`:

```go
type watchPositionDoc struct {
	ID          string  `bson:"_id"`
	TorrentID   string  `bson:"torrentId"`
	FileIndex   int     `bson:"fileIndex"`
	Position    float64 `bson:"position"`
	Duration    float64 `bson:"duration"`
	ContentType string  `bson:"contentType,omitempty"`
	TorrentName string  `bson:"torrentName"`
	FilePath    string  `bson:"filePath"`
	UpdatedAt   int64   `bson:"updatedAt"`
}
```

### Step 2: Add content type detection

Add to `handlers_history.go`:

```go
import "regexp"

var episodePattern = regexp.MustCompile(`(?i)(S\d{1,3}E\d{1,3}|season\s*\d|episode\s*\d|\b\d{1,2}x\d{2}\b)`)

func detectContentType(filePath string) string {
	if episodePattern.MatchString(filePath) {
		return "series"
	}
	return "movie"
}
```

In `handleWatchHistoryByID` PUT handler, before `Upsert`, detect content type:

```go
wp := domain.WatchPosition{
	TorrentID:   torrentID,
	FileIndex:   fileIndex,
	Position:    body.Position,
	Duration:    body.Duration,
	TorrentName: body.TorrentName,
	FilePath:    body.FilePath,
	ContentType: detectContentType(body.FilePath),
}
```

### Step 3: Persist and read contentType in MongoDB

Update `Upsert` in `watch_history.go` mongo repo — add `"contentType"` to the `$set` block:

```go
"contentType": wp.ContentType,
```

Update `watchDocToPosition` — add `ContentType`:

```go
ContentType: doc.ContentType,
```

### Step 4: Add to frontend type

Update `WatchPosition` in `frontend/src/types.ts`:

```typescript
export interface WatchPosition {
  torrentId: string;
  fileIndex: number;
  position: number;
  duration: number;
  progress: number;
  contentType: string;
  torrentName: string;
  filePath: string;
  updatedAt: string;
}
```

**Verify:**
```bash
cd services/torrent-engine && go build ./... && go test ./...
cd frontend && npx tsc --noEmit
```

**Commit:** `git commit -m "feat(watch-history): auto-detect content type from filename"`

---

## Task 5: Watch-History — Continue Watching Backend

**Files:**
- Modify: `services/torrent-engine/internal/services/session/repository/mongo/watch_history.go`
- Modify: `services/torrent-engine/internal/api/http/handlers_history.go`
- Modify: `services/torrent-engine/internal/api/http/server.go` (WatchHistoryStore interface)

### Step 1: Add ListIncomplete to WatchHistoryStore interface

Find the `WatchHistoryStore` interface in `server.go` and add:

```go
ListIncomplete(ctx context.Context, limit int) ([]domain.WatchPosition, error)
```

### Step 2: Implement ListIncomplete in MongoDB repo

Add to `watch_history.go` (mongo repo):

```go
func (r *WatchHistoryRepository) ListIncomplete(ctx context.Context, limit int) ([]domain.WatchPosition, error) {
	if limit <= 0 {
		limit = 10
	}

	filter := bson.M{
		"position": bson.M{"$gte": 10},
		"duration": bson.M{"$gt": 0},
		"$expr": bson.M{
			"$lt": bson.A{"$position", bson.M{"$subtract": bson.A{"$duration", 15}}},
		},
	}

	opts := options.Find().
		SetSort(bson.D{{Key: "updatedAt", Value: -1}}).
		SetLimit(int64(limit))

	cursor, err := r.collection.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var docs []watchPositionDoc
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, err
	}

	positions := make([]domain.WatchPosition, 0, len(docs))
	for _, doc := range docs {
		positions = append(positions, watchDocToPosition(doc))
	}
	return positions, nil
}
```

### Step 3: Add status filter to handleWatchHistory

Modify `handleWatchHistory` in `handlers_history.go`:

```go
func (s *Server) handleWatchHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.watchHistory == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "watch history not configured")
		return
	}

	limit, err := parsePositiveInt(r.URL.Query().Get("limit"), true)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid limit")
		return
	}
	if limit <= 0 {
		limit = 20
	}

	status := strings.TrimSpace(r.URL.Query().Get("status"))

	var positions []domain.WatchPosition
	if status == "incomplete" {
		positions, err = s.watchHistory.ListIncomplete(r.Context(), limit)
	} else {
		positions, err = s.watchHistory.ListRecent(r.Context(), limit)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list watch history")
		return
	}

	writeJSON(w, http.StatusOK, positions)
}
```

### Step 4: Add frontend API function

Add to `frontend/src/api.ts`:

```typescript
export const getIncompleteWatchHistory = async (limit = 10): Promise<WatchPosition[]> => {
  const response = await deduplicatedFetch(
    buildUrl(`/watch-history?status=incomplete&limit=${limit}`),
    undefined,
    POLL_REQUEST_TIMEOUT_MS,
  );
  return handleResponse(response);
};
```

**Verify:**
```bash
cd services/torrent-engine && go build ./... && go test ./...
cd frontend && npx tsc --noEmit
```

**Commit:** `git commit -m "feat(watch-history): add incomplete filter for continue watching"`

---

## Task 6: Continue Watching — Frontend Carousel

**Files:**
- Create: `frontend/src/components/ContinueWatching.tsx`
- Modify: `frontend/src/pages/CatalogPage.tsx`

### Step 1: Create ContinueWatching component

Create `frontend/src/components/ContinueWatching.tsx`:

```tsx
import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Play, Film, Tv } from 'lucide-react';
import { getIncompleteWatchHistory } from '@/api';
import type { WatchPosition } from '@/types';
import { cn } from '@/lib/cn';

function formatTime(seconds: number): string {
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

function ContentIcon({ type }: { type: string }) {
  if (type === 'series') return <Tv className="h-3 w-3" />;
  return <Film className="h-3 w-3" />;
}

export function ContinueWatching() {
  const [items, setItems] = useState<WatchPosition[]>([]);
  const navigate = useNavigate();

  useEffect(() => {
    let cancelled = false;
    getIncompleteWatchHistory(10)
      .then((data) => {
        if (!cancelled) setItems(data);
      })
      .catch(() => {});
    return () => { cancelled = true; };
  }, []);

  if (items.length === 0) return null;

  return (
    <div className="space-y-3">
      <h2 className="text-sm font-medium text-muted-foreground">Continue Watching</h2>
      <div className="flex gap-3 overflow-x-auto pb-2">
        {items.map((item) => {
          const displayName = item.filePath
            ? item.filePath.split('/').pop()?.replace(/\.[^.]+$/, '') ?? item.torrentName
            : item.torrentName;

          return (
            <button
              key={`${item.torrentId}:${item.fileIndex}`}
              type="button"
              className={cn(
                'group flex w-56 flex-none flex-col gap-2 rounded-lg border bg-card p-3',
                'transition-colors hover:border-primary/50 hover:bg-accent/50',
              )}
              onClick={() =>
                navigate(`/player/${item.torrentId}?file=${item.fileIndex}&resume=true`)
              }
            >
              <div className="flex items-start justify-between gap-2">
                <div className="min-w-0 flex-1 text-left">
                  <div className="truncate text-sm font-medium">{displayName}</div>
                  <div className="flex items-center gap-1 text-xs text-muted-foreground">
                    <ContentIcon type={item.contentType} />
                    <span>{formatTime(item.position)} / {formatTime(item.duration)}</span>
                  </div>
                </div>
                <Play className="h-4 w-4 flex-none text-muted-foreground group-hover:text-primary" />
              </div>
              <div className="h-1 w-full overflow-hidden rounded-full bg-muted">
                <div
                  className="h-full rounded-full bg-primary transition-all"
                  style={{ width: `${Math.round(item.progress * 100)}%` }}
                />
              </div>
            </button>
          );
        })}
      </div>
    </div>
  );
}
```

### Step 2: Add ContinueWatching to CatalogPage

Modify `frontend/src/pages/CatalogPage.tsx`:

Add import:
```typescript
import { ContinueWatching } from '@/components/ContinueWatching';
```

Insert `<ContinueWatching />` inside the main `<div className="flex flex-col gap-4">`, after the error Alert block and before the `{showDetails && selectedTorrent ? ...}` conditional (around line 154):

```tsx
{/* Continue Watching carousel */}
{!showDetails && <ContinueWatching />}
```

**Verify:** `cd frontend && npx tsc --noEmit`

**Commit:** `git commit -m "feat(watch-history): add Continue Watching carousel to CatalogPage"`

---

## Verification

```bash
# Go tests
cd services/torrent-engine && go test ./...

# Frontend type-check
cd frontend && npx tsc --noEmit

# Build check
cd services/torrent-engine && go build ./...
```
