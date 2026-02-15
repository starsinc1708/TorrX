# Torrent Stream Documentation

This folder is the onboarding entry point for both developers and AI agents.
It describes current behavior, architecture, API contracts, runtime flow, and frontend integration.

## Start Here
1. Read `docs/architecture.md` to understand layers, ports, adapters, and runtime flow.
2. Read `docs/use-cases.md` for implementation-ordered functional behavior.
3. Read `docs/api.md` for HTTP contracts.
4. Open `docs/openapi.json` or `http://localhost:8080/swagger` for machine-readable API schema.
5. Read `docs/frontend.md` for web client architecture and playback strategy.
6. Use `docs/diagrams/*.puml` for visual sequence and use-case views.

## Quick Mental Model
- Backend stores canonical torrent metadata in MongoDB.
- Runtime torrent sessions live inside anacrolix engine and may be restored from stored source.
- Playback has two modes:
- direct range stream (`/stream`) for browser-native formats
- HLS (`/hls`) for unsupported formats or explicit audio/subtitle track selection
- Media track metadata is discovered through ffprobe (`/media/{fileIndex}`).
- Frontend (`frontend`) orchestrates controls, polling, and mode switching (direct vs HLS).

## Full Docs Index
- `docs/architecture.md`: backend architecture, clean architecture boundaries, data and control flow.
- `docs/use-cases.md`: use-cases in recommended implementation order with diagram links.
- `docs/api.md`: REST contract reference and error model.
- `docs/frontend.md`: React client architecture, endpoint usage, and playback behavior.
- `docs/openapi.json`: OpenAPI 3.0.3 specification used by Swagger UI.
- `docs/diagrams/`: PlantUML source for use-case and sequence diagrams.

## Local Run
- Backend API: `http://localhost:8080`
- Swagger UI: `http://localhost:8080/swagger`
- Frontend (Vite): `http://localhost:5173`
- Torrent search microservice: `http://localhost:8090`

Docker compose startup:

```bash
docker compose -f deploy/docker-compose.yml up --build -d
```

## Runtime Environment
- `HTTP_ADDR`: API listen address. Default `:8080`.
- `LOG_LEVEL`: log level (`debug`, `info`, `warn`, `error`). Default `info`.
- `LOG_FORMAT`: log format (`text`, `json`). Default `text`.
- `MONGO_URI`: MongoDB URI. Default `mongodb://localhost:27017`.
- `MONGO_DB`: Mongo database name. Default `torrentstream`.
- `MONGO_COLLECTION`: Mongo collection name. Default `torrents`.
- `TORRENT_DATA_DIR`: directory for downloaded content. Default `data`.
- `OPENAPI_PATH`: optional custom path to OpenAPI JSON.
- `TORRENT_STORAGE_MODE`: `disk`, `memory`, or `hybrid`. Default `disk`.
- `TORRENT_MEMORY_LIMIT_BYTES`: memory cap for memory/hybrid storage mode. `0` means unlimited.
- `TORRENT_MEMORY_SPILL_DIR`: spill directory for memory/hybrid mode. Default `<TORRENT_DATA_DIR>/.ram-spill`.
- `FFMPEG_PATH`: ffmpeg binary path. Default `ffmpeg`.
- `FFPROBE_PATH`: ffprobe binary path. Default `ffprobe`.
- `HLS_DIR`: optional HLS temp workspace path. Default OS temp directory.

## Code Map
- `services/torrent-engine/cmd/server/main.go`: composition root, dependency wiring, HTTP server lifecycle.
- `services/torrent-engine/internal/api/http`: HTTP routing, validation, API errors, stream and HLS handlers.
- `services/torrent-engine/internal/usecase`: application use-cases.
- `services/torrent-engine/internal/domain`: domain entities and value types.
- `services/torrent-engine/internal/domain/ports`: port interfaces for engine, repository, storage, session.
- `services/torrent-engine/internal/services/torrent/engine/anacrolix`: torrent engine adapter.
- `services/torrent-engine/internal/services/torrent/engine/ffprobe`: media track probing adapter (audio/subtitle metadata).
- `services/torrent-engine/internal/services/session/player`: player session manager.
- `services/torrent-engine/internal/services/session/repository/mongo`: player/watch-history repositories.
- `services/torrent-engine/internal/services/search/parser`: tracker parsing/search service scaffold.
- `services/torrent-engine/internal/repository/mongo`: core MongoDB adapter.
- `services/torrent-engine/internal/storage/memory`: in-memory storage provider.
- `frontend`: React + Vite frontend.
