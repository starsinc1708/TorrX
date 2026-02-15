# Use Cases

API reference: `docs/api.md`  
OpenAPI schema: `docs/openapi.json`  
Ports: `internal/domain/ports/*`

Order below is the recommended implementation order.

## Actors
- `User`
- `Web Client`
- `Video Player`

## UC1: Create Torrent
- Endpoint: `POST /torrents`
- Goal: ingest magnet / `.torrent`, create engine session, persist metadata.
- Sequence: `docs/diagrams/uc-create-torrent.puml`

## UC2: List Torrents
- Endpoint: `GET /torrents`
- Goal: retrieve metadata list (`status`, `view`, pagination).
- Sequence: `docs/diagrams/uc-list-torrents.puml`

## UC3: Get Torrent Info
- Endpoint: `GET /torrents/{id}`
- Goal: read one persisted torrent record.
- Sequence: `docs/diagrams/uc-get-torrent-info.puml`

## UC4: Start Torrent
- Endpoint: `POST /torrents/{id}/start`
- Goal: resume downloading (with session restore if needed).
- Sequence: `docs/diagrams/uc-start-torrent.puml`

## UC5: Stop Torrent
- Endpoint: `POST /torrents/{id}/stop`
- Goal: pause downloading and persist `stopped` state.
- Sequence: `docs/diagrams/uc-stop-torrent.puml`

## UC6: Delete Torrent
- Endpoint: `DELETE /torrents/{id}?deleteFiles=true|false`
- Goal: remove record, optionally remove files.
- Sequence: `docs/diagrams/uc-delete-torrent.puml`

## UC7: Get Torrent State
- Endpoint: `GET /torrents/{id}/state`
- Goal: return live metrics for one session.
- Sequence: `docs/diagrams/uc-get-torrent-state.puml`

## UC8: List Active States
- Endpoint: `GET /torrents/state?status=active`
- Goal: return live metrics for active sessions.
- Sequence: `docs/diagrams/uc-list-active-states.puml`

## UC9: Probe Media Tracks
- Endpoint: `GET /torrents/{id}/media/{fileIndex}`
- Goal: discover audio/subtitle tracks for selected media file.
- Sequence: `docs/diagrams/uc-media-info.puml`

## UC10: Stream via HTTP Range
- Endpoint: `GET /torrents/{id}/stream?fileIndex=N`
- Goal: direct byte-range playback for browser-native formats.
- Sequence: `docs/diagrams/uc-stream-video.puml`
- Detailed range flow: `docs/diagrams/stream-range-sequence.puml`

## UC11: Get HLS Playlist
- Endpoint: `GET /torrents/{id}/hls/{fileIndex}/index.m3u8`
- Query: `audioTrack`, `subtitleTrack`
- Goal: transcode to HLS and select target audio/subtitle behavior.
- Sequence: `docs/diagrams/uc-hls-playlist.puml`

## UC12: Get HLS Segment
- Endpoint: `GET /torrents/{id}/hls/{fileIndex}/{segment}`
- Goal: return generated transport stream segments.
- Sequence: `docs/diagrams/uc-hls-segment.puml`

## Frontend Playback Flow
- Sequence: `docs/diagrams/frontend-playback.puml`
- Web client picks direct stream vs HLS and passes track-selection query params for HLS mode.
