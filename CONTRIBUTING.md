# Contributing to T◎RRX

Thank you for your interest in contributing! This guide will help you get started.

## Development Setup

### Prerequisites

- Go 1.25+
- Node.js 20+
- Docker and Docker Compose
- FFmpeg (for torrent-engine local development)

### Local Development

```bash
# Start infrastructure (MongoDB, Redis, Traefik, etc.)
docker compose -f deploy/docker-compose.yml up -d mongo redis traefik jaeger

# Run torrent-engine
cd services/torrent-engine
go run ./cmd/server

# Run torrent-search
cd services/torrent-search
go run ./cmd/server

# Run frontend dev server
cd frontend
npm install
npm run dev
```

### Using Makefile

```bash
make test          # Run all tests
make lint          # Run linters
make docker-up     # Start full stack
make docker-down   # Stop full stack
```

## Code Style

### Go

- Follow standard Go conventions (`gofmt`, `goimports`).
- Hexagonal architecture in `torrent-engine`: domain has no external imports, use cases accept port interfaces.
- Table-driven tests, co-located with source (`*_test.go`).
- Use `log/slog` for structured logging.

### Frontend (React + TypeScript)

- TypeScript strict mode enabled.
- Tailwind-first styling, use `cn()` from `src/lib/cn.ts` for conditional classes.
- UI components in `src/components/ui/` extend Radix UI primitives.
- Pages in `src/pages/`.

## Branch Strategy

1. Create a feature branch from `main`: `git checkout -b feature/your-feature`
2. Make your changes with clear, focused commits.
3. Ensure all tests pass: `make test`
4. Push and open a Pull Request against `main`.

## Pull Request Guidelines

- Keep PRs focused — one feature or fix per PR.
- Include a clear description of what changed and why.
- Add or update tests for your changes.
- Ensure CI passes before requesting review.
- Reference related issues (e.g., `Closes #123`).

## Reporting Issues

- Use [GitHub Issues](https://github.com/starsinc1708/TorrX/issues) with the provided templates.
- For security vulnerabilities, see [SECURITY.md](SECURITY.md).

## Architecture Overview

See [CLAUDE.md](CLAUDE.md) for detailed architecture documentation, or the service-specific docs:

- [Torrent Engine Architecture](services/torrent-engine/docs/architecture.md)
- [Torrent Engine API](services/torrent-engine/docs/api.md)
- [Torrent Search README](services/torrent-search/README.md)

## License

By contributing, you agree that your contributions will be licensed under the [GPL-3.0 License](LICENSE).
