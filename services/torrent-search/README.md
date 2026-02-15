# Torrent Search Service

Standalone backend microservice for torrent search aggregation.

## Scope

- `GET /search`: aggregate results from multiple providers.
- `GET /search/providers`: list available providers for UI.
- `GET /health`: liveness endpoint.
- Provider model supports:
  - The Pirate Bay provider (`apibay.org` API).
  - 1337x provider (HTML parser via mirror endpoint).
  - RuTracker provider (forum search + topic parsing).
  - DHT search provider (`btdig.com`).
  - additional tracker parsers.

## API

### `GET /search`

Query params:

- `q` (required): search query.
- `limit` (optional, default `50`, max `200`).
- `offset` (optional, default `0`).
- `sortBy` (optional): `relevance` | `seeders` | `sizeBytes` | `publishedAt`.
- `sortOrder` (optional): `desc` | `asc`.
- `providers` (optional): comma-separated provider names, e.g. `piratebay,1337x,rutracker,dht`.

Response:

```json
{
  "query": "ubuntu",
  "items": [],
  "providers": [
    { "name": "dht", "ok": false, "count": 0, "error": "..." }
  ],
  "elapsedMs": 12,
  "totalItems": 0,
  "limit": 30,
  "offset": 0,
  "hasMore": false,
  "sortBy": "relevance",
  "sortOrder": "desc"
}
```

### `GET /search/providers`

Response:

```json
{
  "items": [
    { "name": "piratebay", "label": "The Pirate Bay", "kind": "index", "enabled": true },
    { "name": "1337x", "label": "1337x", "kind": "index", "enabled": true },
    { "name": "rutracker", "label": "RuTracker", "kind": "tracker", "enabled": true },
    { "name": "dht", "label": "DHT Index", "kind": "dht", "enabled": true }
  ]
}
```

## Local Run

```bash
cd services/torrent-search
go test ./...
go run ./cmd/server
```

Default address: `:8090`.

## Env

- `HTTP_ADDR` (default `:8090`)
- `SEARCH_TIMEOUT_SECONDS` (default `15`)
- `LOG_LEVEL` (`debug` | `info` | `warn` | `error`, default `info`)
- `LOG_FORMAT` (`text` | `json`, default `text`)
- `SEARCH_USER_AGENT` (default `torrent-stream-search/1.0`)
- `SEARCH_PROVIDER_PIRATEBAY_ENDPOINT` (default `https://apibay.org/q.php`)
- `SEARCH_PROVIDER_BITTORRENT_ENDPOINT` (legacy alias for Pirate Bay endpoint)
- `SEARCH_PROVIDER_1337X_ENDPOINT` (comma-separated mirrors, default `https://x1337x.ws,https://1337x.to,https://1377x.to`)
- `SEARCH_PROVIDER_RUTRACKER_ENDPOINT` (default `https://rutracker.org/forum/tracker.php`)
- `SEARCH_PROVIDER_RUTRACKER_COOKIE` (optional raw cookie header; recommended for stable RuTracker access)
- `SEARCH_PROVIDER_RUTRACKER_PROXY` (optional proxy URL for RuTracker requests, e.g. `http://proxy:3128` or `socks5://proxy:1080`)
- `SEARCH_PROVIDER_RUTRACKER_BB_SESSION` / `SEARCH_PROVIDER_RUTRACKER_BB_GUID` / `SEARCH_PROVIDER_RUTRACKER_BB_SSL` / `SEARCH_PROVIDER_RUTRACKER_CF_CLEARANCE` (optional cookie parts, auto-assembled)
- `SEARCH_PROVIDER_DHT_ENDPOINT` (default `https://btdig.com/search`)

Notes:

- `1337x` is wired in service, but can be blocked by Cloudflare depending on network.
- `rutracker` becomes fully usable only when cookies are provided.
- Search service keeps an in-memory hot cache (fresh + stale fallback) and background warm-up for popular first-page queries.
- Result ranking for `relevance` includes title normalization (RU/EN tokens), year and season/episode matching, plus swarm quality.
