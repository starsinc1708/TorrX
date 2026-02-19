# PRD: TorrX — Torrent Streaming HTTP Server

**Version:** 1.0
**Date:** 2026-02-19
**Status:** Draft

---

## 1. Application Overview & Goals

### 1.1 Vision

TorrX — self-hosted HTTP-сервер на Go, совмещающий функциональность торрент-клиента и медиасервера. Ключевая идея: пользователь добавляет торрент, и видео из него можно смотреть **немедленно**, не дожидаясь полного скачивания. Сервер приоритизирует скачивание фрагментов, необходимых для текущего воспроизведения, и отдаёт видеопоток по HTTP любому клиенту (браузер, Smart TV, VLC, мобильное приложение).

### 1.2 Core Goals

- **Streaming-first**: воспроизведение начинается сразу после добавления торрента, без ожидания полной загрузки.
- **Smart Buffer**: скользящее окно данных в оперативной памяти обеспечивает мгновенный отклик при воспроизведении и перемотке.
- **Self-hosted simplicity**: развёртывание через Docker Compose за минуты, без внешних зависимостей.
- **Universal client support**: любой HTTP-клиент или видеоплеер может подключиться и получить видеопоток.

### 1.3 Problem Statement

Существующие торрент-клиенты (qBittorrent, Transmission) не поддерживают стриминг. Медиасерверы (Plex, Jellyfin) требуют полностью скачанных файлов. TorrX закрывает этот разрыв — скачивание и воспроизведение происходят одновременно в одном сервисе.

---

## 2. Target Audience

- **Self-hosted энтузиасты** — пользователи, которые поднимают сервисы в домашней сети (Home Lab).
- **Небольшие домашние сети** — семья/соседи, 2–5 одновременных зрителей на разных устройствах (TV, ноутбук, телефон).
- **Пользователи с базовыми техническими навыками** — достаточно уметь запустить `docker-compose up`.

---

## 3. Core Features & Functionality

### 3.1 Torrent Management

**Description:** Полноценный торрент-менеджер — добавление, удаление, мониторинг торрентов.

**Capabilities:**
- Добавление торрентов через magnet-ссылку или загрузку .torrent файла
- Каталог всех добавленных торрентов с отображением статуса (downloading, seeding, paused, completed)
- Просмотр содержимого торрента (список файлов, размеры, прогресс)
- Выбор конкретных файлов для скачивания
- Пауза / возобновление / удаление торрентов
- Настраиваемые лимиты скорости загрузки/отдачи (download/upload rate limits)

**Acceptance Criteria:**
- Пользователь может добавить magnet-ссылку через API или веб-интерфейс, и торрент появляется в каталоге в течение 5 секунд
- Пользователь может добавить .torrent файл через upload endpoint
- Каталог отображает реальный прогресс скачивания, скорость, количество peers
- При удалении торрента пользователь может выбрать: удалить только из каталога или также удалить скачанные файлы с диска
- Состояние всех торрентов сохраняется между перезапусками сервера

**Technical Considerations:**
- Использовать библиотеку `anacrolix/torrent` как основу торрент-клиента
- Состояние торрентов persist в SQLite
- При старте сервера — восстановление всех активных торрентов из БД

---

### 3.2 Intelligent Priority System

**Description:** Автоматическая приоритизация скачивания на основе текущего воспроизведения.

**Capabilities:**
- Торрент, файл из которого сейчас воспроизводится, получает максимальный приоритет скачивания
- Pieces, необходимые для текущей позиции воспроизведения + buffer ahead, скачиваются в первую очередь
- При перемотке (seek) — мгновенное переключение приоритета на новую позицию
- Остальные торренты продолжают скачиваться, но с пониженной скоростью
- Поддержка нескольких одновременных стримов с независимыми приоритетами

**Acceptance Criteria:**
- При начале воспроизведения файла его торрент автоматически получает высший приоритет
- При seek на нескачанную позицию — приоритет скачивания переключается на новые pieces в течение < 1 секунды
- При завершении воспроизведения приоритет торрента возвращается к нормальному
- Несколько одновременных стримов получают равный высокий приоритет, остальные торренты — пониженный

**Technical Considerations:**
- Библиотека `anacrolix/torrent` поддерживает piece priority — использовать `PieceStateRuns` и `Piece.SetPriority()`
- Маппинг byte offset видеофайла → torrent piece index для определения, какие pieces нужны для текущей позиции
- Priority manager как отдельный компонент, координирующий приоритеты между стримами

---

### 3.3 Smart RAM Buffer (Sliding Window)

**Description:** Скользящее окно в оперативной памяти для мгновенной отдачи данных при воспроизведении.

**Capabilities:**
- Конфигурируемый размер RAM-буфера (например, 256 МБ, 512 МБ, 1 ГБ)
- Буфер содержит данные **вперёд** от текущей позиции воспроизведения (основная часть) и **назад** (меньшая часть для быстрой перемотки)
- Автоматическое скольжение буфера вместе с позицией воспроизведения
- Вытеснение наиболее далёких от текущей позиции данных при заполнении буфера
- Отдельный буфер для каждого активного стрима

**Acceptance Criteria:**
- Данные в пределах RAM-буфера отдаются клиенту без дискового I/O (latency < 10 мс)
- При перемотке в пределах буфера — мгновенная отдача данных
- При перемотке за пределы буфера — буфер перестраивается вокруг новой позиции
- Суммарное потребление RAM всех буферов не превышает настроенный лимит
- Размер буфера настраивается через веб-интерфейс без перезапуска сервера

**Technical Considerations:**
- Реализовать как ring buffer или ordered map `[piece_index] → []byte`
- Соотношение буфера: ~80% вперёд, ~20% назад от текущей позиции (настраиваемо)
- Данные в буфер попадают из двух источников: с диска (уже скачанные pieces) и напрямую из torrent download pipeline
- При нехватке RAM на несколько стримов — равномерное распределение буфера между активными стримами
- Metrics: hit rate буфера, текущее использование RAM

---

### 3.4 Dual-Mode Video Delivery

**Description:** Два режима отдачи видео в зависимости от состояния скачивания.

#### Mode 1: HLS Streaming (файл не полностью скачан)

- Транскодирование на лету через FFmpeg
- Генерация HLS-плейлиста (.m3u8) и сегментов (.ts)
- Поддержка выбора аудиодорожки (передача параметром в API)
- FFmpeg читает данные из RAM-буфера / диска

#### Mode 2: HTTP Range Requests (файл полностью скачан)

- Прямая отдача файла с диска без транскодирования
- Поддержка стандартных HTTP Range headers
- Нулевая нагрузка на CPU
- Автоматическое переключение с Mode 1 на Mode 2 при завершении скачивания

**Acceptance Criteria:**
- При запросе стрима незавершённого торрента — сервер отдаёт HLS-плейлист
- HLS-сегменты генерируются с минимальной задержкой (< 5 сек от текущей позиции скачивания)
- При завершении скачивания файла — следующий запрос клиента получает данные через Range Requests
- Переключение между режимами прозрачно для клиента (единый endpoint)
- Поддержка выбора аудиодорожки через query parameter (например, `?audio=1`)

**Technical Considerations:**
- FFmpeg запускается как child process: `ffmpeg -i pipe:0 -c:v copy -c:a aac -f hls ...`
- Если видеокодек совместим с HLS (H.264/H.265) — video stream copy без перекодирования (значительная экономия CPU)
- Перекодирование аудио в AAC для совместимости с HLS
- HLS-сегменты хранятся во временной директории, очищаются при завершении стрима
- Endpoint определяет режим автоматически: `GET /api/stream/{torrent_id}/{file_index}` — возвращает HLS или принимает Range headers в зависимости от статуса файла

---

### 3.5 Playback State Persistence

**Description:** Сохранение позиции воспроизведения для продолжения просмотра.

**Capabilities:**
- Автоматическое сохранение текущей позиции воспроизведения (timestamp)
- Возможность продолжить просмотр с последней позиции после паузы, закрытия клиента или перезапуска сервера
- История просмотров

**Acceptance Criteria:**
- Позиция воспроизведения сохраняется каждые 10 секунд (интервал настраивается)
- При повторном открытии файла — клиент получает информацию о последней позиции
- История просмотров доступна через API и веб-интерфейс
- Данные переживают перезапуск сервера

**Technical Considerations:**
- Клиент периодически отправляет `PUT /api/playback/{torrent_id}/{file_index}/progress` с текущим timestamp
- Сервер сохраняет в SQLite таблицу `playback_history (torrent_id, file_index, position_seconds, updated_at)`
- При запросе стрима — в ответе включается поле `last_position`

---

### 3.6 Web UI (Minimal)

**Description:** Встроенный веб-интерфейс для управления сервером.

**Capabilities:**
- Дашборд: обзор всех торрентов, скорость скачивания/отдачи, активные стримы
- Добавление торрентов (magnet-ссылка, upload .torrent файла)
- Управление торрентами (пауза, удаление, приоритет)
- Просмотр файлов в торренте, запуск воспроизведения
- Встроенный видеоплеер (HLS.js для режима стриминга)
- Страница настроек (RAM-буфер, лимиты скорости, и т.д.)
- Защита через HTTP Basic Auth

**Acceptance Criteria:**
- Веб-интерфейс доступен по адресу `http://<host>:<port>/`
- Авторизация по логину/паролю (Basic Auth)
- Все операции доступные через API также доступны через UI
- Встроенный видеоплеер воспроизводит HLS-потоки без внешних зависимостей
- Интерфейс адаптивен для использования на мобильных устройствах и TV-браузерах

**Technical Considerations:**
- Frontend: React или Vue.js (рекомендация: **React** + TypeScript — широкая экосистема, HLS.js имеет отличную React-интеграцию)
- Бандл встраивается в Go-бинарник через `embed.FS`
- Видеоплеер: `hls.js` для HLS-режима, нативный `<video>` с src для Range Requests режима
- Basic Auth реализуется как middleware в HTTP router

---

## 4. Technical Stack Recommendations

| Component | Technology | Rationale |
|---|---|---|
| **Language** | Go 1.22+ | Отличная конкурентность (горутины), производительная сетевая работа, простой деплой как single binary |
| **Torrent Engine** | `anacrolix/torrent` | Зрелая Go-библиотека, поддержка piece priorities, DHT, magnet links, PEX |
| **HTTP Router** | `chi` или `gin` | Lightweight, middleware support, хорошая производительность |
| **Database** | SQLite via `modernc.org/sqlite` (pure Go) или `mattn/go-sqlite3` (CGo) | Zero-dependency, single file, идеально для self-hosted |
| **Transcoding** | FFmpeg (external binary) | Индустриальный стандарт, поддержка всех кодеков, HLS output |
| **Frontend** | React + TypeScript | Широкая экосистема, HLS.js интеграция, встраивание через `embed.FS` |
| **HLS Player** | hls.js | Лёгкая библиотека для воспроизведения HLS в браузере |
| **Config** | YAML (файл) + SQLite (динамические настройки) | YAML для pre-boot параметров, БД для runtime-настроек |
| **Deployment** | Docker Compose | Один контейнер: Go binary + FFmpeg runtime |

---

## 5. Conceptual Data Model

### 5.1 SQLite Schema

```
torrents
├── id              TEXT PRIMARY KEY (info hash)
├── name            TEXT NOT NULL
├── magnet_uri      TEXT
├── status          TEXT NOT NULL (downloading | seeding | paused | completed | error)
├── total_size      INTEGER (bytes)
├── downloaded       INTEGER (bytes)
├── download_speed  INTEGER (bytes/sec, cached)
├── upload_speed    INTEGER (bytes/sec, cached)
├── added_at        DATETIME NOT NULL
├── completed_at    DATETIME
└── save_path       TEXT NOT NULL

torrent_files
├── id              INTEGER PRIMARY KEY AUTOINCREMENT
├── torrent_id      TEXT NOT NULL → torrents.id
├── file_index      INTEGER NOT NULL
├── path            TEXT NOT NULL (relative path inside torrent)
├── size            INTEGER NOT NULL (bytes)
├── is_selected     BOOLEAN DEFAULT true
├── is_video        BOOLEAN DEFAULT false
└── progress        REAL DEFAULT 0.0 (0.0–1.0)

playback_history
├── id              INTEGER PRIMARY KEY AUTOINCREMENT
├── torrent_id      TEXT NOT NULL → torrents.id
├── file_index      INTEGER NOT NULL
├── position_sec    REAL NOT NULL (seconds)
├── duration_sec    REAL (total duration, populated on first play)
├── updated_at      DATETIME NOT NULL
└── UNIQUE(torrent_id, file_index)

settings
├── key             TEXT PRIMARY KEY
└── value           TEXT NOT NULL (JSON-encoded)

active_streams
├── id              TEXT PRIMARY KEY (stream session UUID)
├── torrent_id      TEXT NOT NULL → torrents.id
├── file_index      INTEGER NOT NULL
├── current_pos     REAL (seconds)
├── buffer_ram_used INTEGER (bytes)
├── started_at      DATETIME NOT NULL
└── client_ip       TEXT
```

### 5.2 In-Memory Structures

```
RAMBuffer
├── stream_id       string
├── pieces          OrderedMap[piece_index → []byte]
├── current_pos     int64 (byte offset)
├── window_start    int64 (byte offset)
├── window_end      int64 (byte offset)
├── max_size        int64 (configured RAM limit)
└── used_size       int64

PriorityManager
├── active_streams  Map[stream_id → StreamPriority]
├── torrent_refs    Map[torrent_id → *torrent.Torrent]
└── StreamPriority
    ├── torrent_id    string
    ├── file_index    int
    ├── current_piece int
    ├── buffer_ahead  int (number of pieces)
    └── buffer_behind int (number of pieces)
```

---

## 6. REST API Design

### 6.1 Torrent Management

```
POST   /api/torrents                    — Add torrent (magnet URI in body or .torrent file upload)
GET    /api/torrents                    — List all torrents with status
GET    /api/torrents/{id}               — Get torrent details + file list
DELETE /api/torrents/{id}?delete_files=  — Remove torrent (optionally delete files)
PUT    /api/torrents/{id}/pause         — Pause torrent
PUT    /api/torrents/{id}/resume        — Resume torrent
```

### 6.2 Streaming

```
GET    /api/stream/{torrent_id}/{file_index}             — Stream endpoint (auto-selects HLS or Range)
GET    /api/stream/{torrent_id}/{file_index}/master.m3u8 — HLS master playlist
GET    /api/stream/{torrent_id}/{file_index}/segment/{n} — HLS segment
GET    /api/stream/{torrent_id}/{file_index}/audio_tracks — List available audio tracks

Query params:
  ?audio={track_index}  — Select audio track (default: 0)
```

### 6.3 Playback

```
GET    /api/playback/{torrent_id}/{file_index}/progress  — Get last playback position
PUT    /api/playback/{torrent_id}/{file_index}/progress  — Update playback position
                                                            Body: { "position_sec": 1234.5 }
GET    /api/playback/history                              — Get playback history
```

### 6.4 Settings

```
GET    /api/settings           — Get all dynamic settings
PUT    /api/settings           — Update settings
                                 Body: { "ram_buffer_mb": 512, "max_download_speed": 0, ... }
```

### 6.5 System

```
GET    /api/status             — Server status (active streams, RAM usage, global speeds)
```

---

## 7. UI Design Principles

- **Минимализм**: чистый, функциональный интерфейс без визуального шума
- **Dashboard-first**: главный экран — обзор торрентов, скорости, активных стримов
- **One-click play**: от списка файлов до воспроизведения — один клик
- **Responsive**: корректная работа на десктопе, планшете, TV-браузере, мобильном
- **Dark theme**: по умолчанию — тёмная тема (типично для медиа-приложений)

### Key Screens

1. **Dashboard** — список торрентов, глобальная скорость, активные стримы
2. **Torrent Detail** — файлы в торренте, прогресс, выбор файла для стриминга
3. **Player** — встроенный видеоплеер с controls, выбор аудиодорожки, отображение буфера
4. **Settings** — RAM-буфер, лимиты скорости, путь скачивания, учётные данные
5. **Add Torrent** — модальное окно с полем для magnet-ссылки / загрузки файла

---

## 8. Security Considerations

- **HTTP Basic Auth** — защита всех API endpoints и веб-интерфейса
- Логин/пароль задаётся в YAML-конфиге или переменных окружения
- HTTPS: не реализуется в самом сервере — рекомендуется использовать reverse proxy (nginx, Caddy) для TLS-терминации при необходимости
- Валидация входных данных: sanitize magnet URI, ограничение размера загружаемых .torrent файлов
- Rate limiting на API endpoints для защиты от случайного abuse
- FFmpeg запускается с минимальными привилегиями, без доступа к файлам вне рабочей директории

---

## 9. Configuration

### 9.1 Static Configuration (YAML / Environment Variables)

```yaml
# torrx.yaml
server:
  host: "0.0.0.0"
  port: 8080

auth:
  username: "admin"
  password: "changeme"

storage:
  download_dir: "/data/downloads"
  db_path: "/data/torrx.db"
  temp_dir: "/data/temp"      # HLS segments, temporary files

ffmpeg:
  binary_path: "/usr/bin/ffmpeg"
```

**Environment variable overrides:**
```
TORRX_SERVER_PORT=8080
TORRX_AUTH_USERNAME=admin
TORRX_AUTH_PASSWORD=changeme
TORRX_STORAGE_DOWNLOAD_DIR=/data/downloads
TORRX_FFMPEG_PATH=/usr/bin/ffmpeg
```

### 9.2 Dynamic Configuration (via API & Web UI, stored in SQLite)

| Setting | Type | Default | Description |
|---|---|---|---|
| `ram_buffer_mb` | int | 256 | Размер RAM-буфера на стрим (МБ) |
| `max_download_speed` | int | 0 | Лимит скорости скачивания (байт/сек, 0 = без лимита) |
| `max_upload_speed` | int | 0 | Лимит скорости отдачи (байт/сек, 0 = без лимита) |
| `max_concurrent_torrents` | int | 5 | Максимум одновременно активных торрентов |
| `buffer_ahead_percent` | int | 80 | Процент буфера, выделяемый вперёд от позиции |
| `playback_save_interval_sec` | int | 10 | Интервал сохранения позиции воспроизведения |
| `hls_segment_duration_sec` | int | 4 | Длительность HLS-сегмента |

---

## 10. Docker Compose Deployment

```yaml
# docker-compose.yml
version: "3.8"

services:
  torrx:
    build: .
    container_name: torrx
    ports:
      - "8080:8080"
    volumes:
      - ./data:/data           # downloads, database, temp files
      - ./config:/config       # torrx.yaml
    environment:
      - TORRX_AUTH_PASSWORD=changeme
    restart: unless-stopped
```

```dockerfile
# Dockerfile
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o torrx ./cmd/server

FROM alpine:3.19
RUN apk add --no-cache ffmpeg
COPY --from=builder /app/torrx /usr/local/bin/torrx
EXPOSE 8080
ENTRYPOINT ["torrx"]
```

---

## 11. Development Phases / Milestones

### Phase 1: Core Foundation (2–3 weeks)

- Инициализация Go-проекта, структура директорий
- HTTP-сервер с chi/gin, Basic Auth middleware
- YAML-конфигурация + env overrides
- SQLite — инициализация БД, миграции
- Интеграция `anacrolix/torrent`: добавление/удаление торрентов, magnet-ссылки
- REST API: CRUD торрентов, список файлов
- Docker + Docker Compose setup

**Deliverable:** Работающий торрент-менеджер через API (без стриминга).

### Phase 2: Streaming Engine (3–4 weeks)

- Piece priority manager: маппинг byte offset → piece index
- RAM buffer: sliding window реализация
- FFmpeg integration: генерация HLS на лету из данных буфера/диска
- HLS endpoint: master.m3u8, сегменты
- HTTP Range Requests для завершённых файлов
- Автоматическое переключение HLS ↔ Range Requests
- Поддержка выбора аудиодорожки

**Deliverable:** Видео из незавершённого торрента можно смотреть через VLC/браузер.

### Phase 3: Smart Buffer & Priority (2–3 weeks)

- Интеллектуальная приоритизация при seek (перемотке)
- Скользящее окно буфера: автоматическое вытеснение, подгрузка
- Поддержка нескольких одновременных стримов с независимыми буферами
- Распределение RAM между стримами
- Buffer metrics и мониторинг

**Deliverable:** Плавная перемотка по видео, мультиклиентный стриминг.

### Phase 4: Web UI (2–3 weeks)

- React + TypeScript проект, сборка, embed в Go binary
- Dashboard: торренты, скорости, стримы
- Torrent detail page: файлы, прогресс
- Встроенный видеоплеер с hls.js
- Страница настроек
- Add torrent modal
- Responsive layout + dark theme

**Deliverable:** Полностью функциональный веб-интерфейс.

### Phase 5: Playback & Polish (1–2 weeks)

- Сохранение / восстановление позиции воспроизведения
- История просмотров
- Динамические настройки через UI
- Graceful shutdown (корректное завершение стримов и сохранение состояния)
- Error handling и logging
- Документация (README, API docs)

**Deliverable:** Production-ready первая версия.

---

## 12. Potential Challenges & Mitigations

| Challenge | Impact | Mitigation |
|---|---|---|
| **Pieces не скачаны для текущей позиции** | Буферизация, задержка воспроизведения | Агрессивная приоритизация pieces вокруг текущей позиции; отображение индикатора загрузки на клиенте |
| **FFmpeg latency при старте стрима** | Задержка 2–5 сек до первого кадра | Pre-buffer: начинать FFmpeg заранее при выборе файла; кэширование первых HLS-сегментов |
| **Высокое потребление CPU при транскодировании** | Нехватка ресурсов при множестве стримов | Video stream copy (без перекодирования видео) когда кодек совместим; лимит на одновременные транскодирования |
| **RAM overflow при множестве стримов** | OOM kill контейнера | Жёсткий лимит на суммарный RAM; равномерное уменьшение буферов; предупреждение в UI |
| **Seek на далёкую нескачанную позицию** | Долгое ожидание | Показ прогресса скачивания на seekbar; UI-подсказка о доступности фрагментов |
| **Несовместимые кодеки** | Клиент не может воспроизвести поток | FFmpeg транскодирование в H.264 + AAC — универсальная совместимость |
| **Торрент с малым количеством сидеров** | Медленная загрузка, частые паузы | Отображение health торрента; предупреждение пользователя; adaptive buffer strategy |

---

## 13. Future Expansion Opportunities

- **Subtitles support**: извлечение встроенных субтитров (SRT/ASS из MKV), отдача через отдельный endpoint, WebVTT конвертация для браузера
- **Search integration**: встроенный поиск торрентов через Jackett/Prowlarr API
- **Chromecast / DLNA**: трансляция на Smart TV и медиа-устройства
- **Transcoding profiles**: адаптивный битрейт, выбор качества (1080p / 720p / 480p)
- **Multi-user support**: раздельные аккаунты с индивидуальной историей просмотра
- **Watch Together**: синхронное воспроизведение для нескольких клиентов
- **Mobile app**: нативный клиент для iOS/Android
- **Telegram bot**: добавление торрентов и уведомления через бота
- **Metrics & Monitoring**: Prometheus endpoint для интеграции с Grafana
- **Automatic media organization**: определение типа контента (фильм, сериал), парсинг имён, группировка

---

## 14. Appendix: Project Structure (Recommended)

```
torrx/
├── cmd/
│   └── server/
│       └── main.go              # Entry point
├── internal/
│   ├── config/                  # YAML + env config loading
│   ├── server/                  # HTTP server, router, middleware
│   ├── api/                     # REST API handlers
│   ├── torrent/                 # Torrent engine wrapper (anacrolix)
│   ├── stream/                  # Streaming engine (HLS, Range Requests)
│   ├── buffer/                  # RAM buffer (sliding window)
│   ├── priority/                # Piece priority manager
│   ├── ffmpeg/                  # FFmpeg process management
│   ├── db/                      # SQLite repository layer
│   └── models/                  # Shared data structures
├── web/                         # React frontend source
│   ├── src/
│   └── dist/                    # Built frontend (embedded)
├── migrations/                  # SQL migration files
├── config/
│   └── torrx.example.yaml
├── docker-compose.yml
├── Dockerfile
├── go.mod
├── go.sum
└── README.md
```
