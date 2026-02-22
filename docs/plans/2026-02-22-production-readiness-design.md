# Production Readiness — Design Document

**Date:** 2026-02-22
**Approach:** Minimal patch (Variant A) — fixes applied directly to existing config files.
**Target environment:** LAN / home server (no inbound internet access).
**Goals:** Stable operation · Reliable deployment · LAN security.

---

## Scope

12 issues across 3 themes. No Go or React code changes — all fixes are in Docker Compose,
Dockerfiles, and env configuration files.

---

## Theme 1: Reliable Deployment

### P1 — Pin Docker image versions
**Files:** `deploy/docker-compose.yml`
**Problem:** `jaeger`, `prometheus`, `grafana`, `jackett`, `flaresolverr`, `prowlarr` use `:latest`.
A `docker compose pull` can silently break the stack.
**Fix:** Replace `:latest` with specific semver tags:
- `jaegertracing/all-in-one:1.65.0`
- `prom/prometheus:v3.2.1`
- `grafana/grafana:11.4.0`
- `linuxserver/jackett:v0.22.1612`
- `ghcr.io/flaresolverr/flaresolverr:v3.3.21`
- `linuxserver/prowlarr:1.31.2`

### P2 — NPM registry default
**Files:** `build/frontend.Dockerfile`
**Problem:** `NPM_REGISTRY` defaults to `https://registry.npmmirror.com` (Chinese mirror).
Builds fail or hang when that mirror is unavailable.
**Fix:** Change default to `https://registry.npmjs.org`.

### P3 — Graceful shutdown for torrent-engine
**Files:** `deploy/docker-compose.yml`
**Problem:** Docker default stop timeout is 10s. Torrent sessions may not close cleanly.
**Fix:** Add `stop_grace_period: 30s` to the `torrentstream` service.

### P4 — Log rotation
**Files:** `deploy/docker-compose.yml`
**Problem:** All containers use json-file logging with no size limit. Logs can fill disk.
**Fix:** Add a top-level `x-logging` anchor and apply it to all services:
```yaml
x-logging: &default-logging
  driver: json-file
  options:
    max-size: "10m"
    max-file: "3"
```

---

## Theme 2: LAN Security

### P5 — Observability ports bound to 0.0.0.0
**Files:** `deploy/docker-compose.yml`
**Problem:** Prometheus (9090), Grafana (3000), Jaeger (16686, 4317, 4318) are reachable
by anyone on the LAN without authentication.
**Fix:** Bind to localhost: `"127.0.0.1:9090:9090"` etc.

### P6 — MongoDB and Redis ports exposed
**Files:** `deploy/docker-compose.yml`
**Problem:** `27017` and `6379` are published to the host. Both services are already
reachable via the internal `core` network; host binding is unnecessary.
**Fix:** Remove `ports:` blocks from `mongo` and `redis`.

### P7 — Grafana default admin password
**Files:** `deploy/docker-compose.yml`, `deploy/.env.example`
**Problem:** `GF_SECURITY_ADMIN_PASSWORD` falls back to `admin` when not set in `.env`.
**Fix:**
- Remove `:-admin` fallback from compose (`${GRAFANA_ADMIN_PASSWORD}` — no default).
- Mark `GRAFANA_ADMIN_PASSWORD` as required in `.env.example` with a comment.

### P8 — Jackett and Prowlarr without resource limits
**Files:** `deploy/docker-compose.yml`
**Problem:** Neither service has `deploy.resources.limits`. They can consume unbounded RAM.
**Fix:** Add `memory: 512m` limits for both.

---

## Theme 3: Stable Operation

### P9 — Disk space guard disabled
**Files:** `deploy/docker-compose.yml`
**Problem:** `TORRENT_MIN_DISK_SPACE_BYTES` is not set → defaults to 0 (disabled).
A full disk will crash MongoDB and potentially the host OS.
**Fix:** Add `TORRENT_MIN_DISK_SPACE_BYTES: "2147483648"` (2 GB) to torrentstream env.

### P10 — Unlimited torrent sessions
**Files:** `deploy/docker-compose.yml`
**Problem:** `TORRENT_MAX_SESSIONS` not set → unlimited concurrent sessions.
Each session holds DHT connections and RAM.
**Fix:** Add `TORRENT_MAX_SESSIONS: "5"` to torrentstream env.

### P11 — Scheduled backup not enabled by default
**Files:** `deploy/.env.example`, optionally `deploy/docker-compose.yml`
**Problem:** `mongo-backup-scheduled` is gated behind `profiles: [backup-scheduled]`.
Watch history and settings are permanently lost on disk failure.
**Fix:** Add a clear note in `.env.example` explaining how to enable the scheduled backup
profile. Optionally remove the profile gate so backup runs by default.

### P12 — HLS directory not cleaned on startup
**Files:** `deploy/docker-compose.yml`
**Problem:** `/hls` accumulates segment files from previous sessions across restarts.
**Fix:** Mount the HLS dir as `tmpfs` so it is automatically cleared on each container start:
```yaml
tmpfs:
  - /hls:size=2g,mode=1777
```
Remove the named `hls_cache` volume.

---

## Files Changed

| File | Changes |
|------|---------|
| `deploy/docker-compose.yml` | P1, P3, P4, P5, P6, P7 (compose side), P8, P9, P10, P11 (optional), P12 |
| `deploy/.env.example` | P7 (required password), P11 (backup note) |
| `build/frontend.Dockerfile` | P2 |

---

## Out of Scope

- Authentication on API endpoints (T◎RRX is LAN-only by design; SECURITY.md documents this)
- TLS certificates (acceptable for LAN; user can add via reverse proxy)
- CI/CD pipeline
- Jaeger persistent storage (trace loss on restart acceptable for LAN observability)
