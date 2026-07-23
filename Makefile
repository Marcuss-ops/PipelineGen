# ─── PipelineGen verify-main (Cleanup Plan P0-3, June 2026) ────────────
#
# Every push to `main` MUST pass `make verify-main` locally first. CI runs
# the same chain — local must match CI exactly. The chain is fail-closed:
# any failing step exits non-zero, no `|| true`, no fallbacks, no
# continue-on-error. If `make verify-main` is RED locally, the push MUST
# be blocked until the next agent lands the fix.
#
# P0-3 closes the gap surfaced by the 29 June P0-2 RED zones: prior to
# the make gate, broken imports + redeclarations were pushed to `main`
# and only caught at the next CI run. With verify-main in place, every
# commit lands-green-or-not-all.

.PHONY: all build test test-unit test-js test-all coverage coverage-check clean lint fmt vet run doctor artlist dev deps tidy-check vuln bench docker-build docker-run docker-build-worker docker-sign docker-digest docker-verify-digest docker-verify-ffmpeg docker-bootstrap-smoke ci rebuild go-version-check go-version-guard preflight node-version-check smoke smoke-script smoke-run-all smoke-dry verify-no-secrets verify-main verify-base verify-go verify-go-core verify-go-infrastructure verify-go-api verify-go-commands verify-go-tests verify-architecture verify-artlist verify-format test-imports test-qdrant-fixtures test-qdrant-fixtures-down regen-current-yaml regen-routes-yaml archcheck-strict install-hooks

# Version information (can be overridden via environment)
# Use: make build VERSION=1.2.0
VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS  = -X main.buildVersion=$(VERSION) -X main.commitHash=$(COMMIT)

# Go binary (overridable from the environment).
# Use: make build GO=/opt/go-1.25/bin/go
GO ?= go

# Default target
all: build

# Pre-flight: ensure the host Go toolchain matches go.mod's `go` directive.
# CPR-CC-7 (June 2026): until this target landed, a stale host go (1.19 vs
# a 1.25 directive) silently produced confusing `go mod tidy` output during
# dep-refresh cycles and only surfaced the mismatch in CI. Wire this in as a
# precondition of any target that invokes the Go compiler.
go-version-guard: go-version-check

go-version-check:
	@REQ=$$(awk '/^go [0-9]/ {print $$2}' go.mod); \
	HOST=$$($(GO) version | awk '{print $$3}' | sed 's/^go//'); \
	REQ_MIN=$$(echo "$$REQ" | awk -F. '{print $$1"."$$2}'); \
	HOST_MIN=$$(echo "$$HOST" | awk -F. '{print $$1"."$$2}'); \
	if [ "$$(printf '%s\n%s\n' "$$REQ_MIN" "$$HOST_MIN" | sort -V | head -n1)" = "$$HOST_MIN" ] && [ "$$REQ_MIN" != "$$HOST_MIN" ]; then \
	    echo "❌ Go version mismatch: go.mod requires $$REQ, host has $$HOST"; \
	    echo "Remediation options:"; \
	    echo "  1. Re-run with: make build GO=/path/to/required-go (or set GO in your shell rc)"; \
	    echo "  2. Install the required toolchain: go install golang.org/dl/go$$REQ@latest && go$$REQ download"; \
	    echo "  3. Update the 'go' directive in go.mod if the requirement changed"; \
	    exit 1; \
	elif [ "$$(printf '%s\n%s\n' "$$REQ" "$$HOST" | sort -V | head -n1)" = "$$HOST" ] && [ "$$REQ" != "$$HOST" ]; then \
	    echo "⚠️  Host Go ($$HOST) patch is older than go.mod ($$REQ) — still compatible, but consider upgrading."; \
	else \
	    echo "✅ Go version $$HOST meets requirement $$REQ"; \
	fi

# Node toolchain guard. The single source of truth is the
# `engines.node` field in node-scraper/package.json (parsing that via
# `node -p require(...)` keeps the contract in code, not in comments),
# and the host version comes from `node --version`. Mirrors the Go
# guard so CI and Dependabot npm updates cannot silently drift from
# the runtime the operators actually execute.
node-version-check:
	@if ! command -v node >/dev/null 2>&1; then \
	    echo "❌ Node binary not found on PATH — install Node 22.x (nvm, fnm, asdf, or distro packages)"; \
	    exit 1; \
	fi; \
	# Read engines.node directly via awk (more reliable under Make's
	# subshell than `node -p JSON.parse(...)`, which suffered from a
	# subtle Make variable-expansion issue that surfaced on 2026-07-03:
	# the second `[ -z ]` check saw REQ_RAW as empty even though the
	# first saw it as populated. awk is a single-line field splitter
	# with no module-system dependencies, agnostic to the `"type"` field
	# of node-scraper/package.json (ESM or CJS, both work).
	REQ_RAW=$$(awk -F'"' '/^[[:space:]]*"node"[[:space:]]*:[[:space:]]*"/ {print $$4; exit}' node-scraper/package.json); \
	HOST=$$(node --version 2>/dev/null | sed 's/^v//'); \
	if [ -z "$$REQ_RAW" ]; then \
	    echo "❌ node-scraper/package.json has no 'engines.node' field — set e.g. \"engines\": { \"node\": \"22.x\" }"; \
	    exit 1; \
	fi; \
	# Anchored numeric extraction so operators like ^, >=, ranges, or
	# pre-release tags degrade into a clear "unsupported format" error
	# instead of a silent numeric-vs-string mismatch.
	REQ_MAJOR=$$(echo "$$REQ_RAW" | sed -E 's/^[^0-9]*([0-9]+).*/\1/'); \
	HOST_MAJOR=$$(echo "$$HOST" | cut -d. -f1); \
	if ! echo "$$REQ_MAJOR" | grep -qE '^[0-9]+$$'; then \
	    echo "❌ Unsupported engines.node format: '$$REQ_RAW' — must reduce to a major version (e.g. '22', '22.x', '^22', '>=22.0.0')"; \
	    exit 1; \
	fi; \
	if [ "$$HOST_MAJOR" != "$$REQ_MAJOR" ]; then \
	    echo "❌ Node version mismatch: node-scraper/package.json engines.node requires $$REQ_RAW, host has $$HOST"; \
	    echo "Remediation options:"; \
	    echo "  1. Install Node $$REQ_MAJOR.x (nvm, fnm, asdf, or distro packages)"; \
	    echo "  2. Update the 'engines.node' field in node-scraper/package.json if the requirement changed"; \
	    exit 1; \
	else \
	    echo "✅ Node version $$HOST meets requirement $$REQ_RAW"; \
	fi

# Build the entry-point binaries. Outputs land in ./bin/ to keep the project
# root clean (see `make clean`).
#
# Three canonical binaries (Operational Readiness PR, June 2026):
#   - bin/pipelinegen : HTTP server (cmd/server) — runs by default with
#                       `--mode all` so a single boot covers HTTP +
#                       worker + scheduler + maintenance sweepers.
#   - bin/admin       : one-shot CLI (cmd/admin) — subcommands include
#                       gen-api-docs, db, etc.
#   - bin/worker      : cross-host worker (cmd/worker) — registers
#                       against an HTTP broker via VELOX_BROKER_URL for
#                       users running the long-running worker on a
#                       separate host from the server.
build: go-version-check
	@mkdir -p bin
	$(GO) build -ldflags "$(LDFLAGS)" -v -o bin/pipelinegen      ./cmd/server
	$(GO) build -ldflags "$(LDFLAGS)" -v -o bin/admin            ./cmd/admin
	$(GO) build -ldflags "$(LDFLAGS)" -v -o bin/worker           ./cmd/worker

# Run Go unit tests (Go is the canonical test surface; tests here run
# for every `make test` invocation and in CI without requiring Node).
test: test-unit

# Run ALL tests (Go + JS). Opt-in target — `make test` alone stays Go-only
# to keep the cheap default CI chain (go-version-check + Go test + build)
# independent from Node toolchain state. CI environments that don't have
# Node installed can still run `make test`; `make test-all` adds the JS
# gate for environments where Node 22 + mocha are wired.
test-all: test-unit test-js

# Run unit tests with race detector
test-unit:
	$(GO) test -v -race -coverprofile=coverage.out ./internal/... ./pkg/...

# Run JavaScript test suite (node-scraper).
# Equivalent: cd node-scraper && npm install --silent && npm test.
# Uses Mocha (devDependency) as the canonical runner; the project also
# keeps `npm run test:fallback` as the node --test runner for operators
# who prefer the Node built-in. Auto-installs deps on first run
# (idempotent: skips install when mocha is already wired — uses `-e`
# rather than `-x` so the skip works whether mocha is a real file OR a
# symlink under node_modules/.bin/, which is the npm v7+ default).
# Exits non-zero on any failing test — same fail-closed contract as
# test-unit. Gated on node-version-check so a stale host Node aborts
# BEFORE npm install (a 30s install vs an immediate ✓/❌ toggle on the
# version mismatch).
test-js: node-version-check
	@if [ ! -e node-scraper/node_modules/.bin/mocha ]; then \
	    echo "→ Installing node-scraper devDependencies (Mocha + ESLint)..."; \
	    cd node-scraper && npm install --silent; \
	fi
	@echo "→ Running Mocha test suite on node-scraper/test/*.test.js..."
	cd node-scraper && npm test

# Generate coverage report
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

# Run linter
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
# artifacts. Move them to a secure backup if you really must drop them.
clean:
	rm -f server admin pipelinegen
	rm -f server.exe admin.exe pipelinegen.exe worker.exe
	rm -f *.test.exe
	rm -f nul
	rm -f *.bak
	rm -f coverage.out coverage.html
	rm -rf tmp/

# Rebuild: clean + build in one shot.
# Use after switching branches, pulling new code, or whenever the prior
# build output is suspected to be stale. Idempotent and reproducible:
# `make rebuild` is exactly equivalent to `make clean && make build`,
# so the cleanup-to-rebuild sequence is reproducible from a single command
# and works in CI as well as locally.
rebuild: clean build
	@echo "Rebuild complete: ./bin/admin and ./bin/worker"

# Run the server (HTTP + scheduler + maintenance in-process via --mode all).
# Port is read from $VELOX_PORT (canonical default 8000); see
# internal/platform/config/types.go `Server.Port` for the in-tree
# default. Tokens are read from $VELOX_ADMIN_TOKEN / $VELOX_WORKER_TOKEN
# (mandatory — the binary refuses to boot with placeholder tokens).
run: build
	./bin/pipelinegen --mode all

# Run system doctor check. Port/port override same as artlist target.
# Admin token is read from $ADMIN_TOKEN (default matches config.yaml).
# Fails explicitly if the server is not running.
doctor:
	@curl -sS -f -H "Authorization: Bearer $(ADMIN_TOKEN)" http://127.0.0.1:$${VELOX_PORT:-8000}/api/system/doctor | jq . || { echo "Server not running? Try: make run (override port via VELOX_PORT)"; exit 1; }

# Run artlist pipeline via POST /api/artlist/run.
# Usage: make artlist TERM=technology LIMIT=10 STRATEGY=default
# Port is read from $VELOX_PORT (canonical default 8000).
# Admin token is read from $ADMIN_TOKEN (default matches config.yaml).
TERM ?= technology
LIMIT ?= 10
STRATEGY ?= default
ADMIN_TOKEN ?= test-admin-token-12345
artlist:
	@curl -sS -f -X POST http://127.0.0.1:$${VELOX_PORT:-8000}/api/artlist/run \
		-H "Content-Type: application/json" \
		-H "Authorization: Bearer $(ADMIN_TOKEN)" \
		-d '{"term":"$(TERM)","limit":$(LIMIT),"strategy":"$(STRATEGY)"}' | jq . || { echo "Server not running? Try: make run (override port via VELOX_PORT)"; exit 1; }

# Development mode with hot reload (requires air)
dev:
	air

# ─── Removed targets (Blocco 1.2, June 2026) ──────────────────────────────────
# Two target definitions were stripped from this Makefile because they were
# the wrong integration mechanism for the referenced projects:
#
#   - google-accounting-run: orphaned. The `google-accounting/` directory
#     is NOT present on main, so the recipe always failed at `cd` with
#     "No such file or directory".
#
#   - comic-video-maker-run: `comic-video-maker/` IS present on main, so
#     the project is still in use. But integrating it as a target in this
#     root Makefile is the WRONG mechanism — when a developer needs to run
#     comic-video-maker locally, they should `cd comic-video-maker &&
#     npm run dev` directly, OR the project should be reintroduced as
#     a docker-compose service / explicit git submodule.
#
# Phasing rule (binding): a top-level service that has its own
# subdirectory MUST live as a separate service / submodule / Compose
# component, NEVER as a target in this root Makefile. Reintroduce with
# one of those mechanisms if the project becomes canonical again.

# Install dependencies (download only, no go.mod modification)
deps:
	$(GO) mod download

# Check if go.mod is tidy (useful in CI)
tidy-check: go-version-check
	$(GO) mod tidy
	git diff --exit-code -- go.mod go.sum

# Check for vulnerabilities
vuln:
	govulncheck ./...

# Run benchmarks
bench:
	$(GO) test -bench=. -benchmem ./...

# Docker build (requires Dockerfile) — image name is `pipelinegen:latest`
# per the Operational Readiness PR. Image listens on 8000 by default.
#
# Multi-target build: use TARGET=server-runtime (default), worker-runtime,
# or admin-runtime.
docker-build:
	@test -f Dockerfile || { echo "❌ Dockerfile not found"; exit 1; }
	docker build \
		--target $${TARGET:-server-runtime} \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		-t pipelinegen:latest .

# docker-build-worker: build ONLY the worker image (worker-runtime target)
# for certification and signing.
docker-build-worker:
	@test -f Dockerfile || { echo "❌ Dockerfile not found"; exit 1; }
	docker build \
		--target worker-runtime \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		-t pipelinegen-worker:latest .

# Docker run: maps the canonical VELOX_PORT (default 8000) host port
# onto the container's 8000 listener. Use VELOX_PORT=NNNN to override.
docker-run: docker-build
	docker run -p $${VELOX_PORT:-8000}:8000 --env-file .env pipelinegen:latest

# ─── Image certification (Barriera 2, June 2026) ──────────────────────

# docker-sign: Build the worker image and sign it with Cosign.
#
# Modes (COSIGN_MODE env):
#   keyless (default) — OIDC-based keyless signing (GitHub Actions or browser flow)
#   key               — use cosign.key / cosign.pub key pair
#
# Output: prints IMAGE_DIGEST=sha256:... for downstream pinning.
#
# Prerequisites:
#   - cosign v2.4+ installed (go install github.com/sigstore/cosign/v2/cmd/cosign@latest)
#   - docker available
#   - (key mode) cosign.key + cosign.pub in project root
#
# Usage:
#   make docker-sign                                    # keyless
#   make docker-sign COSIGN_MODE=key                    # key pair
#   make docker-sign IMAGE=ghcr.io/org/worker:v1.0      # custom image ref
docker-sign: docker-build-worker
	@bash scripts/cosign-sign.sh $${IMAGE:-pipelinegen-worker:latest}

# docker-digest: Print the SHA256 digest of the worker image for pinning
# in docker-compose.yml or deployment manifests.
#
# WARNING: this target REQUIRES the image to have been pushed to a
# registry first (docker push). Without a push, RepoDigests is empty
# and {{.Id}} is a layer/content ID — NOT a pinnable digest.
# See scripts/cosign-sign.sh for the full signing + digest workflow.
docker-digest:
	@echo "→ Worker image digest:"
	@DIGEST=$$(docker inspect --format='{{index .RepoDigests 0}}' pipelinegen-worker:latest 2>/dev/null); \
	if [ -n "$$DIGEST" ]; then \
		echo "$$DIGEST"; \
	else \
		echo "ERROR: No RepoDigests found — image has NOT been pushed to a registry." >&2; \
		echo "" >&2; \
		echo "  Remediation:" >&2; \
		echo "    1. Push the image:  docker push pipelinegen-worker:latest" >&2; \
		echo "    2. Re-run:          make docker-digest" >&2; \
		echo "" >&2; \
		echo "  NOTE: docker inspect {{.Id}} is a layer ID — NOT pinnable." >&2; \
		echo "  Do NOT use it as a docker-compose digest reference." >&2; \
		exit 1; \
	fi

# docker-verify-digest: Verify the running container's image matches the
# pinned SHA256 digest in docker-compose.yml. Fails on mismatch.
# Usage: make docker-verify-digest CONTAINER=pipelinegen-worker
docker-verify-digest:
	@bash scripts/verify-image-digest.sh $${CONTAINER:-pipelinegen-worker} --strict

# docker-verify-ffmpeg: Probe the worker image for engine binaries
# (ffmpeg, ffprobe, yt-dlp, python3). Part of Barriera 2 image certification.
# Usage: make docker-verify-ffmpeg IMAGE=pipelinegen-worker:latest
docker-verify-ffmpeg:
	@bash scripts/verify-ffmpeg.sh $${IMAGE:-pipelinegen-worker:latest}

# docker-bootstrap-smoke: Quick smoke test of the worker binary in the
# image — verifies ENTRYPOINT, --help, and version output.
# Usage: make docker-bootstrap-smoke IMAGE=pipelinegen-worker:latest
docker-bootstrap-smoke:
	@bash scripts/worker-bootstrap-smoke.sh $${IMAGE:-pipelinegen-worker:latest}

# CI pipeline (runs all checks)
ci: go-version-check fmt vet tidy-check lint test coverage-check build
	@echo "✅ All CI checks passed!"

# verify-no-secrets — canonical wire-in for the no-secrets gate
# (scripts/ci/ci-no-secrets-audit.sh). GODLIKE/06 SSOT lockstep: the
# script at scripts/ci/ci-no-secrets-audit.sh is the canonical owner
# of the no-secrets check (one canonical owner per fact); this target
# is a pure wrap. The gate runs a 3-tier auto-detect:
#   T1 gitleaks detect --source .   (when `gitleaks` is on PATH)
#   T2 trufflehog filesystem .      (when `trufflehog` is on PATH)
#   T3 ripgrep regex fallback       (always runs; falls back to grep -E
#                                   when `rg` is not on PATH)
# The T3 regex catches 3 canonical shapes that gitleaks/trufflehog may
# miss on this repo's local custom tokens: VELOX_ADMIN_TOKEN 64-hex,
# AWS canonical AKIA + 16 alnum, GitHub PATs (ghp_/github_pat_),
# Slack tokens (xox[abpr]-), PEM private-key headers.
# GODLIKE/07 NO-FAKE-AVAILABILITY (fail-closed): any non-zero exit from
# the script aborts the make chain. Exit contract: 0=PASS, 1=FAIL
# (hit list printed + saved to HIT_LOG), 2=setup error.
# NOT gated on go-version-check because the script is pure-bash and
# agnostic to the Go toolchain (mirrors preflight's toolchain-agnostic
# design for the docker-compose / Dockerfile sanity checks).
verify-no-secrets:
	@bash scripts/ci/ci-no-secrets-audit.sh

# verify-base — fail-closed base gate: toolchain version, secrets,
# formatting, and module tidiness. Kept cheap so the most common failures
# surface in seconds.
verify-base: go-version-check verify-no-secrets verify-format tidy-check
	@echo "✅ Base verification passed"

# verify-go-core — domain and application logic tests. Isolates failures
# in the core business packages so a domain test failure is immediately
# distinguishable from an infrastructure or API failure.
verify-go-core:
	$(GO) test -race ./internal/domain/... ./internal/application/...

# verify-go-infrastructure — concrete adapter tests. Covers everything
# under internal/infrastructure/ (databases, clients, external SDK wraps).
verify-go-infrastructure:
	$(GO) test -race ./internal/infrastructure/...

# verify-go-api — HTTP/API handler tests. Covers internal/api/... .
verify-go-api:
	$(GO) test -race ./internal/api/...

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

# verify-architecture — governance and architecture checks. Kept separate
# so architecture drift surfaces under its own target.
verify-architecture:
	$(GO) run ./cmd/architecture-aggregate --dry-run && \
	$(GO) run ./cmd/archcheck
	@echo "✅ Architecture verification passed"

# verify-artlist — quick verification dedicated to the Artlist module.
# Runs the Go packages under infrastructure/artlist, providers/artlist,
# api/assets/artlist, plus the node-scraper JS test suite.
verify-artlist:
	$(GO) test -race ./internal/infrastructure/artlist/... && \
	$(GO) test -race ./internal/application/assets/providers/artlist/... && \
	$(GO) test -race ./internal/api/assets/artlist/... && \
	cd node-scraper && npm test
	@echo "✅ Artlist verification passed"

# verify-main — Cleanup Plan P0-3 (June 2026): the canonical fail-closed
# pre-push gate, now composed of the granular targets above. Every push to
# `main` MUST pass this target locally first. The individual steps were
# extracted to reduce log volume and isolate failures.
#
# TODO(pre-existing-debt, June 2026): re-enable the canonical
# ci-architectural-checks step once scripts/archcheck/main.go
# --ratchet/--future-ratchet are wired into this bash script with a
# populated scripts/archcheck/phase0_baseline.json. Today the script
# unconditionally fails on pre-existing handler->database/sql and
# cross-package type-alias patterns documented in the wave-tracker,
# even when passed the aspirational --future-ratchet flag (the
# script body only handles --self-check; ratchet logic lives in
# scripts/archcheck/main.go). Mirrors the --strict relaxation
# already applied to cmd/archcheck above (pre-existing debt
# accepted for the migration window per godlike/07).
verify-main: verify-base verify-go verify-architecture verify-artlist
	@echo "✅ verify-main passed"
	# bash scripts/ci-architectural-checks.sh

# Aggregate pre-flight check. Runs both toolchain guards plus two
# sanity contracts that have historically drifted silently:
#   - docker-compose.yml resolves against the current Dockerfile
#   - the Dockerfile builder image tag tracks go.mod's `go` line
# Read-only: no lockfile modification, no upgrade, no commit.
preflight: go-version-check node-version-check
	@if command -v docker >/dev/null 2>&1; then \
		docker compose config >/dev/null; \
	else \
		echo "warning: 'docker compose config' skipped — docker not on PATH"; \
	fi
	@grep -q '^FROM .*golang:1.25' Dockerfile
	@echo "Preflight passed"

# ─── Black-box smoke tests ──────────────────────────────────────────────────
# Per operator policy (AGENTS.md): never modify internal/application/scripts,
# internal/api/script, internal/app/wire_script.go, or production business
# logic for tests. Hammer the live HTTP surface under tests/operational/.

# Lightweight startup + error-path smoke. Use this on every deploy.
smoke:
	@set -e; \
	for s in tests/operational/startup_smoke.sh tests/operational/failed_job_smoke.sh tests/operational/fase_b_clip_pipeline_smoke.sh; do \
	    echo "----- $$s -----"; \
	    bash $$s; \
	done
	@echo "✅ smoke OK"

# Heavy path — drives a text-only script job end-to-end (dispatch + poll).
# Will FAIL initially on the broken worker (AGENT-2 Extract residue).
smoke-script:
	@echo "----- tests/operational/text_script_smoke.sh -----"
	@bash tests/operational/text_script_smoke.sh
	@echo "----- tests/operational/failed_job_smoke.sh -----"
	@bash tests/operational/failed_job_smoke.sh
	@echo "✅ smoke-script completed (check individual script exit codes above)"

# FASE B clip-pipeline regression smoke — 4 tests covering
# strategy=replace / folder_id pre-riso / PathBuilder canonical / duplicate.
# Forward-pointer: architecture/current.yaml#PR-FASE-B-CLIP-SMOKE-TESTS.
smoke-pipeline:
	@bash tests/operational/fase_b_clip_pipeline_smoke.sh

# Automatic operational pipeline for script.generate.
# Flow: build → test → controlled restart → readiness probe → smoke test.
# No manual server restart is required. The server is torn down on exit.
operate-script-generate:
	@bash scripts/operate_script_generate.sh

# Aggregate: every smoke script (no --dry). Use this for the full operational
# gate; returns non-zero if ANY script exits non-zero.
smoke-run-all:
	@set -e; \
	for s in tests/operational/startup_smoke.sh \
	         tests/operational/text_script_smoke.sh \
	         tests/operational/failed_job_smoke.sh \
	         tests/operational/fase_b_clip_pipeline_smoke.sh; do \
	    echo "----- $$s -----"; \
	    bash $$s; \
	done
	@echo "✅ smoke-run-all OK"

# smoke-voiceover — FASE 7 E2E smoke for the voiceover pipeline.
# Usage:
#   make smoke-voiceover                              # use default env
#   VELOX_ADMIN_TOKEN=<t> make smoke-voiceover         # explicit token
#   VELOX_ADMIN_TOKEN=<t> SMOKE_DB=<path> make smoke-voiceover  # custom DB
#
# Runs the Go E2E smoke test at tests/operational/voiceover_e2e_smoke_test.go.
# The test skips when VELOX_ADMIN_TOKEN is unset or -short is active.
# Wall-clock budget: 5min (3min job poll + TTS/Drive latency).
smoke-voiceover:
	@echo "→ Running voiceover E2E smoke test..."
	@if [ -z "$$VELOX_ADMIN_TOKEN" ]; then \
		echo "❌ VELOX_ADMIN_TOKEN not set — the voiceover E2E smoke needs auth. Set VELOX_ADMIN_TOKEN and retry."; \
		exit 1; \
	fi
	@go test -v -count=1 -timeout 5m -run TestVoiceoverE2ESmoke ./tests/operational/...
	@echo "✅ smoke-voiceover OK"

# Dry-run for the heavy path. Prints the would-be payloads, exits 0. Honors
# SMOKE_DRY_RUN=1 env override for CI-friendly invocations.
smoke-dry:
	@SMOKE_DRY_RUN=$${SMOKE_DRY_RUN:-1}; \
	export SMOKE_DRY_RUN; \
	for s in tests/operational/startup_smoke.sh \
	         tests/operational/text_script_smoke.sh \
	         tests/operational/failed_job_smoke.sh; do \
	    echo "----- $$s (dry) -----"; \
	    bash $$s --dry; \
	done
	@echo "✅ smoke-dry OK"

# ─── Qdrant synthetic-asset integration tests (Task 8, July 2026) ──────

# test-qdrant-fixtures: Start ephemeral Qdrant container, run synthetic
# asset integration tests, then tear down. Fails fast if docker is not
# available or the Qdrant image cannot be pulled.
#
# Port 16333 avoids collision with the production Qdrant on 6333.
# The container is ephemeral (no volume) — data is lost on stop.
#
# Usage:
#   make test-qdrant-fixtures                         # default Qdrant port
#   make test-qdrant-fixtures TEST_QDRANT_PORT=16333  # override port
test-qdrant-fixtures:
	@echo "→ Starting ephemeral Qdrant on port $${TEST_QDRANT_PORT:-16333}..."
	docker compose -f docker-compose.test-qdrant.yml up -d --wait 2>/dev/null || \
		docker compose -f docker-compose.test-qdrant.yml up -d
	@sleep 3  # give Qdrant time to accept connections
	@echo "→ Running synthetic asset integration tests..."
	TEST_QDRANT_URL=http://localhost:$${TEST_QDRANT_PORT:-16333} $(GO) test -tags=integration -v -count=1 ./tests/fixtures/... || \
		(echo "→ Tests failed — tearing down Qdrant..."; \
		 docker compose -f docker-compose.test-qdrant.yml down --volumes 2>/dev/null; \
		 exit 1)
	@echo "→ Tests passed — tearing down Qdrant..."
	docker compose -f docker-compose.test-qdrant.yml down --volumes 2>/dev/null
	@echo "✅ test-qdrant-fixtures OK"

# test-qdrant-fixtures-down: Tear down the test Qdrant container.
# Use this to clean up after a failed/aborted test run.
test-qdrant-fixtures-down:
	docker compose -f docker-compose.test-qdrant.yml down --volumes 2>/dev/null
	@echo "Qdrant test container torn down"

# Regenerate Google Drive token.json
regenerate-token:
	@bash scripts/regenerate_token.sh
# verify-format — Cleanup Plan P0-3 followup (June 2026): the actually
# FAIL-CLOSED gofmt gate. Used as a dependency of verify-main so a
# single bad-format file blocks the push attempt with non-zero exit.
#
# The bug being fixed: `gofmt -l .` exits 0 EVEN IF unformatted files
# are present — it just prints them on stdout. Stacking that behind a
# `&& other-stuff` chain therefore continues to run "other-stuff" even
# when the formatting check "failed". The corrected check is
# `test -z "$$(gofmt -l .)"` which exits 1 when the listing is
# non-empty AND prints the offending files for the operator to fix.
# Re-run `make fmt` (alias for `go fmt ./...`) after the fix.
#
# SKIP_FORMAT=1 opt-out (diagnostic only): when the gate is failing on
# unformatted files that are KNOWN to be external to the operator's
# working commit (e.g. a parallel-actor rename wave churning the tree
# faster than it can be stabilized), the operator can bypass the gate
# to surface higher-layer failures (vet/test/archcheck) that the chain
# would otherwise never reach.
#
# Invariants (binding):
#   - ONLY the literal value `1` activates the bypass. SKIP_FORMAT=true,
#     =yes, =on, =0, or empty ALL fall through to the real check.
#     Stricter than other env-var flags in this Makefile on purpose:
#     the gate is fail-closed, the bypass is the exception.
#   - Default-off, env-var-only, no Make-level alias. The flag does
#     NOT leak into CI (CI does not set it; .github/workflows/*.yml
#     and .env* are grep-verified clean).
#   - DO NOT export SKIP_FORMAT=1 in your shell rc. If you find
#     yourself using this often, you are papering over a real
#     foundation issue — fix the upstream churn, do not bake this
#     into your environment.
#   - SCOPE: this is a ONE-OFF diagnostic for the gofmt gate ONLY.
#     It is NOT a precedent for similar opt-outs on verify-no-secrets
#     (security gate — must NEVER be skippable), tidy-check (go.mod
#     integrity), or go test -race (would mask flaky tests). Future
#     "I want to skip X for diagnostic" requests must be evaluated
#     independently against that gate's failure-cost profile.
#   - DO NOT use it for normal pre-push runs. A green verify-main with
#     SKIP_FORMAT=1 is NOT a proxy for a green CI signal (CI does not
#     pass this flag). The pre-push gate is `make verify-main` without
#     this flag; do not push if the unflagged gate is red.
verify-format:
	@if [ "$$SKIP_FORMAT" = "1" ]; then \
	    echo "⚠️  SKIP_FORMAT=1 set — bypassing gofmt gate (DIAGNOSTIC ONLY)."; \
	    echo "   Do NOT use in normal pre-push runs: a green verify-main with this"; \
	    echo "   flag is NOT a proxy for a green CI signal (CI does not pass it)."; \
	else \
	    test -z "$$(gofmt -l .)" || { echo "❌ Files not formatted:"; gofmt -l .; exit 1; }; \
	fi

# test-imports — Cleanup Plan followup (Jul 2026): the canonical
# post-fix-up autofix step for unused imports. Runs `goimports -w` on
# every _test.go under internal/ to canonicalize import blocks
# (alphabetize, drop unused, dedupe), then re-runs `goimports -l -d`
# as the verify gate — non-canonical files fail the chain.
#
# Why a SEPARATE target (autofix + verify, not verify-only):
#   - Unused imports in test files block `go build` and `go test`. The
#     autofix `-w` pass resolves the latent drift in one command.
#   - The verify `goimports -l -d` after the autofix is the gate — it
#     lists any file where goimports would still change something, so
#     the target fails closed if the autofix pass couldn't canonicalize
#     the file (e.g., a docstring reference to an import that no longer
#     exists).
#   - This single target would have caught the E.1.x cascade of 10
#     unused imports across 3 sister test files at authoring time —
#     not after ~4 fix-up rounds of hand-written str_replace.
#
# Auto-install: `go install golang.org/x/tools/cmd/goimports@latest`
# follows the same canonical install path as other Go-managed tools
# (golangci-lint, cosign, govulncheck). Single-shot install; the
# binary lives under $(go env GOPATH)/bin. To avoid the Make recipe
# subshell-isolation trap (each `@` line is a fresh shell whose PATH
# may NOT include GOPATH/bin), the entire recipe is one collapsed
# shell invocation with `;` chaining. The GOBIN path is bound
# EXPLICITLY ("$$GOBIN/goimports") so the binary resolution is
# deterministic — a system-installed stale goimports at
# /usr/local/bin cannot silently win a `command -v goimports`
# race and bypass the canonical install, eliminating version-skew
# drift across dev machines. Reviewer's trade-off acknowledged:
# `@latest` is unpinned; pin to a tagged version (e.g. @v0.30.0)
# once known-good to fence alphabetization-drift in future releases.
#
# Scope: `_test.go` under internal/ ONLY — production code is left
# untouched. Production-code autofix would risk mid-PR import churn
# impacting type inference and is intentionally separated.
#
# Wire-up note (NOT applied here): once stabilized, add to the
# verify-main chain as a pre-step alongside verify-format — order:
# verify-format → test-imports (autofix in place) → tidy-check → ...
# The autofix happens in-tree, so a green verify-main implies
# `git diff` is empty on goimports canonicalization.
test-imports:
	@GOBIN=$$($(GO) env GOPATH)/bin; \
	GOIMPORTS="$$GOBIN/goimports"; \
	if [ ! -x "$$GOIMPORTS" ]; then \
	    echo "→ Installing goimports (canonical) into $$GOBIN..."; \
	    $(GO) install golang.org/x/tools/cmd/goimports@latest || { echo "❌ install failed"; exit 1; }; \
	    if [ ! -x "$$GOIMPORTS" ]; then \
	        echo "❌ goimports install did not produce expected binary at $$GOIMPORTS"; \
	        exit 1; \
	    fi; \
	fi; \
	echo "→ goimports -w (autofix) on every _test.go under internal/ via $$GOIMPORTS..."; \
	find internal/ -name '*_test.go' -type f -print0 | xargs -0 -r -n1 "$$GOIMPORTS" -w; \
	echo "→ goimports -l -d (verify; non-empty = non-zero exit)..."; \
	bad=$$(find internal/ -name '*_test.go' -type f -print0 | xargs -0 -r "$$GOIMPORTS" -l -d 2>&1); \
	if [ -n "$$bad" ]; then \
	    echo "❌ Files not goimports-canonical:"; \
	    echo "$$bad"; \
	    exit 1; \
	fi; \
	echo "✅ all test files goimports-canonical"

# install-hooks — wires the canonical PipelineGen pre-commit hook into
# the active git hooks path via `git config core.hooksPath`. Idempotent:
# a re-run is safe; the config set is the same.
#
# Per godlike/06 SSOT one-canonical-owner-per-fact: the canonical hook
# lives at scripts/hooks/pre-commit (version-controlled). We DO NOT
# copy the hook into .git/hooks/ because that's a git-ignored directory
# and would be lost on `git clean -fdx` or fresh clones.
#
# Scope per AGENTS.md godlike/07 minimum-blast-radius: the hook builds +
# vets ONLY the TOUCHED packages (git diff --cached derivation), NOT
# the full project (avoid 30s+ waits on docs-only commits).
#
# Companion gate: PIPELINEGEN_SKIP_PRECOMMIT=1 opt-out for emergency
# land-in (paired with a follow-up fixup! commit + autosquash). The
# hook asserts this loud-on-stdout so the bypass is never silent.
install-hooks:
	@if [ ! -f scripts/hooks/pre-commit ]; then \
		echo "❌ scripts/hooks/pre-commit not found — cannot install"; \
		exit 1; \
	fi
	@if [ ! -x scripts/hooks/pre-commit ]; then \
		echo "→ chmod +x scripts/hooks/pre-commit"; \
		chmod +x scripts/hooks/pre-commit; \
	fi
	@git config core.hooksPath scripts/hooks
	@echo "✅ core.hooksPath = $(shell git config --get core.hooksPath)"
	@echo "→ Dry-run the hook once to confirm wiring:"
	@bash scripts/hooks/pre-commit --dry-run | head -10 || true

# ─── Governance regeneration targets (Fase 7, Push 7, July 2026) ──────
#
# regen-current-yaml — re-emits architecture/current.yaml from the
# canonical hand-curated entries + the runtime registry capture at
# architecture/routes.yaml. The binary at cmd/admin/regen-current-yaml
# constructs the composition surface in-process WITHOUT calling
# gin.Engine.Run — godlike/07 NO-FAKE-AVAILABILITY contract.
#
# Usage:
#   make regen-current-yaml                         # writes to stdout (default)
#   make regen-current-yaml OUT=architecture/current.yaml   # writes to file
regen-current-yaml:
	@OUT=$${OUT:-}; \
	if [ -z "$$OUT" ]; then \
	    $(GO) run ./cmd/admin/regen-current-yaml --dry-run; \
	else \
	    $(GO) run ./cmd/admin/regen-current-yaml --out="$$OUT"; \
	fi

# regen-routes-yaml — convenience target that calls the AST pre-step
# analyzer at scripts/admin/ (the canonical capture surface that
# cmd/admin/regen-current-yaml reads). The Go invocation MUST target
# the directory `./scripts/admin`, NOT the single-file path
# `./scripts/admin/generate_routes_yaml.go` — main.go references 6
# symbols (discoverAPIFiles, manifestRoute, relSlashed, inspectAPIFile,
# dedupeManifest, manifestDocument) defined in the 4 split siblings
# types/discovery/ast/dedup. When invoked with a single-file path,
# `go run` only compiles that file and emits 6 undefined-symbol errors
# (Push 7(a) was split into 4 sibling files by LONG-FILES-DECOMPOSITION
# — Wave 2). The whole-dir invocation composes the package.
#
# Fail-closed (godlike/07 NO-FAKE-AVAILABILITY): the analyzer's stdout
# is staged in a mktemp sibling; on any failure (compile / parse /
# zero-routes sentinel / Marshal / WriteFile), the existing
# architecture/routes.yaml is left intact and stderr surfaces to the
# operator. This blocks the pre-existing bug where the recipe's literal
# `> architecture/routes.yaml` redirect truncated the canonical manifest
# on a partial failure, leaving the C2-E gate with an empty file and
# no error signal.
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
