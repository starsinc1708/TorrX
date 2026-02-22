# Integrations — Design Document

**Date:** 2026-02-22
**Approach:** New microservice `torrent-notifier` — isolated, self-contained.
**Target environment:** LAN / home server.
**Goals:** Jellyfin + Emby library notify on download complete · Sonarr/Radarr via qBittorrent API · Homepage/Homarr dashboard widget.

---

## Scope

Three integration features in one new service. No changes to `torrent-engine` or `torrent-search`.

| Feature | What it does |
|---------|-------------|
| **Jellyfin / Emby notify** | POST `/Library/Refresh` when a torrent reaches `completed` status |
| **qBittorrent API v2** | Compatibility layer so Sonarr/Radarr can add/list/delete torrents |
| **Widget endpoint** | `GET /widget` returns JSON with torrent stats for Homepage/Homarr |

---

## Architecture

### New service: `torrent-notifier`

```
services/torrent-notifier/
├── cmd/server/main.go
└── internal/
    ├── app/
    │   └── config.go          — env-based config (same pattern as torrent-engine)
    ├── domain/
    │   └── settings.go        — IntegrationSettings value object
    ├── repository/
    │   └── mongo/
    │       └── settings.go    — MongoDB upsert/get for "integrations" document
    ├── watcher/
    │   └── watcher.go         — MongoDB change stream, fires on status→completed
    ├── notifier/
    │   └── notifier.go        — HTTP POST to Jellyfin/Emby /Library/Refresh
    ├── qbt/
    │   └── handler.go         — qBittorrent API v2 compatibility routes
    └── api/http/
        ├── server.go          — HTTP server, route registration
        ├── handlers_settings.go  — GET/PATCH /settings/integrations
        └── handlers_widget.go    — GET /widget, GET /health
```

### Networks

```
torrent-notifier
├── core: MongoDB (mongodb://mongo:27017), torrentstream:8080
└── edge: Traefik
```

Port: `127.0.0.1:8070:8070` (localhost only, same policy as other internal services).

---

## Feature 1: Jellyfin + Emby Notify

### How completion is detected

`watcher.go` opens a MongoDB change stream on the `torrents` collection:

```go
pipeline := mongo.Pipeline{
    bson.D{{"$match", bson.D{
        {"operationType", "update"},
        {"updateDescription.updatedFields.status", "completed"},
    }}},
}
```

On each event: reads `fullDocument.name`, fires `notifier.Notify(ctx, torrentName)`.

### Notify call

Both Jellyfin and Emby share the same API (Emby forked from Jellyfin's predecessor):

```
POST {baseURL}/Library/Refresh
Headers:
  X-Emby-Token: {apiKey}
  Content-Type: application/json
```

No body needed — this triggers a full library rescan.

### Config model

```go
type MediaServerConfig struct {
    Enabled bool   `bson:"enabled" json:"enabled"`
    URL     string `bson:"url"     json:"url"`     // e.g. "http://jellyfin:8096"
    APIKey  string `bson:"apiKey"  json:"apiKey"`
}

type IntegrationSettings struct {
    Jellyfin MediaServerConfig `bson:"jellyfin" json:"jellyfin"`
    Emby     MediaServerConfig `bson:"emby"     json:"emby"`
    QBT      struct {
        Enabled bool `bson:"enabled" json:"enabled"`
    } `bson:"qbt" json:"qbt"`
    UpdatedAt int64 `bson:"updatedAt" json:"updatedAt"`
}
```

Stored as single document `_id: "integrations"` in collection `settings` (same DB, same collection pattern as encoding/hls/storage settings in torrent-engine).

### Error handling

Notify failures are **logged and ignored** — the torrent is already completed and saved. Failed notify does not block anything. The service retries once with a 5s timeout per request.

---

## Feature 2: qBittorrent API v2 Compatibility

Sonarr/Radarr support qBittorrent as a download client via its WebAPI v2. They need a minimal subset:

### Endpoints implemented

| Method | Path | Maps to |
|--------|------|---------|
| `POST` | `/api/v2/auth/login` | Always returns `"Ok."` (no real auth — localhost only) |
| `GET`  | `/api/v2/app/version` | Returns `"4.6.0"` |
| `GET`  | `/api/v2/app/webapiVersion` | Returns `"2.8.3"` |
| `GET`  | `/api/v2/torrents/info` | Proxies `GET http://torrentstream:8080/torrents` → maps to qBt format |
| `POST` | `/api/v2/torrents/add` | Proxies `POST http://torrentstream:8080/torrents` (magnet or .torrent) |
| `POST` | `/api/v2/torrents/delete` | Proxies `DELETE http://torrentstream:8080/torrents/{hash}` |

Auth: qBittorrent uses a `SID` cookie. Since this is localhost-only, login always succeeds and all subsequent requests are accepted without checking the cookie.

### Status mapping

| T◎RRX status | qBittorrent state |
|-------------|------------------|
| `active`    | `downloading`    |
| `completed` | `uploading`      |
| `stopped`   | `pausedDL`       |
| `error`     | `error`          |

### `torrents/info` response shape (minimal)

```json
[
  {
    "hash": "abc123",
    "name": "Movie.Name.2023",
    "state": "downloading",
    "progress": 0.42,
    "size": 4294967296,
    "downloaded": 1805123584,
    "dlspeed": 2097152,
    "save_path": "/data/",
    "category": "",
    "added_on": 1708612800
  }
]
```

### Sonarr/Radarr connection settings

Users configure in Sonarr/Radarr:
- **Host:** `<server-ip>` (or `localhost`)
- **Port:** `8070`
- **Password:** *(empty — auth disabled)*
- **Category:** *(leave empty)*

---

## Feature 3: Widget Endpoint

`GET /widget` — JSON for Homepage custom API widget or Homarr:

```json
{
  "active": 2,
  "completed": 15,
  "stopped": 1,
  "total": 18,
  "download_speed_bytes": 2097152,
  "download_speed_human": "2.0 MB/s"
}
```

Homepage config example (`services.yaml`):
```yaml
- name: T◎RRX
  href: http://server-ip
  widget:
    type: customapi
    url: http://server-ip:8070/widget
    mappings:
      - field: active
        label: Active
      - field: download_speed_human
        label: Speed
```

> **Bonus:** Since qBittorrent API is implemented, Homarr and Homepage also support the built-in qBittorrent widget pointing at `http://server-ip:8070`.

---

## Docker Compose

New service in `deploy/docker-compose.yml`:

```yaml
torrent-notifier:
  build:
    context: ../services/torrent-notifier
    dockerfile: ../../build/torrent-notifier.Dockerfile
  logging: *default-logging
  restart: unless-stopped
  ports:
    - "127.0.0.1:8070:8070"
  environment:
    HTTP_ADDR: ":8070"
    LOG_LEVEL: "info"
    LOG_FORMAT: "text"
    MONGO_URI: "mongodb://mongo:27017"
    MONGO_DB: "torrentstream"
    TORRENT_ENGINE_URL: "http://torrentstream:8080"
    OTEL_EXPORTER_OTLP_ENDPOINT: "http://jaeger:4318"
    OTEL_SERVICE_NAME: "torrent-notifier"
  depends_on:
    mongo:
      condition: service_healthy
    torrentstream:
      condition: service_healthy
    jaeger:
      condition: service_healthy
  networks:
    - edge
    - core
  deploy:
    resources:
      limits:
        memory: 128m
        cpus: "0.25"
  healthcheck:
    test: ["CMD-SHELL", "wget --spider --quiet http://localhost:8070/health || exit 1"]
    interval: 10s
    timeout: 5s
    retries: 3
```

### Traefik routing

New entries in `deploy/traefik/dynamic.yml`:

```yaml
routers:
  integrations:
    entryPoints: [web]
    rule: "PathPrefix(`/settings/integrations`) || PathPrefix(`/api/v2`) || PathPrefix(`/widget`)"
    service: torrent-notifier-api
    middlewares: [security-headers]
    priority: 195

services:
  torrent-notifier-api:
    loadBalancer:
      servers:
        - url: "http://torrent-notifier:8070"
```

Also add HTTPS mirror (`integrations-secure`).

---

## Frontend: Integrations Settings Section

New section added to `frontend/src/pages/SettingsPage.tsx` (same pattern as existing Storage/Encoding sections).

### UI layout

```
── Integrations ─────────────────────────────────────────
  Media Servers
  ┌─ Jellyfin ──────────────────────────────────────────┐
  │  [✓] Enabled                                         │
  │  URL        [ http://jellyfin:8096               ]  │
  │  API Key    [ ****************************       ]  │
  │                                    [Test] [Save]    │
  └─────────────────────────────────────────────────────┘
  ┌─ Emby ──────────────────────────────────────────────┐
  │  [ ] Enabled                                         │
  │  URL        [                                    ]  │
  │  API Key    [                                    ]  │
  └─────────────────────────────────────────────────────┘

  Download Clients
  ┌─ qBittorrent API (Sonarr / Radarr) ────────────────┐
  │  [✓] Enabled                                         │
  │  Connect Sonarr/Radarr to:                          │
  │    Host: <this server IP>  Port: 8070               │
  │    Leave password empty                             │
  └─────────────────────────────────────────────────────┘
```

### New API calls in `frontend/src/api.ts`

```typescript
GET  /settings/integrations  → IntegrationSettings
PATCH /settings/integrations → IntegrationSettings
POST  /settings/integrations/test-jellyfin  → { ok: bool, error?: string }
POST  /settings/integrations/test-emby      → { ok: bool, error?: string }
```

`Test` button calls the test endpoint which makes a live HTTP call to the configured server and returns success/failure.

---

## Build: `build/torrent-notifier.Dockerfile`

```dockerfile
FROM golang:1.25 AS build

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/torrent-notifier ./cmd/server

FROM alpine:3.20

RUN apk add --no-cache ca-certificates

WORKDIR /app

COPY --from=build /out/torrent-notifier /app/torrent-notifier

ENV HTTP_ADDR=:8070

EXPOSE 8070

ENTRYPOINT ["/app/torrent-notifier"]
```

---

## Files Changed

| File | Change |
|------|--------|
| `services/torrent-notifier/` | New service (all files) |
| `build/torrent-notifier.Dockerfile` | New Dockerfile |
| `deploy/docker-compose.yml` | Add `torrent-notifier` service |
| `deploy/traefik/dynamic.yml` | Add integrations router + service |
| `frontend/src/pages/SettingsPage.tsx` | Add Integrations section |
| `frontend/src/api.ts` | Add integration settings API calls |

---

## Out of Scope

- Webhook on torrent *start* or *progress* (only completion)
- Plex (requires cloud account, different auth)
- Bazarr / subtitle automation
- Full qBittorrent API (categories, labels, RSS feeds) — minimal subset only
- Persistent retry queue for failed notifications
