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

# verify-repository-integrity — fail-closed repository metadata checks.
# The canonical script validates every tracked mode-160000 gitlink against
# .gitmodules without touching ignored local working-tree directories.
verify-repository-integrity:
	@bash scripts/ci/ci-submodule-integrity.sh

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
verify-foundation: go-version-check node-version-check node-version-check-test verify-no-secrets verify-repository-integrity verify-format tidy-check
	@bash -n scripts/hooks/pre-push scripts/hooks/pre-commit
	@echo "✅ Foundation verification passed"

# verify-static — Go static analysis + full build (Web Admin removed 2026-08-25).
verify-static: go-version-check

	$(GO) vet ./...
	$(GO) build ./...
	@echo "✅ Static verification passed"

# verify-fast — dev-loop gate: foundation + static. On a warm dependency
# cache it is the cheapest fail-closed chain that catches the most common
# errors (toolchain mismatch, leaked secrets, formatting drift, embedded UI
# build failure, vet/build break). Used during active development. verify-main adds
# standard Go tests, the native Node probe, and architecture checks;
# verify-full and verify-release add the heavier race, Node, and integration
# gates.
verify-fast: verify-foundation verify-static verify-architecture
	@echo "✅ verify-fast passed"

verify-dev: verify-foundation verify-static
	@echo "✅ verify-dev passed"

verify-changed:
	@GO="$(GO)" bash scripts/ci/verify-changed.sh

# verify-agent — agent development loop gate: verify-dev (foundation + static)
# plus registry-driven tests of ONLY the components impacted by the current
# Git changes. Targets 1-3 minutes on a warm dependency cache. This is the
# canonical target for agent iterations; per AGENTS.md, agents must not run
# verify-main during development (verify-main runs exactly once, immediately
# before push).
verify-agent: verify-dev verify-changed
	@echo "✅ agent development verification passed"

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
# Component tests run through verify-changed-components, whose content-addressed
# cache skips only deterministic PASS results with an identical fingerprint.
verify-main: verify-push verify-node-native verify-architecture
	@echo "✅ verify-main passed"

# verify-race — explicit race-detector gate. Foundation runs as a shared
# prerequisite once, while unit and registry component suites use their
# race-enabled commands. It is independent of verify-main for direct use.
verify-race: verify-foundation verify-unit-race verify-race-components
	@echo "✅ verify-race passed"

# verify-clean-checkout-build — reproducibility gate that materializes the
# current HEAD in a temporary clone, then builds the embedded frontend,
# vets/tests all Go packages, and builds the three entry-point binaries.
# The script owns temporary-directory cleanup and never writes to the caller's
# working copy, including when a command fails.
verify-clean-checkout-build:
	@GO="$(GO)" bash scripts/ci/ci-clean-checkout-build.sh

# verify-full — complete headless gate: verify-main, the explicit race gate,
# the full Node test suite, and clean-checkout reproducibility. Shared
# prerequisites such as foundation are deduplicated by GNU Make within this
# aggregate invocation.
verify-full: verify-main verify-race verify-node-tests verify-clean-checkout-build
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
	$(GO) run ./cmd/archcheck && \
	$(GO) run -tags=c2_source_catalog_only scripts/archcheck/gates/gate_c2_source_catalog_only_main.go . && \
	$(GO) run -tags=c2_route_manifest scripts/archcheck/gates/gate_c2_route_manifest_main.go --baseline=171 --root=.
	@echo "✅ Architecture verification passed"

# test-main-stock — diagnostic Stock-focused gate. The authoritative Stock
# levels are verify-stock-unit/integration/live/release in youtube_stock.mk.
test-main-stock: verify-foundation verify-static verify-architecture verify-stock-unit
	@echo "✅ test-main-stock passed"

# verify-main-clip — compatibility alias for the registry-backed Clips gate.
verify-main-clip: verify-foundation verify-static verify-architecture verify-clips
	@echo "✅ verify-main-clip passed"

# whisper-preflight — canonical host-side Whisper runtime preflight. Runs
# scripts/tools/whisper_preflight.py with the .venv-whisper interpreter, the
# same check systemd runs via ExecStartPre. Fails closed when the requested
# device is unusable (e.g. VELOX_WHISPER_DEVICE=cuda without a usable GPU).
# Usage: make whisper-preflight [VELOX_WHISPER_DEVICE=cuda]
#
# Recreate the host venv on demand (it is gitignored — see .gitignore):
#   python3 -m venv .venv-whisper
#   .venv-whisper/bin/python -m pip install --no-cache-dir \
#       -r requirements/whisper.lock.txt
whisper-preflight:
	@test -x .venv-whisper/bin/python3 || { echo "❌ .venv-whisper missing — recreate it (see the comment above): python3 -m venv .venv-whisper && .venv-whisper/bin/pip install -r requirements/whisper.lock.txt" >&2; exit 1; }
	@.venv-whisper/bin/python3 scripts/tools/whisper_preflight.py

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

# regen-routes-yaml — refreshes the runtime-captured docs and the structured
# manifest in one transaction. The old AST-only recipe could emit module-
# relative paths and reintroduce route coverage drift; cmd/admin is the
# canonical composition-root capture and now writes both artifacts.
regen-routes-yaml:
	@$(GO) run ./cmd/admin gen-api-docs
	@echo "✅ regen-routes-yaml refreshed runtime docs and route manifest"

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
