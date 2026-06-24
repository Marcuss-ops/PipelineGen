.PHONY: all build test test-unit coverage coverage-check clean lint fmt vet run doctor artlist dev google-accounting-run comic-video-maker-run deps tidy-check vuln bench docker-build docker-run ci rebuild go-version-check go-version-guard

# Version information (can be overridden via environment)
# Use: make build VERSION=1.2.0
VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS  = -X main.buildVersion=$(VERSION) -X main.commitHash=$(COMMIT)

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
	HOST=$$(go version | awk '{print $$3}' | sed 's/^go//'); \
	REQ_MIN=$$(echo "$$REQ" | awk -F. '{print $$1"."$$2}'); \
	HOST_MIN=$$(echo "$$HOST" | awk -F. '{print $$1"."$$2}'); \
	if [ "$$(printf '%s\n%s\n' "$$REQ_MIN" "$$HOST_MIN" | sort -V | head -n1)" = "$$HOST_MIN" ] && [ "$$REQ_MIN" != "$$HOST_MIN" ]; then \
	    echo "❌ Go version mismatch: go.mod requires $$REQ, host has $$HOST"; \
	    echo "Remediation options:"; \
	    echo "  1. Re-run with GO=/home/pierone/.local/go/bin/go make <target>"; \
	    echo "  2. Install the required toolchain: \`go install golang.org/dl/go$$REQ@latest && go$$REQ download\`"; \
	    echo "  3. Update the `go ` directive in go.mod if the requirement changed"; \
	    exit 1; \
	elif [ "$$(printf '%s\n%s\n' "$$REQ" "$$HOST" | sort -V | head -n1)" = "$$HOST" ] && [ "$$REQ" != "$$HOST" ]; then \
	    echo "⚠️  Host Go ($$HOST) patch is older than go.mod ($$REQ) — still compatible, but consider upgrading."; \
	else \
	    echo "✅ Go version $$HOST meets requirement $$REQ"; \
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
	go build -ldflags "$(LDFLAGS)" -v -o bin/pipelinegen ./cmd/server
	go build -ldflags "$(LDFLAGS)" -v -o bin/admin      ./cmd/admin
	go build -ldflags "$(LDFLAGS)" -v -o bin/worker     ./cmd/worker

# Run all tests
test: test-unit

# Run unit tests with race detector
test-unit:
	go test -v -race -coverprofile=coverage.out ./internal/... ./pkg/...

# Generate coverage report
coverage: test-unit
	go tool cover -func=coverage.out
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Check coverage threshold (60%)
coverage-check: test-unit
	@COVERAGE=$$(go tool cover -func=coverage.out | grep total | awk '{print $$3}' | sed 's/%//'); \
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
	go fmt ./...

# Run go vet
vet: go-version-check
	go vet ./...

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
# Port is read from $VELOX_PORT (default 8080); see
# internal/infrastructure/config/types.go `Server.Port` for the in-tree
# default. Tokens are read from $VELOX_ADMIN_TOKEN / $VELOX_WORKER_TOKEN
# (mandatory — the binary refuses to boot with placeholder tokens).
run: build
	./bin/pipelinegen --mode all

# Run system doctor check. The port is read from $VELOX_PORT (default
# 8080) so the same Makefile works whether the operator runs on the
# canonical port or overrides at runtime. Fails explicitly if the
# server is not running.
doctor:
	@curl -sS -f http://127.0.0.1:$${VELOX_PORT:-8080}/api/system/doctor | jq . || { echo "Server not running? Try: make run (override port via VELOX_PORT)"; exit 1; }

# Run artlist with smart presets
# Usage: make artlist TERM=technology LIMIT=10 PRESET=youtube_1080p_7s
# Port is read from $VELOX_PORT (default 8080).
TERM ?= technology
LIMIT ?= 10
PRESET ?= youtube_1080p_7s
artlist:
	@curl -sS -f -X POST http://127.0.0.1:$${VELOX_PORT:-8080}/api/artlist/run-smart \
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
	go mod download

# Check if go.mod is tidy (useful in CI)
tidy-check: go-version-check
	go mod tidy
	git diff --exit-code -- go.mod go.sum

# Check for vulnerabilities
vuln:
	govulncheck ./...

# Run benchmarks
bench:
	go test -bench=. -benchmem ./...

# Docker build (requires Dockerfile) — image name is `pipelinegen:latest`
# per the Operational Readiness PR. Image listens on 8080 by default.
docker-build:
	@test -f Dockerfile || { echo "❌ Dockerfile not found"; exit 1; }
	docker build -t pipelinegen:latest .

# Docker run: maps the canonical VELOX_PORT (default 8080) host port
# onto the container's 8080 listener. Use VELOX_PORT=NNNN to override.
docker-run: docker-build
	docker run -p $${VELOX_PORT:-8080}:8080 --env-file .env pipelinegen:latest

# CI pipeline (runs all checks)
ci: go-version-check fmt vet tidy-check lint test coverage-check build
	@echo "✅ All CI checks passed!"