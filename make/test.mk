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
	$(GO) test -race ./internal/kernel/... ./internal/capabilities/... ./internal/app/...

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

# verify-unit-fast — the fast non-race subset consumed by verify-push, and
# therefore by the pre-push hook and CI (`.github/workflows/ci.yml` runs
# verify-main). Kept in sync with verify-go-core + verify-go-commands, which
# today means ./internal/app/... is included: the composition root's tests
# must run in the gate that actually blocks a push.
verify-unit-fast:
	$(GO) test ./internal/kernel/... ./internal/capabilities/... ./internal/app/... ./cmd/... ./pkg/...

# bench-cliprender — guarded entry point for the clip.render throughput suite.
# The former synthetic scenario harness has been removed. This target refuses
# to report a successful benchmark when its selector matches zero tests; restore
# the benchmark suite before claiming current throughput evidence.
#
#   make bench-cliprender                       default scenario selector
#   make bench-cliprender BENCH_CLIPRENDER_RUN=TestName  explicit test selector
#
# VELOX_BENCH_WRITE_REPORT is passed to the matching test when the suite exists.
BENCH_CLIPRENDER_RUN ?= TestScenario
bench-cliprender:
	@set -eu; \
		tests=$$($(GO) test ./internal/capabilities/cliprender/ -list '$(BENCH_CLIPRENDER_RUN)'); \
		if ! printf '%s\n' "$$tests" | grep -Eq '$(BENCH_CLIPRENDER_RUN)'; then \
			echo "ERROR: no cliprender benchmark tests match '$(BENCH_CLIPRENDER_RUN)'; the canonical scenario harness is missing or the selector is stale" >&2; \
			exit 1; \
		fi; \
		VELOX_BENCH_WRITE_REPORT=$(VELOX_BENCH_WRITE_REPORT) \
			$(GO) test ./internal/capabilities/cliprender/ -run '$(BENCH_CLIPRENDER_RUN)' -count=1 -v

VERIFY_JOBS ?= 2
verify-unit: go-version-check
	@$(MAKE) -j$(VERIFY_JOBS) verify-go-core verify-go-infrastructure verify-go-api verify-go-commands
	@echo "✅ Unit verification passed"
