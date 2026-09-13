# Makefile - invocation entry only.
#
# Per AGENTS.md max_lines_per_file: 1000 plus the P2 Manutenibilita
# directive (July 2026), the canonical build chain is split into
# thematic includes under make/. The root holds ONLY include make/*.mk
# plus the default all: build target. Per-bucket targets, comments,
# and recipes live in their include. Runtime shell consumers use the
# canonical fallback VELOX_PORT:-8000.
#
# HONOUR-RULE (binding, July 2026) for git push: scripts/hooks/pre-push
# invokes make verify-main as the fail-closed pre-push gate. The
# verify-main target itself lives in make/verify.mk. A RED verify-main
# BLOCKS the push atomically. DO NOT bypass via git push --no-verify
# on NORMAL pushes (canonical exception: CI emergencies only, paired
# with a fixup! commit plus git rebase --autosquash once the red gate is
# fixed). The split-refactor keeps this contract on the registry-driven
# verify-main gate while exposing heavier checks through verify-race and verify-full.

# Consolidated .PHONY declaration for the make/*.mk split. Declaring
# every public target up-front prevents a stray file in cwd named `build`,
# `test`, `clean`, etc. from silently winning the target-name race and
# masking the recipe (the canonical failure mode that motivates this
# block). One source of truth, grouped by include file so the ownership
# mapping is visible at-a-glance.
.PHONY: \
	help all \
	go-version-guard go-version-check build build-muscles build-server clean rebuild run dev \
	test test-all test-unit coverage coverage-check lint fmt vet \
	verify-go-core verify-go-infrastructure verify-go-api verify-go-commands verify-go-tests verify-go verify-unit verify-unit-fast \
	verify-audio-chunked verify-audio-combined verify-audio-copy verify-audio-benchmark verify-audio-release \
	verify-rust-muscles verify-no-secrets verify-repository-integrity verify-base verify-foundation verify-static verify-fast verify-dev verify-agent verify-push verify-changed verify-changed-components verify-components verify-race-components verify-unit-race verify-race verify-full \
	verify-integration verify-architecture \
	verify-images verify-script verify-research verify-clips	verify-qdrant verify-indexing verify-drive verify-docs verify-voiceover verify-translation verify-timeline verify-storage verify-database verify-jobs verify-api	verify-ollama verify-youtube verify-artlist verify-kernel verify-main test-main-stock verify-main-clip verify-release \
	verify-race-script verify-race-research verify-race-clips verify-race-stock verify-race-qdrant verify-race-indexing verify-race-drive verify-race-docs verify-race-voiceover verify-race-images verify-race-translation verify-race-timeline verify-race-storage verify-race-database verify-race-jobs verify-race-api	verify-race-ollama verify-race-youtube verify-race-artlist verify-race-kernel \
	whisper-preflight \
	verify-component-coverage verify-reconciliation-contracts verify-orphan-cleanup verify-retention verify-cancel-recovery verify-migrations verify-migration-upgrade verify-db-integrity verify-qdrant-rebuild \
	regen-routes-yaml archcheck-strict \
	verify-artlist \
	test-youtube-url test-youtube-metadata test-youtube-transcript test-highlight-selection \
	verify-stock-unit verify-stock-integration \
	test-stock-component test-stock-download test-stock-cut test-stock-cache test-stock-dedupe test-stock-index \
	test-stock-recovery test-stock-youtube-e2e benchmark-stock-download \
	test-stock-acquisition test-stock-indexing \
	test-youtube-highlights test-stock-download-plan test-stock-partial-download test-stock-drive \
	test-stock-concurrency test-race-youtube-stock test-youtube-stock-fast test-youtube-stock-local \
	test-youtube-stock-resilience test-youtube-stock-release benchmark-youtube-stock \
	diagnose-youtube-stock \
	verify-vidrush-contract verify-sceneir verify-visualner verify-mediasampler verify-stockintelligence verify-vidrush-semantic verify-media-intelligence vidrush-pre-final verify-vidrush-extraction verify-vidrush-query-planning \
	verify-vidrush-artlist-search verify-vidrush-artlist-download verify-vidrush-artlist-persist verify-vidrush-artlist-index \
	verify-vidrush-image-search verify-vidrush-image-download verify-vidrush-image-validation verify-vidrush-image-persist verify-vidrush-image-index \
	verify-vidrush-image-generation verify-vidrush-image-generation-cache verify-vidrush-image-generation-persist \
	verify-vidrush-binding verify-vidrush-dedupe verify-vidrush-cache verify-vidrush-recovery verify-vidrush-concurrency \
	verify-vidrush-fast verify-vidrush-local verify-vidrush-resilience verify-vidrush-release \
	benchmark-vidrush doctor-vidrush \
	docker-build docker-build-worker docker-run docker-digest \
	docker-verify-whisper \
	verify-pipeline-e2e verify-pipeline-e2e-live \
	test-qdrant-fixtures test-qdrant-fixtures-down \
	test-postgres test-postgres-down \
	doctor artlist auth-check regenerate-token \
	smoke-voiceover \
	deps tidy-check vuln bench bench-cliprender benchmark-e2e e2e-up e2e-status e2e-down dev-up dev-down velox ci preflight preflight-e2e verify-format test-imports install-hooks regen-current-yaml

# help - discoverability for the split Makefile. Curated cheat sheet of
# the high-traffic targets; for the FULL ~90-target catalog see the
# .PHONY block above. Each line below is a real TAB-indented @echo
# recipe step (NOT a heredoc body) because GNU Make 3.82+'s default
# per-line invocation runs each TAB-indented recipe line in a separate
# shell; a multi-line heredoc body across recipe lines would otherwise
# have its body lines parsed as orphan shell commands. .ONESHELL: was
# explicitly REJECTED here to preserve default semantics for recursive
# $(MAKE) calls in verify.mk + per-line error semantics in the smoke
# recipes under tests/operational.
help:
	@echo "PipelineGen Makefile - Command Cheat Sheet"
	@echo ""
	@echo "BUILD / RUN"
	@echo "  make build            Build server, admin, and worker binaries"
	@echo "  make build-muscles    Build the Rust media execution binary"
	@echo "  make build-server     Build server binary"
	@echo "  make run              Run server (HTTP + scheduler + maintenance via --mode all)"
	@echo "  make rebuild          Clean + build (idempotent equivalent of clean && build)"
	@echo ""
	@echo "TEST (unit, headless)"
	@echo "  make test             Go unit tests with race detector (fast)"
	@echo "  make test-all         Go unit tests"
	@echo "  make lint             golangci-lint run --timeout=5m"
	@echo "  make fmt              go fmt ./..."
	@echo "  make vet              go vet ./..."
	@echo ""
	@echo "VERIFY (registry-driven gate chain; foundation once per aggregate)"
	@echo "  make verify-fast      Foundation (toolchain + secrets + repository integrity + format + tidy) + static (vet + build)"
	@echo "  make verify-agent     Agent dev loop: foundation + static + only impacted component tests (1-3 min)"
	@echo "  make verify-repository-integrity  Validate tracked gitlinks against .gitmodules"
	@echo "  make verify-main      Daily headless gate: foundation + static + changed components + architecture"
	@echo "  make test-main-stock   Diagnostic Stock-focused gate (non-authoritative)"
	@echo "  make verify-main-clip   Fast Clip gate: targeted tests + architecture"
	@echo "  make verify-race      Explicit race gate: unit + all registered components"
	@echo "  make verify-full      Full headless gate: main + race"
	@echo "  make verify-release   Pre-deploy gate: verify-full + integration"
	@echo "  make verify-unit      Race-tested Go unit tests by area (excludes ./tests/...)"
	@echo "  make verify-components   All registered components (fast)"
	@echo "  make verify-race-components  All registered components (race)"
	@echo "  make verify-script       Script component"
	@echo "  make verify-stock-unit        Stock unit/contract gate"
	@echo "  make verify-clips        Clips component"
	@echo "  make verify-drive        Drive component"
	@echo "  make verify-research     Research component"
	@echo "  make verify-qdrant       Qdrant component"
	@echo "  make verify-indexing     Indexing component"
	@echo "  make verify-docs         Documents component"
	@echo "  make verify-voiceover    Voiceover component"
	@echo "  make verify-database     Database component"
	@echo "  make verify-jobs         Jobs component"
	@echo "  make verify-component-coverage  Fail-closed registry coverage gate"
	@echo ""
	@echo "BENCHMARK"
	@echo "  make bench-cliprender   Canonical clip.render benchmark (headless; no server/GPU needed)"
	@echo "  make benchmark-e2e       Run BENCHMARK_COMMAND behind the live preflight"
	@echo ""
	@echo "BENCHMARK (require running server + /ready)"
	@echo "  make dev-up              Deterministic staged startup (Infrastructure→Server→Worker→Preflight)"
	@echo "  make dev-down            Stop all services + remove orphans"
	@echo "  make velox ARGS='doctor' Operations CLI (velox up|down|api|query|doctor|env)"
	@echo "  make e2e-up              Start E2E harness + preflight (fast, no readiness gates)"
	@echo "  make e2e-down            Stop E2E harness"
	@echo "  make e2e-status          Show E2E harness status + preflight"
	@echo ""
	@echo "DOMAIN-SPECIFIC (live operational; require running server / external stack)"
	@echo "  make auth-check       Operator pre-flight against /api/artlist/job-consumer (fails closed)"
	@echo "  make verify-pipeline-e2e       Hermetic 10-step pipeline contract battery (no server needed)"
	@echo "  make verify-pipeline-e2e-live  Live 10-step pipeline gate (server + token + Drive; 10/10 required)"
	@echo "  make doctor           GET /api/system/doctor with admin token"
	@echo "  make artlist          POST /api/artlist/run (TERM= LIMIT= STRATEGY=)"
	@echo ""
	@echo "NEVER push when verify-main is RED. See scripts/hooks/pre-push for the gate."

include make/build.mk
include make/test.mk
include make/verify.mk
include make/verify.components.mk
include make/reconciliation.mk
include make/youtube_stock.mk
include make/live.mk
include make/vidrush.mk
include make/docker.mk
include make/operations.auth.mk
include make/operations.smoke-voiceover.mk
include make/operations.tidy.mk
include make/audio.mk

all: build
