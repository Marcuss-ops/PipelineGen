.PHONY: all build test test-unit coverage coverage-check clean lint fmt vet run doctor artlist dev google-accounting-run comic-video-maker-run deps tidy-check vuln bench docker-build docker-run ci rebuild go-version-check go-version-guard preflight node-version-check

# Version information (can be overridden via environment)
# Use: make build VERSION=1.2.0
VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS  = -X main.buildVersion=$(VERSION) -X main.commitHash=$(COMMIT)

# Go binary (overridable from the environment).
# Use: make build GO=/opt/go-1.25/bin/go
GO ?= go

# Default target
all: build

# Pre-flight: ensure the host Go toolchain matches go.mod's `go` directive.
# CPR-CC-7 (June 2026): until this target landed, a stale host go (1.19 vs
# a 1.25 directive) silently produced confusing `go mod tidy` output during
# dep-refresh cycles and only surfaced the mismatch in CI. Wire this in as a
# precondition of any target that invokes the Go compiler.
go-version-guard: go-version-check

go-version-check:
	@REQ=$$(awk '/^go [0-9]/ {print $$2}' go.mod); \
	HOST=$$($(GO) version | awk '{print $$3}' | sed 's/^go//'); \
	REQ_MIN=$$(echo "$$REQ" | awk -F. '{print $$1"."$$2}'); \
	HOST_MIN=$$(echo "$$HOST" | awk -F. '{print $$1"."$$2}'); \
	if [ "$$(printf '%s\n%s\n' "$$REQ_MIN" "$$HOST_MIN" | sort -V | head -n1)" = "$$HOST_MIN" ] && [ "$$REQ_MIN" != "$$HOST_MIN" ]; then \
	    echo "❌ Go version mismatch: go.mod requires $$REQ, host has $$HOST"; \
	    echo "Remediation options:"; \
	    echo "  1. Re-run with: make build GO=/path/to/required-go (or set GO in your shell rc)"; \
	    echo "  2. Install the required toolchain: go install golang.org/dl/go$$REQ@latest && go$$REQ download"; \
	    echo "  3. Update the 'go' directive in go.mod if the requirement changed"; \
	    exit 1; \
	elif [ "$$(printf '%s\n%s\n' "$$REQ" "$$HOST" | sort -V | head -n1)" = "$$HOST" ] && [ "$$REQ" != "$$HOST" ]; then \
	    echo "⚠️  Host Go ($$HOST) patch is older than go.mod ($$REQ) — still compatible, but consider upgrading."; \
	else \
	    echo "✅ Go version $$HOST meets requirement $$REQ"; \
	fi

# Node toolchain guard. The single source of truth is the
# `engines.node` field in node-scraper/package.json (parsing that via
# `node -p require(...)` keeps the contract in code, not in comments),
# and the host version comes from `node --version`. Mirrors the Go
# guard so CI and Dependabot npm updates cannot silently drift from
# the runtime the operators actually execute.
node-version-check:
	@if ! command -v node >/dev/null 2>&1; then \
	    echo "❌ Node binary not found on PATH — install Node 22.x (nvm, fnm, asdf, or distro packages)"; \
	    exit 1; \
	fi; \
	REQ_RAW=$$(node -p "require('./node-scraper/package.json').engines.node" 2>/dev/null); \
	HOST=$$(node --version 2>/dev/null | sed 's/^v//'); \
	if [ -z "$$REQ_RAW" ]; then \
	    echo "❌ node-scraper/package.json has no 'engines.node' field — set e.g. \"engines\": { \"node\": \"22.x\" }"; \
	    exit 1; \
	fi; \
	# Anchored numeric extraction so operators like ^, >=, ranges, or
	# pre-release tags degrade into a clear "unsupported format" error
	# instead of a silent numeric-vs-string mismatch.
	REQ_MAJOR=$$(echo "$$REQ_RAW" | sed -E 's/^[^0-9]*([0-9]+).*/\1/'); \
	HOST_MAJOR=$$(echo "$$HOST" | cut -d. -f1); \
	if ! echo "$$REQ_MAJOR" | grep -qE '^[0-9]+$$'; then \
	    echo "❌ Unsupported engines.node format: '$$REQ_RAW' — must reduce to a major version (e.g. '22', '22.x', '^22', '>=22.0.0')"; \
	    exit 1; \
	fi; \
	if [ "$$HOST_MAJOR" != "$$REQ_MAJOR" ]; then \
	    echo "❌ Node version mismatch: node-scraper/package.json engines.node requires $$REQ_RAW, host has $$HOST"; \
	    echo "Remediation options:"; \
	    echo "  1. Install Node $$REQ_MAJOR.x (nvm, fnm, asdf, or distro packages)"; \
	    echo "  2. Update the 'engines.node' field in node-scraper/package.json if the requirement changed"; \
	    exit 1; \
	else \
	    echo "✅ Node version $$HOST meets requirement $$REQ_RAW"; \
	fi

# Build the entry-point binaries. Outputs land in ./bin/ to keep the project
# root clean (see `make clean`).
#
# Three canonical binaries (Operational Readiness PR, June 2026):
#   - bin/pipelinegen : HTTP server (cmd/server) — runs by default with
#                       `--mode all` so a single boot covers HTTP +
#                       worker + scheduler + maintenance sweepers.
#   - bin/admin       : one-shot CLI (cmd/admin) — subcommands include
#                       gen-api-docs, seed-channels, etc.
#   - bin/worker      : cross-host worker (cmd/worker) — registers
#                       against an HTTP broker via VELOX_BROKER_URL for
#                       users running the long-running worker on a
#                       separate host from the server.
build: go-version-check
	@mkdir -p bin
	$(GO) build -ldflags "$(LDFLAGS)" -v -o bin/pipelinegen ./cmd/server
	$(GO) build -ldflags "$(LDFLAGS)" -v -o bin/admin      ./cmd/admin
	$(GO) build -ldflags "$(LDFLAGS)" -v -o bin/worker     ./cmd/worker

# Run all tests
test: test-unit

# Run unit tests with race detector
test-unit:
	$(GO) test -v -race -coverprofile=coverage.out ./internal/... ./pkg/...

# Generate coverage report
coverage: test-unit
	$(GO) tool cover -func=coverage.out
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Check coverage threshold (60%)
coverage-check: test-unit
	@COVERAGE=$$($(GO) tool cover -func=coverage.out | grep total | awk '{print $$3}' | sed 's/%//'); \
	echo "Total coverage: $$COVERAGE%"; \
	if (( $$(echo "$$COVERAGE < 60" | bc -l) )); then \
		echo "❌ Coverage $$COVERAGE% is below threshold of 60%"; \
		exit 1; \
	fi; \
	echo "✅ Coverage $$COVERAGE% meets threshold of 60%"

# Run linter
lint:
	golangci-lint run --timeout=5m

# Format code
fmt:
	$(GO) fmt ./...

# Run go vet
vet: go-version-check
	$(GO) vet ./...

# Clean build artifacts on the project root.
# Covers primary Linux/macOS binaries, cross-compiled Windows .exe, test
# binaries, stale backups, the accidental `nul` sink, coverage reports, and
# the scratch tmp/ dir.
# NOTE: Local secrets (credentials.json, token.json, token_full.json) are
# intentionally NOT removed — they are user-owned runtime data, not build
# artifacts. Move them to a secure backup if you really must drop them.
clean:
	rm -f server admin pipelinegen
	rm -f server.exe admin.exe pipelinegen.exe worker.exe
	rm -f *.test.exe
	rm -f nul
	rm -f *.bak
	rm -f coverage.out coverage.html
	rm -rf tmp/

# Rebuild: clean + build in one shot.
# Use after switching branches, pulling new code, or whenever the prior
# build output is suspected to be stale. Idempotent and reproducible:
# `make rebuild` is exactly equivalent to `make clean && make build`,
# so the cleanup-to-rebuild sequence is reproducible from a single command
# and works in CI as well as locally.
rebuild: clean build
	@echo "Rebuild complete: ./bin/admin and ./bin/worker"

# Run the server (HTTP + scheduler + maintenance in-process via --mode all).
# Port is read from $VELOX_PORT (canonical default 8000); see
# internal/infrastructure/config/types.go `Server.Port` for the in-tree
# default. Tokens are read from $VELOX_ADMIN_TOKEN / $VELOX_WORKER_TOKEN
# (mandatory — the binary refuses to boot with placeholder tokens).
run: build
	./bin/pipelinegen --mode all

# Run system doctor check. The port is read from $VELOX_PORT (canonical
# default 8000) so the same Makefile works whether the operator runs on the
# canonical port or overrides at runtime. Fails explicitly if the
# server is not running.
doctor:
	@curl -sS -f http://127.0.0.1:$${VELOX_PORT:-8000}/api/system/doctor | jq . || { echo "Server not running? Try: make run (override port via VELOX_PORT)"; exit 1; }

# Run artlist with smart presets
# Usage: make artlist TERM=technology LIMIT=10 PRESET=youtube_1080p_7s
# Port is read from $VELOX_PORT (canonical default 8000).
TERM ?= technology
LIMIT ?= 10
PRESET ?= youtube_1080p_7s
artlist:
	@curl -sS -f -X POST http://127.0.0.1:$${VELOX_PORT:-8000}/api/artlist/run-smart \
		-H "Content-Type: application/json" \
		-d '{"term":"$(TERM)","limit":$(LIMIT),"preset":"$(PRESET)"}' | jq . || { echo "Server not running? Try: make run (override port via VELOX_PORT)"; exit 1; }

# Development mode with hot reload (requires air)
dev:
	air

# Run Google Accounting service
# Usage: make google-accounting-run
google-accounting-run:
	cd google-accounting && uvicorn main:app --reload --port 8000

# Run Comic Video Maker service
# Usage: make comic-video-maker-run
comic-video-maker-run:
	cd comic-video-maker && npm run dev

# Install dependencies (download only, no go.mod modification)
deps:
	$(GO) mod download

# Check if go.mod is tidy (useful in CI)
tidy-check: go-version-check
	$(GO) mod tidy
	git diff --exit-code -- go.mod go.sum

# Check for vulnerabilities
vuln:
	govulncheck ./...

# Run benchmarks
bench:
	$(GO) test -bench=. -benchmem ./...

# Docker build (requires Dockerfile) — image name is `pipelinegen:latest`
# per the Operational Readiness PR. Image listens on 8000 by default.
docker-build:
	@test -f Dockerfile || { echo "❌ Dockerfile not found"; exit 1; }
	docker build -t pipelinegen:latest .

# Docker run: maps the canonical VELOX_PORT (default 8000) host port
# onto the container's 8000 listener. Use VELOX_PORT=NNNN to override.
docker-run: docker-build
	docker run -p $${VELOX_PORT:-8000}:8000 --env-file .env pipelinegen:latest

# CI pipeline (runs all checks)
ci: go-version-check fmt vet tidy-check lint test coverage-check build
	@echo "✅ All CI checks passed!"

# Aggregate pre-flight check. Runs both toolchain guards plus two
# sanity contracts that have historically drifted silently:
#   - docker-compose.yml resolves against the current Dockerfile
#   - the Dockerfile builder image tag tracks go.mod's `go` line
# Read-only: no lockfile modification, no upgrade, no commit.
preflight: go-version-check node-version-check
	@if command -v docker >/dev/null 2>&1; then \
		docker compose config >/dev/null; \
	else \
		echo "warning: 'docker compose config' skipped — docker not on PATH"; \
	fi
	@grep -q '^FROM .*golang:1.25' Dockerfile
	@echo "Preflight passed"
