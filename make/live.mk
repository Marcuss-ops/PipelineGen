# make/live.mk - thematic include (P2 Manutenibilita, July 2026).
#
# Per AGENTS.md max_lines_per_file: 1000 plus the P2 directive,
# the canonical build chain is split into 7 thematic includes.
# This file holds the live/artifact-bound gates. The 2026-09-13 retirement
# removed the batteries whose drivers no longer existed; the pipeline E2E gate
# below is the one live gate whose driver is a tracked, executable artifact.
# Root Makefile contains include make/*.mk plus all: build.

# ─── Post-deploy live batteries: RETIRED 2026-09-13 ──────────────────
#
# Every target in this section (verify-live, verify-images-live,
# verify-script-live, verify-nlp-online-images-docs-live,
# test-intro-hook-stock-live, verify-vidrush-live, verify-artlist-scale-live)
# invoked a shell driver deleted by commit 7e6965aab ("purge 94% shell + 87%
# python dust"): tests/operational/{test2_images.sh,generate/run.sh,
# vidrush_script_generate_e2e.sh,artlist_scale_e2e.sh,
# boxers-generate/run_intro_hook_stock.sh} and
# scripts/verify_nlp_online_images_docs_certification.sh are all absent. A
# target whose only possible outcome is "No such file or directory" is not a
# gate (see the certification-driver retirement note in make/verify.mk) and it
# kept the post-deploy matrix advertised in AGENTS.md, docs/operations/
# verify-release-and-live.md and .github/workflows/{ci,nightly,manual}.yml
# alive while nothing could run.
#
# Live coverage is currently owned by the Go surfaces that replaced the shell
# layer: internal/platform/httpserver E2E tests, tests/e2e/**, and the
# per-provider Go suites under internal/capabilities/**/[provider]/... . Do not
# re-add a target here until its driver is a tracked, executable artifact.

# ─── Pipeline E2E gate: 10 steps (September 2026) ─────────────────────
#
# verify-pipeline-e2e — HERMETIC half. In-process contract battery over the
# real gin router and the real handlers (YouTube clips, unified search,
# stock pipeline, idempotency middleware). No server, no token, no Drive, no
# pgvector: it proves the wiring and the wire contracts. Step 8 pins the
# Drive-artifact CONTRACT SHAPE only — it never claims a file was produced.
#
# verify-pipeline-e2e-live — LIVE half, the only place that asserts reality:
# a real Drive file under the requested folder, an MP4 >100 KB with a
# decodable video stream, and the asset retrievable from the canonical
# catalog. 10 steps, 10/10 required; it depends on auth-check because every
# step hits /api/* with the admin token.
#
#   make verify-pipeline-e2e                      # hermetic, always runnable
#   make verify-pipeline-e2e-live                 # needs server + token + Drive
#   DRIVE_ROOT_FOLDER_ID=<id> STOCK_DIRECT_URL=<mp4 url> \
#     bash tests/operational/pipeline_live_e2e.sh --dry
#
# Both halves share one step map; see tests/operational/pipeline_live_e2e.sh
# for the per-step acceptance criteria.
.PHONY: verify-pipeline-e2e verify-pipeline-e2e-live
verify-pipeline-e2e:
	@echo "→ Pipeline E2E contract battery (hermetic, in-process)"
	@$(GO) test -count=1 -timeout 10m -run TestPipelineE2E ./internal/platform/httpserver/
	@echo "✅ verify-pipeline-e2e passed"

verify-pipeline-e2e-live: auth-check
	@echo "→ Pipeline live E2E (10 steps: YouTube + Stock → Drive + catalog)"
	@bash tests/operational/pipeline_live_e2e.sh
	@echo "✅ verify-pipeline-e2e-live passed"

# ─── CORE_READY tail gate (September 2026) ─────────────────────────────
#
# gate-core-ready-tail — validates the CORE_READY tail invariants against
# recorded script.generate E2E artifacts. The two independent tail
# reconstructions (the recorded critical-path chain after `persistence` vs the
# stage durations document + complete_finalize + post_writer_finalize) must
# agree within the run's own unattributed_ms. A disagreement means the timing
# model regressed and the tail number must not be quoted.
#
# It reads stored JSON only — no auth, no live service, no Chrome — which is
# why it carries no auth-check dependency and is safe for the pre-push chain.
#
# Scoped to the shipped E2E corpora by default. Point it at a fresh run:
#   make gate-core-ready-tail RESULTS_DIRS="tests/operational/results/<run-dir>"
# Budget a tail ceiling in milliseconds:
#   make gate-core-ready-tail MAX_TAIL_MS=30000
# Enforce the artifact projection invariant (an artifact carrying a CORE_READY
# stage must also carry core_ready_ms) on runs produced by a binary that
# includes the projection:
#   make gate-core-ready-tail CORE_READY_ENFORCE_PROJECTION=1
.PHONY: gate-core-ready-tail
# audit-repro: tail = wall_ms - core_ready_ms on a recorded run; see
# docs/tickets/TICKET-CORE-READY-DURABLE-DAG.md section 7 for the baseline.
gate-core-ready-tail:
	@CORE_READY_ENFORCE_PROJECTION="$(CORE_READY_ENFORCE_PROJECTION)" \
	 MAX_TAIL_MS="$(MAX_TAIL_MS)" \
	 bash tests/operational/measure_core_ready_tail.sh --gate $(RESULTS_DIRS)

# verify-images — quick verification dedicated to the Images module.
