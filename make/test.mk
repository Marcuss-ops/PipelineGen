# make/test.mk - Go test and verification targets.

test: test-unit

# Run all Go tests; the retired Node scraper suite is no longer part of the project.
test-all: test-unit

test-unit:
	GOFLAGS="$(GO_BUILD_GOFLAGS)" $(GO) test -v -race -coverprofile=coverage.out ./internal/... ./pkg/...

coverage: test-unit
	$(GO) tool cover -func=coverage.out
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

coverage-check: test-unit
	@COVERAGE=$$($(GO) tool cover -func=coverage.out | grep total | awk '{print $$3}' | sed 's/%//'); \
	echo "Total coverage: $$COVERAGE%"; \
	if (( $$(echo "$$COVERAGE < 60" | bc -l) )); then \
		echo "❌ Coverage $$COVERAGE% is below threshold of 60%"; \
		exit 1; \
	fi; \
	echo "✅ Coverage $$COVERAGE% meets threshold of 60%"

lint:
	golangci-lint run --timeout=5m

fmt:
	$(GO) fmt ./...

vet: go-version-check
	$(GO) vet ./...

verify-go-core:
	$(GO) test -race ./internal/kernel/... ./internal/capabilities/...

verify-go-infrastructure:
	$(GO) test -race ./internal/platform/...

verify-go-api:
	$(GO) test -race ./internal/platform/httpserver/...

verify-go-commands:
	$(GO) test -race ./cmd/... ./pkg/...

verify-go-tests:
	$(GO) test -race ./tests/...

verify-go:
	@$(MAKE) verify-go-core
	@$(MAKE) verify-go-infrastructure
	@$(MAKE) verify-go-api
	@$(MAKE) verify-go-commands
	@$(MAKE) verify-go-tests
	$(GO) vet ./...
	$(GO) build ./...
	@echo "✅ Go verification passed"

verify-unit-fast:
	$(GO) test ./internal/kernel/... ./internal/capabilities/... ./cmd/... ./pkg/...

VERIFY_JOBS ?= 2
verify-unit: go-version-check
	@$(MAKE) -j$(VERIFY_JOBS) verify-go-core verify-go-infrastructure verify-go-api verify-go-commands
	@echo "✅ Unit verification passed"
