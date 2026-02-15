# T◎RRX

[![CI](https://github.com/starsinc1708/TorrX/actions/workflows/ci.yml/badge.svg)](https://github.com/starsinc1708/TorrX/actions/workflows/ci.yml)
[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0)
[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![React](https://img.shields.io/badge/React-18-61DAFB?logo=react&logoColor=white)](https://react.dev)
[![Docker](https://img.shields.io/badge/Docker-Compose-2496ED?logo=docker&logoColor=white)](https://docs.docker.com/compose/)

Self-hosted platform for torrent search, download management, and in-browser video playback.

Three services behind Traefik: a **Go torrent engine**, a **Go search aggregator**, and a **React frontend**.

## Features

- **Multi-provider torrent search** — Pirate Bay, 1337x, RuTracker, Torznab (Jackett/Prowlarr), DHT
- **SSE streaming results** — results appear as providers respond, with real-time ranking
- **In-browser video playback** — HLS.js player with server-side transcoding via FFmpeg
- **Track selection** — audio, subtitle, and video track switching
- **Resume playback** — watch position saved and restored across sessions
- **Torrent management** — add, start, stop, delete torrents with priority control
- **TMDB enrichment** — automatic metadata, posters, and descriptions
- **Flexible storage** — disk, memory, or hybrid storage modes
- **Observability** — Prometheus metrics, Grafana dashboards, Jaeger distributed tracing
- **Theming** — light/dark mode with 7 accent color presets

## Architecture

```
                        ┌─────────────────────────────┐
                        │       Traefik (:80)          │
                        │       reverse proxy          │
                        └──────┬──────┬──────┬─────────┘
                               │      │      │
              ┌────────────────┘      │      └────────────────┐
              ▼                       ▼                       ▼
   ┌──────────────────┐   ┌──────────────────┐   ┌──────────────────┐
   │  torrent-engine   │   │  torrent-search  │   │    frontend      │
   │  Go — :8080       │   │  Go — :8090      │   │  React — :5173   │
   │                   │   │                  │   │                  │
   │  /torrents        │   │  /search         │   │  /* (SPA)        │
   │  /settings/*      │   │  /search/provid. │   │                  │
   │  /watch-history   │   │  /image          │   │                  │
   │  /swagger         │   │  /health         │   │                  │
   └────────┬──────────┘   └────────┬─────────┘   └──────────────────┘
            │                       │
     ┌──────┴──────┐         ┌──────┴──────┐
     │   MongoDB   │         │    Redis    │
     │  persistence│         │   cache     │
     └─────────────┘         └─────────────┘
```

## Tech Stack

| Layer | Technology |
|-------|-----------|
| Torrent engine | Go, [anacrolix/torrent](https://github.com/anacrolix/torrent), FFmpeg, FFprobe |
| Search aggregator | Go, Redis, FlareSolverr |
| Frontend | React 18, TypeScript, Vite, Tailwind CSS, Radix UI, HLS.js |
| Database | MongoDB 7 |
| Cache | Redis 7 |
| Reverse proxy | Traefik v3 |
| Observability | Prometheus, Grafana, Jaeger, OpenTelemetry |
| Containerization | Docker, Docker Compose |

## Quick Start

### Prerequisites

- [Docker](https://docs.docker.com/get-docker/) and [Docker Compose](https://docs.docker.com/compose/install/)

### Run

```bash
# Clone the repository
git clone https://github.com/starsinc1708/TorrX.git
cd TorrX

# Copy and configure environment
cp deploy/.env.example deploy/.env
# Edit deploy/.env with your settings (RuTracker cookies, etc.)

# Start all services
docker compose -f deploy/docker-compose.yml up --build -d
```

Open [http://localhost](http://localhost) in your browser.

### Services & URLs

| Service | URL |
|---------|-----|
| Web UI | [http://localhost](http://localhost) |
| Search page | [http://localhost/discover](http://localhost/discover) |
| Torrent API | [http://localhost/torrents](http://localhost/torrents) |
| Search API | [http://localhost/search](http://localhost/search) |
| Swagger UI | [http://localhost/swagger](http://localhost/swagger) |
| Traefik dashboard | [http://localhost:8081](http://localhost:8081) |
| Prometheus | [http://localhost:9090](http://localhost:9090) |
| Grafana | [http://localhost:3000](http://localhost:3000) (admin/admin) |
| Jaeger | [http://localhost:16686](http://localhost:16686) |
| Jackett | [http://jackett.localhost](http://jackett.localhost) |
| Prowlarr | [http://prowlarr.localhost](http://prowlarr.localhost) |

### Torznab Integration

1. Configure indexers in Jackett or Prowlarr UI.
2. Open **Settings** in the web app and add the Jackett/Prowlarr endpoint and API key.
3. Use **Auto-detect** in Settings to fetch API keys automatically.
4. Optionally link FlareSolverr from Settings for Cloudflare-protected sites.

## Development

### Frontend

```bash
cd frontend
npm install
npm run dev          # Dev server on :5173, proxies API to backend
npx tsc --noEmit     # Type-check
npm run build        # Production build
```

### Backend

```bash
# Torrent engine
cd services/torrent-engine
go test ./...

# Search service
cd services/torrent-search
go test ./...
```

### Full Stack (Makefile)

```bash
make dev             # Frontend dev server
make test            # Run all tests (Go + frontend type-check)
make lint            # Run linters
make docker-up       # Start Docker Compose stack
make docker-down     # Stop Docker Compose stack
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for detailed development guidelines.

## Project Structure

```
TorrX/
├── services/
│   ├── torrent-engine/      # Go torrent engine (hexagonal architecture)
│   │   ├── cmd/server/      # Entry point
│   │   ├── internal/
│   │   │   ├── domain/      # Domain models and port interfaces
│   │   │   ├── usecase/     # Business logic orchestration
│   │   │   ├── api/http/    # REST API handlers, HLS streaming
│   │   │   ├── repository/  # MongoDB persistence
│   │   │   └── services/    # Adapters (anacrolix, ffprobe, session)
│   │   └── docs/            # Architecture docs, OpenAPI spec
│   └── torrent-search/      # Go search aggregator
│       ├── cmd/server/      # Entry point
│       └── internal/
│           ├── providers/   # Search providers (PirateBay, 1337x, RuTracker, Torznab, DHT, TMDB)
│           ├── search/      # Aggregator, normalization, caching, health
│           └── api/http/    # REST API handlers, SSE streaming
├── frontend/                # React SPA
│   └── src/
│       ├── pages/           # CatalogPage, SearchPage, PlayerPage, SettingsPage
│       ├── components/      # UI kit (Radix-based) + VideoPlayer
│       ├── hooks/           # useTorrents, useVideoPlayer, useSessionState
│       └── app/             # Context providers (theme, catalog, toast)
├── build/                   # Dockerfiles
├── deploy/                  # Docker Compose, Traefik, Prometheus, Grafana configs
└── docs/                    # Roadmap and implementation plans
```

## Observability

Pre-configured dashboards are auto-provisioned in Grafana:

- **SLO Overview** — service level objectives and error budgets
- **Torrent Engine** — download speeds, active sessions, HLS metrics
- **Traefik RED** — request rate, error rate, duration

Prometheus alerting rules are defined in `deploy/prometheus/rules/slo.yml`.

## Roadmap

See [docs/roadmap/Prioritized_Improvements.md](docs/roadmap/Prioritized_Improvements.md) for the prioritized improvement list and [docs/roadmap/implementation-plan.md](docs/roadmap/implementation-plan.md) for the phased implementation plan.

Key upcoming features:
- Security profiles (LAN / hardened mode)
- Contract-first API with versioning (`/api/v1/`)
- Torrent catalog with ingestion pipeline
- SSRF protection for outbound requests
- Optional authentication (JWT / API key)

## License

This project is licensed under the GNU General Public License v3.0 — see the [LICENSE](LICENSE) file for details.
