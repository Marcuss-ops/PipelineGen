# make/verify.mk - thematic include (P2 Manutenibilita, July 2026).
#
# Per AGENTS.md max_lines_per_file: 1000 plus the P2 directive,
# the canonical build chain is split into 7 thematic includes.
# This file holds only the verify-bucket targets. Cross-bucket
# dependencies (e.g. verify-artlist-live -> auth-check) resolve
# naturally via Make's recursive target resolution.
# Root Makefile contains include make/*.mk plus all: build.

verify-no-secrets:
	@bash scripts/ci/ci-no-secrets-audit.sh

# verify-base — fail-closed base gate: toolchain version, secrets,
# formatting, and module tidiness. Kept cheap so the most common failures
# surface in seconds. GO-ONLY (no node-version-check); use verify-foundation
# below for the Node-aware chain. NOTE: verify-base and verify-foundation
# share 4 of 5 prereqs by design (the "non sostitutivi" constraint of the
# refactor). When adding/removing a prereq here, mirror it in
verify-base: go-version-check verify-no-secrets verify-format tidy-check
	@echo "✅ Base verification passed"

# verify-foundation — cheapest pre-flight gate: toolchain versions (Go +
# Node), secrets, formatting, module tidiness, AND hook syntax. Runs in
# seconds. It is the Node-aware foundation used by verify-fast and the
# pre-push chain; verify-base remains the Go-only foundation for callers
# that deliberately do not require Node.
#
# bash -n lint on the canonical hooks (scripts/hooks/pre-push +
# scripts/hooks/pre-commit): catches a syntactic break in any hook
# BEFORE the pre-push gate can be invoked, mirroring the
# go-build/go-vet pre-flight pattern ("validate the gate itself
# before letting the gate run"). Cheap (<100ms) and fail-fast: a
# red bash -n short-circuits the rest of the chain and gets surfaced
# to the operator while still in the dev loop. Replaces the
# fragile-yet-permissive default of "hook is invoked once per
# `git push`; a syntax error there is opaque to the dev".
#
# NOTE: verify-base and verify-foundation share 4 of 5 prereqs by design
# (the "non sostitutivi" constraint of the refactor). When adding/removing
# a prereq here, mirror it in verify-base above to prevent drift between
verify-foundation: go-version-check node-version-check verify-no-secrets verify-format tidy-check
	@bash -n scripts/hooks/pre-push scripts/hooks/pre-commit
	@echo "✅ Foundation verification passed"

# verify-static — Go static analysis + full build. No tests, no
# integration, no Node. Catches vet regressions and compile errors in
# <1min on a warm cache. Complementary to verify-go (which adds the
# per-area race-tested suites); use it as a fail-fast smoke between full
# builds. go-version-check is a hard prereq so a stale host Go fails
# fast with the canonical version mismatch message rather than late and
# opaquely inside `go vet` / `go build`.
verify-static: go-version-check
	$(GO) vet ./...
	$(GO) build ./...
	@echo "✅ Static verification passed"

# verify-fast — dev-loop gate: foundation + static. Runs in <1min on a
# clean tree and is the cheapest fail-closed chain that catches the
# most common errors (toolchain mismatch, leaked secrets, formatting
# drift, vet/build break). Used during active development. verify-main adds
# standard Go tests, the native Node probe, and architecture checks;
# verify-full and verify-release add the heavier race, Node, and integration
# gates.
verify-fast: verify-foundation verify-static
	@echo "✅ verify-fast passed"

verify-dev: verify-foundation verify-static
	@echo "✅ verify-dev passed"

verify-changed:
	@GO="$(GO)" bash scripts/ci/verify-changed.sh

# verify-push — daily foundation/static/unit gate plus registry-driven
# verification of only the components impacted by the current Git changes.
# Component targets never depend on verify-fast; foundation is a direct shared
# prerequisite and GNU Make executes it once per aggregate invocation.
verify-push: verify-foundation verify-static verify-unit-fast verify-changed-components
	@echo "✅ verify-push passed"

# Explicit race unit gate. Keep the race flag visible in the dry-run plan so
# the verify-split contract can prove this gate is not an alias for fast unit
# tests. The registry component race suite is owned by verify-race-components.
verify-unit-race: go-version-check
	$(GO) test -race ./internal/... ./cmd/... ./pkg/...

# verify-main — canonical daily fail-closed headless gate. It composes the
# push gate, the native Node probe, and architecture checks. GNU Make
# de-duplicates verify-foundation/verify-static inherited through verify-push.
verify-main: verify-push verify-node-native verify-architecture
	@echo "✅ verify-main passed"

# verify-race — explicit race-detector gate. Foundation runs as a shared
# prerequisite once, while unit and registry component suites use their
# race-enabled commands. It is independent of verify-main for direct use.
verify-race: verify-foundation verify-unit-race verify-race-components
	@echo "✅ verify-race passed"

# verify-full — complete headless gate: verify-main, the explicit race gate,
# and the full Node test suite. Shared prerequisites such as foundation are
# deduplicated by GNU Make within this aggregate invocation.
verify-full: verify-main verify-race verify-node-tests
	@echo "✅ verify-full passed"

# verify-go-core — domain and application logic tests. Isolates failures
# in the core business packages so a domain test failure is immediately
verify-node-native:
	@if [ ! -d node-scraper/node_modules/better-sqlite3 ]; then \
	    echo "→ Installing node-scraper devDependencies (better-sqlite3 native build)..."; \
	    cd node-scraper && npm install --silent; \
	fi
	@echo "→ Probing better-sqlite3 native binding (catches 'Module did not self-register')..."
	@cd node-scraper && node -e 'const Database = require("better-sqlite3"); const db = new Database(":memory:"); db.exec("CREATE TABLE probe(id INTEGER)"); db.close(); console.log("✅ better-sqlite3 loaded");'
	@echo "✅ verify-node-native passed"

# verify-node-tests — Node test runner over node-scraper/test/*.test.js.
# Thin alias of `make test-js`: same install guard, same node-version-check,
# same npm test invocation. Kept separate from verify-node-native so the
# native-binding probe can fail fast without paying the npm-install cost
# and so verify-node-native can be run in isolation during Node upgrades.
verify-node-tests: test-js
	@echo "✅ verify-node-tests passed"

# verify-node — complete Node toolchain gate. Composes the fast native
# binding probe and the full Node test suite. Node verification is explicit;
# verify-main delegates only to changed registry components.
verify-node: verify-node-native verify-node-tests
	@echo "✅ Node verification passed"

# verify-integration — operational, integration, and E2E tests under
# ./tests/... . Kept ISOLATED from verify-unit and verify-node because:
#   (a) some suites require external services (Drive, Qdrant, scraper) and
verify-integration: go-version-check
	@$(MAKE) verify-go-tests
	@echo "✅ Integration verification passed"

# verify-architecture — governance and architecture checks. Kept separate
# so architecture drift surfaces under its own target.
verify-architecture:
	$(GO) run ./cmd/architecture-aggregate --dry-run && \
	$(GO) run ./cmd/archcheck
	@echo "✅ Architecture verification passed"

# verify-main-stock — compatibility alias for the registry-backed Stock gate.
verify-main-stock: verify-foundation verify-static verify-architecture verify-stock
	@echo "✅ verify-main-stock passed"

# verify-main-clip — compatibility alias for the registry-backed Clips gate.
verify-main-clip: verify-foundation verify-static verify-architecture verify-clips
	@echo "✅ verify-main-clip passed"

# verify-release — pre-deploy gate: the complete headless gate plus the
# slow ./tests/... integration suite, which may depend on external services
# (Drive, Qdrant, scraper). Run before deploy, NOT on every routine push.
verify-release: verify-full verify-integration
	@echo "✅ Release verification passed"

# verify-split — structural gate for the verification graph. This is
# intentionally independent from application test results: it checks that
# each cost tier keeps its contract and shared prerequisites are not repeated.
verify-split:
	@bash scripts/ci/verify-split-contract.sh

# Aggregate pre-flight check. Runs both toolchain guards plus two
# sanity contracts that have historically drifted silently:
#   - docker-compose.yml resolves against the current Dockerfile
#   - the Dockerfile builder image tag tracks go.mod's `go` line
# Read-only: no lockfile modification, no upgrade, no commit.
regen-routes-yaml:
	@TMP=$$(mktemp architecture/routes.yaml.tmp.XXXXXX) || { echo "❌ regen-routes-yaml: mktemp failed (architecture/ missing or read-only?)" >&2; exit 1; }; \
	if [ ! -f scripts/admin/generate_routes_yaml.go ]; then \
	    rm -f "$$TMP"; \
	    echo "❌ scripts/admin/generate_routes_yaml.go not present — current hand-routes.yaml is authoritative until Push 7(a) lands." >&2; \
	    exit 1; \
	fi; \
	if ! $(GO) run ./scripts/admin --output="$$TMP" 2>"$$TMP.stderr"; then \
	    echo "❌ regen-routes-yaml: AST analyzer failed; architecture/routes.yaml untouched." >&2; \
	    cat "$$TMP.stderr" >&2; \
	    rm -f "$$TMP" "$$TMP.stderr"; \
	    exit 1; \
	fi; \
	rm -f "$$TMP.stderr"; \
	mv "$$TMP" architecture/routes.yaml; \
	echo "✅ regen-routes-yaml wrote architecture/routes.yaml"

# archcheck-strict — invokes go run ./cmd/archcheck --strict which is
# the gate-promoted Phase-0 governance check. Used by CI + locally as
# the failure-mode baseline for promote-to-enforce-zero ratchets.
# Mirrors scripts/ci-architectural-checks.sh with the --strict flag
# applied (any violation = non-zero exit).
archcheck-strict:
	@$(GO) run ./cmd/archcheck --strict

# ─── Sidecar Node scraper (PR-LIVE-VERIFY-1, P0) ───────────────────────────
#
# scraper-up — launches the Node.js artlist scraper sidecar as a background
# process for live-verify runs. Per architecture/issues.yaml::PR-LIVE-VERIFY-1
# follow_up: brings up the sidecar via `node node-scraper/artlist_server.js`
# with CHROME_EXECUTABLE=/usr/bin/google-chrome +
# ARTLIST_SCRAPER_BIND=127.0.0.1 + ARTLIST_SCRAPER_PORT=9123, then
# confirms /health responds healthy=true (the dry-run preflight contract).
