# Frontend Architecture

Location: `frontend`  
Stack: React 18 + TypeScript + Vite + `hls.js`

## Goals
- Manage torrents (create/start/stop/delete).
- Observe real-time session state.
- Play selected file in browser.
- Support audio/subtitle selection for multi-track media.

## Main Files
- `frontend/src/App.tsx`: page composition and cross-component wiring.
- `frontend/src/api.ts`: typed API client and URL builders.
- `frontend/src/hooks/useTorrents.ts`: torrent list and control actions.
- `frontend/src/hooks/useSessionState.ts`: selected torrent state polling.
- `frontend/src/hooks/useVideoPlayer.ts`: playback mode, selected file, media track state.
- `frontend/src/components/VideoPlayer.tsx`: player UI, controls, settings menu, HLS/direct source binding.
- `frontend/src/styles.css`: layout and controls styling.

## API Usage
Frontend calls:
- `POST /torrents`
- `GET /torrents`
- `GET /torrents/{id}`
- `POST /torrents/{id}/start`
- `POST /torrents/{id}/stop`
- `DELETE /torrents/{id}`
- `GET /torrents/{id}/state`
- `GET /torrents/state?status=active`
- `GET /torrents/{id}/media/{fileIndex}`

Playback URL builders:
- direct stream: `/torrents/{id}/stream?fileIndex={n}`
- HLS playlist: `/torrents/{id}/hls/{fileIndex}/index.m3u8[?audioTrack=&subtitleTrack=]`

## Playback Decision Logic
`useVideoPlayer` chooses source by rules:
1. If file extension is browser-native (`.mp4`, `.webm`, `.ogg`, `.m4v`, `.mov`) and no explicit track override, use direct stream.
2. Otherwise use HLS.
3. If audio/subtitle track is explicitly selected, force HLS and append query params.

`VideoPlayer` behavior:
- native HLS for Safari (`video.canPlayType('application/vnd.apple.mpegurl')`)
- `hls.js` for other browsers
- custom controls for seek, volume, fullscreen, screenshot
- settings popover for audio/subtitle selection

## Track Selection
- Audio and subtitle lists come from `GET /media/{fileIndex}`.
- Audio selection maps to `audioTrack`.
- Subtitle selection maps to `subtitleTrack`.
- Subtitle mode is burn-in via backend ffmpeg pipeline.

## Local Run
```bash
cd frontend
npm install
npm run dev -- --host 0.0.0.0 --port 5173
```

Environment:
- `VITE_API_BASE_URL`: explicit API base URL.
- `VITE_API_PROXY_TARGET`: Vite proxy target, default `http://localhost:8080`.

## Docker
- frontend image: `build/frontend.Dockerfile`
- compose service: `web-client` in `deploy/docker-compose.yml`
- exposed port: `5173`
