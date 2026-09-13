# Verify-Release Workflow (tier 3)

**Owner**: this doc is the operator-facing canonical reference for
`make verify-release`, the tier-3 pre-deploy gate.

**Lockstep surface**: complements
`docs/operations/verify-main-workflow.md` (tier 1 + 2 — dev loop + pre-push
headless). Do not duplicate the per-area Make-target reference or the
recommended dev-loop workflow that already lives there.

**Tier 4 (`make verify-live`) was retired on 2026-09-13.** See
[Retired: tier-4 live batteries](#retired-tier-4-live-batteries) below. The
single replacement live gate is the 10-step pipeline E2E battery — see
[Current live gate](#current-live-gate-pipeline-e2e-10-steps).

**Audience**: every operator running pre-deploy certification.

---

## When to run `make verify-release`

`make verify-release` is the **pre-deploy gate (tier 3)**.

### Composition (per Makefile)

```text
verify-release  =  verify-full  +  verify-integration
                 =  (verify-main + verify-race)
                 with shared foundation prerequisites deduplicated by Make
                 +  verify-integration   (= verify-go-tests = the ./tests/... suite)
```

Verify the live position with `grep -nE '^verify-release:' make/verify.mk` —
do not hardcode line numbers; the Make fragments are the SSOT.

### When to run

- **After every merge commit lands on `main`** (post-merge verification of
  the integration surface).
- **Before triggering the deploy job** (final pre-deploy gate).
- **On a fresh clone or after `git pull origin main`** — catches drift
  between the pushed state and the deployed branch.

### What it costs

A few minutes for the inherited `verify-main` chain (headless tier 2), plus
several minutes more for `verify-integration` (Go tests under `./tests/...` —
some suites exercise cross-package integration surfaces and may depend on
Drive / Qdrant / scraper fixtures). These are **approximate budgets**;
measure on the actual operational host before relying on them for scheduling.

### What to do if RED (fail-closed)

Per AGENTS.md fail-closed + "never represent absence as success":

- **DO NOT proceed to deploy.**
- Identify the failing sub-gate. `verify-release` fails atomically — the
  sub-gate that printed the first non-zero exit is the culprit. Re-run each
  sub-gate individually:

```bash
make verify-main          # tier 2: foundation + static + changed components + architecture
make verify-race          # explicit race gate
make verify-integration   # ./tests/... suite
```

- Fix the failing gate and re-run the whole chain. There is no bypass flag.

---

## Retired: tier-4 live batteries

`make verify-live` and every member battery it composed
(`verify-images-live`, `verify-artlist-live`, `verify-script-live`,
`verify-vidrush-live`, `verify-vidrush-maya`/`-dry`, `verify-artlist-scale-live`,
`verify-nlp-online-images-docs-live`, `test-intro-hook-stock-live`,
`verify-stock-live`, `verify-stock-release`) invoked shell drivers that were
deleted by commit `7e6965aab` ("purge 94% shell + 87% python dust"). The
targets and their CI jobs were removed together:

| Former surface | Retired driver (absent) | Removed from |
|---|---|---|
| `verify-images-live` | `tests/operational/test2_images.sh` | `make/live.mk`, `verify-live` |
| `verify-script-live` | `tests/operational/generate/run.sh` | `make/live.mk`, `verify-live` |
| `verify-vidrush-live` | `tests/operational/vidrush_script_generate_e2e.sh` | `make/live.mk`, `verify-live` |
| `verify-vidrush-maya` | `tests/operational/maya_vidrush_e2e.sh` | `make/operations.smoke.mk` (deleted) |
| `verify-artlist-live` + 9 granular gates | `tests/operational/artlist/0{1..9}_*.sh`, `run_all.sh` | `make/artlist.mk` (deleted) |
| `verify-artlist-scale-live` | `tests/operational/artlist_scale_e2e.sh` | `make/live.mk` |
| `verify-stock-live` / `verify-stock-release` | `tests/operational/stock_e2e_full_battery.sh`, `scripts/ci/verify-stock-{receipt,claim}.sh` | `make/youtube_stock.mk`, `ci.yml`, `nightly.yml`, `manual.yml` |
| `verify-nlp-online-images-docs-live` | `scripts/verify_nlp_online_images_docs_certification.sh` | `make/live.mk` |
| `test-intro-hook-stock-live` | `tests/operational/boxers-generate/run_intro_hook_stock.sh` | `make/live.mk` |

Rationale: a target (or CI job) whose only possible outcome is
"No such file or directory" is not a gate — it trains operators to ignore red
output and it keeps stale certification claims alive in AGENTS.md and in this
directory. The same reasoning retired the `certify-*` driver targets (see the
retirement note at the bottom of `make/verify.mk`).

**Current live/end-to-end coverage** is owned by tracked surfaces:

- `tests/operational/pipeline_live_e2e.sh` — the 10-step live battery
  (`make verify-pipeline-e2e-live`), the only live gate; see
  [Current live gate](#current-live-gate-pipeline-e2e-10-steps).
- `internal/platform/httpserver/server_pipeline_e2e_test.go` — the hermetic
  twin of the same 10 steps (`make verify-pipeline-e2e`).
- `internal/platform/httpserver/*_e2e_test.go` — in-process HTTP E2E over the
  canonical routes (clips process/destination/idempotency, jobs polling).
- `tests/e2e/**` — hermetic contract and replay/resume E2E tests.
- `internal/platform/media/rustexec/*_test.go` — the L2 Go adapter → Rust
  StockRust boundary (canonical `render_plan`, final audio copy, tamper
  hash-drift).
- Per-provider Go suites under `internal/capabilities/**` for the domains that
  the shell batteries used to probe.

Do not re-add a `*-live` target until its driver is a tracked, executable
artifact in the repository.

---

## Current live gate: pipeline E2E (10 steps)

The battery replaced the retired tier-4 matrix with one honest gate. It ships
in two layers, and both are required before quoting PipelineGen E2E coverage:

| Layer | Target | Driver | Needs |
|---|---|---|---|
| Hermetic (always runnable) | `make verify-pipeline-e2e` | `internal/platform/httpserver/server_pipeline_e2e_test.go` (`TestPipelineE2E`) | nothing — in-process router, real handlers, stubbed external edges |
| Live (asserts reality) | `make verify-pipeline-e2e-live` | `tests/operational/pipeline_live_e2e.sh` | running server + `VELOX_ADMIN_TOKEN` + `DRIVE_ROOT_FOLDER_ID` + `STOCK_DIRECT_URL`; depends on `auth-check` |

Ten steps, **10/10 PASS required**: YouTube keyword discovery → metadata →
`clips/process` → Drive hierarchy → catalog retrieval; then Stock
`/run` (direct URL) → `/search-and-run` → Drive artifact → catalog retrieval +
real download (>100 KB, decodable video stream, duration > 0) → idempotent
replay with no second canonical identity.

The live layer is the ONLY place that asserts a real Drive file, a decodable
MP4, or a catalog hit; the hermetic layer pins the wire contracts, the
destination normalization, the legacy-destination rejection and the
idempotency replay, and step 8 of the hermetic battery pins the Drive-artifact
CONTRACT SHAPE only — it never claims a file was produced.

Plan a run without touching the stack:

```bash
export DRIVE_ROOT_FOLDER_ID=<drive folder id>
export STOCK_DIRECT_URL=https://.../test-video.mp4
bash tests/operational/pipeline_live_e2e.sh --dry
```

A green run retains its payloads, submissions, job/status snapshots and the
downloaded MP4 under `tests/operational/results/pipeline-live/`.

The gate is registered in `config/verify-components.json`
(`stock.live_tests`) and runs only when the component runner is invoked with
live scope enabled — it is never part of the push/pre-push chain.

---

## Auth contract (mandatory for any authenticated HTTP surface)

- Canonical secret file: `/etc/pipelinegen/pipelinegen.env` (mode `0640`,
  owner `root:pipelinegen-agents`), exported as `TOKEN_FILE`.
- Canonical variable: `VELOX_ADMIN_TOKEN` (64-hex). No alternates.
- Agents and operators never read the file directly: route through
  `scripts/with-velox-auth`, which validates the shape, exports, and `exec`s.
- `make auth-check` is the canonical fail-closed probe (non-200 on
  `/api/artlist/job-consumer` fails the target). It never prints the token.
- Redact token values as `REDACTED` or `<64-hex>` in every captured output.

---

## SSOT cross-references

- `docs/operations/verify-main-workflow.md` — tier 1 + 2 SSOT (complementary,
  not duplicate).
- `AGENTS.md` — operational rules, gate hierarchy, pre-push contract.
- `make/verify.mk`, `make/verify.components.mk` — executable source of truth.
- `scripts/hooks/pre-push` — the pre-push wiring of `make verify-main`.
