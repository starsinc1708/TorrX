# API Contracts

Base URL: `http://localhost:8080`  
Swagger UI: `/swagger`  
OpenAPI JSON: `/swagger/openapi.json` (source: `docs/openapi.json`)

All non-stream responses use `application/json`.

## Error Envelope
```json
{
  "error": {
    "code": "invalid_request",
    "message": "details"
  }
}
```

Common `error.code` values:
- `invalid_request`
- `not_found`
- `engine_error`
- `repository_error`
- `internal_error`
- `stream_unavailable`

## Torrent Control
- `POST /torrents`
- `GET /torrents`
- `GET /torrents/{id}`
- `POST /torrents/{id}/start`
- `POST /torrents/{id}/stop`
- `DELETE /torrents/{id}?deleteFiles=true|false`

## Session State
- `GET /torrents/{id}/state`
- `GET /torrents/state?status=active`

## Storage Settings
- `GET /settings/storage`
  - returns current storage mode and memory-spill settings.
- `PATCH /settings/storage`
  - body:
```json
{
  "memoryLimitBytes": 536870912
}
```
  - updates RAM limit at runtime (`0` = unlimited).
  - for `disk` mode returns `409 unsupported_operation`.

## Media Streaming
- `GET /torrents/{id}/stream?fileIndex={n}`
  - supports `Range: bytes=start-end`
  - returns `200` or `206`
- `GET /torrents/{id}/hls/{fileIndex}/index.m3u8`
  - optional query:
  - `audioTrack` (int, default `0`)
  - `subtitleTrack` (int, optional, burn-in)
  - returns playlist and triggers/reuses transcoding job
- `GET /torrents/{id}/hls/{fileIndex}/{segment}`
  - returns `.ts` segment

## Media Metadata
- `GET /torrents/{id}/media/{fileIndex}`
  - probes file with ffprobe
  - returns audio/subtitle track metadata
  - response:
```json
{
  "tracks": [
    {
      "index": 0,
      "type": "audio",
      "codec": "aac",
      "language": "eng",
      "title": "English",
      "default": true
    }
  ]
}
```

If probing fails (e.g. metadata not available yet), API returns `200` with empty `tracks`.

## Notes
- `TorrentRecord.Source` is persisted internally for session restore and not exposed in API JSON.
- `subtitleTrack` is implemented as subtitle burn-in in ffmpeg HLS pipeline.
- Complete schema reference: `docs/openapi.json`.
