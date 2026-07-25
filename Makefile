# Makefile - invocation entry only.
#
# Per AGENTS.md max_lines_per_file: 1000 plus the P2 Manutenibilita
# directive (July 2026), the canonical build chain is split into 7
# thematic includes under make/. The root holds ONLY include make/*.mk
# plus the default all: build target. Per-bucket targets, comments,
# and recipes live in their include.
#
# HONOUR-RULE (binding, July 2026) for git push: scripts/hooks/pre-push
# invokes make verify-main as the fail-closed pre-push gate. The
# verify-main target itself lives in make/verify.mk. A RED verify-main
# BLOCKS the push atomically. DO NOT bypass via git push --no-verify
# on NORMAL pushes (canonical exception: CI emergencies only, paired
# with a fixup! commit plus git rebase --autosquash once the red gate is
# fixed). The split-refactor preserves this contract byte-equivalent.

# Consolidated .PHONY declaration for the 7-include split. Declaring
# every public target up-front prevents a stray file in cwd named `build`,
# `test`, `clean`, etc. from silently winning the target-name race and
# masking the recipe (the canonical failure mode that motivates this
# block). One source of truth, grouped by include file so the ownership
# mapping is visible at-a-glance.
.PHONY: \
	help all \
	go-version-guard go-version-check node-version-check build clean rebuild run dev \
	test test-all test-unit test-js coverage coverage-check lint fmt vet \
	verify-go-core verify-go-infrastructure verify-go-api verify-go-commands verify-go-tests verify-go verify-unit \
	verify-no-secrets verify-base verify-foundation verify-static verify-fast \
	verify-node-native verify-node-tests verify-node verify-integration verify-architecture \
	verify-images verify-stock verify-main verify-release \
	regen-routes-yaml archcheck-strict \
	verify-artlist verify-artlist-startup verify-artlist-search verify-artlist-stream \
	verify-artlist-download verify-artlist-pipeline verify-artlist-drive verify-artlist-index \
	verify-artlist-cache verify-artlist-errors verify-artlist-live \
	verify-images-live verify-script-live verify-vidrush-live verify-artlist-scale-live verify-live \
	docker-build docker-build-worker docker-run docker-sign docker-digest \
	docker-verify-digest docker-verify-ffmpeg docker-bootstrap-smoke \
	test-qdrant-fixtures test-qdrant-fixtures-down \
	doctor artlist auth-check deps tidy-check vuln bench ci preflight \
	smoke smoke-script smoke-pipeline operate-script-generate smoke-run-all \
	smoke-voiceover smoke-dry regenerate-token \
	verify-format test-imports install-hooks regen-current-yaml scraper-up

# help - discoverability for the split Makefile. Curated cheat sheet of
# the high-traffic targets; for the FULL 88-target catalog see the
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
	@echo "  make build            Build server, admin, and worker entry-point binaries"
	@echo "  make run              Run server (HTTP + scheduler + maintenance via --mode all)"
	@echo "  make rebuild          Clean + build (idempotent equivalent of clean && build)"
	@echo ""
	@echo "TEST (unit, headless)"
	@echo "  make test             Go unit tests with race detector (fast)"
	@echo "  make test-all         Go unit tests + node-scraper Mocha suite"
	@echo "  make lint             golangci-lint run --timeout=5m"
	@echo "  make fmt              go fmt ./..."
	@echo "  make vet              go vet ./..."
	@echo ""
	@echo "VERIFY (gate chain; verify-fast first, then verify-main pre-push)"
	@echo "  make verify-fast      Foundation (toolchain + secrets + format + tidy) + static (vet + build)"
	@echo "  make verify-main      Pre-push headless gate: unit + node + architecture"
	@echo "  make verify-release   Pre-deploy gate: verify-main + integration"
	@echo "  make verify-live      Post-deploy operational battery (needs live external stack)"
	@echo "  make verify-unit      Race-tested Go unit tests by area (excludes ./tests/...)"
	@echo "  make verify-node      Node toolchain gate (native probe + Mocha)"
	@echo ""
	@echo "DOMAIN-SPECIFIC (live operational; require running server / external stack)"
	@echo "  make auth-check       Operator pre-flight against /api/artlist/job-consumer (fails closed)"
	@echo "  make doctor           GET /api/system/doctor with admin token"
	@echo "  make artlist          POST /api/artlist/run (TERM= LIMIT= STRATEGY=)"
	@echo "  make scraper-up       Bring up the Node artlist scraper sidecar (dev-loop)"
	@echo ""
	@echo "NEVER push when verify-main is RED. See scripts/hooks/pre-push for the gate."

include make/build.mk
include make/test.mk
include make/verify.mk
include make/artlist.mk
include make/live.mk
include make/docker.mk
include make/operations.mk

all: build
