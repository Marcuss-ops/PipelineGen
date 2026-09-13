# Live & end-to-end verification

**Owner**: this doc is the canonical reference for the **live / end-to-end
layer** — the retired tier-4 shell matrix, the 10-step pipeline E2E gate that
replaced it, and the auth contract for live HTTP surfaces.

**Headless gates (tiers 1–3, `make verify-main` … `make verify-release`) live in
[`verify-main-workflow.md`](verify-main-workflow.md).** Do not duplicate the
gate family or the dev loop here.

**Audience**: every operator running post-deploy or E2E certification.

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

### Other tracked end-to-end surfaces

- `internal/platform/httpserver/*_e2e_test.go` — in-process HTTP E2E over the
  canonical routes (clips process/destination/idempotency, jobs polling).
- `tests/e2e/**` — hermetic contract and replay/resume E2E tests.
- `internal/platform/media/rustexec/*_test.go` — the L2 Go adapter → Rust
  StockRust boundary (canonical `render_plan`, final audio copy, tamper
  hash-drift).
- `make gate-core-ready-tail` → `tests/operational/measure_core_ready_tail.sh`
  — CORE_READY tail invariants against recorded `script.generate` artifacts
  (no auth, no live service).
- Per-provider Go suites under `internal/capabilities/**` for the domains that
  the shell batteries used to probe.

Do not re-add a `*-live` target until its driver is a tracked, executable
artifact in the repository.

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

Their orphan scenario/fixture sets were removed on 2026-09-13
(`tests/operational/{boxers-generate,generate,generate-certification,fixtures}`).

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

- [`verify-main-workflow.md`](verify-main-workflow.md) — headless gates
  (tiers 1–3), dev loop, operator/credential boundaries.
- [`component-verification.md`](component-verification.md) — component
  registry, path ownership, invalidation set.
- `AGENTS.md` — operational rules, gate hierarchy, pre-push contract.
- `make/verify.mk`, `make/live.mk`, `make/verify.components.mk` — executable
  source of truth.
- `scripts/hooks/pre-push` — the pre-push wiring of `make verify-main`.
