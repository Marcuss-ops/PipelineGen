# make/build.mk - thematic include (P2 Manutenibilita, July 2026).
#
# Per AGENTS.md max_lines_per_file: 1000 plus the P2 directive,
# the canonical build chain is split into 7 thematic includes.
# This file holds only the build-bucket targets. Cross-bucket
# dependencies (e.g. verify-artlist-live -> auth-check) resolve
# naturally via Make's recursive target resolution.
# Root Makefile contains include make/*.mk plus all: build.

#   verify-<area>          Registered component checks (HEADLESS, part of verify-main/full)
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

# Rust execution-plane build. Keep the toolchain explicit so rustup does not
# silently select a host default while the migration is being rolled out.
RUST_CARGO ?= rustup run stable cargo

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

# Node toolchain guard. The canonical implementation lives in the shared
# script so Make, CI, Docker, and focused tests execute exactly one resolver.
# It reads engines.node from both package manifests, requires equal majors,
# and requires the host Node major to match them.
node-version-check:
	@NODE_VERSION_CHECK_ROOT="$$(pwd)" bash scripts/ci/node-version-check.sh

node-version-check-test:
	@bash scripts/ci/node-version-check_test.sh

# Install the embedded admin console dependencies from the committed lockfile.
web-install: node-version-check
	npm ci --prefix web

# Build the embedded admin console and fail closed if the embed entrypoint is
# missing. Keep installation and build in one canonical dependency chain so
# local, CI, and Docker callers do not duplicate the frontend setup.
web-build: web-install
	npm run build --prefix web
	test -f web/dist/index.html

# Remove frontend dependencies and generated artifacts. The regular `clean`
# target intentionally keeps node_modules so local Go-only rebuilds remain
# fast; use `make web-clean` for a fully fresh frontend checkout.
web-clean:
	rm -rf web/node_modules web/dist

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
build-muscles:
	@mkdir -p bin
	$(RUST_CARGO) build --release --manifest-path rust/Cargo.toml
	install -m 0755 rust/target/release/pipelinegen-muscles bin/pipelinegen-muscles

build: go-version-check web-build build-muscles
	@mkdir -p bin
	$(GO) build -ldflags "$(LDFLAGS)" -v -o bin/pipelinegen      ./cmd/server
	$(GO) build -ldflags "$(LDFLAGS)" -v -o bin/admin            ./cmd/admin
	$(GO) build -ldflags "$(LDFLAGS)" -v -o bin/worker           ./cmd/worker

# Build only the server binary and its embedded admin console. This is the
# canonical target for server smoke checks and lightweight container builds.
build-server: go-version-check web-build build-muscles
	@mkdir -p bin
	$(GO) build -ldflags "$(LDFLAGS)" -v -o bin/pipelinegen ./cmd/server

# Run Go unit tests (Go is the canonical test surface; tests here run
# for every `make test` invocation and in CI without requiring Node).
clean:
	rm -rf bin/
	rm -rf web/dist
	rm -f server admin pipelinegen worker
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
