.PHONY: test test-engine test-search test-frontend lint build dev docker-up docker-down clean

# ── Tests ────────────────────────────────────────────────────────────
test: test-engine test-search test-frontend

test-engine:
	cd services/torrent-engine && go test ./... -race

test-search:
	cd services/torrent-search && go test ./... -race

test-frontend:
	cd frontend && npx tsc --noEmit
	cd frontend && npx vitest run

# ── Lint ─────────────────────────────────────────────────────────────
lint: lint-go lint-frontend

lint-go:
	@echo "── Go (torrent-engine) ──"
	cd services/torrent-engine && go vet ./...
	@echo "── Go (torrent-search) ──"
	cd services/torrent-search && go vet ./...

lint-frontend:
	cd frontend && npm run lint

# ── Build ────────────────────────────────────────────────────────────
build: build-engine build-search build-frontend

build-engine:
	cd services/torrent-engine && go build -o bin/torrentstream ./cmd/server

build-search:
	cd services/torrent-search && go build -o bin/torrent-search ./cmd/server

build-frontend:
	cd frontend && npm run build

# ── Dev ──────────────────────────────────────────────────────────────
dev:
	cd frontend && npm run dev

# ── Docker ───────────────────────────────────────────────────────────
docker-up:
	docker compose -f deploy/docker-compose.yml up --build -d

docker-down:
	docker compose -f deploy/docker-compose.yml down

docker-logs:
	docker compose -f deploy/docker-compose.yml logs -f

# ── Clean ────────────────────────────────────────────────────────────
clean:
	rm -rf services/torrent-engine/bin services/torrent-search/bin frontend/dist
