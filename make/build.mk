# make/build.mk - thematic include (P2 Manutenibilita, July 2026).
#
# Per AGENTS.md max_lines_per_file: 1000 plus the P2 directive,
# the canonical build chain is split into 7 thematic includes.
# This file holds only the build-bucket targets. Cross-bucket
# dependencies (e.g. verify-artlist-live -> auth-check) resolve
# naturally via Make's recursive target resolution.
# Root Makefile contains include make/*.mk plus all: build.

#   verify-<area>          Go unit tests for the area (HEADLESS, part of verify-main)
#   verify-<area>-live     Operational battery for the area (BROWSER+DRIVE+QDRANT,
#                          NOT part of verify-main — run only post-deploy)
# Examples: verify-images (Go tests) vs verify-images-live (images_e2e.sh battery).
# Mixing the two surfaces in a single target is forbidden: keep the headless
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
build: go-version-check
	@mkdir -p bin
	$(GO) build -ldflags "$(LDFLAGS)" -v -o bin/pipelinegen      ./cmd/server
	$(GO) build -ldflags "$(LDFLAGS)" -v -o bin/admin            ./cmd/admin
	$(GO) build -ldflags "$(LDFLAGS)" -v -o bin/worker           ./cmd/worker

# Run Go unit tests (Go is the canonical test surface; tests here run
# for every `make test` invocation and in CI without requiring Node).
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
rebuild: clean build
	@echo "Rebuild complete: ./bin/admin and ./bin/worker"

# Run the server (HTTP + scheduler + maintenance in-process via --mode all).
# Port is read from $VELOX_PORT (canonical default 8000); see
# internal/platform/config/types.go `Server.Port` for the in-tree
run: build
	./bin/pipelinegen --mode all

# Run system doctor check. Port/port override same as artlist target.
# Admin token is read from $VELOX_ADMIN_TOKEN (canonical SSOT). The forbidden
# legacy `ADMIN_TOKEN ?= test-admin-token-12345` placeholder has been removed;
dev:
	air
