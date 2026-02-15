# Implementation Plan (Small Phases)

## Context
- Deployment model: self-host, trusted `localhost/LAN` environment.
- Backend is a reusable platform for web UI and custom clients.
- Next major feature: torrent catalog fed by search service + torrent engine.

---

## ✅ Critical Bug Fixes (Completed 2026-02-15)

**All P0 critical bugs from ROADMAP.md have been fixed** (commit `bb55b61`):

1. ✅ HLS reader double-close panic → Fixed with explicit Close() on each exit path
2. ✅ CreateTorrent race on duplicate magnets → Fixed with ErrAlreadyExists handling
3. ✅ StreamTorrent session leak on Start failure → Fixed with session.Stop() cleanup
4. ✅ DeleteTorrent data loss risk → Fixed by swapping operation order (DB first, files second)
5. ✅ MongoDB Update silent no-op → Fixed with MatchedCount check

**Impact:** Platform stability is now sufficient for feature development. No known critical data integrity or resource leak issues remain.

---

### Phase 0: Baseline and project hygiene
Goal: make implementation predictable and measurable.

Tasks:
- [ ] P0.1 Create branch and roadmap issue set (`phase-0` ... `phase-6`) with owners.
- [ ] P0.2 Add `docs/roadmap/implementation-plan.md` with phase checklists and links to PRs.
- [ ] P0.3 Fix BOM in `services/torrent-engine/internal/storage/memory/provider.go`.
- [ ] P0.4 Add minimal CI workflow: `go test` for both services + frontend `npm run build`.
- [ ] P0.5 Add definition of done template for tasks (tests, docs, backward compatibility notes).

Done when:
- CI runs on every PR.
- `go test ./... -cover` no longer fails because of encoding/tooling issues.

### Phase 1: Security profiles for LAN and hardened mode
Goal: preserve LAN simplicity while making production-hardening a config switch.

Tasks:
- [ ] P1.1 Add shared config flag `SECURITY_PROFILE` with values `lan` and `hardened`.
- [ ] P1.2 Implement profile resolver package in each backend service (`internal/app/security_profile.go`).
- [ ] P1.3 In `hardened` mode disable insecure observability defaults and require non-default admin creds.
- [ ] P1.4 In `hardened` mode restrict CORS origins and admin endpoint exposure.
- [ ] P1.5 Add startup logs printing active profile and effective security controls.
- [ ] P1.6 Document migration path in `deploy/.env.example` and README.

Done when:
- Switching profile changes behavior without code changes.
- LAN defaults still work out of the box.

Dependencies:
- Phase 0 CI baseline.

### Phase 2: Contract-first backend for multiple clients
Goal: make backend stable for web UI and custom clients.

Tasks:
- [ ] P2.1 Introduce public API prefix (`/api/v1`) while keeping temporary compatibility routes.
- [ ] P2.2 Define and freeze error envelope and error code registry (`docs/api/error-model.md`).
- [ ] P2.3 Define idempotency semantics for mutation endpoints (`start/stop/delete/update settings`).
- [ ] P2.4 Split public endpoints and internal/admin endpoints in routing and docs.
- [ ] P2.5 Add OpenAPI contract checks in CI (diff check against committed spec).
- [ ] P2.6 Add compatibility tests for old routes (deprecation window).

Done when:
- OpenAPI is authoritative and versioned.
- Public client contract is stable and documented.

Dependencies:
- Phase 0.

### Phase 3: SSRF-safe outbound network layer
Goal: protect LAN/internal network from unsafe provider-driven requests.

Tasks:
- [ ] P3.1 Extract reusable outbound URL validation utility from image proxy logic.
- [ ] P3.2 Apply URL/IP validation to Torznab fallback torrent fetch flow.
- [ ] P3.3 Add provider allowlist config (`SEARCH_EGRESS_ALLOWLIST`) with sane defaults.
- [ ] P3.4 Add tests for blocked hosts: loopback, private ranges, service DNS names.
- [ ] P3.5 Add diagnostics endpoint/metric for blocked outbound attempts.

Done when:
- Unsafe URLs are rejected before request execution.
- Tests cover both allowed and blocked network targets.

Dependencies:
- Phase 1 profile config (for per-profile strictness).

### Phase 4: Catalog domain foundation
Goal: create independent catalog bounded context.

Tasks:
- [ ] P4.1 Create catalog domain package with entities: `CatalogItem`, `SourceLink`, `IngestionState`.
- [ ] P4.2 Choose persistence model (Mongo collection set or dedicated DB schema) and create indexes.
- [ ] P4.3 Add catalog repository interfaces and initial adapter implementations.
- [ ] P4.4 Add API endpoints for catalog read/list/get (`/api/v1/catalog/...`).
- [ ] P4.5 Add endpoint for enqueueing ingestion jobs from query or provider result set.
- [ ] P4.6 Add status model for ingestion jobs (queued/running/failed/completed).

Done when:
- Catalog data persists independently from torrent runtime state.
- API supports listing and reading catalog entries.

Dependencies:
- Phase 2 contract-first API.

### Phase 5: Ingestion pipeline (search -> normalize -> dedupe -> enrich -> persist)
Goal: automate catalog growth from search and engine signals.

Tasks:
- [ ] P5.1 Implement ingestion orchestrator service with explicit pipeline stages.
- [ ] P5.2 Implement normalization stage (title/year/season/episode/source normalization).
- [ ] P5.3 Implement deduplication strategy (infohash-first, fallback composite keys).
- [ ] P5.4 Implement enrichment stage (quality, language, provider metadata, optional TMDB).
- [ ] P5.5 Implement persistence stage with upsert and provenance history.
- [ ] P5.6 Add periodic refresh worker for stale catalog entries.
- [ ] P5.7 Add observability metrics for each stage latency and failure rate.

Done when:
- Running ingestion from search query creates/updates catalog entries end-to-end.
- Duplicate items are merged predictably.

Dependencies:
- Phase 4 catalog domain.
- Phase 3 safe outbound policy.

### Phase 6: Client enablement and developer experience
Goal: make backend easy for custom clients and long-term maintenance.

Tasks:
- [ ] P6.1 Publish API usage guide for custom clients (auth modes, retries, errors, pagination).
- [ ] P6.2 Generate typed client SDK (or API client templates) from OpenAPI.
- [ ] P6.3 Add frontend tests for API contract-critical flows (`search`, `catalog`, `settings`).
- [ ] P6.4 Replace frontend runtime container with production static build image.
- [ ] P6.5 Add release checklist with compatibility and migration notes.

Done when:
- New client can be built without reading backend code internals.
- Release process validates compatibility before merge.

Dependencies:
- Phase 2 and Phase 4.

## Task Execution Order
0. ✅ **Critical Bug Fixes (COMPLETED 2026-02-15)** — prerequisite for all phases
1. Phase 0 — Baseline and project hygiene
2. Phase 1 and Phase 2 (can run partially in parallel)
3. Phase 3 — SSRF-safe outbound network layer
4. Phase 4 — Catalog domain foundation
5. Phase 5 — Ingestion pipeline
6. Phase 6 — Client enablement and developer experience

## Suggested PR Granularity
- One PR per task for `P0.x-P3.x`.
- For catalog (`P4.x-P5.x`), one PR per subdomain slice:
- Data model and repository
- API read endpoints
- Ingestion orchestration
- Enrichment and dedupe
- Background refresh and metrics
