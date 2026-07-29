# make/operations.smoke.mk - thematic include (P2 Manutenibilita, July 2026).
# Sub-bucket of the former make/operations.mk (414 lines → 4 sub-files).
# Holds the remaining black-box smoke test targets.
# Root Makefile contains include make/*.mk plus all: build.

# ─── Black-box smoke tests ──────────────────────────────────────────────────
# Per operator policy (AGENTS.md): never modify internal/application/scripts,
# internal/api/script, internal/app/wire_script.go, or production business
# logic for tests. Hammer the live HTTP surface under tests/operational/.

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

# VidRush operational battery — runs all 12 scenarios via full_battery.sh.
# Fail-closed: exits non-zero if any scenario fails.
# Requires: SMOKE_TOKEN set, server running on SMOKE_API_BASE.
verify-vidrush:
	@bash tests/operational/vidrush/full_battery.sh

# Dry-run for the VidRush battery. Prints all would-be invocations, exits 0.
verify-vidrush-dry:
	@bash tests/operational/vidrush/full_battery.sh --dry

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
