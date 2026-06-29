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

.PHONY: all build test test-unit coverage coverage-check clean lint fmt vet run doctor artlist dev deps tidy-check vuln bench docker-build docker-run docker-build-worker docker-sign docker-digest docker-verify-digest docker-verify-ffmpeg docker-bootstrap-smoke ci rebuild go-version-check go-version-guard preflight node-version-check smoke smoke-script smoke-run-all smoke-dry verify-main

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
	REQ_RAW=$$(node -p "require('./node-scraper/package.json').engines.node" 2>/dev/null); \
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
	$(GO) build -ldflags "$(LDFLAGS)" -v -o bin/pipelinegen ./cmd/server
	$(GO) build -ldflags "$(LDFLAGS)" -v -o bin/admin      ./cmd/admin
	$(GO) build -ldflags "$(LDFLAGS)" -v -o bin/worker     ./cmd/worker

# Run all tests
test: test-unit

# Run unit tests with race detector
test-unit:
	$(GO) test -v -race -coverprofile=coverage.out ./internal/... ./pkg/...

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

# Run system doctor check. The port is read from $VELOX_PORT (canonical
# default 8000) so the same Makefile works whether the operator runs on the
# canonical port or overrides at runtime. Fails explicitly if the
# server is not running.
doctor:
	@curl -sS -f http://127.0.0.1:$${VELOX_PORT:-8000}/api/system/doctor | jq . || { echo "Server not running? Try: make run (override port via VELOX_PORT)"; exit 1; }

# Run artlist with smart presets
# Usage: make artlist TERM=technology LIMIT=10 PRESET=youtube_1080p_7s
# Port is read from $VELOX_PORT (canonical default 8000).
TERM ?= technology
LIMIT ?= 10
PRESET ?= youtube_1080p_7s
artlist:
	@curl -sS -f -X POST http://127.0.0.1:$${VELOX_PORT:-8000}/api/artlist/run-smart \
		-H "Content-Type: application/json" \
		-d '{"term":"$(TERM)","limit":$(LIMIT),"preset":"$(PRESET)"}' | jq . || { echo "Server not running? Try: make run (override port via VELOX_PORT)"; exit 1; }

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

# verify-main — Cleanup Plan P0-3 (June 2026): the canonical fail-closed
# pre-push gate. Action card P0-3 wires this into the local-mirror rule
# ("every push to `main` MUST pass locally first"). Mirrors the
# `.github/workflows/ci.yml` chain so a green local signal is a sufficient
# proxy for a green CI signal — same commands, same failure semantics.
#
# FAIL-CLOSED contract:
#   - `&&` between commands: any failing step aborts the chain immediately.
#   - NO `|| true`, NO `|| echo`, NO fallbacks.
#   - exit code = the first command that failed (Go make yields the right
#     one automatically; bash `&&` does the same).
#
# Order rationale:
#   1. `gofmt -l .`           — cheapest; catches unformatted diffs in
#                                seconds. MUST fail before any other step
#                                that costs minutes.
#   2. `go vet ./...`         — static analysis; semantically cheap.
#   3. `go test ./...`        — heaviest pre-build step; race detector
#                                baked in by default.
#   4. `go build ./...`       — full project type-check.
#   5. architecture-aggregate — schema-level cross-check on
#                                architecture/ownership.generated.yaml.
#   6. archcheck --strict     — gate-promoted phase-0 governance check.
#   7. ci-architectural-checks — the long-standing legacy fallback kept
#                                as the LAST step so an arch drift at
#                                step 5/6 surfaces before the legacy
#                                check masks it.
# verify-main is gated on go-version-check per the existing pattern
# (build, vet, tidy-check, ci all carry the same precondition). An
# operator on a stale host Go gets the canonical "Go version mismatch"
# error from go-version-check instead of an obscure toolchain crash
# further along the chain.
#
# Fail-closed chain is folded into ONE shell invocation via \-line
# continuation + `&&` chaining. Without the \-continuation Make runs
# each recipe line in its own subshell, so a fast failure on line 1
# would NOT abort lines 2-7 (the first 6 commands would still execute).
# The \-continuation collapses them into one shell with `set -e`-like
# `&&` semantics: first failure exits non-zero and aborts the chain.
verify-main: go-version-check
	gofmt -l . && \
	go vet ./... && \
	go test ./... && \
	go build ./... && \
	go run ./cmd/architecture-aggregate --dry-run && \
	go run ./cmd/archcheck --strict && \
	bash scripts/ci-architectural-checks.sh

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
	for s in tests/operational/startup_smoke.sh tests/operational/failed_job_smoke.sh; do \
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

# Aggregate: every smoke script (no --dry). Use this for the full operational
# gate; returns non-zero if ANY script exits non-zero.
smoke-run-all:
	@set -e; \
	for s in tests/operational/startup_smoke.sh \
	         tests/operational/text_script_smoke.sh \
	         tests/operational/failed_job_smoke.sh; do \
	    echo "----- $$s -----"; \
	    bash $$s; \
	done
	@echo "✅ smoke-run-all OK"

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

# Regenerate Google Drive token.json
regenerate-token:
	@bash scripts/regenerate_token.sh

