# Operational tests (`tests/operational/`)

This directory holds the **live / artifact-bound operational checks** that
cannot run inside the headless pre-push chain. Everything here either drives the
external HTTP surface of a running PipelineGen (and therefore needs a token, a
server, and occasionally Drive), or reads recorded run artifacts.

Headless verification lives elsewhere: `internal/**/*_test.go`, `tests/e2e/**`
(hermetic contract/E2E) and the registry-driven `make verify-*` gates.

## What is in the tree

| Path | Purpose | Entry point |
|---|---|---|
| `pipeline_live_e2e.sh` | **Canonical live gate.** 10 steps: YouTube discovery → metadata → `clips/process` → Drive hierarchy → catalog retrieval, then Stock `/run` → `/search-and-run` → Drive artifact → catalog retrieval + real download → idempotent replay. Requires server + token + `DRIVE_ROOT_FOLDER_ID` + `STOCK_DIRECT_URL`; 10/10 PASS required. Hermetic twin: `internal/platform/httpserver/server_pipeline_e2e_test.go` | `make verify-pipeline-e2e-live` / `make verify-pipeline-e2e` / `bash tests/operational/pipeline_live_e2e.sh [--dry]` |
| `person_overlay_drive_e2e.sh` | Live E2E: text → PERSON/important_phrases → entity image catalog/Drive → timed OverlayPlan → Chronon overlay render → Drive | `bash tests/operational/person_overlay_drive_e2e.sh [--dry]` |
| `jordan_entity_overlay_drive_e2e.sh` | Live E2E canary for the multi-entity (five-person) overlay/Drive path | `bash tests/operational/jordan_entity_overlay_drive_e2e.sh [--dry]` |
| `measure_core_ready_tail.sh` | Validates the CORE_READY tail invariants against **recorded** `script.generate` artifacts (no auth, no live service) | `make gate-core-ready-tail` |
| `lib/common.sh` | Source-able bash helper library: `smoke_curl`, `smoke_poll_terminal`, `smoke_require`, token redaction, `--dry-run`, timeouts | sourced by the scripts above |
| `results/` | Retained run artifacts (production-shaped timings). Output-only: never read by code, never a gate input. See AGENTS.md for the retention decision. |
| `voiceover_*_test.go`, `voiceover_harness.go` | Go E2E harness for the voiceover vertical slice (`make smoke-voiceover`) | |
| `worker-integration/`, `generate/`, `generate-certification/`, `boxers-generate/`, `vidrush/`, `fixtures/` | Scenario manifests, payload fixtures and result baselines consumed by the Go suites and by operators | |

## Pipeline E2E gate — two layers, one step map

The pipeline gate exists twice on purpose, and each layer claims only what it
can prove:

| Layer | Driver | Asserts |
|---|---|---|
| Hermetic (always runnable) | `internal/platform/httpserver/server_pipeline_e2e_test.go` | real router + real handlers + real idempotency middleware; wire contracts, destination normalization, legacy top-level destination rejection, replay caching. Step 8 pins the Drive-artifact **contract shape** only |
| Live (asserts reality) | `pipeline_live_e2e.sh` | a real Drive file under the requested folder, an MP4 >100 KB with a decodable video stream, and the asset retrievable from the canonical catalog |

A hermetic PASS is never sufficient to quote E2E coverage, and a live run is
never required to catch a wiring regression. Both are registered against the
`stock` component (`config/verify-components.json`: `stock.live_tests`); the
live layer only runs when the component runner is invoked with live scope.

Environment for the live layer:

| Variable | Required | Notes |
|---|---|---|
| `DRIVE_ROOT_FOLDER_ID` | yes | real destination root for steps 3/4/6/7/8 |
| `STOCK_DIRECT_URL` | yes | public http(s) MP4 for the direct-URL run |
| `VIDEO_URL` | no | pins step 2/3; otherwise the URL discovered in step 1 is used |
| `PIPELINE_E2E_JOB_ID` | no | skips the `youtube_clip.extract` job lookup in step 3 |
| `PIPELINE_E2E_YT_QUERY` / `_STOCK_QUERY` / `_RUN_TAG` / `_RESULTS_DIR` / `_POLL_TIMEOUT_SECONDS` | no | overrides with documented defaults |

`POST /api/clips/process` returns an ACK only (no `job_id`), so step 3 locates
its job through `GET /api/jobs?type=youtube_clip.extract` by matching the
segment name; `POST /api/media/search` returning 503 means the search backend
is not mounted — reported verbatim, never softened into a PASS.

## Exit-code discipline (`lib/common.sh`)

| Code | Meaning |
|---|---|
| `0` | Every assertion passed. |
| `1` | One or more assertions failed. |
| `2` | Setup error (unknown flag, missing token, missing binary). |
| `124` | Poll loop or wall-clock timeout exceeded. |

Output is funnelled through `smoke_echo_safe()`, which redacts
`Authorization: Bearer …`, bare `Bearer …`, JSON `"token":"…"` and
`VELOX_ADMIN_TOKEN=…`. Never print a token directly.

## Environment

| Variable | Default | Notes |
|---|---|---|
| `API_BASE` | `127.0.0.1:${VELOX_PORT:-8080}` | `host:port`; the script prepends `http://`. |
| `VELOX_PORT` | `8080` | Honoured by `API_BASE` resolution. |
| `TOKEN_FILE` | `/etc/pipelinegen/pipelinegen.env` | Canonical env file read by `scripts/with-velox-auth`. |
| `NO_COLOR` | unset | Non-empty disables ANSI colours. |
| `SMOKE_*` | see `lib/common.sh` | Timeouts, poll interval, dry-run switch. |

## Retired shell batteries

The following operational batteries and their `make` targets were retired on
2026-09-13, after their drivers were deleted by commit `7e6965aab`
("purge 94% shell + 87% python dust"): the Stock battery
(`stock_e2e_*_smoke.sh` + `stock_e2e_full_battery.sh`), the Artlist DoD battery
(`artlist/0{1..9}_*.sh` + `run_all.sh`), the VidRush battery
(`vidrush/full_battery.sh` + `run_scenario.sh`), the black-box smoke suite
(`startup_smoke.sh`, `text_script_smoke.sh`, `failed_job_smoke.sh`,
`fase_b_clip_pipeline_smoke.sh`) and the one-off live probes
(`test2_images.sh`, `generate/run.sh`, `vidrush_script_generate_e2e.sh`,
`artlist_scale_e2e.sh`, `maya_vidrush_e2e.sh`,
`boxers-generate/run_intro_hook_stock.sh`, `youtube_stock_live_e2e.sh`,
`stockrust_live_e2e.sh`).

A target or CI job whose only possible outcome is "No such file or directory" is
not a gate. Nothing may reference a path here that is not in the tree; do not
re-add a battery until its driver is a tracked, executable artifact.
