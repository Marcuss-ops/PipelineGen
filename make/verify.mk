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

verify-rust-muscles:
	@cargo test --manifest-path rust/Cargo.toml -p pipelinegen-muscles

# verify-repository-integrity — fail-closed repository metadata checks.
# The canonical script validates every tracked mode-160000 gitlink against
# .gitmodules without touching ignored local working-tree directories.
verify-repository-integrity:
	@bash scripts/ci/ci-submodule-integrity.sh

# verify-base — fail-closed base gate: toolchain version, secrets,
# formatting, and module tidiness. Kept cheap so the most common failures
# surface in seconds. GO-ONLY; use verify-foundation below for the full
# toolchain foundation. NOTE: verify-base and verify-foundation share 4 of 5 prereqs by design (the "non sostitutivi" constraint of the
# refactor). When adding/removing a prereq here, mirror it in
verify-base: go-version-check verify-no-secrets verify-format tidy-check
	@echo "✅ Base verification passed"

# verify-foundation — cheapest pre-flight gate: Go toolchain, secrets,
# repository integrity, formatting, module tidiness, AND hook syntax.
# Runs in seconds. The retired Node sidecar is not part of this foundation.
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
verify-foundation: go-version-check verify-no-secrets verify-repository-integrity verify-format tidy-check
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
# standard Go tests and architecture checks;
# verify-full and verify-release add the heavier race, Node, and integration
# gates.
verify-fast: verify-foundation verify-static
	@echo "✅ verify-fast passed"

verify-dev: verify-foundation verify-static
	@echo "✅ verify-dev passed"

# verify-changed — agent-loop component gate. The legacy shell runner
# (scripts/ci/verify-changed.sh) was purged in the Sept 2026 dust cleanup;
# the registry-driven runner is the canonical replacement (verify-push /
# verify-main consume the same verify-changed-components target).
verify-changed:
	@$(MAKE) verify-changed-components

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

# verify-main — canonical daily fail-closed headless gate.
verify-main: verify-push verify-architecture
	@echo "✅ verify-main passed"

# verify-race — explicit race-detector gate. Foundation runs as a shared
# prerequisite once, while unit and registry component suites use their
# race-enabled commands. It is independent of verify-main for direct use.
verify-race: verify-foundation verify-unit-race verify-race-components
	@echo "✅ verify-race passed"

# verify-clean-checkout-build — RETIRED 2026-09-13. Its driver
# (scripts/ci/ci-clean-checkout-build.sh) was deleted by commit 7e6965aab
# ("purge 94% shell + 87% python dust"), so the target could only ever fail
# with "No such file or directory". A target that cannot pass is not a gate
# (same rationale as the certification drivers at the bottom of this file).
# Clean-checkout reproducibility is currently a manual operator step:
#   git clone --depth 1 . /tmp/clean && cd /tmp/clean && make build
# Reintroduce it as a Go program before it becomes a gate again.

# verify-full — complete headless gate: verify-main + the explicit race gate.
verify-full: verify-main verify-race
	@echo "✅ verify-full passed"

# verify-go-core — domain and application logic tests. Isolates failures
# in the core business packages so a domain test failure is immediately
# verify-integration — operational, integration, and E2E tests under ./tests/.
verify-integration: go-version-check
	@$(MAKE) verify-go-tests
	@echo "✅ Integration verification passed"

# verify-architecture — governance and architecture checks. Kept separate
# so architecture drift surfaces under its own target.
verify-architecture:
	@bash scripts/ci/check_clip_render_cutover.sh && \
	$(GO) run ./cmd/architecture-aggregate --dry-run && \
	$(GO) run ./cmd/archcheck && \
	$(GO) run -tags=c2_source_catalog_only cmd/archcheck/gates/gate_c2_source_catalog_only_main.go . && \
	$(GO) run -tags=c2_route_manifest cmd/archcheck/gates/gate_c2_route_manifest_main.go --root=.
	@echo "✅ Architecture verification passed"

# test-main-stock — diagnostic Stock-focused gate. The remaining authoritative
# Stock levels are verify-stock-unit/integration in youtube_stock.mk (the
# live/release batteries were retired 2026-09-13 with their shell drivers).
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

# verify-split — RETIRED 2026-09-13. Its driver
# (scripts/ci/verify-split-contract.sh) was deleted by commit 7e6965aab, so the
# structural contract it certified no longer exists to be checked. The
# registry-driven runner (make verify-changed-components) is the current owner
# of tier/prerequisite separation; re-express the invariant as a Go gate there
# before reintroducing a target.

# regen-routes-yaml — refreshes the runtime-captured docs and the structured
# manifest in one transaction. The old AST-only recipe could emit module-
# relative paths and reintroduce route coverage drift; cmd/admin is the
# canonical composition-root capture and now writes both artifacts.
regen-routes-yaml:
	@$(GO) run ./cmd/admin gen-api-docs
	@echo "✅ regen-routes-yaml refreshed runtime docs and route manifest"

# archcheck-strict — invokes go run ./cmd/archcheck --strict, the
# gate-promoted Phase-0 governance check. Used by CI + locally as the
# failure-mode baseline for promote-to-enforce-zero ratchets (any
# violation = non-zero exit, plus the fail-closed debt-budget check).
# This is the SAME enforcement CI runs in
# .github/workflows/architecture.yml; make verify-main only runs the
# report-only form (`go run ./cmd/archcheck`).
archcheck-strict:
	@$(GO) run ./cmd/archcheck --strict

# ── Certification drivers: RETIRED ─────────────────────────────────────
#
# The certify-storage / certify-data-layer / certify-media-cutover targets (and
# their -json twins) invoked the scripts/ci/certify-*.sh drivers deleted by
# commit 7e6965aab ("purge 94% shell + 87% python dust"). They were kept for a
# while as fail-closed stubs, but a target whose only possible outcome is
# "NOTHING is certified" is not a gate — it trains operators to ignore red
# output and it kept stale certificate claims alive in AGENTS.md and
# docs/operations. They were removed on 2026-09-13; see git history for the
# fail-closed form. The live enforcement for those axes is:
#
#   make archcheck-strict                       (all structural hard gates)
#   go test ./internal/platform/sqlite/...      (data-layer / migration path)
#   TEST_POSTGRES_DSN=… \
#     go test ./internal/platform/postgres/media/ -count=1
#
# certify-rust-migration is retired the same way (see make/vidrush.mk).
