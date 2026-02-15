# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What is T◎RRX

Self-hosted platform for torrent search, download management, and in-browser video playback. Three services behind Traefik: a Go torrent engine, a Go search aggregator, and a React frontend.

## Build & Run

```bash
# Full stack (Docker)
docker compose -f deploy/docker-compose.yml up --build -d

# Frontend only (dev server on :5173, proxies API to :8080/:8090)
cd frontend && npm install && npm run dev

# Frontend type-check
cd frontend && npx tsc --noEmit

# Frontend production build
cd frontend && npm run build

# Go backend tests
cd services/torrent-engine && go test ./...
cd services/torrent-search && go test ./...

# Run single Go test
cd services/torrent-engine && go test ./internal/usecase/ -run TestCreateTorrent
cd services/torrent-search && go test ./internal/search/ -run TestAggregator
```

## Architecture

```
Traefik (:80, :8081)
├── /torrents, /settings/*, /watch-history, /swagger → torrent-engine (:8080)
├── /search                                          → torrent-search (:8090)
└── /* (default)                                     → frontend (:5173)
```

Routing rules: `deploy/traefik/dynamic.yml`. Observability: Prometheus (:9090), Grafana (:3000, admin/admin), Jaeger (:16686).

### torrent-engine (Go, `services/torrent-engine/`)

Hexagonal architecture. Module name: `torrentstream`.

- **Domain** (`internal/domain/`) — value objects (`TorrentRecord`, `Source`, `Priority`, `SessionMode`) and port interfaces (`Engine`, `Repository`, `Session`, `Storage`, `Stream`). No external imports.
- **Use cases** (`internal/usecase/`) — orchestrate domain ports: `CreateTorrent`, `StartTorrent`, `StopTorrent`, `DeleteTorrent`, `StreamTorrent`, `GetTorrentState`, `SyncState`, `SessionRestore`, `SlidingPriorityReader`.
- **Adapters**:
  - `internal/repository/mongo/` — MongoDB persistence (torrents, encoding settings, storage settings)
  - `internal/services/torrent/engine/anacrolix/` — wraps `anacrolix/torrent` library for DHT/downloading
  - `internal/services/torrent/engine/ffprobe/` — media file analysis
  - `internal/services/session/player/` — current player state
  - `internal/services/session/repository/mongo/` — watch history, player settings
- **HTTP API** (`internal/api/http/`) — REST handlers, HLS streaming/caching, middleware, Swagger
- **Config** (`internal/app/`) — env-based config for MongoDB, HLS, FFmpeg, memory limits

### torrent-search (Go, `services/torrent-search/`)

Module name: `torrentstream/searchservice`. Lightweight aggregator with Redis caching.

- **Providers** (`internal/providers/`) — each in own package: `bittorrentindex` (Pirate Bay), `x1337`, `rutracker`, `dht`, `torznab` (Jackett/Prowlarr), `tmdb` (metadata enrichment). Common utilities in `common/`.
- **Search core** (`internal/search/`) — `aggregator.go` (multi-provider fan-out), `normalize.go` (dedup + ranking), `cache.go`/`cache_redis.go`, `health.go` (provider circuit breaker), `query_expand.go`, `dubbing.go`.
- **HTTP API** (`internal/api/http/`) — `/search` (SSE streaming + regular), `/search/providers`, `/image` proxy.

### frontend (React + TypeScript + Vite, `frontend/`)

SPA with React Router. Tailwind CSS + Radix UI primitives (shadcn-style component kit).

- **Pages**: `CatalogPage` (torrent list + details), `SearchPage` (multi-provider search with SSE streaming, filters, ranking profile), `PlayerPage` (HLS.js video player with track selection, resume, keyboard shortcuts), `SettingsPage` (providers, encoding, storage, theme), `ProviderDiagnosticsPage`.
- **State**: React Context providers (`ThemeAccentProvider`, `CatalogMetaProvider`, `ToastProvider`) + custom hooks (`useTorrents`, `useSessionState`, `useVideoPlayer`). localStorage for persistence.
- **API client** (`src/api.ts`) — fetch-based with timeout handling, covers all torrent-engine and torrent-search endpoints.
- **Design system** (`src/lib/design-system.ts`, `src/styles/globals.css`) — CSS variables for light/dark themes, 7 accent color presets, runtime accent override. Shared CSS classes: `ts-dropdown-trigger`, `ts-dropdown-panel`, `ts-dropdown-item`, `app-backdrop`.
- **UI kit** (`src/components/ui/`) — button, card, input, select, multi-select, dropdown-menu, dialog, switch, tabs, badge, alert, textarea. All use `cn()` from `src/lib/cn.ts` (clsx + tailwind-merge).
- **Video player** (`src/components/VideoPlayer.tsx`, ~2000 lines) — HLS.js with server-side seek fallback, timeline preview canvas, track selection, screenshot, keyboard shortcuts, auto-hide controls, watch position saving.

## Key Conventions

- **Go**: Hexagonal architecture with ports in `domain/ports/`. Use cases accept port interfaces, not concrete types. Tests are co-located (`*_test.go`), table-driven.
- **Frontend**: Tailwind-first styling. UI components in `src/components/ui/` extend Radix primitives. Pages in `src/pages/`. Use `cn()` for conditional classes. `Select` component wraps native `<select>` with `ts-select ts-dropdown-trigger` classes. Dropdown panels use `ts-dropdown-panel` / `ts-dropdown-item` CSS classes.
- **API proxy**: In dev mode, Vite proxies `/torrents`, `/settings/*`, `/watch-history` to `:8080` and `/search` to `:8090`. Configurable via `VITE_API_PROXY_TARGET` / `VITE_SEARCH_PROXY_TARGET`.
- **Docker**: Multi-stage Go builds in `build/`. Compose config in `deploy/docker-compose.yml`. Networks: `edge` (Traefik-facing) and `core` (internal).
