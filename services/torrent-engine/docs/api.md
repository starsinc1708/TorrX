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
  - returns storage limits and data directory usage snapshot.
- `PATCH /settings/storage`
  - body (partial update supported):
```json
{
  "maxSessions": 8,
  "minDiskSpaceBytes": 2147483648
}
```
  - `maxSessions`: `0` means unlimited.
  - `minDiskSpaceBytes`: threshold used by disk-pressure guard.

## Media Streaming
- `GET /torrents/{id}/stream?fileIndex={n}`
  - supports `Range: bytes=start-end`
  - returns `200` or `206`
- `GET /torrents/{id}/hls/{fileIndex}/index.m3u8`
  - optional query:
  - `audioTrack` (int, default `0`)
  - returns playlist and triggers/reuses transcoding job
- `GET /torrents/{id}/hls/{fileIndex}/{segment}`
  - returns `.ts` segment
- `GET /torrents/{id}/subtitles/{fileIndex}/{subtitleTrack}.vtt`
  - returns subtitle track as WebVTT (`text/vtt`)
  - `subtitleTrack` index is taken from `GET /torrents/{id}/media/{fileIndex}` subtitle tracks
  - `HEAD` is supported for readiness checks

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

## Media Organization
- `GET /torrents?view=full` and `GET /torrents/{id}` include optional `mediaOrganization`:
  - `contentType`: `series | movie | mixed | unknown`
  - `groups`: grouped files (seasons, movies, other) with stable file references (`fileIndex`, `filePath`)

## Notes
- `TorrentRecord.Source` is persisted internally for session restore and not exposed in API JSON.
- Subtitle rendering uses WebVTT endpoint (`/subtitles/...vtt`) instead of burn-in in HLS video.
- Complete schema reference: `docs/openapi.json`.
