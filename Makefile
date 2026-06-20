.PHONY: all build test test-unit coverage coverage-check clean lint fmt vet swagger run doctor artlist dev google-accounting-run comic-video-maker-run deps tidy-check vuln bench docker-build docker-run ci

# Version information (can be overridden via environment)
# Use: make build VERSION=1.2.0
VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS  = -X main.buildVersion=$(VERSION) -X main.commitHash=$(COMMIT)

# Default target
all: build

# Build the entry-point binaries. Outputs land in ./bin/ to keep the project
# root clean (see `make clean`). The admin CLI is the canonical orchestrator
# entry — it exposes subcommands for the server, worker, and admin flows
# (see cmd/admin/main.go for the available modes). The worker binary covers
# the long-running pipeline worker process.
build:
	@mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -v -o bin/admin ./cmd/admin
	go build -ldflags "$(LDFLAGS)" -v -o bin/worker ./cmd/worker

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
vet:
	go vet ./...

# Generate Swagger docs
swagger:
	swag init -g cmd/server/main.go

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

# Run the server
run: build
	./server

# Run system doctor check
# Fails explicitly if the server is not running.
doctor:
	@curl -sS -f http://127.0.0.1:18080/api/system/doctor | jq . || { echo "Server not running? Try: make run"; exit 1; }

# Run artlist with smart presets
# Usage: make artlist TERM=technology LIMIT=10 PRESET=youtube_1080p_7s
TERM ?= technology
LIMIT ?= 10
PRESET ?= youtube_1080p_7s
artlist:
	@curl -sS -f -X POST http://127.0.0.1:18080/api/artlist/run-smart \
		-H "Content-Type: application/json" \
		-d '{"term":"$(TERM)","limit":$(LIMIT),"preset":"$(PRESET)"}' | jq . || { echo "Server not running? Try: make run"; exit 1; }

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
tidy-check:
	go mod tidy
	git diff --exit-code -- go.mod go.sum

# Check for vulnerabilities
vuln:
	govulncheck ./...

# Run benchmarks
bench:
	go test -bench=. -benchmem ./...

# Docker build (requires Dockerfile)
docker-build:
	@test -f Dockerfile || { echo "❌ Dockerfile not found"; exit 1; }
	docker build -t velox-go-master:latest .

# Docker run (requires docker-build)
docker-run: docker-build
	docker run -p 18080:18080 velox-go-master:latest

# CI pipeline (runs all checks)
ci: fmt vet tidy-check lint test coverage-check build
	@echo "✅ All CI checks passed!"