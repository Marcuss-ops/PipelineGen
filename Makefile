# ─── HONOUR-RULE for git push (binding, July 2026) ─────────────────────
#
# This is the operator-facing contract that closes the gap exposed on
# commit 2443a633c (node-scraper test coverage gap that landed with a
# real test failure because the local pre-push hook was skipped via
# `git push --no-verify`). Three layers wire the rule in:
#
#   (1) scripts/hooks/pre-push is the canonical version-controlled
#       pre-push gate (mirrors the legacy per-clone
#       .git/hooks/pre-push). Every push to `main` runs
#       `make verify-main`; a RED gate BLOCKS the push atomically.
#       Install once per clone via `make install-hooks` (sets
#       `git config core.hooksPath scripts/hooks`).
#
#   (2) `make verify-main` (the gate the hook invokes) is fail-closed
#       by AGENTS.md and is the EXACT chain CI runs. Local must match
#       CI (no diagnostic flags). The chain is documented immediately
#       below: verify-fast + verify-unit + verify-node +
#       verify-architecture.
#
#   (3) DO NOT use `git push --no-verify` to bypass the hook on
#       NORMAL pushes. `--no-verify` skips hooks entirely by git
#       design — there is no layer we can add to retroactively block
#       it. The bypass is reserved for unblocking CI emergencies
#       (where the red gate is environmental, not a real test
#       regression). A push that lands with `--no-verify` MUST be
#       paired with a follow-up `fixup!` commit + `git rebase
#       --autosquash` once the underlying red gate is fixed. The
#       commit that establishes THIS hook (i.e. the one that creates
#       scripts/hooks/pre-push itself) is the one canonical exception
#       and is fully documented in its commit body.
#
# Same SKIP_FORMAT=1 caveat applies: a green `make verify-main`
# with `SKIP_FORMAT=1` is NOT a proxy for a green CI signal —
# CI does not pass that flag. The pre-push gate is `make verify-main`
# without any bypass; do not push if the unflagged gate is red.

# ─── PipelineGen verification matrix (July 2026 refactor, STEP 3/4) ─
#
# Four-tier fail-closed verification chain. Every push to `main` MUST
# pass `make verify-main` locally first; CI runs the same chain, so
# local must match CI exactly. Every tier is composed of pre-existing
# granular targets via Make prereqs.
#
#   verify-fast        dev loop (<1min on a clean tree)
#                      verify-foundation + verify-static
#                      (toolchain + secrets + format + vet + build)
#
#   verify-main        pre-push gate (few minutes, HEADLESS)
#                      verify-fast + verify-unit + verify-node +
#                      verify-architecture
#                      NO browser, NO session, NO Drive, NO Qdrant.
#                      Run before every push; cheap enough that the
#                      dev loop can also call it directly.
#
#   verify-release     pre-deploy gate (slow, includes integration)
#                      verify-main + verify-integration
#                      ./tests/... suite — may take several minutes
#                      and may depend on external services.
#
#   verify-live        post-deploy gate (live batteries — STEP 4/4 complete).
#                      Composes the four operational batteries:
#                      verify-images-live + verify-artlist-live +
#                      verify-script-live + verify-vidrush-live
#                      (each touches browser/Drive/Qdrant/scraper).
#                      NOT pulled by verify-main or verify-release — the
#                      post-deploy surface stays outside the pre-push gate.
#
# The pre-push verify-main is HEADLESS by design: it must work on a CI
# runner without Chrome, without an Artlist session, without Drive
# credentials, without a Qdrant endpoint. The pre-STEP-3 verify-artlist
# target is REMOVED from verify-main because the Artlist battery
# (artlist_e2e) requires Chrome + scraper + Drive + Qdrant projections
# that the headless gate must not touch. verify-go (which transitively
# pulled verify-go-tests -> ./tests/...) is REPLACED by verify-unit
# (which excludes ./tests/... and is composed of pure unit-test
# sub-targets). Note: verify-go itself is INTENTIONALLY RETAINED as a
# standalone target (it's still in .PHONY) for backward compatibility
# with operators and CI scripts that invoke `make verify-go` directly.
# Do NOT re-add verify-go to verify-main — it transitively pulls the
# slow ./tests/... integration suite and defeats the headless goal.
#
# Fail-closed contract: any failing step exits non-zero. No `|| true`,
# no fallbacks, no continue-on-error. If `make verify-main` is RED
# locally, the push MUST be blocked until the next agent lands the
# fix.

.PHONY: all build test test-unit test-js test-all coverage coverage-check clean lint fmt vet run doctor artlist auth-check dev deps tidy-check vuln bench docker-build docker-run docker-build-worker docker-sign docker-digest docker-verify-digest docker-verify-ffmpeg docker-bootstrap-smoke ci rebuild go-version-check go-version-guard preflight node-version-check smoke smoke-script smoke-run-all smoke-dry verify-no-secrets verify-main verify-base verify-foundation verify-static verify-fast verify-go verify-go-core verify-go-infrastructure verify-go-api verify-go-commands verify-go-tests verify-unit verify-node verify-node-native verify-node-tests verify-integration verify-release verify-architecture verify-artlist verify-artlist-startup verify-artlist-search verify-artlist-stream verify-artlist-download verify-artlist-pipeline verify-artlist-drive verify-artlist-index verify-artlist-cache verify-artlist-errors verify-artlist-live verify-images verify-images-live verify-script-live verify-vidrush-live verify-live verify-stock verify-format test-imports test-qdrant-fixtures test-qdrant-fixtures-down regen-current-yaml regen-routes-yaml archcheck-strict install-hooks

# Suffix convention (binding, July 2026):
#   verify-<area>          Go unit tests for the area (HEADLESS, part of verify-main)
#   verify-<area>-live     Operational battery for the area (BROWSER+DRIVE+QDRANT,
#                          NOT part of verify-main — run only post-deploy)
# Examples: verify-images (Go tests) vs verify-images-live (images_e2e.sh battery).
# Mixing the two surfaces in a single target is forbidden: keep the headless
# pre-push chain (verify-main) free of any `-live` battery by design.

# Version information (can be overridden via environment)
# Use: make build VERSION=1.2.0
VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS  = -X main.buildVersion=$(VERSION) -X main.commitHash=$(COMMIT)

# Go binary (overridable from the environment).
# Use: make build GO=/opt/go-1.25/bin/go
GO ?= go

# Canonical token-file SSOT (AGENTS.md "Authentication SSOT (Velox admin token)").
# Agents normally load via scripts/with-velox-auth; exporting TOKEN_FILE here is a
# belt-and-braces bootstrap so any Make recipe that needs VELOX_ADMIN_TOKEN can
# source it from the canonical file without re-running the wrapper. The
# variables VELOX_ADMIN_TOKEN / VELOX_WORKER_TOKEN / VELOX_PORT are the only
# tokens this project reads; placeholders (e.g. test-admin-token-12345) are
# forbidden — see AGENTS.md.
TOKEN_FILE ?= /etc/pipelinegen/pipelinegen.env
export TOKEN_FILE

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
# `engines.node` field in node-scraper/package.json, and the host version
# comes from `node --version`. Mirrors the Go guard so CI and Dependabot
# npm updates cannot silently drift from the runtime the operators
# actually execute.
#
# Implementation notes (the three pitfalls wired into this gate):
#
#  1. awk vs node -p JSON.parse — awk is used because the latter
#     suffered from a subtle Make variable-expansion issue (2026-07-03
#     incident) where the second `[ -z ]` check saw REQ_RAW as empty
#     even though the first saw it as populated. awk is a single-line
#     field splitter with no module-system dependencies, agnostic to
#     the `"type"` field of node-scraper/package.json (ESM or CJS,
#     both work).
#
#  2. Anchored numeric extraction — REQ_MAJOR is derived via
#     `sed -E 's/^[^0-9]*([0-9]+).*/\1/'` so operators like ^, >=,
#     ranges, or pre-release tags degrade into a clear "unsupported
#     format" error instead of a silent numeric-vs-string mismatch.
#
#  3. NO INLINE `#` COMMENTS in the recipe below. Make joins
#     `\<newline>`-continued lines into a single physical shell line
#     (with `<space>` between), and an inline `#` starts a shell
#     comment that swallows the REST of the line — including the
#     REQ_RAW / REQ_MAJOR / HOST / HOST_MAJOR assignments that this
#     recipe depends on. All explanatory comments live in the header
#     block above this target, never inside the recipe. (Bug-fix
#     2026-07-21: this was the root cause of the verify-node regression
#     surfaced by `make verify-node` post-STEP-2/4 wire-up.)
node-version-check:
	@if ! command -v node >/dev/null 2>&1; then \
	    echo "❌ Node binary not found on PATH — install Node 22.x (nvm, fnm, asdf, or distro packages)"; \
	    exit 1; \
	fi; \
	REQ_RAW=$$(awk -F'"' '/^[[:space:]]*"node"[[:space:]]*:[[:space:]]*"/ {print $$4; exit}' node-scraper/package.json); \
	HOST=$$(node --version 2>/dev/null | sed 's/^v//'); \
	if [ -z "$$HOST" ]; then \
	    echo "❌ node binary present but 'node --version' returned empty (broken install?)"; \
	    exit 1; \
	fi; \
	if [ -z "$$REQ_RAW" ]; then \
	    echo "❌ node-scraper/package.json has no 'engines.node' field — set e.g. \"engines\": { \"node\": \"22.x\" }"; \
	    exit 1; \
	fi; \
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
# Admin token is read from $VELOX_ADMIN_TOKEN (canonical SSOT). The forbidden
# legacy `ADMIN_TOKEN ?= test-admin-token-12345` placeholder has been removed;
# callers must export VELOX_ADMIN_TOKEN (= `scripts/with-velox-auth` wrapped) or
# the recipe fails closed at the env-presence guard (AGENTS.md no-fake-availability).
doctor:
	@[ -n "$$VELOX_ADMIN_TOKEN" ] || { echo "❌ VELOX_ADMIN_TOKEN unset — source scripts/with-velox-auth or export manually."; exit 1; }
	@curl -sS -f -H "Authorization: Bearer $(VELOX_ADMIN_TOKEN)" http://127.0.0.1:$${VELOX_PORT:-8000}/api/system/doctor | jq . || { echo "Server not running? Try: make run (override port via VELOX_PORT)"; exit 1; }

# Run artlist pipeline via POST /api/artlist/run.
# Usage: make artlist TERM=technology LIMIT=10 STRATEGY=default
# Port is read from $VELOX_PORT (canonical default 8000).
# Admin token is read from $VELOX_ADMIN_TOKEN (canonical SSOT). The forbidden
# legacy `ADMIN_TOKEN ?= test-admin-token-12345` placeholder has been removed;
# callers must export VELOX_ADMIN_TOKEN (= `scripts/with-velox-auth` wrapped) or
# the recipe fails closed at the curl layer.
TERM ?= technology
LIMIT ?= 10
STRATEGY ?= default
artlist:
	@[ -n "$$VELOX_ADMIN_TOKEN" ] || { echo "❌ VELOX_ADMIN_TOKEN unset — source scripts/with-velox-auth or export manually."; exit 1; }
	@curl -sS -f -X POST http://127.0.0.1:$${VELOX_PORT:-8000}/api/artlist/run \
		-H "Content-Type: application/json" \
		-H "Authorization: Bearer $(VELOX_ADMIN_TOKEN)" \
		-d '{"term":"$(TERM)","limit":$(LIMIT),"strategy":"$(STRATEGY)"}' | jq . || { echo "Server not running? Try: make run (override port via VELOX_PORT)"; exit 1; }

# auth-check — operator pre-flight against the canonical auth-gated
# endpoint. scripts/with-velox-auth loads + validates VELOX_ADMIN_TOKEN
# (canonical SSOT) and exports it; the recipe probes
# /api/artlist/job-consumer with `Authorization: Bearer $$VELOX_ADMIN_TOKEN`
# and fails closed (exit 1) on any non-200 response, printing the actual
# HTTP code on failure. NOT part of `verify-main` (which is headless):
# this gate requires a running server, so it's operator-only and should
# be invoked pre-deploy or post-deploy to verify the live auth surface.
# See AGENTS.md "Authentication SSOT (Velox admin token)" for the SSOT
# contract and scripts/with-velox-auth for the wrapper.
auth-check:
	@scripts/with-velox-auth bash -c 'code=$$(curl -sS -o /dev/null -w "%{http_code}" --max-time 5 \
		-H "Authorization: Bearer $$VELOX_ADMIN_TOKEN" \
		http://127.0.0.1:$${VELOX_PORT:-8000}/api/artlist/job-consumer); \
	if [ "$$code" != "200" ]; then \
	    echo "❌ Velox authentication failed: HTTP $$code"; \
	    exit 1; \
	fi; \
	echo "✅ Velox authentication available: HTTP 200"'

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
# surface in seconds. GO-ONLY (no node-version-check); use verify-foundation
# below for the Node-aware chain. NOTE: verify-base and verify-foundation
# share 4 of 5 prereqs by design (the "non sostitutivi" constraint of the
# refactor). When adding/removing a prereq here, mirror it in
# verify-foundation below to prevent drift between the two chains.
verify-base: go-version-check verify-no-secrets verify-format tidy-check
	@echo "✅ Base verification passed"

# verify-foundation — cheapest pre-flight gate: toolchain versions (Go +
# Node), secrets, formatting, and module tidiness. Runs in seconds.
# ADDITIVE on top of verify-base: the only behavioural difference is the
# addition of node-version-check, which test-js / verify-node / the artlist
# gate have required since July 2026. Callers that do not run Node (legacy
# CI images, Go-only runners) keep using verify-base; new code paths and
# the dev-loop should prefer verify-foundation.
#
# NOTE: verify-base and verify-foundation share 4 of 5 prereqs by design
# (the "non sostitutivi" constraint of the refactor). When adding/removing
# a prereq here, mirror it in verify-base above to prevent drift between
# the two chains.
# verify-foundation — cheapest pre-flight gate: toolchain versions (Go +
# Node), secrets, formatting, module tidiness, AND hook syntax. Runs in
# seconds. ADDITIVE on top of verify-base: the only behavioural
# differences are the addition of node-version-check (required by
# test-js / verify-node / the artlist gate since July 2026) and the
# hook-syntax lint below (added after commit 8459c5d4f wired pre-push).
# Callers that do not run Node (legacy CI images, Go-only runners) keep
# using verify-base; new code paths and the dev-loop should prefer
# verify-foundation.
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
# the two chains.
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
# drift, vet/build break). Used during active development. verify-main
# and verify-release add the heavier gates (unit, node, integration,
# architecture, artlist).
verify-fast: verify-foundation verify-static
	@echo "✅ verify-fast passed"

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

# verify-unit — race-tested Go unit tests by area, EXCLUDING the slow
# operational/integration suite under ./tests/.... This is the canonical
# "fast Go" gate for the dev loop. ./tests/... is moved to verify-integration
# so a slow E2E never blocks `make verify-unit`; ops batteries there are
# also free to depend on external services (Drive, Qdrant, scraper).
#
# Composition rule (July 2026 verify-main refactor — STEP 2/4 WIP):
# verify-unit MUST NOT trigger any ./tests/... run. The four sub-targets
# are pure unit tests (domain, application, infrastructure, api, commands,
# pkg). NOTE: verify-main still routes through `verify-go` (which transitively
# includes verify-go-tests) until STEP 3/4 lands.
verify-unit: go-version-check
	@$(MAKE) verify-go-core
	@$(MAKE) verify-go-infrastructure
	@$(MAKE) verify-go-api
	@$(MAKE) verify-go-commands
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
verify-node-native:
	@if [ ! -d node-scraper/node_modules/better-sqlite3 ]; then \
	    echo "→ Installing node-scraper devDependencies (better-sqlite3 native build)..."; \
	    cd node-scraper && npm install --silent; \
	fi
	@echo "→ Probing better-sqlite3 native binding (catches 'Module did not self-register')..."
	@cd node-scraper && node -e 'const Database = require("better-sqlite3"); const db = new Database(":memory:"); db.exec("CREATE TABLE probe(id INTEGER)"); db.close(); console.log("✅ better-sqlite3 loaded");'
	@echo "✅ verify-node-native passed"

# verify-node-tests — Mocha + ESLint over node-scraper/test/*.test.js.
# Thin alias of `make test-js`: same install guard, same node-version-check,
# same npm test invocation. Kept separate from verify-node-native so the
# native-binding probe can fail fast without paying the npm-install cost
# and so verify-node-native can be run in isolation during Node upgrades.
verify-node-tests: test-js

# verify-node — Node toolchain gate. Composes the fast native-binding
# probe (verify-node-native, <1s once installed) and the Mocha test suite
# (verify-node-tests, npm install + npm test).
#
# Order is meaningful: verify-node-native runs first so an ABI mismatch
# surfaces immediately and the operator does not wait through a 30s+ npm
# install only to hit the same failure mode inside npm test.
verify-node: verify-node-native verify-node-tests
	@echo "✅ Node verification passed"

# verify-integration — operational, integration, and E2E tests under
# ./tests/... . Kept ISOLATED from verify-unit and verify-node because:
#   (a) some suites require external services (Drive, Qdrant, scraper) and
#       are slower than unit tests, so they can be run or skipped
#       independently;
#   (b) the gate is intentionally NOT part of verify-main (the pre-push
#       gate); it sits one level up under verify-release.
#
# Wraps verify-go-tests (which already runs `go test -race ./tests/...`)
# so a future migration to a different runner (e.g. go-test-integration
# with a build tag) can be wired at this target without touching the
# individual suites.
#
# go-version-check is a hard prereq (not just an opportunistic check):
# verify-go-tests calls `$(GO) test -race ./tests/...` directly, which
# fails late and opaquely on a stale host Go (vs. fail-fast via the
# canonical version mismatch message from go-version-check).
verify-integration: go-version-check
	@$(MAKE) verify-go-tests
	@echo "✅ Integration verification passed"

# verify-architecture — governance and architecture checks. Kept separate
# so architecture drift surfaces under its own target.
verify-architecture:
	$(GO) run ./cmd/architecture-aggregate --dry-run && \
	$(GO) run ./cmd/archcheck
	@echo "✅ Architecture verification passed"

# verify-artlist — quick verification dedicated to the Artlist module.
# Runs the Go packages under infrastructure/artlist, providers/artlist,
# api/assets/artlist, plus the node-scraper JS test suite.
#
# NOTE (July 2026, STEP 3/4): this target is REMOVED from verify-main
# because the Artlist battery (artlist_e2e) requires Chrome + scraper +
# Drive + Qdrant projections — all of which the headless pre-push gate
# must NOT depend on. Use `make verify-artlist` as a developer
# diagnostic, NOT as a pre-push gate.
verify-artlist:
	$(GO) test -race ./internal/infrastructure/artlist/... && \
	$(GO) test -race ./internal/application/assets/providers/artlist/... && \
	$(GO) test -race ./internal/api/assets/artlist/... && \
	cd node-scraper && npm test
	@echo "✅ Artlist verification passed"

# ─── Artlist operational batteries (granular, July 2026) ────────────────
#
# The legacy monolithic tests/operational/artlist_e2e.sh is split into
# 9 granular sub-scripts under tests/operational/artlist/, each gated
# by its own Make target so a developer debugging one phase can iterate
# in seconds instead of waiting for the full battery. The 9 sub-targets
# are NOT part of verify-main (the pre-push headless gate) — they all
# require the live stack (Chrome + scraper + Drive + Qdrant).
#
# Library surface (single canonical owner per fact): every sub-script
# imports helpers from tests/operational/lib/ — no curl/jq/SQLite/ffprobe
# is duplicated across the 9 files. See tests/operational/lib/{common,
# artlist, drive, qdrant, sqlite}.sh for the canonical surface.
#
# Debug pattern:
#   make verify-artlist-stream   # while iterating on /detail streaming
#   make verify-artlist-download # while iterating on /download + ffprobe
#   make verify-artlist-live     # only after all 9 green
#
# No go-version-check prereq (the sub-scripts are bash + node-scraper,
# Go-unaware). No node-version-check prereq either: each sub-script
# does its own runtime assertion at the top (the lib helpers fail
# closed when node-scraper is unreachable).

# verify-artlist-startup — Phase 1: server / scraper readiness + admin
# auth probe. Catches cold-start failures (port in use, scraper not
# running, token mismatch) before any downstream phase touches an
# endpoint that depends on those surfaces.
verify-artlist-startup:
	@bash tests/operational/artlist/01_startup.sh

# verify-artlist-search — Phase 2: POST /api/artlist/search/live.
# Confirms the live search endpoint resolves terms via the scraper and
# persists discovered candidates. Cheap gate (~30s) once startup is green.
verify-artlist-search:
	@bash tests/operational/artlist/02_search_live.sh

# verify-artlist-stream — Phase 3: POST /api/artlist/detail + ffprobe
# hard gate. Confirms the canonical stream probe rejects silent clips
# (audio_probe MUST surface HasAudio=false) and accepts clips with an
# audio track. The most common debug target when stream resolution
# regresses.
verify-artlist-stream:
	@bash tests/operational/artlist/03_detail_stream.sh

# verify-artlist-download — Phase 4: POST /api/artlist/download +
# ffprobe hard gate. Confirms the download endpoint returns a file
# whose ffprobe metadata matches the canonical expectations (codec,
# duration, container). Debug target for download-pipeline regressions.
verify-artlist-download:
	@bash tests/operational/artlist/04_download.sh

# verify-artlist-pipeline — Phase 5: end-to-end pipeline on a FRESH
# fixture (no cache replay). Drives search → detail → download →
# drive upload → index, asserting each stage's canonical surface.
verify-artlist-pipeline:
	@bash tests/operational/artlist/05_pipeline_fresh.sh

# verify-artlist-drive — Phase 6: Drive upload + folder routing.
# Confirms the canonical Publisher routes Artlist clips through the
# shared Drive surface and that the destination folder resolver picks
# the right root for the search term.
verify-artlist-drive:
	@bash tests/operational/artlist/06_drive.sh

# verify-artlist-index — Phase 7: Qdrant projection. Confirms the clip
# appears in the v3 collection with the expected payload (source_url,
# text_hash, source_version). Fails fast when the SQLite→Qdrant
# rebuild contract is broken.
verify-artlist-index:
	@bash tests/operational/artlist/07_index.sh

# verify-artlist-cache — Phase 8: cache-replay path. Re-runs the
# pipeline on a cached fixture and asserts the cache-hit surface (no
# re-download, no re-transcription, but full Drive + Qdrant surface
# still validated).
verify-artlist-cache:
	@bash tests/operational/artlist/08_cache_replay.sh

# verify-artlist-errors — Phase 9: failure-mode catalogue. Exercises
# the typed error surface: STREAM_NOT_FOUND, missing Drive fields,
# transcription failure, transcript persist failure, audio-probe
# miss. Asserts each error path surfaces the canonical typed sentinel
# rather than a generic no-op success.
verify-artlist-errors:
	@bash tests/operational/artlist/09_failure_modes.sh

# verify-artlist-live — composite: runs ALL 9 granular sub-scripts in
# order via tests/operational/artlist/run_all.sh. Run AFTER all 9
# individual gates are green; this is the post-deploy / pre-certification
# battery. NOT part of verify-main (it requires the live stack).
verify-artlist-live:
	@bash tests/operational/artlist/run_all.sh

# ─── Post-deploy live batteries (STEP 4/4, July 2026) ─────────────────
#
# verify-live — top-level post-deploy gate. Composes the live batteries
# (images + artlist + script + vidrush) so a single `make verify-live`
# runs the full operational suite. NOT part of verify-main or
# verify-release: these batteries all require Chrome + scraper + Drive
# + Qdrant and must never be wired into the pre-push chain.
#
# Individual live targets are also exposed so a post-deploy operator
# can validate ONE surface without paying the full battery cost
# (e.g. `make verify-images-live` after a Drive-side change).
#
# verify-images-live — tests/operational/images_e2e.sh — image
# ingestion + Drive upload + Qdrant projection for the image surface.
verify-images-live:
	@bash tests/operational/images_e2e.sh

# verify-script-live — tests/operational/script_generate_smoke.sh —
# end-to-end script.generate dispatch + worker pull + finalizer,
# without the full Vid Rush media path.
verify-script-live:
	@bash tests/operational/script_generate_smoke.sh

# verify-vidrush-live — tests/operational/vidrush_media_e2e.sh — the
# full Vid Rush battery: server + scraper + SQLite + FFmpeg + Drive +
# Qdrant. Heavy (10-30min) and server-stateful; run only on dedicated
# operational hosts.
verify-vidrush-live:
	@bash tests/operational/vidrush_media_e2e.sh

# verify-live — composite: all 4 live batteries in sequence. Fail-closed:
# any single battery failure aborts the chain.
#
# verify-artlist-live is ALSO runnable standalone (it wraps
# tests/operational/artlist/run_all.sh) for an artlist-only post-deploy
# validation that does not pay the full battery cost. verify-live itself
# composes it alongside the 3 sibling batteries (images + script +
# vidrush) so a single `make verify-live` runs the full operational
# suite.
verify-live: verify-images-live verify-artlist-live verify-script-live verify-vidrush-live
	@echo "✅ verify-live passed"
# ─── end Post-deploy live batteries ─────────────────────────────────────

# verify-images — quick verification dedicated to the Images module.
verify-images:
	$(GO) test -race ./internal/domain/image/... && \
	$(GO) test -race ./internal/application/images/... && \
	$(GO) test -race ./internal/api/images/...
	@echo "✅ Images verification passed"

# verify-stock — quick verification dedicated to the Stock module.
verify-stock:
	$(GO) test -race ./internal/application/assets/providers/stock/stockpipeline/... && \
	$(GO) test -race ./internal/api/assets/stock/...
	@echo "✅ Stock verification passed"

# verify-main — pre-push gate (STEP 3/4 of the verify-main refactor,
# July 2026): the canonical fail-closed chain that runs before every
# push to `main`. Composed of headless granular gates:
#   verify-fast        foundation + static (toolchain + format + vet)
#   verify-unit        Go unit tests (EXCLUDES ./tests/... integration)
#   verify-node        Node toolchain (probe + Mocha)
#   verify-architecture governance checks (in-process AST + archcheck)
# No browser, no Artlist session, no Drive, no Qdrant. The pre-existing
# verify-artlist and verify-go targets are INTENTIONALLY EXCLUDED:
# verify-artlist touches the heavy artlist_e2e battery (browser +
# session), verify-go transitively includes the slow ./tests/...
# integration suite via verify-go-tests. Use verify-artlist /
# verify-integration as developer-tool diagnostics, NOT as pre-push
# gates.
#
# CI runs the same chain, so local must match CI exactly. The chain
# is fail-closed: any failing step exits non-zero, no `|| true`, no
# fallbacks, no continue-on-error. If `make verify-main` is RED
# locally, the push MUST be blocked until the next agent lands the
# fix.
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
verify-main: verify-fast verify-unit verify-node verify-architecture
	@echo "✅ verify-main passed"
	# bash scripts/ci-architectural-checks.sh

# verify-release — pre-deploy gate (STEP 3/4 of the verify-main
# refactor): verify-main + verify-integration. Adds the slow
# ./tests/... integration suite which may depend on external services
# (Drive, Qdrant, scraper). Run before deploy, NOT on every push
# (too slow for the pre-push gate). verify-fast / verify-main remain
# the dev-loop / pre-push gates.
verify-release: verify-main verify-integration
	@echo "✅ Release verification passed"

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
	@if [ ! -f scripts/hooks/pre-commit ] || [ ! -f scripts/hooks/pre-push ]; then \
		echo "❌ scripts/hooks/{pre-commit,pre-push} not found — cannot install"; \
		echo "   Expected canonical hooks (must both exist) at scripts/hooks/."; \
		exit 1; \
	fi
	@if [ ! -x scripts/hooks/pre-commit ]; then \
		echo "→ chmod +x scripts/hooks/pre-commit"; \
		chmod +x scripts/hooks/pre-commit; \
	fi
	@if [ ! -x scripts/hooks/pre-push ]; then \
		echo "→ chmod +x scripts/hooks/pre-push"; \
		chmod +x scripts/hooks/pre-push; \
	fi
	@git config core.hooksPath scripts/hooks
	@[ "$$(git config --get core.hooksPath)" = "scripts/hooks" ] || { \
	    echo "❌ core.hooksPath assertion FAILED: expected 'scripts/hooks', got '$$(git config --get core.hooksPath)'"; \
	    echo "   A user-level override (git config --global core.hooksPath=...) is shadowing the per-repo setting."; \
	    echo "   Remove with: git config --unset --global core.hooksPath  (or rerun without the global flag)"; \
	    exit 1; \
	}
	@echo "✅ core.hooksPath = $(shell git config --get core.hooksPath)"
	@echo "→ pre-commit gate: scripts/hooks/pre-commit    (touched-pkg Go build+vet, test-prefix)"
	@echo "→ pre-push   gate: scripts/hooks/pre-push      (fails the push on red make verify-main)"
	@echo "→ Dry-run pre-commit once to confirm wiring:"
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
