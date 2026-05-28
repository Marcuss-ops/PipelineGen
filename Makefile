.PHONY: all build test test-unit test-integration coverage clean lint fmt vet swagger

# Version information (can be overridden via environment)
# Use: make build VERSION=1.2.0
VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS  = -X main.buildVersion=$(VERSION) -X main.commitHash=$(COMMIT)

# Default target
all: build

# Build the server with version info
build:
	go build -ldflags "$(LDFLAGS)" -v ./cmd/server

# Run all tests
test: test-unit

# Run unit tests only
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

# Clean build artifacts
clean:
	rm -f server
	rm -f coverage.out coverage.html
	rm -rf tmp/

# Run the server
run: build
	./server

# Run system doctor check
doctor:
	@curl -s http://127.0.0.1:8080/api/system/doctor | jq . || echo "Server not running? Try: make run"

# Run artlist with smart presets
# Usage: make artlist TERM=technology LIMIT=10 PRESET=youtube_1080p_7s
TERM ?= technology
LIMIT ?= 10
PRESET ?= youtube_1080p_7s
artlist:
	@curl -s -X POST http://127.0.0.1:8080/api/artlist/run-smart \
		-H "Content-Type: application/json" \
		-d '{"term":"$(TERM)","limit":$(LIMIT),"preset":"$(PRESET)"}' | jq . || echo "Server not running? Try: make run"

# Run workflow content package
# Usage: make workflow TITLE="10 shocking moments in WWE history"
TITLE ?= "10 shocking moments"
workflow:
	@curl -s -X POST http://127.0.0.1:8080/api/workflows/content-package \
		-H "Content-Type: application/json" \
		-d '{"title":"$(TITLE)","style":"news","assets":"artlist","output":"google_doc"}' | jq . || echo "Server not running? Try: make run"

# Development mode with hot reload (requires air)
dev:
	air

# Run Google Accounting service
# Usage: make google-accounting-run
google-accounting-run:
	cd google-accounting && uvicorn main:app --reload --port 8000

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

# Docker build
docker-build:
	docker build -t velox-go-master:latest .

# Docker run
docker-run:
	docker run -p 8080:8080 velox-go-master:latest

# CI pipeline (runs all checks)
ci: fmt vet lint test coverage-check build
	@echo "✅ All CI checks passed!"