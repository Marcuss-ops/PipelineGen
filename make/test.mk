# make/test.mk - thematic include (P2 Manutenibilita, July 2026).
#
# Per AGENTS.md max_lines_per_file: 1000 plus the P2 directive,
# the canonical build chain is split into 7 thematic includes.
# This file holds only the test-bucket targets. Cross-bucket
# dependencies (e.g. verify-artlist-live -> auth-check) resolve
# naturally via Make's recursive target resolution.
# Root Makefile contains include make/*.mk plus all: build.

test: test-unit

# Run ALL tests (Go + JS). Opt-in target — `make test` alone stays Go-only
# to keep the cheap default CI chain (go-version-check + Go test + build)
# independent from Node toolchain state. CI environments that don't have
# Node installed can still run `make test`; `make test-all` adds the JS
# gate for environments where Node 22 is wired.
test-all: test-unit test-js

# Run unit tests with race detector
test-unit:
	GOFLAGS="$(GO_BUILD_GOFLAGS)" $(GO) test -v -race -coverprofile=coverage.out ./internal/... ./pkg/...

# Run JavaScript test suite (node-scraper).
# Equivalent: cd node-scraper && npm install --silent && npm test.
# Uses Node's built-in test runner and installs dependencies on first run.
# Exits non-zero on any failing test — same fail-closed contract as
# test-unit. Gated on node-version-check so a stale host Node aborts
# BEFORE npm install (a 30s install vs an immediate ✓/❌ toggle on the
test-js: node-version-check
	@if [ ! -d node-scraper/node_modules ]; then \
	    echo "→ Installing node-scraper devDependencies..."; \
	    cd node-scraper && npm install --silent; \
	fi
	@echo "→ Running Node test suite on node-scraper/test/*.test.js..."
	cd node-scraper && npm test

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

# verify-go-core — domain and application logic tests. Isolates failures
# in the core business packages so a domain test failure is immediately
# distinguishable from an infrastructure or API failure.
verify-go-core:
	$(GO) test -race ./internal/kernel/... ./internal/capabilities/...

# verify-go-infrastructure — concrete adapter tests. Covers everything
# under internal/infrastructure/ (databases, clients, external SDK wraps).
verify-go-infrastructure:
	$(GO) test -race ./internal/platform/...

# verify-go-api — HTTP/API handler tests. Covers internal/api/... .
verify-go-api:
	$(GO) test -race ./internal/platform/httpserver/...

# verify-go-commands — CLI entrypoints and shared packages. Covers cmd/
# and pkg/, which are not tied to a specific delivery layer.
verify-go-commands:
	$(GO) test -race ./cmd/... ./pkg/...

# verify-go-tests — operational, integration, and E2E tests under tests/.
# Kept separate because some suites require external services or are slower
# than unit tests, so they can be run (or skipped) independently.
verify-go-tests:
	$(GO) test -race ./tests/...

# verify-go — Go static analysis, race-tested unit tests by area, and full
# build. Orchestrates the five per-area test targets above, then runs vet
# and build. Fail-closed: any failing target aborts the chain.
verify-go:
	@$(MAKE) verify-go-core
	@$(MAKE) verify-go-infrastructure
	@$(MAKE) verify-go-api
	@$(MAKE) verify-go-commands
	@$(MAKE) verify-go-tests
	$(GO) vet ./...
	$(GO) build ./...
	@echo "✅ Go verification passed"

# verify-unit — race-tested Go unit tests by area, EXCLUDING the slow
# operational/integration suite under ./tests/.... This is the canonical
# "fast Go" gate for the dev loop. ./tests/... is moved to verify-integration
# so a slow E2E never blocks `make verify-unit`; ops batteries there are
# also free to depend on external services (Drive, Qdrant, scraper).
#
# Composition rule: verify-unit MUST NOT trigger any ./tests/... run. The
# four sub-targets
# are pure unit tests (domain, application, infrastructure, api, commands,
# pkg). Operational tests are owned by verify-integration.
VERIFY_JOBS ?= 2

verify-unit-fast:
	$(GO) test ./internal/kernel/... \
		./internal/capabilities/... \
		./cmd/... \
		./pkg/...

verify-unit: go-version-check
	@$(MAKE) -j$(VERIFY_JOBS) \
		verify-go-core \
		verify-go-infrastructure \
		verify-go-api \
		verify-go-commands
	@echo "✅ Unit verification passed"

# verify-node-native — probe better-sqlite3 by loading it against an
# in-memory database. Designed to surface the "Module did not self-register"
# failure mode in SECONDS rather than minutes: when better-sqlite3's native
# binding is built against the wrong Node ABI (typically after a Node major
# upgrade that runs `npm test` before `npm rebuild`), the very first
# `new Database(':memory:')` throws synchronously, and `node -e` exits
# non-zero — failing this gate fast.
#
# Install-guard semantics (IMPORTANT):
#   - The `[ -d node_modules/better-sqlite3 ]` check is a PERF OPTIMISATION
#     (skip the 30s npm install on subsequent runs), NOT a correctness gate.
#   - The probe IS the correctness gate: it catches stale-ABI cases where
#     the directory exists but the .node binding is built against the wrong
#     Node version (the directory check would pass and the require() would
#     still throw). Do NOT trust a green directory check.
#   - When the probe fails, the fix is `cd node-scraper && npm rebuild
#     better-sqlite3` (or `npm install` for first-time installs).
#
# CAUTION on the recipe below: `node -e '...'` is single-quoted to keep the
# JS as one shell argument. Do NOT introduce literal single quotes inside
# the JS (e.g. for a different probe message) without migrating to a
# heredoc or a dedicated probe script under node-scraper/scripts/.
#
# Runs BEFORE verify-node-tests so a native-binding mismatch fails the chain
# in seconds, BEFORE the slower `npm install` / `npm test` round-trip.
