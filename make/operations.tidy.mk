# make/operations.tidy.mk - thematic include (P2 Manutenibilita, July 2026).
# Sub-bucket of the former make/operations.mk (414 lines → 4 sub-files).
# Holds tidy / cleanup / format / CI targets: deps, tidy-check, vuln, bench,
# ci, preflight, verify-format, test-imports, install-hooks, regen-current-yaml.
# Root Makefile contains include make/*.mk plus all: build.

# ─── Removed targets (Blocco 1.2, June 2026) ──────────────────────────────────
# Two target definitions were stripped from this Makefile because they were
# the wrong integration mechanism for the referenced projects:
#
#   - google-accounting-run: orphaned. The `google-accounting/` directory
#     is NOT present on main, so the recipe always failed at `cd` with
#     "No such file or directory".
#
#   - comic-video-maker-run: `comic-video-maker/` is an ignored local
#     directory, not a tracked project on main. It must not be treated as
#     a root Make target or an implicit submodule. If the project becomes
#     canonical again, reintroduce it explicitly as a tracked service,
#     valid git submodule, or Compose component with its own contract.
#
# Phasing rule (binding): a top-level service that has its own
# subdirectory MUST live as a separate service / explicit submodule / Compose
# component, NEVER as a target in this root Makefile. The ignored local
# directory is intentionally untouched by repository verification.

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

# Run benchmarks. The local E2E benchmark must depend on preflight-e2e;
# existing Go microbenchmarks remain independent and read-only.
bench:
	$(GO) test -bench=. -benchmem ./...

# Canonical E2E benchmark gate. The actual workload is supplied by the
# operator through BENCHMARK_COMMAND; no workload is executed if preflight
# fails. Keep this target on main-only via scripts/preflight-e2e.sh.
benchmark-e2e: preflight-e2e
	@test -n "$(BENCHMARK_COMMAND)" || { echo "❌ BENCHMARK_COMMAND is required" >&2; exit 2; }
	@bash -lc '$(BENCHMARK_COMMAND)'

# benchmark-generate — canonical video generation benchmark. Uses
# scripts/bench/generate-video.sh which submits N jobs, polls for
# completion, and emits a structured JSON timing report.
#
# Modes:
#   BENCH_TOPIC=X          Generate clips from topic (default: Matt Damon)
#   BENCH_CLIP_ID=A,B,C    Render existing clip IDs
#   BENCH_WATERMARK=X      Apply watermark asset ID
#   BENCH_DRIVE_FOLDER=X   Upload to specific Drive folder
#
# Examples:
#   make benchmark-generate BENCH_TOPIC="Dune" BENCH_CLIPS=3
#   make benchmark-generate BENCH_CLIP_ID=asset_abc,asset_def
#   make benchmark-generate BENCH_TOPIC="Interstellar" BENCH_WATERMARK=wm_xyz
benchmark-generate: preflight-e2e
	@_CLIP_ARGS=""; \
	if [ -n "$(BENCH_CLIP_ID)" ]; then \
		IFS=',' read -ra _IDS <<< "$(BENCH_CLIP_ID)"; \
		for _id in "$${_IDS[@]}"; do _CLIP_ARGS="$${_CLIP_ARGS} --clip-id $${_id}"; done; \
	fi; \
	bash scripts/bench/generate-video.sh \
		$${_CLIP_ARGS} \
		$${BENCH_TOPIC:+--topic "$(BENCH_TOPIC)"} \
		$${BENCH_TOPIC:+--clips "$(BENCH_CLIPS:-5)"} \
		${BENCH_WATERMARK:+--watermark $(BENCH_WATERMARK)} \
		${BENCH_DRIVE_FOLDER:+--drive-folder $(BENCH_DRIVE_FOLDER)} \
		${BENCH_OUTPUT:+--output $(BENCH_OUTPUT)}
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
preflight: go-version-check node-version-check
	@if command -v docker >/dev/null 2>&1; then \
		docker compose config >/dev/null; \
	else \
		echo "warning: 'docker compose config' skipped — docker not on PATH"; \
	fi
	@grep -q '^FROM .*golang:1.25' Dockerfile
	@test -d migrations/sqlite || { echo "❌ migrations/sqlite/ missing — server cannot apply migrations" >&2; exit 1; }
	@N=$$(ls -1 migrations/sqlite/*.sql 2>/dev/null | wc -l); if [ "$${N}" -eq 0 ]; then echo "❌ migrations/sqlite/ is empty — no .sql files found" >&2; exit 1; fi
	@test -f .env || echo "⚠️  .env not found — copy .env.example to .env (server may refuse boot without VELOX_ADMIN_TOKEN)" >&2
	@echo "✅ SQLite migrations: $$N files"
	@echo "Preflight passed"

# preflight-e2e — canonical runtime dependency matrix. This is deliberately
# separate from the repository/config preflight above: it probes the live
# services and is the mandatory dependency of the E2E benchmark target.
preflight-e2e:
	@bash scripts/preflight-e2e.sh

# Canonical local E2E lifecycle. The harness owns only its recorded local
# processes and the Compose project; it never mutates migrations or databases.
e2e-up:
	@bash scripts/dev/e2e-up.sh up
e2e-status:
	@bash scripts/dev/e2e-up.sh status
e2e-down:
	@bash scripts/dev/e2e-up.sh down

# dev-up — deterministic staged startup with readiness gates at each stage.
# Startup order: Infrastructure → Server → Worker → Preflight.
# Each stage blocks until its readiness probe passes or times out.
# Environment overrides:
#   E2E_READINESS_TIMEOUT=60    Per-service timeout (default 60s)
#   E2E_SKIP_PREFLIGHT=1        Skip preflight (debugging only)
#   E2E_START_LOCAL_PROCESSES=1 Start server/worker locally instead of Compose
dev-up:
	@bash scripts/dev/e2e-up.sh dev-up
dev-down:
	@bash scripts/dev/e2e-up.sh dev-down

# velox — canonical operations CLI. Single entry point for lifecycle,
# API, database, and environment operations. Never run raw curl,
# sqlite3, or source .env — use this instead.
velox:
	@bash scripts/ops.sh $(ARGS)

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
