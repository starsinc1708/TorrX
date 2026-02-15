# Frontend Refactoring Guide

This document outlines refactoring opportunities for large React components in the TorrX frontend.

**Last updated:** 2026-02-15

---

## VideoPlayer Component (~1941 lines)

**File:** `frontend/src/components/VideoPlayer.tsx`

**Current State:**
- 29 `useState` hooks
- 33 `useRef` hooks
- Mixes multiple concerns: HLS management, keyboard shortcuts, timeline preview, screenshot capture, watch position saving

**Memory Leak Audit Result:** ✅ **PASSED**
- All event listeners have matching cleanup (`removeEventListener`)
- HLS instances (main + preview) properly destroyed via `hlsRef.current.destroy()`
- All timers cleared in useEffect cleanup functions
- No memory leaks detected

**Refactoring Opportunities:**

### 1. Extract `useHlsPlayer` Hook

**Purpose:** Manage HLS.js instance lifecycle and error recovery

**State to extract:**
- `hlsRef`, `hlsError`, `hlsRecoveryCountRef`, `hlsRetryTimerRef`
- `runtimeStatus`, `runtimeStatusText`

**Functions to extract:**
- HLS initialization logic (lines 547-935)
- HLS error handling and recovery
- `clearRuntimeStatus()`, `setRuntimeStatus()`

**Benefits:**
- Isolates complex HLS.js integration
- Easier to test HLS error recovery independently
- Reduces VideoPlayer size by ~400 lines

### 2. Extract `useKeyboardShortcuts` Hook ✅ **CREATED**

**File:** `frontend/src/hooks/useKeyboardShortcuts.ts`

**Purpose:** Handle keyboard shortcuts for player controls

**Usage:**
```typescript
useKeyboardShortcuts({
  onPlayPause: togglePlay,
  onSeekBackward: () => skip(-10),
  onSeekForward: () => skip(10),
  onToggleMute: toggleMute,
  onToggleFullscreen: toggleFullscreen,
  onTakeScreenshot: takeScreenshot,
});
```

**Status:** Hook created but not yet integrated into VideoPlayer

### 3. Extract `useTimelinePreview` Hook

**Purpose:** Manage timeline hover preview with HLS and canvas rendering

**State to extract:**
- `timelinePreview`, `previewVideoRef`, `previewHlsRef`, `previewCanvasRef`
- `previewSeekTimerRef`, `previewPendingTimeRef`, `previewRequestTokenRef`
- `previewReadyRef`, `previewLastTimeRef`

**Functions to extract:**
- `capturePreviewFrame()` (lines 312-415)
- `schedulePreviewFrameCapture()` (lines 417-431)
- `updateTimelinePreview()` (lines 433-447)
- `disposeTimelinePreviewSource()` (lines 449-469)
- Preview HLS setup (lines 975-1028)

**Benefits:**
- Isolates complex preview rendering logic
- Reduces VideoPlayer by ~600 lines
- Makes preview logic reusable

### 4. Extract `useWatchPositionSave` Hook ✅ **CREATED**

**File:** `frontend/src/hooks/useWatchPositionSave.ts`

**Purpose:** Autosave watch position every 5 seconds while playing

**Usage:**
```typescript
const { savePosition } = useWatchPositionSave(
  () => videoRef.current?.currentTime ?? 0,
  {
    torrentId,
    selectedFile,
    enabled: playing,
    saveIntervalMs: 5000,
  },
);
```

**Status:** Hook created but not yet integrated into VideoPlayer

### 5. Extract `useVideoState` Hook

**Purpose:** Consolidate video element state management

**State to extract:**
- `playing`, `currentTime`, `duration`, `volume`, `muted`
- `bufferedTimelineRanges`, `seeking`, `playbackRate`

**Functions to extract:**
- Video event handlers (lines 1210-1224)
- `tryPlay()`, `resolveMediaDuration()`
- `syncBufferedTimelineRanges()`

**Benefits:**
- Groups related video state
- Reduces main component complexity

---

## SearchPage Component (~1568 lines)

**File:** `frontend/src/pages/SearchPage.tsx`

**Current State:**
- 25 `useState` hooks
- Inline ranking profile logic
- Inline filter presets logic
- SSE streaming implementation
- Results rendering mixed with page logic

**Refactoring Opportunities:**

### 1. Extract `useSearchRankingProfile` Hook

**Purpose:** Manage search ranking profile with localStorage persistence

**State to extract:**
- `profile` state
- `loadStoredProfile()` logic (lines 164-179)

**Interface:**
```typescript
interface UseSearchRankingProfileReturn {
  profile: SearchRankingProfile;
  updateProfile: (updates: Partial<SearchRankingProfile>) => void;
  resetProfile: () => void;
}
```

**Benefits:**
- Reusable profile management
- Cleaner localStorage abstraction
- ~50 lines extracted

### 2. Extract `useSearchFilterPresets` Hook

**Purpose:** Manage saved filter presets with CRUD operations

**State to extract:**
- `savedFilterPresets`, `selectedFilterPresetId`
- `loadSavedFilterPresets()`, `saveFilterPresets()` (lines 140-162)

**Interface:**
```typescript
interface UseSearchFilterPresetsReturn {
  presets: SavedFilterPreset[];
  selectedPresetId: string;
  loadPreset: (id: string) => ResultFilters | null;
  savePreset: (name: string, filters: ResultFilters) => void;
  updatePreset: (id: string, updates: Partial<SavedFilterPreset>) => void;
  deletePreset: (id: string) => void;
}
```

**Benefits:**
- Encapsulates preset logic
- Easier to test CRUD operations
- ~100 lines extracted

### 3. Extract `useSearchStream` Hook

**Purpose:** Handle SSE streaming search with phase updates

**State to extract:**
- `streamActive`, `phaseMessage`
- SSE connection management
- Phase-based result aggregation

**Functions to extract:**
- `performStreamSearch()` (SSE logic around lines 600-700)
- Phase label mapping
- Error handling for stream interruptions

**Interface:**
```typescript
interface UseSearchStreamReturn {
  isStreaming: boolean;
  phaseMessage: string;
  performStreamSearch: (params: SearchParams) => Promise<void>;
  cancelStream: () => void;
}
```

**Benefits:**
- Isolates complex SSE logic
- Makes streaming reusable
- ~200 lines extracted

### 4. Create `SearchResults` Subcomponent

**Purpose:** Extract results grid and detail view into separate component

**Props:**
```typescript
interface SearchResultsProps {
  items: SearchResultItem[];
  isLoading: boolean;
  totalItems: number;
  elapsedMs: number;
  sortBy: SearchSortBy;
  sortOrder: SearchSortOrder;
  providerStatus: SearchProviderStatus[];
  filters: ResultFilters;
  addState: Record<string, 'adding' | 'added' | 'error'>;
  onAdd: (item: SearchResultItem) => void;
  onOpenDetail: (item: SearchResultItem) => void;
  onLoadMore?: () => void;
}
```

**Benefits:**
- Separates presentation from business logic
- ~400 lines extracted from SearchPage
- Easier to optimize rendering

### 5. Create `SearchFiltersPanel` Subcomponent

**Purpose:** Extract filter UI into separate component

**Props:**
```typescript
interface SearchFiltersPanelProps {
  filters: ResultFilters;
  providers: SearchProviderInfo[];
  presets: SavedFilterPreset[];
  selectedPresetId: string;
  onFiltersChange: (filters: ResultFilters) => void;
  onPresetSelect: (id: string) => void;
  onPresetSave: (name: string) => void;
  onPresetDelete: (id: string) => void;
}
```

**Benefits:**
- Reduces SearchPage by ~300 lines
- Makes filter panel reusable
- Cleaner component hierarchy

---

## Implementation Strategy

**For VideoPlayer:**
1. Start with `useWatchPositionSave` (already created, just integrate)
2. Then `useKeyboardShortcuts` (already created, just integrate)
3. Then `useTimelinePreview` (most complex)
4. Finally `useHlsPlayer` and `useVideoState`

**For SearchPage:**
1. Start with `useSearchRankingProfile` (simplest)
2. Then `useSearchFilterPresets`
3. Create `SearchResults` subcomponent
4. Create `SearchFiltersPanel` subcomponent
5. Finally `useSearchStream` (most complex)

**Testing Strategy:**
- Ensure no visual regressions
- Test all keyboard shortcuts still work
- Verify HLS playback and error recovery
- Test search streaming and filtering
- Check localStorage persistence

**Estimated Impact:**
- VideoPlayer: 1941 lines → ~800 lines (58% reduction)
- SearchPage: 1568 lines → ~600 lines (62% reduction)

**Risk Level:** Medium-High
- Large surface area for regressions
- Complex state dependencies
- Recommend incremental refactoring with thorough testing

---

## Completed Work (2026-02-15)

✅ **Created Hooks:**
- `useWatchPositionSave` - Autosave watch position logic
- `useKeyboardShortcuts` - Keyboard shortcut handling

✅ **Memory Leak Audit:**
- VideoPlayer: All cleanup verified, no leaks found
- Event listeners properly removed
- HLS instances properly destroyed
- Timers properly cleared

**Next Steps:**
1. Integrate created hooks into VideoPlayer
2. Test integration thoroughly
3. Continue with remaining hook extractions
4. Create subcomponents for SearchPage
