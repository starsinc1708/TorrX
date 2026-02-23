# Integrations Design: Automation + Subtitles

**Date:** 2026-02-23
**Status:** Approved

## Goals

1. **Automation** — Sonarr + Radarr auto-downloading via existing qBittorrent-compatible API
2. **Subtitles** — Bazarr for completed torrents + OpenSubtitles API fallback in player

## Phase 1: Docker Infrastructure (No Code)

Add Sonarr, Radarr, Bazarr to `deploy/docker-compose.yml`.

| Service | Image | Port | Purpose |
|---------|-------|------|---------|
| Sonarr | `linuxserver/sonarr` | 8989 | TV series automation |
| Radarr | `linuxserver/radarr` | 7878 | Movie automation |
| Bazarr | `linuxserver/bazarr` | 6767 | Subtitle management |

### Volumes

- Sonarr/Radarr: `torrent_data:/media` (rw — need rename/move)
- Bazarr: `torrent_data:/media:ro` (read-only)
- Config: `./data/{service}/:/config` each

### Network

All in `edge` network — need to reach each other and notifier qBT API (`:8070`).

### Integration Flow

```
Prowlarr ──→ pushes indexers to ──→ Sonarr + Radarr
Sonarr/Radarr ──→ send torrents via ──→ qBT API (notifier :8070)
Bazarr ──→ reads media list from ──→ Sonarr + Radarr
Bazarr ──→ searches subtitles on ──→ OpenSubtitles
```

### Resources

512 MB / 0.5 CPU per service (matches Prowlarr).

## Phase 2: OpenSubtitles Backend (torrent-engine)

### New Endpoints

```
GET  /torrents/{id}/subtitles/{fileIndex}/search?lang=ru,en
GET  /torrents/{id}/subtitles/{fileIndex}/download?subtitle_id=XXX
GET  /settings/subtitles
PATCH /settings/subtitles
```

### Search Logic

1. Compute OpenSubtitles file hash (first + last 64KB)
2. Query OpenSubtitles REST API: `GET /subtitles?moviehash=...&languages=ru,en`
3. Fallback: search by filename if hash returns no results
4. Return list: `[{ id, language, rating, format, release }]`

### Download Logic

1. Fetch `.srt` from OpenSubtitles by subtitle ID
2. Convert to WebVTT
3. Return as `text/vtt`

### Domain Types

```go
// internal/domain/
type SubtitleResult struct {
    ID       string
    Language string
    Rating   float64
    Format   string
    Release  string
}

type SubtitleSettings struct {
    Enabled    bool     `json:"enabled" bson:"enabled"`
    APIKey     string   `json:"apiKey" bson:"apiKey"`
    Languages  []string `json:"languages" bson:"languages"`
    AutoSearch bool     `json:"autoSearch" bson:"autoSearch"`
}
```

### Adapter

`internal/services/subtitles/opensubtitles/` — HTTP client for OpenSubtitles REST API v1.

### Use Cases

- `SearchSubtitles(torrentID, fileIndex, languages) → []SubtitleResult`
- `DownloadSubtitle(subtitleID) → io.Reader (WebVTT)`

### Settings Storage

MongoDB collection `subtitle_settings` (same pattern as encoding/storage settings).

### Environment

- `OPENSUBTITLES_API_KEY` — env var for API key (overridable via settings UI)

## Phase 3: Frontend Subtitles

### API Client (`api.ts`)

```ts
searchSubtitles(torrentId: string, fileIndex: number, lang: string[]): Promise<SubtitleResult[]>
downloadSubtitleUrl(torrentId: string, fileIndex: number, subtitleId: string): string
getSubtitleSettings(): Promise<SubtitleSettings>
updateSubtitleSettings(s: Partial<SubtitleSettings>): Promise<SubtitleSettings>
```

### Player Integration (`VideoPlayer.tsx`)

- On media load: if no embedded subtitles for preferred languages → auto-call `searchSubtitles`
- New button in controls: "Find Subtitles" → opens panel with results list
- Selected subtitle → add as external VTT track via HLS.js `addCueTrack` or `<track>` element

### Settings Page

New "Subtitles" section in SettingsPage:
- Language list with drag-to-reorder priority
- Auto-search toggle
- API key input field

## Implementation Order

1. Phase 1: Docker config (Sonarr + Radarr + Bazarr) — immediate
2. Phase 2: OpenSubtitles backend — plan + implement
3. Phase 3: Frontend subtitles — plan + implement
