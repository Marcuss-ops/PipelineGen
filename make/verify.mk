# make/verify.mk - thematic include (P2 Manutenibilita, July 2026).
#
# Per AGENTS.md max_lines_per_file: 1000 plus the P2 directive,
# the canonical build chain is split into 7 thematic includes.
# This file holds only the verify-bucket targets. Cross-bucket
# dependencies (e.g. a -live target -> auth-check) resolve
# naturally via Make's recursive target resolution.
# Root Makefile contains include make/*.mk plus all: build.

verify-no-secrets:
	@bash scripts/ci/ci-no-secrets-audit.sh

# verify-rust-muscles — Go consumer DTO validation for mediaexec.v2. The
# BrainRust workspace and semantic workers are tested in BrainRust CI.
verify-rust-muscles:
	@go test ./internal/platform/media/rustexec -run 'TestRequestValidate' -count=1

# Self-test for the release-artifact consumer (synthetic local bundle, no network).
verify-rust-fetch:
	@bash scripts/ci/test-rust-fetch.sh

# verify-lexicon-parity — cross-repo stopword gate: the Go embedded projection
# (`internal/capabilities/linguistics/profiles.generated.json`) must match the
# Rust language packs word for word. Needs both checkouts; the local agent
# loop and the pre-deploy gate call this target, while CI runs the same script
# in the `lexicon-parity` job after checking out BrainRust at the published SHA
# recorded in architecture/releases.json.
verify-lexicon-parity:
	@test -d ../BrainRust/lexicon/packs || { echo "❌ BrainRust submodule missing: run 'git submodule update --init BrainRust'" >&2; exit 1; }
	@python3 ../BrainRust/lexicon/tools/check_go_stopword_parity.py --repo-root ..
	@echo "✅ Lexicon parity verified"

# verify-release-pin — the tracked external revision pin is well formed and the
# container ARG defaults are in lockstep with it. This is the one gate that
# catches a stale Dockerfile default (v0.3.0 vs v0.3.8) before a build pulls a
# release whose semantic-engine predates the current wire contract.
RELEASE_PIN ?= architecture/releases.json
RELEASE_PIN_READER ?= scripts/ci/release_pin.py
PIN_BRAINRUST_VERSION := $(shell python3 $(RELEASE_PIN_READER) brainrust.version $(RELEASE_PIN))
PIN_BRAINRUST_SHA := $(shell python3 $(RELEASE_PIN_READER) brainrust.git_sha $(RELEASE_PIN))
PIN_BRAINRUST_ICU := $(shell python3 $(RELEASE_PIN_READER) brainrust.icu_major $(RELEASE_PIN))
PIN_BRAINRUST_MEDIASEXEC := $(shell python3 $(RELEASE_PIN_READER) brainrust.mediaexec_schema $(RELEASE_PIN))
verify-release-pin:
	@test -f "$(RELEASE_PIN)" || { echo "missing release pin $(RELEASE_PIN)" >&2; exit 1; }
	@printf '%s' "$(PIN_BRAINRUST_SHA)" | grep -Eq '^[0-9a-f]{40}$$' || { echo "brainrust.git_sha must be a full commit SHA (got '$(PIN_BRAINRUST_SHA)')" >&2; exit 1; }
	@printf '%s' "$(PIN_BRAINRUST_VERSION)" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$$' || { echo "brainrust.version must be a vX.Y.Z tag (got '$(PIN_BRAINRUST_VERSION)')" >&2; exit 1; }
	@test "$(PIN_BRAINRUST_MEDIASEXEC)" = "mediaexec.v2" || { echo "brainrust.mediaexec_schema must be mediaexec.v2" >&2; exit 1; }
	@grep -q "^ARG BRAINRUST_VERSION=$(PIN_BRAINRUST_VERSION)$$" Dockerfile || { echo "Dockerfile ARG BRAINRUST_VERSION is not $(PIN_BRAINRUST_VERSION) (make verify-release-pin)" >&2; exit 1; }
	@grep -q "^ARG RUST_ICU_MAJOR_VERSION_NUMBER=$(PIN_BRAINRUST_ICU)$$" Dockerfile || { echo "Dockerfile ARG RUST_ICU_MAJOR_VERSION_NUMBER is not $(PIN_BRAINRUST_ICU) (make verify-release-pin)" >&2; exit 1; }
	@echo "✅ Release pin verified ($(PIN_BRAINRUST_VERSION) @ $(PIN_BRAINRUST_SHA), ICU $(PIN_BRAINRUST_ICU))"

# verify-rust-contract — check consumer goldens against the exact PUBLISHED
# BrainRust revision recorded in architecture/releases.json, without
# initializing or building the submodule. It used to read a `rust` gitlink that
# does not exist in this repository, so the gate could only fail.
RUST_CONTRACT_REPO ?= https://github.com/Marcuss-ops/BrainRust.git
RUST_CONTRACT_COMMIT ?= $(PIN_BRAINRUST_SHA)
verify-rust-contract: verify-release-pin
	@test -n "$(RUST_CONTRACT_COMMIT)" || (echo "Cannot resolve the pinned BrainRust commit from $(RELEASE_PIN)" >&2; exit 1)
	@tmp="$$(mktemp -d)"; trap 'rm -rf "$$tmp"' EXIT; \
	 git clone --quiet --no-checkout --filter=blob:none "$(RUST_CONTRACT_REPO)" "$$tmp/brainrust" && \
	 git -C "$$tmp/brainrust" checkout --quiet --detach "$(RUST_CONTRACT_COMMIT)" && \
	 python3 -m venv "$$tmp/venv" && \
	 "$$tmp/venv/bin/pip" install --quiet -r "$$tmp/brainrust/contract/mediaexec.v2/requirements.txt" && \
	 "$$tmp/venv/bin/python" "$$tmp/brainrust/contract/mediaexec.v2/validate.py" && \
	 bash "$$tmp/brainrust/contract/mediaexec.v2/check_consumer_copy.sh" testdata/mediaexec/v2 && \
	 go test ./internal/platform/media/rustexec -run 'TestMediaexecV2SharedGoldens' -count=1

# verify-repository-integrity — fail-closed repository metadata checks.
# The canonical script validates every tracked mode-160000 gitlink against
# .gitmodules without touching ignored local working-tree directories.
verify-repository-integrity:
	@bash scripts/ci/ci-submodule-integrity.sh

# DEMOL-05/RGA-02: consume the reviewed, published Queue module directly.
# This check prevents a local replacement or vendored copy from hiding a
# missing release in clean CI and remote worker builds.
queue-module-check:
	@grep -Eq '^[[:space:]]*github.com/Marcuss-ops/RenderingGen/queue v0\.1\.0$$' go.mod || { \
		echo "go.mod must require published Queue module v0.1.0" >&2; \
		exit 1; \
	}
	@! grep -Eq '^[[:space:]]*replace[[:space:]]+github.com/Marcuss-ops/RenderingGen/queue([[:space:]]|$$)' go.mod || { \
		echo "go.mod must not locally replace the published Queue module" >&2; \
		exit 1; \
	}
	@test ! -d third_party/queue || { echo "third_party/queue must not exist (DEMOL-05)" >&2; exit 1; }

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

# verify-deadcode — ratcheting dead-code guard. The threshold is the measured
# unreachable count from the 2026-11-21 sweep and may only go down. Kept out
# of verify-agent so the agent loop does not require the external deadcode
# binary; CI can call this target when the tool is installed.
verify-deadcode:
	@bash scripts/ci/check-deadcode-baseline.sh

# verify-cyclo — Go-native cyclomatic-complexity ratchet. The AST counter,
# baseline and comparison live in cmd/cyclo-budget; no external gocyclo
# install is required. A new function above 40 or a baseline function that
# grows fails. After a deliberate improvement, run
# `go run ./cmd/cyclo-budget -write` in the same change.
verify-cyclo:
	$(GO) run ./cmd/cyclo-budget -check

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
verify-agent: verify-dev verify-changed verify-lexicon-parity
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
# distinguishable from an infrastructure or API failure. The recipe keeps
# both halves of that name: "domain" is internal/kernel + internal/capabilities
# and "application" is internal/app, the WAVE-24 successor of the retired
# internal/application root. Dropping the application leg leaves the
# composition root's tests unexecuted by every verify target, so verify-main
# (and therefore CI) would pass green without ever running them.

# verify-integration — operational, integration, and E2E tests under ./tests/.
verify-integration: go-version-check
	@$(MAKE) verify-go-tests
	@echo "✅ Integration verification passed"

# verify-architecture — governance and architecture checks. Kept separate
# so architecture drift surfaces under its own target.
verify-architecture: queue-module-check
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
# (Drive, scraper). Run before deploy, NOT on every routine push.
verify-release: verify-full verify-integration verify-lexicon-parity
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
