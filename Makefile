.PHONY: all build test test-unit test-integration coverage clean lint fmt vet swagger

# Default target
all: build

# Build the server
build:
	go build -v ./cmd/server

# Run all tests
test: test-unit test-integration

# Run unit tests only
test-unit:
	go test -v -race -coverprofile=coverage.out ./internal/... ./pkg/...

# Run integration tests only
test-integration:
	go test -v ./tests/integration/...

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

# Development mode with hot reload (requires air)
dev:
	air

# Install dependencies
deps:
	go mod download
	go mod tidy

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