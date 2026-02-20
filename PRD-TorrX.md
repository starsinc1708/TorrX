# PRD: T◎RRX — Torrent Streaming Platform

**Version:** 2.0
**Date:** 2026-02-19
**Status:** Reflects current implementation

---

## 1. Application Overview & Goals

### 1.1 Vision

T◎RRX — self-hosted платформа для поиска торрентов, управления загрузками и воспроизведения видео прямо в браузере. Три микросервиса за Traefik: Go torrent engine, Go search aggregator и React frontend. Ключевая идея: пользователь ищет контент, добавляет торрент и начинает смотреть видео **немедленно**, не дожидаясь полного скачивания. Сервер приоритизирует скачивание фрагментов, необходимых для текущего воспроизведения, и транскодирует видеопоток в HLS через FFmpeg для отдачи в браузер.

### 1.2 Core Goals

- **Streaming-first**: воспроизведение начинается сразу после добавления торрента — адаптивное HLS-транскодирование с multi-variant качеством (480p/720p/1080p).
- **Integrated Search**: встроенный мультипровайдерный поиск торрентов с SSE-стримингом результатов, TMDB-обогащением метаданными и рейтинговыми профилями.
- **Intelligent Priority**: пятиуровневая система приоритизации pieces с адаптивным скользящим окном, dormancy-системой и автоматическими boost'ами при stall'ах.
- **Self-hosted platform**: развёртывание через Docker Compose (12 сервисов) с полным observability stack (Prometheus, Grafana, Jaeger).
- **Browser-native playback**: полнофункциональный видеоплеер на HLS.js с выбором аудио/субтитров, timeline preview, resume позиции и keyboard shortcuts.

### 1.3 Problem Statement

Существующие торрент-клиенты (qBittorrent, Transmission) не поддерживают стриминг. Медиасерверы (Plex, Jellyfin) требуют полностью скачанных файлов и не имеют встроенного поиска. T◎RRX объединяет поиск, скачивание и воспроизведение в единой self-hosted платформе.

---

## 2. Target Audience

- **Self-hosted энтузиасты** — пользователи, которые поднимают сервисы в домашней сети (Home Lab).
- **Небольшие домашние сети** — семья/соседи, несколько одновременных зрителей на разных устройствах (TV, ноутбук, телефон).
- **Пользователи с базовыми техническими навыками** — достаточно уметь запустить `docker compose up`.

---

## 3. System Architecture

### 3.1 Service Topology

```
Traefik (:80, :443)
├── /torrents, /settings/*, /watch-history, /swagger, /ws → torrent-engine (:8080)
├── /search                                                → torrent-search (:8090)
└── /* (default)                                           → web-client (:80)
```

### 3.2 Services

| Service | Technology | Responsibility |
|---|---|---|
| **torrent-engine** | Go 1.25, anacrolix/torrent | Torrent management, HLS transcoding, streaming, watch history |
| **torrent-search** | Go 1.25, Redis | Multi-provider search aggregation, TMDB enrichment, SSE streaming |
| **web-client** | React 18 + TypeScript, Vite, Nginx | SPA: catalog, search, video player, settings |
| **Traefik v3.2** | Reverse proxy | Routing, TLS termination, metrics, OTEL tracing |
| **MongoDB 7** | Primary datastore | Torrents, settings, watch history persistence |
| **Redis 7** | Search cache | Search result caching with LRU eviction (256 MB) |
| **Jaeger** | Distributed tracing | OTLP collector, trace visualization |
| **Prometheus** | Metrics collection | Scrape + SLO recording rules + alerting |
| **Grafana** | Dashboards | Traefik RED, SLO overview, custom dashboards |
| **Jackett** | Torznab indexer | Torrent indexer aggregator для search-провайдеров |
| **Prowlarr** | Torznab indexer | Альтернативный indexer aggregator |
| **FlareSolverr** | Anti-bot bypass | Cloudflare/anti-bot protection bypass для поисковых провайдеров |

### 3.3 Network Layout

- **edge** — Traefik-facing: все сервисы доступные через reverse proxy
- **core** — Internal: MongoDB, Redis, межсервисная коммуникация

---

## 4. Core Features & Functionality

### 4.1 Torrent Management

**Description:** Полноценный торрент-менеджер с hexagonal architecture, focus mode для приоритизации стриминга, и real-time обновлениями через WebSocket.

**Capabilities:**
- Добавление торрентов через magnet-ссылку (JSON) или загрузку .torrent файла (multipart, max 5 MB)
- Каталог торрентов с фильтрацией по статусу (active, completed, stopped, all), текстовому поиску, тегам
- Сортировка по имени, дате создания/обновления, размеру, прогрессу
- Пагинация (limit/offset)
- Просмотр файлов торрента с per-file прогрессом (BytesCompleted)
- Start / Stop / Delete торрентов (с опцией удаления файлов)
- Bulk операции: start/stop/delete до 100 торрентов за запрос
- Focus mode: текущий торрент получает 100% пропускной способности (ModeFocused)
- Тегирование торрентов для организации
- Real-time обновления через WebSocket (состояние торрентов, player health)

**Domain Model (TorrentRecord):**
```
TorrentRecord
├── ID          TorrentID (info hash hex)
├── Name        string
├── Status      TorrentStatus (pending | active | completed | stopped | error)
├── Source      TorrentSource (Magnet URI or .torrent file path)
├── Files       []FileRef (index, path, length, bytesCompleted)
├── TotalBytes  int64
├── DoneBytes   int64
├── CreatedAt   time.Time
├── UpdatedAt   time.Time
└── Tags        []string
```

**Session State Machine (SessionMode):**
```
ModeIdle → {ModeDownloading, ModePaused, ModeStopped}
ModeDownloading → {ModeStopped, ModeFocused, ModePaused, ModeCompleted}
ModeFocused → {ModeDownloading, ModeStopped, ModeCompleted}
ModePaused → {ModeDownloading, ModeFocused, ModeStopped}
ModeStopped → {ModeDownloading, ModePaused, ModeIdle}
ModeCompleted → {ModeStopped, ModeFocused}
```

**Technical Implementation:**
- Hexagonal architecture: domain ports (Engine, Session, Repository, Storage, StreamReader) → adapter implementations
- `anacrolix/torrent` как torrent engine с MaxEstablishedConns=35
- MongoDB для persistence (text index на name, indexes на tags/createdAt/updatedAt/progress)
- SyncState background loop (10s): синхронизация engine state → MongoDB (atomic $max для progress)
- SessionRestore при старте: восстановление активных торрентов из БД
- Session limit eviction: LRU idle sessions при достижении MaxSessions

---

### 4.2 Intelligent Priority System

**Description:** Пятиуровневая система приоритизации скачивания pieces с адаптивным скользящим окном, dormancy-системой для idle readers, и автоматическими boost'ами.

**Priority Levels:**
```
PriorityNone (-1)     → PiecePriorityNone      — не скачивать
PriorityLow (0)       → PiecePriorityNone      — фоновый
PriorityNormal (1)    → PiecePriorityNormal    — обычный
PriorityReadahead (2) → PiecePriorityReadahead — упреждающий
PriorityNext (3)      → PiecePriorityNext      — следующий нужный
PriorityHigh (4)      → PiecePriorityNow       — немедленно
```

**SlidingPriorityReader — адаптивное скользящее окно:**

- Базовый размер окна: readahead × 4, clamped [32 MB, 256 MB], масштабируется до 1% длины файла
- Четырёхуровневый градиент приоритетов в окне:
  ```
  [pos, pos+2MB)          → PriorityHigh (immediate)
  [pos+2MB, pos+4MB)      → PriorityNext (very next)
  [pos+4MB, pos+~6MB)     → PriorityReadahead (prefetch)
  [pos+~6MB, pos+window)  → PriorityNormal
  ```
- Защита границ файла: первые и последние 8 MB никогда не деприоритизируются (MP4 moov, MKV SeekHead/Cues)
- Startup gradient: при начале стрима загрузка хвоста файла (16 MB) + 4-tier gradient на первые фрагменты

**Адаптивные boost'ы:**
- **Seek Boost** (10s): удвоение окна после перемотки для снижения столлов
- **Buffer-Low Boost** (5s): активация при downstream buffer < 30%, расширение high band до 6 MB
- **Dynamic Window**: EMA-smoothed read throughput (α=0.3), обновление каждые 500ms, target 30s buffered content

**Dormancy система:**
- Tracking множественных readers per torrent
- Idle readers (нет Read/Seek > 60s) усыпляются: readahead → 0, окно деприоритизируется
- Просыпаются при доступе (Read/Seek)
- Предотвращает захват bandwidth idle readers

**Focus Mode:**
- Один торрент в ModeFocused получает 100% bandwidth
- Остальные активные торренты переходят в ModePaused (DisallowData + SetMaxEstablishedConns(0))
- Автоматический unfocus при завершении стриминга

---

### 4.3 Tri-Mode Video Delivery

**Description:** Три режима отдачи видео в зависимости от состояния скачивания и совместимости кодеков.

#### Mode 1: HLS Adaptive Bitrate Streaming

Основной режим для незавершённых файлов и любых контейнеров (MKV, AVI, etc.).

- Multi-variant quality: 480p (1.5 Mbps), 720p (3 Mbps), 1080p (6 Mbps)
- FFmpeg transcoding с настраиваемыми параметрами (preset, CRF, audio bitrate)
- Burn-in субтитры через FFmpeg (`-vf subtitles=...`)
- Выбор аудио- и субтитр-дорожек через query parameters
- Server-side seek: soft (в рамках текущего FFmpeg job) и hard (рестарт FFmpeg с нового byte offset)
- Watchdog: детекция stall'ов → boost window → рестарт FFmpeg
- RAM buffer для HLS: configurable 4–4096 MB, prebuffer 1–1024 MB

#### Mode 2: Direct Browser Playback (MP4 Remux)

Для завершённых файлов с H.264+AAC кодеками.

- MP4/M4V: прямая отдача оригинального файла
- MKV с H.264+AAC: автоматический MP4 remux (codec copy, без перекодирования)
- Нулевая нагрузка на CPU
- HTTP 202 Accepted с Retry-After если remux ещё в процессе

#### Mode 3: Direct Raw Stream (HTTP Range Requests)

Для внешних плееров (VLC, mpv) и завершённых файлов.

- Прямая отдача с поддержкой HTTP Range headers (206 Partial Content)
- HEAD request support
- Kernel-level sendfile для завершённых файлов
- Responsive reader (EOF вместо блокировки при отсутствии pieces)

**Media Detection:**
- FFprobe анализ: video/audio/subtitle tracks, codec, duration, fps
- In-memory cache (TTL 5 min)
- Определение совместимости с direct playback (H.264 + AAC)
- Проверка готовности субтитров (файл на диске)

**HLS Configuration (dynamic, via API):**

| Setting | Range | Default | Description |
|---|---|---|---|
| `segmentDuration` | 2–10 | 4 | Длительность HLS-сегмента (сек) |
| `ramBufSizeMB` | 4–4096 | 16 | RAM buffer для FFmpeg input |
| `prebufferMB` | 1–1024 | 4 | Prebuffer перед запуском FFmpeg |
| `windowBeforeMB` | 1–1024 | 8 | Priority window позади playhead |
| `windowAfterMB` | 4–4096 | 32 | Priority window впереди playhead |

**Encoding Settings (dynamic, via API):**

| Setting | Options | Default | Description |
|---|---|---|---|
| `preset` | ultrafast → medium | fast | FFmpeg encoding preset |
| `crf` | 0–51 | 23 | Quality (lower = better) |
| `audioBitrate` | 96k, 128k, 192k, 256k | 128k | Audio bitrate |

---

### 4.4 Multi-Provider Torrent Search

**Description:** Отдельный микросервис (torrent-search) для агрегированного поиска по множеству источников с SSE-стримингом результатов, Redis кэшированием и TMDB обогащением.

**Search Providers:**

| Provider | Type | Protocol | Notes |
|---|---|---|---|
| **Pirate Bay** | REST API | JSON (apibay.org) | Простой HTTP GET, прямые magnet links |
| **1337x** | HTML scraping | Regex, multi-mirror | Двухпроходный: search results → detail pages |
| **RuTracker** | HTML scraping | Windows-1251, cookies | Требует аутентификацию, поддержка proxy |
| **DHT (BtDig)** | HTML scraping | Magnet regex | Stateless, без seeders/leechers |
| **Torznab** | XML API | Jackett/Prowlarr | Per-indexer fan-out, динамическая конфигурация |
| **TMDB** | REST API | JSON | Metadata enrichment (poster, rating, overview) |

**Capabilities:**
- Concurrent fan-out (до 10 провайдеров одновременно)
- SSE streaming: bootstrap → update per provider → done (инкрементальные результаты)
- Дедупликация по info_hash (primary) и title+size (fallback)
- Рейтинговые профили: weights для freshness, seeders, quality, language, size
- Query expansion: определение дубляжа, сезон/эпизод парсинг, транслитерация кириллицы
- Фильтрация: quality, content type, dubbing, year range, min seeders
- TMDB enrichment: poster, rating, overview, год, дубляж
- FlareSolverr интеграция для обхода Cloudflare
- Provider circuit breaker: 3 consecutive failures → exponential backoff (2min × 2^n, max 15min)

**Caching Strategy:**
- Двухуровневый кэш: in-memory (400 entries) + Redis (optional)
- Fresh: 6 часов, Stale: 18 часов (stale-while-revalidate)
- Background warmer: каждые 5 мин refreshes top-12 popular queries
- Popular query tracking: hit count + recency (max 200 entries)

---

### 4.5 Watch History & Playback Persistence

**Description:** Сохранение и восстановление позиции воспроизведения с историей просмотров.

**Capabilities:**
- Автоматическое сохранение позиции и длительности через PUT endpoint
- Resume: при открытии файла клиент получает последнюю позицию и предлагает продолжить
- История просмотров с пагинацией (по умолчанию последние 20)
- Metadata: torrent name, file path, watchedAt timestamp
- Persist в MongoDB, переживает перезапуск сервера

**Data Model (WatchPosition):**
```
WatchPosition
├── TorrentID    string
├── FileIndex    int
├── Position     float64 (seconds)
├── Duration     float64 (seconds)
├── TorrentName  string
├── FilePath     string
└── UpdatedAt    time.Time
```

---

### 4.6 Web UI

**Description:** Полнофункциональный React SPA с пятью экранами, тёмной/светлой темой, акцентными цветами и адаптивной вёрсткой.

**Technology Stack:**
- React 18 + TypeScript + React Router v7
- Vite 5.4 (build + dev server с proxy)
- Tailwind CSS 3.4 + Radix UI primitives (shadcn-style)
- HLS.js 1.5 для видеоплеера
- Lucide React для иконок
- Inter + JetBrains Mono шрифты

**Screens:**

1. **Catalog** (`/`) — основной экран: список торрентов, фильтрация/сортировка, bulk операции, detail view с файлами и session state, tag management, добавление торрентов
2. **Search** (`/discover`) — мультипровайдерный поиск с SSE-стримингом, расширенные фильтры, рейтинговые профили, saved presets, TMDB enrichment, one-click добавление в каталог
3. **Player** (`/watch/:torrentId/:fileIndex?`) — видеоплеер: HLS.js с multi-variant quality, выбор аудио/субтитр-дорожек, timeline preview canvas, screenshot, keyboard shortcuts, auto-hide controls, resume position, server-side seek (soft/hard)
4. **Settings** (`/settings`) — encoding (preset/CRF/bitrate), HLS (buffers/segments), search providers, FlareSolverr, тема и акцентный цвет
5. **Provider Diagnostics** (`/diagnostics`) — health checks, test провайдеров, circuit breaker status

**Design System:**
- CSS variables для light/dark тем
- 7 акцентных пресетов: indigo, blue, teal, green, orange, rose, violet + custom hex
- Runtime override через CSS custom properties
- Shared CSS классы: `ts-dropdown-trigger`, `ts-dropdown-panel`, `ts-dropdown-item`, `app-backdrop`

**State Management:**
- React Context: ThemeAccentProvider, WebSocketProvider, CatalogMetaProvider, ToastProvider, SearchProvider
- Custom hooks: useTorrents, useSessionState, useVideoPlayer, useSearch, useWebSocket, useKeyboardShortcuts, useAutoHideControls, useWatchPositionSave, useFullscreen, useScreenshot, useTimelinePreview
- localStorage для player prefs, theme, watch state

**API Client Features:**
- GET request deduplication (collapse duplicate in-flight requests)
- Configurable timeouts: 15s default, 7s polls, 90s long operations
- AbortController support
- Structured error handling (ApiRequestError with code, status, message)

---

## 5. REST API Design

### 5.1 Torrent Management (torrent-engine :8080)

```
POST   /torrents                      — Add torrent (JSON: magnet, Multipart: .torrent file)
GET    /torrents                      — List torrents (?status, ?search, ?tags, ?sortBy, ?sortOrder, ?limit, ?offset, ?view)
GET    /torrents/{id}                 — Get torrent details
DELETE /torrents/{id}?deleteFiles=    — Remove torrent
POST   /torrents/{id}/start           — Start downloading
POST   /torrents/{id}/stop            — Stop downloading
PUT    /torrents/{id}/tags            — Update tags
POST   /torrents/{id}/focus           — Set as current (100% bandwidth)
POST   /torrents/unfocus              — Clear focus mode
POST   /torrents/bulk/start           — Bulk start (max 100 IDs)
POST   /torrents/bulk/stop            — Bulk stop
POST   /torrents/bulk/delete          — Bulk delete
GET    /torrents/state?status=active  — List active session states
GET    /torrents/{id}/state           — Get session state (progress, peers, speeds, bitfield)
```

### 5.2 Streaming (torrent-engine :8080)

```
GET    /torrents/{id}/stream?fileIndex=      — Direct raw stream (Range Requests)
GET    /torrents/{id}/hls/{fileIndex}/index.m3u8  — HLS master playlist
GET    /torrents/{id}/hls/{fileIndex}/v{n}/index.m3u8  — HLS variant playlist
GET    /torrents/{id}/hls/{fileIndex}/{segment}.ts     — HLS MPEG-TS segment
POST   /torrents/{id}/hls/{fileIndex}/seek?time=&audioTrack=&subtitleTrack=  — Server-side seek (soft/hard)
GET    /torrents/{id}/direct/{fileIndex}     — Direct browser playback (MP4 remux)
GET    /torrents/{id}/media/{fileIndex}      — Media info (tracks, duration, codec detection)
```

### 5.3 Watch History (torrent-engine :8080)

```
GET    /watch-history                           — List recent positions (?limit)
GET    /watch-history/{torrentId}/{fileIndex}    — Get watch position
PUT    /watch-history/{torrentId}/{fileIndex}    — Save position (body: position, duration, name, path)
```

### 5.4 Settings (torrent-engine :8080)

```
GET    /settings/encoding        — Get encoding settings (preset, crf, audioBitrate)
PATCH  /settings/encoding        — Update encoding settings
GET    /settings/hls             — Get HLS settings (segmentDuration, buffers)
PATCH  /settings/hls             — Update HLS settings
GET    /settings/player          — Get player settings (currentTorrentId)
PATCH  /settings/player          — Update player settings
```

### 5.5 System (torrent-engine :8080)

```
GET    /internal/health/player   — Player health (HLS jobs, active sessions, issues)
GET    /metrics                  — Prometheus metrics
GET    /swagger                  — Swagger UI
GET    /swagger/openapi.json     — OpenAPI 3.0 spec
WS     /ws                      — WebSocket (torrents, player_settings, health events)
```

### 5.6 Search (torrent-search :8090)

```
GET    /search                    — Search (?q, ?limit, ?offset, ?providers, ?sortBy, ?sortOrder, ?nocache, ranking weights, filters)
GET    /search/stream             — SSE streaming search (same params)
GET    /search/suggest?q=         — TMDB autocomplete suggestions
GET    /search/image?url=         — Image proxy
GET    /search/providers          — List available providers
GET    /search/providers/health   — Provider diagnostics
POST   /search/providers/test     — Test provider connectivity
GET    /search/settings/providers — Get provider runtime config
PATCH  /search/settings/providers — Update provider config
POST   /search/settings/providers/autodetect — Auto-detect provider credentials
GET    /search/settings/flaresolverr  — Get FlareSolverr settings
POST   /search/settings/flaresolverr  — Apply FlareSolverr settings
GET    /health                    — Service health
GET    /metrics                   — Prometheus metrics
```

### 5.7 Error Response Format

```json
{
  "error": {
    "code": "not_found | invalid_request | internal_error | engine_error | stream_unavailable | not_configured",
    "message": "Human readable description"
  }
}
```

---

## 6. Technical Stack

| Component | Technology | Rationale |
|---|---|---|
| **Language** | Go 1.25 | Горутины, высокая производительность сети, single binary |
| **Torrent Engine** | `anacrolix/torrent` | Piece priorities, DHT, magnet links, PEX, responsive readers |
| **Database** | MongoDB 7 | Flexible schema, good for document-oriented torrent records |
| **Cache** | Redis 7 (256 MB, LRU) | Search result caching, TMDB metadata, provider config |
| **Transcoding** | FFmpeg (child process) | H.264/H.265 → HLS multi-variant, subtitle burn-in |
| **Media Analysis** | FFprobe | Codec detection, track enumeration, duration |
| **Frontend** | React 18 + TypeScript + Vite 5 | SPA с HLS.js, Radix UI, Tailwind CSS |
| **UI Components** | Radix UI primitives (shadcn-style) | Accessible, composable, unstyled |
| **Video Player** | HLS.js 1.5 | Adaptive bitrate, quality switching, seek support |
| **Reverse Proxy** | Traefik v3.2 | Routing, metrics, OTEL tracing, security headers |
| **Observability** | Prometheus + Grafana + Jaeger (OTEL) | Metrics, dashboards, distributed tracing |
| **Search Indexers** | Jackett / Prowlarr + FlareSolverr | External torrent indexer aggregation |
| **Deployment** | Docker Compose | 12 сервисов, 2 networks (edge + core), named volumes |

---

## 7. Data Model

### 7.1 MongoDB Collections

**torrents** — основная коллекция торрентов:
```
├── _id              string (info hash hex)
├── name             string
├── status           string (pending | active | completed | stopped | error)
├── source           { type: "magnet"|"file", uri/path }
├── files            [{ index, path, length, bytesCompleted }]
├── totalBytes       int64
├── doneBytes        int64
├── progress         float64 (0.0–1.0, cached for sort)
├── tags             []string
├── createdAt        datetime
├── updatedAt        datetime
└── Indexes: text(name), tags, createdAt, updatedAt, progress
```

**watch-history** — позиции воспроизведения:
```
├── torrentId        string
├── fileIndex        int
├── position         float64 (seconds)
├── duration         float64 (seconds)
├── torrentName      string
├── filePath         string
└── updatedAt        datetime
    UNIQUE(torrentId, fileIndex)
```

**encoding-settings**, **hls-settings**, **player-settings** — dynamic configuration documents.

### 7.2 In-Memory Structures

**SessionState** (runtime torrent state snapshot):
```
├── ID, Status, Mode, Progress (0-1)
├── Peers, DownloadSpeed, UploadSpeed
├── Files []FileRef (with BytesCompleted)
├── NumPieces, PieceBitfield
└── UpdatedAt
```

**SlidingPriorityReader** (adaptive buffering per stream):
```
├── windowBytes      int64 (current window size)
├── highBand         int64 (2 MB default, 6 MB on boost)
├── nextBand         int64 (2 MB)
├── prefetchBand     int64 (dynamic)
├── seekBoostUntil   time.Time
├── bufLowUntil      time.Time
├── consumptionRate  EMA (α=0.3, 500ms tick)
├── dormant          bool (idle > 60s)
└── generation       int64 (HLS stale detection)
```

---

## 8. UI Design Principles

- **Минимализм**: чистый, функциональный интерфейс без визуального шума
- **Catalog-first**: главный экран — обзор торрентов с фильтрацией, поиском и bulk операциями
- **One-click play**: от списка файлов до воспроизведения — один клик
- **Responsive**: desktop, tablet, TV-браузер, mobile
- **Theming**: тёмная/светлая тема + 7 акцентных цветов + custom hex
- **Keyboard-first player**: space, arrow keys, shortcuts для всех действий
- **Progressive search**: SSE-стриминг результатов по мере поступления от провайдеров

---

## 9. Configuration

### 9.1 Static Configuration (Environment Variables)

**Torrent Engine:**
```
HTTP_ADDR=:8080
MONGO_URI=mongodb://mongo:27017
MONGO_DB=torrentstream
MONGO_COLLECTION=torrents
TORRENT_DATA_DIR=/data
TORRENT_MAX_SESSIONS=0                    # 0 = unlimited
TORRENT_MIN_DISK_SPACE_BYTES=0
FFMPEG_PATH=/usr/bin/ffmpeg
FFPROBE_PATH=/usr/bin/ffprobe
HLS_DIR=/hls
HLS_PRESET=veryfast
HLS_CRF=23
HLS_AUDIO_BITRATE=128k
HLS_SEGMENT_DURATION=4
HLS_RAMBUF_SIZE_MB=16
HLS_PREBUFFER_MB=4
HLS_WINDOW_BEFORE_MB=8
HLS_WINDOW_AFTER_MB=32
CORS_ALLOWED_ORIGINS=                     # empty = allow all
OTEL_EXPORTER_OTLP_ENDPOINT=http://jaeger:4318
LOG_LEVEL=info
LOG_FORMAT=text                           # text | json
```

**Search Service:**
```
HTTP_ADDR=:8090
SEARCH_TIMEOUT_SECONDS=25
SEARCH_USER_AGENT=torrent-stream-search/1.0
SEARCH_PROVIDER_PIRATEBAY_ENDPOINT=https://apibay.org/q.php
SEARCH_PROVIDER_1337X_ENDPOINT=x1337x.ws,1337x.to,1377x.to
SEARCH_PROVIDER_RUTRACKER_ENDPOINT=https://rutracker.org/forum/tracker.php
SEARCH_PROVIDER_RUTRACKER_COOKIE=                  # combined cookies
SEARCH_PROVIDER_RUTRACKER_PROXY=                    # HTTP proxy
REDIS_URL=redis://redis:6379/0
TMDB_API_KEY=                                       # optional
FLARESOLVERR_URL=http://flaresolverr:8191/
SEARCH_CACHE_TTL_HOURS=6
SEARCH_CACHE_DISABLED=false
```

### 9.2 Dynamic Configuration (via API & Web UI, stored in MongoDB)

| Setting | Type | Range | Default | Description |
|---|---|---|---|---|
| `preset` | string | ultrafast→medium | fast | FFmpeg encoding preset |
| `crf` | int | 0–51 | 23 | Quality (lower = better) |
| `audioBitrate` | string | 96k–256k | 128k | Audio bitrate |
| `segmentDuration` | int | 2–10 | 4 | HLS segment duration (сек) |
| `ramBufSizeMB` | int | 4–4096 | 16 | RAM buffer для FFmpeg input |
| `prebufferMB` | int | 1–1024 | 4 | Prebuffer перед запуском FFmpeg |
| `windowBeforeMB` | int | 1–1024 | 8 | Priority window позади playhead |
| `windowAfterMB` | int | 4–4096 | 32 | Priority window впереди playhead |

---

## 10. Middleware & Security

- **CORS**: whitelist-based origin validation (empty = allow all)
- **Rate Limiting**: global token bucket (100 RPS, burst 200), skip для health/metrics
- **Recovery**: panic recovery с stack trace logging
- **Logging**: structured logs (method, path, status, bytes, duration, client IP), noisy paths → debug level
- **Security Headers**: через Traefik middleware
- **Input Validation**: magnet URI sanitization, .torrent file size limit (5 MB), query parameter validation
- **Path Traversal Protection**: при удалении файлов
- **HTTPS**: через Traefik TLS termination (не в самих сервисах)

---

## 11. Observability

### 11.1 Metrics (Prometheus)

- **Torrent Engine**: request counts/durations by route/method/status, active sessions, HLS job stats
- **Search Service**: provider latency, success rates, cache hits/misses
- **Traefik**: HTTP request metrics
- **Jaeger**: trace latency, span counts

### 11.2 SLO Rules & Alerting

| SLO | Target | Alerts |
|---|---|---|
| Search Availability | 99.0% (7d) | Error rate >5% / >10%, error budget <25% / exhausted |
| Search Latency | P95 <5s | P95 >5s / >15s |
| API Availability | 99.5% (7d) | Error rate >1% / >5%, error budget <25% / exhausted |
| API Latency | P95 <1.2s | P95 >1.2s |

### 11.3 Distributed Tracing

- OpenTelemetry (OTLP/HTTP) в обоих Go-сервисах
- Traefik → Jaeger tracing (configurable sample rate, default 10%)
- Jaeger all-in-one с in-memory storage (10K traces)

### 11.4 Health Checks

- **Player Health** (`/internal/health/player`): active sessions, HLS job diagnostics, issues detection
- **Search Health** (`/health`): service status
- **Provider Health** (`/search/providers/health`): per-provider circuit breaker, consecutive failures, latency

---

## 12. Docker Compose Deployment

### 12.1 Services

```yaml
# deploy/docker-compose.yml — 12 сервисов

services:
  traefik       # v3.2 — reverse proxy, routing, metrics, tracing
  mongo         # 7 — primary datastore
  redis         # 7-alpine — search cache (256MB, allkeys-lru)
  jaeger        # all-in-one — distributed tracing
  prometheus    # scrape + SLO rules + alerting
  grafana       # dashboards (Traefik RED, SLO overview)
  jackett       # torrent indexer aggregator
  prowlarr      # alternative indexer
  flaresolverr  # Cloudflare bypass
  torrent-search  # Go search aggregator
  torrentstream   # Go torrent engine
  web-client      # React SPA (Nginx)
```

### 12.2 Volumes

| Volume | Service | Mount | Purpose |
|---|---|---|---|
| `torrent_data` | torrentstream | `/data` | Downloaded torrent files |
| `hls_cache` | torrentstream | `/hls` | HLS transcode working directory |
| `anacrolix_cache` | torrentstream | `/root/.cache/anacrolix` | Torrent library DHT/metadata cache |
| `mongo_data` | mongo | `/data/db` | Database persistence |
| `redis_data` | redis | `/data` | Search cache persistence |
| `prometheus_data` | prometheus | `/prometheus` | Time-series metrics (15d retention) |
| `grafana_data` | grafana | `/var/lib/grafana` | Dashboard config |
| `mongo_backups` | mongo-backup | `/backups` | DB backups (7d retention) |

### 12.3 Build Pipeline

- **Torrent Engine**: Go 1.25 Alpine → Alpine 3.20 + FFmpeg
- **Search Service**: Go 1.25 Alpine → Alpine 3.20
- **Frontend**: Node 20 Alpine → Nginx 1.27 Alpine (gzip, SPA fallback, asset caching 1y)

---

## 13. Potential Challenges & Mitigations

| Challenge | Impact | Mitigation |
|---|---|---|
| **Pieces не скачаны для текущей позиции** | Задержка воспроизведения | 4-tier gradient priority + seek boost (2× window, 10s) + buffer-low boost (6MB high band) |
| **FFmpeg latency при старте** | Задержка 2–5 сек | Prebuffer (4 MB default) + HLS watchdog с auto-restart |
| **CPU при транскодировании** | Нехватка ресурсов | Configurable preset (ultrafast→medium), video stream copy где возможно, direct playback для H.264+AAC |
| **Seek на далёкую позицию** | Долгое ожидание | Soft seek (no restart) vs hard seek (restart from new offset), server returns seekMode для UI feedback |
| **Idle readers stealing bandwidth** | Медленная загрузка активного стрима | Dormancy система (60s idle → sleep, readahead=0, deprioritize) |
| **Search provider failures** | Неполные результаты | Circuit breaker (3 failures → backoff), query expansion fallback, stale cache serving |
| **Cloudflare protection** | Провайдеры недоступны | FlareSolverr интеграция, proxy support, mirror endpoints |
| **MongoDB connection loss** | Потеря state | SyncState использует atomic $max (no overwrites), SessionRestore при reconnect |
| **RAM overflow** | OOM kill | Configurable HLS RAM buffer limits, Redis LRU eviction (256 MB), in-memory cache caps |

---

## 14. Future Expansion Opportunities

- **Chromecast / DLNA**: трансляция на Smart TV и медиа-устройства
- **Multi-user support**: раздельные аккаунты с индивидуальной историей просмотра
- **Watch Together**: синхронное воспроизведение для нескольких клиентов
- **Mobile app**: нативный клиент для iOS/Android
- **Telegram bot**: добавление торрентов и уведомления через бота
- **Automatic media organization**: определение типа контента (фильм, сериал), парсинг имён, группировка в коллекции
- **WebVTT subtitles**: отдельная субтитр-дорожка (сейчас burn-in) для browser-native rendering
- **Adaptive bitrate profiles**: пользовательские quality profiles помимо 480p/720p/1080p

---

## 15. Project Structure

```
torrent-stream/
├── services/
│   ├── torrent-engine/                    # Go torrent engine service
│   │   ├── cmd/server/main.go             # Entry point
│   │   ├── internal/
│   │   │   ├── domain/                    # Value objects, port interfaces (Engine, Session, Repository, Storage, StreamReader)
│   │   │   ├── usecase/                   # Business logic (Create/Start/Stop/Delete/Stream/SyncState/SessionRestore/SlidingPriorityReader)
│   │   │   ├── api/http/                  # REST handlers, HLS streaming FSM, middleware, Swagger
│   │   │   ├── repository/mongo/          # MongoDB persistence
│   │   │   ├── services/
│   │   │   │   ├── torrent/engine/anacrolix/  # anacrolix/torrent wrapper
│   │   │   │   ├── torrent/engine/ffprobe/    # Media analysis
│   │   │   │   └── session/                   # Player state, watch history
│   │   │   └── app/                       # Configuration
│   │   ├── go.mod
│   │   └── go.sum
│   │
│   └── torrent-search/                    # Go search aggregator service
│       ├── cmd/server/main.go
│       ├── internal/
│       │   ├── providers/                 # bittorrentindex, x1337, rutracker, dht, torznab, tmdb
│       │   │   └── common/               # Shared provider utilities
│       │   ├── search/                    # Aggregator, normalization, caching, health, query expansion
│       │   ├── api/http/                  # REST + SSE handlers
│       │   └── app/                       # Configuration
│       ├── go.mod
│       └── go.sum
│
├── frontend/                              # React SPA
│   ├── src/
│   │   ├── pages/                         # CatalogPage, SearchPage, PlayerPage, SettingsPage, ProviderDiagnosticsPage
│   │   ├── components/                    # VideoPlayer, VideoControls, VideoTimeline, TorrentList, TorrentDetails, Header, AddTorrentModal
│   │   │   └── ui/                        # button, card, input, select, dialog, dropdown-menu, switch, tabs, badge, alert
│   │   ├── app/providers/                 # ThemeAccent, WebSocket, CatalogMeta, Toast, Search contexts
│   │   ├── hooks/                         # useTorrents, useSessionState, useVideoPlayer, useSearch, etc.
│   │   ├── api.ts                         # Fetch-based API client with deduplication
│   │   ├── lib/                           # design-system.ts, cn.ts
│   │   └── styles/globals.css             # CSS variables, themes, shared classes
│   ├── package.json
│   └── vite.config.ts
│
├── deploy/
│   ├── docker-compose.yml                 # 12 services
│   ├── traefik/                           # Static + dynamic routing config
│   ├── prometheus/                        # Scrape config + SLO rules
│   └── grafana/                           # Dashboards + datasource provisioning
│
├── build/                                 # Multi-stage Dockerfiles
│   ├── torrent-engine/Dockerfile
│   ├── torrent-search/Dockerfile
│   └── web-client/Dockerfile
│
├── CLAUDE.md
└── PRD-TorrX.md
```
