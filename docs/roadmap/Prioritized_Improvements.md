# Prioritized Improvements (Updated for localhost/LAN)

## Assumptions
- Deployment model: self-host, trusted `localhost/LAN` environment.
- Backend is a reusable platform for web UI and custom clients.
- Next major feature: torrent catalog fed by search service + torrent engine.

## High Priority

### 1. Define explicit security profiles (`lan` and `hardened`)
Why: with LAN-only assumptions, strict internet-grade auth is optional, but accidental exposure is still realistic (port forwarding, VPN, router/NAT misconfig).
Risk: once exposed, open control endpoints and admin dashboards are immediately vulnerable.
How:
- Add startup profile: `SECURITY_PROFILE=lan|hardened`.
- In `lan`: allow simplified local setup.
- In `hardened`: require auth, disable insecure dashboards, restrict CORS/origins.
- Default bind for admin surfaces to loopback or internal network only.

### 2. Make backend contract-first for multi-client compatibility
Why: custom clients need stable contracts and predictable behavior, not UI-coupled API semantics.
Risk: breaking mobile/desktop/custom clients when UI changes.
How:
- Introduce API versioning (`/api/v1/...`).
- Freeze error contract and idempotency guarantees for mutation endpoints.
- Keep OpenAPI as source of truth and add compatibility checks in CI.
- Split public API (for clients) from internal/admin endpoints.

### 3. Prepare catalog as a separate bounded context
Why: catalog is becoming a product-level domain, not just a UI view.
Risk: tight coupling between search/runtime engine and catalog logic will slow future features.
How:
- Create catalog domain model (`CatalogItem`, `SourceLink`, `IngestionState`, `QualitySignals`).
- Implement async ingestion pipeline: `search -> normalize -> dedupe -> enrich -> persist`.
- Store provenance (provider, score, timestamp) and refresh policy.
- Keep engine state ephemeral and catalog metadata durable.

### 4. Close SSRF-class gaps in Torznab fallback fetches
Why: even in LAN-only mode, SSRF can target internal services in the same network.
Risk: internal network probing or access to unintended services.
How:
- Reuse `image_proxy` host/IP validation for torrent URL fetch flows.
- Block private/loopback/service DNS unless explicitly allowlisted.
- Add egress allowlist for providers.

## Medium Priority

### 1. Auth should become optional-but-ready (token mode) for external clients
Why: LAN setup can work without mandatory auth, but universal backend should support secure non-browser clients.
How: add API key/JWT mode that can be enabled per profile.

### 2. Observability/admin hardening by profile
Why: `api.insecure`, default Grafana creds, broad published ports are acceptable for quick local bring-up, but risky for long-running LAN nodes.
How: remove defaults in `hardened` profile, require explicit credentials, minimize published ports.

### 3. Search cache policy is underused
Why: frontend always sends `nocache=1`, neutralizing server cache value.
How: use cache-first for normal browsing, `nocache` only for explicit refresh/diagnostics.

### 4. Potential scaling bottleneck in `progress` sorting
Why: `sortBy=progress` loads full set and sorts in memory.
How: persist sortable progress field and let DB sort/paginate.

### 5. WebSocket reconnect lifecycle issue
Why: reconnect can continue after unmount.
How: guard reconnect with mounted/shouldReconnect flag.

### 6. Frontend container runs dev server
Why: for LAN lab this is acceptable, but not ideal for stable node operation.
How: switch to production static build image.

## Low Priority

### 1. Large files reduce maintainability
Refactor oversized handlers/pages into smaller feature modules.

### 2. Frontend test coverage gap
Add unit/integration tests and test script in `frontend/package.json`.

### 3. Query string logging hygiene
Mask/hash sensitive query fragments in request logs.

### 4. Fix BOM-related tooling issue
Remove UTF-8 BOM from `services/torrent-engine/internal/storage/memory/provider.go` to stabilize coverage tooling.

## Architecture Recommendations (for your roadmap)
- Keep `torrent-engine` as runtime/session domain (stateful).
- Keep `torrent-search` as stateless aggregation domain (scale horizontally).
- Add `catalog` as durable read-optimized domain with ingestion jobs.
- Expose a clean client-facing API layer independent from frontend internals.

## Development Suggestions
- Add CI pipeline with: contract diff check, Go tests/coverage, frontend tests, lint.
- Add migration path doc: "LAN-only now -> hardened later".
- Add event hooks for catalog refresh (`item_added`, `provider_changed`, `engine_state_changed`).

## Detailed Implementation Plan
- Full phased task plan moved to `docs/roadmap/implementation-plan.md`.
