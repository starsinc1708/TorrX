# Production Readiness Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Fix 12 configuration issues to make the LAN home-server deployment stable, reliably deployable, and secure.

**Architecture:** All changes are in `deploy/docker-compose.yml`, `deploy/.env.example`, and `build/frontend.Dockerfile`. No Go or React code is modified.

**Tech Stack:** Docker Compose v2, Traefik v3, YAML configuration.

---

## Verification commands (reference)

```bash
# Validate compose file syntax
docker compose -f deploy/docker-compose.yml config --quiet

# Check running containers are healthy
docker compose -f deploy/docker-compose.yml ps

# Inspect bound ports on host
docker compose -f deploy/docker-compose.yml ps --format json | python -m json.tool
```

---

### Task 1: Pin Docker image versions

**Files:**
- Modify: `deploy/docker-compose.yml` (lines with `:latest` images)

Images currently using `:latest` and their pinned replacements:
| Service | Current | Pinned |
|---------|---------|--------|
| jaeger | `jaegertracing/all-in-one:latest` | `jaegertracing/all-in-one:1.65.0` |
| prometheus | `prom/prometheus:latest` | `prom/prometheus:v3.2.1` |
| grafana | `grafana/grafana:latest` | `grafana/grafana:11.4.0` |
| jackett | `linuxserver/jackett:latest` | `linuxserver/jackett:v0.22.1612` |
| flaresolverr | `ghcr.io/flaresolverr/flaresolverr:latest` | `ghcr.io/flaresolverr/flaresolverr:v3.3.21` |
| prowlarr | `linuxserver/prowlarr:latest` | `linuxserver/prowlarr:1.31.2` |

> **Note:** Verify these are still current stable releases before applying. Check Docker Hub / GitHub releases pages.

**Step 1: Apply version pins**

In `deploy/docker-compose.yml` replace each `:latest` tag:
```yaml
# jaeger
image: jaegertracing/all-in-one:1.65.0

# prometheus
image: prom/prometheus:v3.2.1

# grafana
image: grafana/grafana:11.4.0

# jackett
image: linuxserver/jackett:v0.22.1612

# flaresolverr
image: ghcr.io/flaresolverr/flaresolverr:v3.3.21

# prowlarr
image: linuxserver/prowlarr:1.31.2
```

**Step 2: Validate compose**
```bash
docker compose -f deploy/docker-compose.yml config --quiet
```
Expected: no output (no errors).

**Step 3: Commit**
```bash
git add deploy/docker-compose.yml
git commit -m "fix(deploy): pin docker image versions to avoid surprise updates"
```

---

### Task 2: Fix NPM registry default

**Files:**
- Modify: `build/frontend.Dockerfile`

**Step 1: Find the current ARG**

Look for the line:
```dockerfile
ARG NPM_REGISTRY="https://registry.npmmirror.com"
```

**Step 2: Change to official registry**
```dockerfile
ARG NPM_REGISTRY="https://registry.npmjs.org"
```

**Step 3: Validate the Dockerfile parses**
```bash
docker build --no-cache --target deps -f build/frontend.Dockerfile . --dry-run 2>&1 | head -5
```
Expected: no syntax errors.

**Step 4: Commit**
```bash
git add build/frontend.Dockerfile
git commit -m "fix(deploy): use official npm registry as default"
```

---

### Task 3: Add graceful shutdown and log rotation

These two changes both go in `deploy/docker-compose.yml` and are committed together.

**Files:**
- Modify: `deploy/docker-compose.yml`

**Step 1: Add `stop_grace_period` to torrentstream**

Find the `torrentstream:` service block. Add after `restart: unless-stopped`:
```yaml
  torrentstream:
    ...
    restart: unless-stopped
    stop_grace_period: 30s
```

**Step 2: Add log rotation to all services**

Add a YAML anchor at the top of the `services:` section (right before `traefik:`):
```yaml
x-logging: &default-logging
  driver: json-file
  options:
    max-size: "10m"
    max-file: "3"
```

Then add `logging: *default-logging` to every service that doesn't already have it:
`traefik`, `jaeger`, `prometheus`, `grafana`, `jackett`, `flaresolverr`, `prowlarr`, `torrent-search`, `torrentstream`, `redis`, `mongo`, `web-client`.

Example for one service:
```yaml
  traefik:
    image: traefik:v3.2
    restart: unless-stopped
    logging: *default-logging
    command:
      ...
```

**Step 3: Validate**
```bash
docker compose -f deploy/docker-compose.yml config --quiet
```
Expected: no output.

**Step 4: Commit**
```bash
git add deploy/docker-compose.yml
git commit -m "fix(deploy): add graceful shutdown (30s) and log rotation (10m/3 files)"
```

---

### Task 4: Restrict observability ports to localhost

**Files:**
- Modify: `deploy/docker-compose.yml`

These ports should only be accessible from the host machine itself (e.g. via SSH tunnel), not from other LAN devices.

**Step 1: Apply port bindings**

For each observability service, change the port format from `"PORT:PORT"` to `"127.0.0.1:PORT:PORT"`:

```yaml
  jaeger:
    ports:
      - "127.0.0.1:16686:16686"
      - "127.0.0.1:4317:4317"
      - "127.0.0.1:4318:4318"
      - "127.0.0.1:14269:14269"

  prometheus:
    ports:
      - "127.0.0.1:9090:9090"

  grafana:
    ports:
      - "127.0.0.1:3000:3000"

  jackett:
    ports:
      - "127.0.0.1:9117:9117"

  flaresolverr:
    ports:
      - "127.0.0.1:8191:8191"

  prowlarr:
    ports:
      - "127.0.0.1:9696:9696"
```

> **Note:** The main app ports `80` and `443` on Traefik stay on `0.0.0.0` — they need to be reachable from LAN devices.

**Step 2: Validate**
```bash
docker compose -f deploy/docker-compose.yml config --quiet
```

**Step 3: Commit**
```bash
git add deploy/docker-compose.yml
git commit -m "fix(deploy): bind observability ports to 127.0.0.1 only"
```

---

### Task 5: Remove MongoDB and Redis host port exposure

**Files:**
- Modify: `deploy/docker-compose.yml`

These services live in the `core` network and are not needed on the host. Removing their `ports:` blocks closes them from the host entirely.

**Step 1: Remove ports blocks**

For `redis:` — delete the entire `ports:` block:
```yaml
# DELETE these lines:
    ports:
      - "6379:6379"
```

For `mongo:` — delete the entire `ports:` block:
```yaml
# DELETE these lines:
    ports:
      - "27017:27017"
```

**Step 2: Validate**
```bash
docker compose -f deploy/docker-compose.yml config --quiet
```

**Step 3: Verify services still connect (dry run)**
```bash
docker compose -f deploy/docker-compose.yml config | grep -A5 "redis:\|mongo:"
```
Expected: no `ports:` entry under redis or mongo.

**Step 4: Commit**
```bash
git add deploy/docker-compose.yml
git commit -m "fix(deploy): remove host port exposure for mongo and redis"
```

---

### Task 6: Require Grafana password via env

**Files:**
- Modify: `deploy/docker-compose.yml`
- Modify: `deploy/.env.example`

**Step 1: Remove hardcoded fallback in compose**

Find in the `grafana:` service:
```yaml
      GF_SECURITY_ADMIN_USER: "${GRAFANA_ADMIN_USER:-admin}"
      GF_SECURITY_ADMIN_PASSWORD: "${GRAFANA_ADMIN_PASSWORD:-admin}"
```

Change to (keep user fallback, remove password fallback):
```yaml
      GF_SECURITY_ADMIN_USER: "${GRAFANA_ADMIN_USER:-admin}"
      GF_SECURITY_ADMIN_PASSWORD: "${GRAFANA_ADMIN_PASSWORD}"
```

**Step 2: Update `.env.example`**

Add a required section at the top of `deploy/.env.example`:
```bash
# ============================================================
# REQUIRED — must be set before running docker compose up
# ============================================================

# Grafana admin password (no default — must be set explicitly)
GRAFANA_ADMIN_PASSWORD=changeme
```

**Step 3: Validate compose**
```bash
docker compose -f deploy/docker-compose.yml config --quiet
```

**Step 4: Commit**
```bash
git add deploy/docker-compose.yml deploy/.env.example
git commit -m "fix(deploy): require grafana admin password via env, remove default"
```

---

### Task 7: Add resource limits for Jackett and Prowlarr

**Files:**
- Modify: `deploy/docker-compose.yml`

**Step 1: Add limits**

For the `jackett:` service, add after `networks:`:
```yaml
    deploy:
      resources:
        limits:
          memory: 512m
          cpus: "0.5"
```

For the `prowlarr:` service, add after `networks:`:
```yaml
    deploy:
      resources:
        limits:
          memory: 512m
          cpus: "0.5"
```

**Step 2: Validate**
```bash
docker compose -f deploy/docker-compose.yml config --quiet
```

**Step 3: Commit**
```bash
git add deploy/docker-compose.yml
git commit -m "fix(deploy): add memory/cpu limits for jackett and prowlarr"
```

---

### Task 8: Enable disk space guard and session limit

**Files:**
- Modify: `deploy/docker-compose.yml`

**Step 1: Add env vars to torrentstream**

In the `torrentstream:` service `environment:` block, add:
```yaml
      TORRENT_MIN_DISK_SPACE_BYTES: "2147483648"   # 2 GB free minimum
      TORRENT_MAX_SESSIONS: "5"
```

**Step 2: Validate**
```bash
docker compose -f deploy/docker-compose.yml config --quiet
```

**Step 3: Confirm the vars are picked up by the engine**

Search for where these env vars are consumed:
```bash
grep -r "MIN_DISK_SPACE\|MAX_SESSIONS" services/torrent-engine/internal/app/
```
Expected: both are read in `config.go`.

**Step 4: Commit**
```bash
git add deploy/docker-compose.yml
git commit -m "fix(deploy): enable disk space guard (2GB) and max 5 torrent sessions"
```

---

### Task 9: Document scheduled backup + use tmpfs for HLS

Two independent fixes; commit separately.

**Files:**
- Modify: `deploy/.env.example`
- Modify: `deploy/docker-compose.yml`

**Step 1: Document backup in `.env.example`**

Add to `deploy/.env.example`:
```bash
# ============================================================
# BACKUP (optional but recommended)
# ============================================================
# Enable scheduled daily MongoDB backup (runs at 3 AM).
# Start with: docker compose --profile backup-scheduled up -d
# Backups are stored in the mongo_backups volume.
# To run a one-off backup: docker compose --profile backup run --rm mongo-backup
```

**Step 2: Commit backup docs**
```bash
git add deploy/.env.example
git commit -m "docs(deploy): document how to enable scheduled mongo backup"
```

**Step 3: Replace hls_cache volume with tmpfs**

In `deploy/docker-compose.yml`, in the `torrentstream:` service:

Remove from `volumes:`:
```yaml
      - hls_cache:/hls
```

Add a `tmpfs:` block:
```yaml
    tmpfs:
      - /hls:size=2147483648,mode=1777
```
(2 GB tmpfs; adjust to match server RAM — or use `size=1073741824` for 1 GB.)

In the `volumes:` section at the bottom, remove:
```yaml
  hls_cache:
```

**Step 4: Validate**
```bash
docker compose -f deploy/docker-compose.yml config --quiet
```

**Step 5: Commit HLS tmpfs**
```bash
git add deploy/docker-compose.yml
git commit -m "fix(deploy): mount HLS dir as tmpfs to auto-clean segments on restart"
```

---

## Final Verification

After all tasks are committed, do a full stack smoke test:

```bash
# Pull new pinned images
docker compose -f deploy/docker-compose.yml pull

# Bring up the stack
docker compose -f deploy/docker-compose.yml up -d

# Wait ~30s then check all services are healthy
docker compose -f deploy/docker-compose.yml ps

# Verify observability ports are NOT reachable from LAN
# (run this from another machine on the LAN)
curl -m 3 http://HOST_IP:9090  # should time out / refuse

# Verify main app is reachable
curl -m 3 http://HOST_IP/      # should return HTML
```

Expected final state: all services show `healthy`, observability ports unreachable from LAN, main app accessible.

---

## Summary of commits

1. `fix(deploy): pin docker image versions to avoid surprise updates`
2. `fix(deploy): use official npm registry as default`
3. `fix(deploy): add graceful shutdown (30s) and log rotation (10m/3 files)`
4. `fix(deploy): bind observability ports to 127.0.0.1 only`
5. `fix(deploy): remove host port exposure for mongo and redis`
6. `fix(deploy): require grafana admin password via env, remove default`
7. `fix(deploy): add memory/cpu limits for jackett and prowlarr`
8. `fix(deploy): enable disk space guard (2GB) and max 5 torrent sessions`
9. `docs(deploy): document how to enable scheduled mongo backup`
10. `fix(deploy): mount HLS dir as tmpfs to auto-clean segments on restart`
