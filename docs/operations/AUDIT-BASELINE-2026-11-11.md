# Audit baseline — 2026-11-11

Status: **frozen before remediation**. This file is the canonical snapshot to
compare every later run against. It records what exists, not what is planned.

## 1. Repository identities

| Repository | Path | Branch | HEAD | Dirty at capture |
| --- | --- | --- | --- | --- |
| Monorepo (VeloxEditing) | `.` | `main` | `a043a2b55a74ee9b86a0975a742ff23b0eb6ba41` | 48 untracked/modified paths |
| PipelineGen | `refactored` | `main` | `57e0ee0ccb8abdda30a8dcd05ae8a394cf900245` | remediation worktree |
| RenderingGen | `RenderingGen` | `main` | `9f1bda952a2e1124c7d3df63761662e888ac5b6e` | remediation worktree |

Submodules of the monorepo at capture:

```text
 59b5fa25680b80c51a18cd79ffe721fd6ca12395 BrainRust (v0.1.0-37-g59b5fa2)
-0ac4ef26a6e97e067b5f9a2f8e9b3a98601e3e7c Chronon3d
-e6d06874ef9f2f95fd7b6c2b893ba027da1ee5a7 ChrononMotion3D
-bdb91c7a2611cc11913046486f5eaca5bba267fe ChrononPlugin
 77b419b1d692b5e659d4d2818dc1b5285ab2ec73 ChrononTemplate (heads/main)
 9f1bda952a2e1124c7d3df63761662e888ac5b6e RenderingGen (heads/main)
 57e0ee0ccb8abdda30a8dcd05ae8a394cf900245 refactored (worker-calendar-v1.1.0-276-g57e0ee0cc)
-13f9fc81c491aec66ec581fabd53d5d1caa446e7 refactored/rust
```

The two audit targets are therefore exactly the SHAs named in the audit:
PipelineGen `57e0ee0…` and RenderingGen `9f1bda9…`.

## 2. Toolchain and host

| Fact | Value |
| --- | --- |
| OS | Linux amd64 |
| Go (PipelineGen module) | `go1.26.10` |
| Go (RenderingGen module) | `go1.25.9` |
| ffprobe | `7.0.2-static` |
| jq | `1.6` |
| `git diff --check` | clean in both audit repositories |

Commands used:

```bash
git rev-parse HEAD
git status --short --branch
git submodule status --recursive
go version
ffprobe -version | head -1
jq --version
git diff --check
```

## 3. Tracked media inventory

PipelineGen tracks **0** MP4 files. RenderingGen tracks **275** MP4 files
totalling **278 MB** in the current tree:

| Class | Files | Size | Retention decision |
| --- | ---: | ---: | --- |
| `assets/backgrounds/` | 5 | 58 MB | **keep** — source plates |
| `testdata/golden/` | 1 | — | **keep** — fixture |
| `evidence/image-effects-runtime-demo/` | 10 | 4.2 MB | **keep** — compact certification evidence |
| `Upload/` | 4 | 6.9 MB | remove — derived render |
| `UploadDrive/` | 233 | 201 MB | remove — derived render |
| `hdr_bokeh_focus_pull_20261108/` | 22 | 7.5 MB | remove — derived render |

The five source plates are:

```text
assets/backgrounds/background-boxe.mp4
assets/backgrounds/background-crime.mp4
assets/backgrounds/background-discovery.mp4
assets/backgrounds/background-music.mp4
assets/backgrounds/background-wwe.mp4
```

The removal is manifest-backed: every path, byte count and SHA-256 is recorded
under `artifacts-retention/` before the index and worktree are changed. Because
this baseline is captured before a commit, the bytes remain recoverable from
the current `HEAD` with `git checkout HEAD -- <path>`.

## 4. CI inventory

| Repository | Workflows |
| --- | --- |
| PipelineGen | `architecture.yml`, `ci.yml`, `manual.yml`, `nightly.yml` |
| RenderingGen | `build.yaml` |

PipelineGen `ci.yml` owns the fail-closed `verify-main` job, plus independent
`lint`, `vuln` (`govulncheck`) and secret-scan jobs. The audit's remote
observation was a running `verify-main` with the vulnerability check failed;
that remote state cannot be certified from this host without GitHub
credentials. The local gate is exercised in the closeout run.

Branch protection is an **operator action**, not a repository file:

```bash
gh api -X PUT repos/Marcuss-ops/PipelineGen/branches/main/protection \
  -f required_status_checks.strict=true \
  -f 'required_status_checks.contexts[]=verify-main (fail-closed local-mirror gate)' \
  -F enforce_admins=true \
  -F required_pull_request_reviews.required_approving_review_count=1 \
  -F restrictions=
```

The exact context string must be read back from the latest run; the command
above is the shape, not a claim that protection is enabled.

## 5. Secret-history scan

A non-destructive scan was run against both audit repositories. Only paths and
commit prefixes were retained; matching secret values were never printed.

| Repository | Tracked `.env` / private-key files | History line matches | Assessment |
| --- | ---: | ---: | --- |
| PipelineGen | 0 | 58 (31 commit/path groups) | all matches are scanner fixtures, tests or documentation examples (`scripts/ci/ci-no-secrets-audit*`, `test_verify_component.py`, `AGENTS.md`, migration comments) |
| RenderingGen | 0 | 0 | no match |

The scan used:

```bash
git log --all -p --no-color |
  grep -aE 'BEGIN (RSA|OPENSSH|EC|DSA) PRIVATE KEY|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9]{36}|xox[baprs]-[A-Za-z0-9-]{10,}|sk-[A-Za-z0-9]{20,}'
```

This is a targeted signature scan, not a replacement for `gitleaks`/GitHub
secret scanning over every blob. The exposed sudo password from the session is
**not** in these repositories; rotating it and checking access logs is an
operator action outside the repository and is still required. Never commit the
rotated value or the old one.

## 6. Tests observed at baseline

```text
go test ./internal/capabilities/overlays/model/...   PASS
go test ./internal/capabilities/audio/...            PASS
go test ./internal/capabilities/voiceover/service/... PASS
go test ./internal/capabilities/scripts/adapters/    PASS
go test ./pkg/atomicwrite/...                        PASS
go test ./internal/app/wiring/rendering/             PASS
go test ./renderinggen/internal/overlay/ (focused)   PASS
```

The full `make verify-main` result is recorded in the closeout section below,
not inferred from these focused runs.

## 7. Operational actions that require credentials or hardware

These are deliberately **not** claimed as done by this baseline:

1. Rotate the exposed sudo password and audit access logs on the affected host.
2. Enable GitHub branch protection and required checks with an admin token.
3. Rewrite Git history to remove already-committed media blobs; this changes
   historical SHAs and requires a coordinated force-push and backup.
4. Run a live GPU render and inspect the final MP4 frames. The repository-level
   goldens prove the producer/consumer contract, not a specific production
   delivery.

## 8. Remediation closeout — 2026-11-11

Status: **local gates green** on the pinned SHAs plus the uncommitted
remediation. Remote GitHub CI, branch protection and live production renders
remain operator actions; they are not certified here.

### 8.1 Gates actually run

| Repository | Command | Result |
| --- | --- | --- |
| PipelineGen | `make verify-main` | **PASS** (exit 0) |
| PipelineGen | `go run ./cmd/archcheck --strict` | **PASS**, `has_hard_gate_hits: false` |
| RenderingGen | `make verify` | **PASS** (exit 0: gofmt, standalone builds, vet, unit tests) |
| RenderingGen tools | `python3 -m unittest -v test_verify_delivery_manifest.py` | **6/6 PASS** |
| Both | `git diff --check` | clean |

The focused regressions exercised by the gates include:

- explicit image pins: `TestVidRushFanoutPlanExplicitImageQueriesAreExclusive`;
- five-image contract: `TestCoalesceFiveImagesPreserveFiveIdentitiesAndMotions`
  and `TestCompileFiveImageChainKeepsFiveDistinctVisualSegments`;
- standalone maps and rejected mixed chains in `chain_coalesce_test.go`;
- versioned audio policy and the 73.3 s → 64.7 s timing regression;
- `submit-and-poll` polling separation and preflight;
- atomic write/commit/quarantine and the rendered-artifact publish gate;
- prepared-package reuse and the queue lease-generation fencing test;
- exact-replay batch submission (HTTP 200) and fail-closed 409 conflicts.

The full RenderingGen run also fixed three test-side contract mismatches without
weakening production checks: prepared packages now enable native output profiles
and declare their frame total, the batch fake queue models the verified 200
replay, and the two upload READMEs/examples use `$HOME` instead of a developer
path.

### 8.2 Retention executed

| Fact | Value |
| --- | ---: |
| MP4 tracked at baseline | 275 files / 290,508,601 bytes |
| Removed from the index and worktree | 259 files / 225,168,502 bytes |
| MP4 remaining | 16 files / 65,340,099 bytes |
| Remaining classes | 5 source plates, 1 golden fixture, 10 compact evidence clips |

The per-file inventory is
`RenderingGen/artifacts-retention/media-retention-2026-11-11.json`; the policy
and recovery instructions are in the adjacent `README.md`. Because the removal
is not committed yet, every removed path remains recoverable from the current
`HEAD` with `git checkout HEAD -- <path>`. No history rewrite was performed.

### 8.3 Canonical documentation decision

The canonical remediation state is this file under `docs/operations/`. The
historical `docs/stato/` links found in older documents are not recreated as a
second source of truth; a later documentation pass should either redirect them
here or mark those documents as archived. No duplicate state file was added.

### 8.4 Operator-only actions still open

1. Rotate the exposed sudo password and audit the affected host's access logs.
2. Enable GitHub branch protection and required `verify-main` checks with an
   admin token; remote workflow state cannot be certified from this host.
3. Review and commit the two worktrees, then rerun the same gates on the remote
   commit. Do not rewrite Git history just to shrink clones; that changes every
   historical SHA and needs a coordinated backup/force-push.
4. Produce the canonical Garlasco r151 and Kaufmann r12 delivery manifests from
   real artifacts and inspect their first, middle and last frames on the GPU
   host.
5. Inventory the parent monorepo's `ops/jobs` (about 34 GB) before deleting
   anything; those files were intentionally left untouched because they may
   contain unverified final deliveries. Local upload trees are also retained on
   disk and excluded from Git rather than deleted blindly.

## 9. Re-verification — 2026-11-12

Status: **local contracts green; production certification still operator-only**.
This section supersedes the narrative of the 11-11 closeout where the two
disagree; the SHAs below are the ones the working tree was re-verified against.

### 9.1 Repository identities and toolchain

| Repository | HEAD | Dirty paths at re-verification |
| --- | --- | ---: |
| PipelineGen (`refactored`) | `57e0ee0ccb8abdda30a8dcd05ae8a394cf900245` | 22 |
| RenderingGen | `9f1bda952a2e1124c7d3df63761662e888ac5b6e` | 272 |
| Chronon3d | `0ac4ef26a6e97e067b5f9a2f8e9b3a98601e3e7c` | 12 |
| BrainRust | `59b5fa25680b80c51a18cd79ffe721fd6ca12395` | 0 |

Toolchain: Go `1.25.9`, git `2.34.1`, ffprobe `7.0.2-static`, Python `3.10.12`,
node `22.22.2`. The repository had 36 GiB free at re-verification (89% used).

### 9.2 Additional fail-closed work landed on top of 8.1

- **Image search modes**: `MediaExtractionPolicy.ImageSearchMode` now supports
  `explicit_only`, `automatic`, `explicit_then_fallback` and `disabled`.
  `explicit_only` never appends NER, semantic-ladder or scene-sentence
  fallbacks and never truncates the caller's authorized list. Tests cover all
  four modes in `vidrush_fanout_modes_test.go`.
- **Renderer map contract**: `RenderingGen/renderinggen/internal/overlay/chain_compile.go`
  rejects a map segment inside `phrase_chain`/`image_chain`/`mixed_chain` and
  rejects a `map_chain` segment without a map. Producer and consumer both fail
  closed (`chain_map_contract_test.go`).
- **Audio cache identity**: `GenerateVoiceoverItemCommand` and
  `ProcessSegmentCommand` carry a versioned `SilencePolicy`; the resolved
  policy and TTS rate are folded into fingerprint V4 and the idempotency key.
  The scriptgen adapter supplies the canonical policy explicitly instead of
  hiding it inside the provider identity.
- **Zero-frame completion**: RenderingGen's queue completion gate rejects a
  `video/*` artifact with `frame_count <= 0` or `duration_us <= 0`; the test
  fixtures were updated to carry real video facts rather than the gate being
  relaxed.
- **Shared conformance**: a byte-identical fixture
  (`refactored/testdata/contracts/chain-segments-v1.json` and
  `RenderingGen/contracts/chain-segments-v1.json`, SHA-256
  `c8b8195de94e39b3ff36ffba938f4941a3e929ce0bdb1fdd0c8b37e445088faa`) is
  validated by PipelineGen as a producer plan and compiled by RenderingGen as
  five distinct image layers. Either side editing its copy alone fails the
  SHA pin.
- **Source window**: `Chronon3d/tests/video/test_video_source_window.cpp` pins
  the `map_video_frame` clamp/loop behavior. The video node already probes by
  detecting EOS, records the observed duration and retries under
  `VideoLoopMode::Loop` or clamps to the last frame; it never assumes frames
  past EOF exist.
- **Atomic publication**: `pkg/atomicwrite.CommitFile` and `Quarantine` now have
  direct tests for fsync+rename publication and reason-sidecar quarantine.
- **CI-independent motion catalog**: the canonical emitted catalog is vendored
  at `internal/capabilities/overlays/testdata/chronontemplate_catalog.v1.json`
  (SHA-256 `c21a42f764ae07f9aa10e40853e09f9608cd15db87a61a9a01d9fc9cd1b2fc3e`).
  The parity tests read that snapshot and fail on drift when the sibling
  RenderingGen checkout is present, so a PipelineGen-only CI checkout no
  longer fails with "could not locate canonical ChrononTemplate motion
  catalog".
- **Automatic stale-job recovery**: `StartStaleJobSweeper` runs every minute
  and fails `LEASED`/`RUNNING`/`FINALIZING` rows whose lease expired more than
  15 minutes ago, using the canonical `MarkRunningJobsOlderThanFailed`
  transition. It is wired through `MaintenanceServices.StaleJobs`; the
  operator-only `zombie-sweep` CLI remains available as a manual override.
- **Source window execution**: the new C++ cases compile into
  `chronon3d_media_video_tests` and the suite passes 153/153, including the
  clamp, authorized-loop and `source_start` variants.

### 9.3 Tests actually run after these changes

| Repository | Command | Result |
| --- | --- | --- |
| PipelineGen | `go build ./...` | PASS |
| PipelineGen | `go test ./internal/capabilities/overlays/... ./internal/app/wiring/lifecycle/...` | PASS |
| PipelineGen | `make verify-main` | PASS (architecture, queue vendor and all component gates) |
| PipelineGen | `python3 scripts/ci/check_queue_vendor.py` | PASS — 157 files at queue version 0.1.2 |
| RenderingGen | `make verify` | PASS (renderinggen, queue and objectstore unit suites) |
| RenderingGen queue | `go test -count=1 ./...` | PASS |
| Chronon3d | `cmake --build build/chronon/linux-video-release --target chronon3d_media_video_tests` | PASS |
| Chronon3d | `./build/chronon/linux-video-release/tests/chronon3d_media_video_tests` | PASS — 153/153 cases |

### 9.4 Delivery status (unchanged by local gates)

- **Garlasco r151**: 24 overlay assets and a remote master final job are
  recorded; the assembled master MP4 is not present locally and was not
  frame-checked. Not locally certifiable.
- **Kaufmann r12**: the observed job state is `CANCELLED`; no final MP4 exists.
  Not delivered.

The README's operator-only list (rotation, branch protection, remote CI,
history rewrite, GPU delivery certification) remains open. Local green tests
are not a substitute for those actions.

### 9.5 Remote CI and operator-only work not certified here

- **GitHub CI**: local `make verify-main` is green, but the remote PipelineGen
  `govulncheck` finding `GO-2026-6859` and the `golangci-lint` SA4023 crash in
  `artifactsinfra` were not reproduced locally because `govulncheck`,
  `staticcheck` and `golangci-lint` are not installed on this host. The remote
  workflows must be rerun on the pinned toolchain before the CI criterion is
  called closed.
- **Branch protection** and required-check enforcement require repository
  administrator access and were not changed.
- **Credential rotation** and removal of any historical exposure require the
  external hosts and secret stores; no secret values were written to this
  repository during the re-verification.
- **Real delivery certification** remains limited to the recorded evidence:
  Garlasco r151 has no local master MP4, and Kaufmann r12 is `CANCELLED` with
  no final artifact. No `SUCCEEDED` manifest was fabricated for either.
- **History rewrite** for large historical blobs remains a separate coordinated
  operation; the current remediation removed 259 MP4 files from the indexed
  tree with a SHA manifest, without rewriting Git history.

## 10. Reachability closeout — 2026-11-12 (second pass)

Status: **every locally reachable gate is green; the remaining checklist items
need an operator, a product decision, or a tool that is not installed on this
host.** No remote CI mutation, branch-protection change, credential operation,
history rewrite, bulk deletion or fabricated delivery was performed.

### 10.1 Identities at re-verification

| Repository | HEAD | Dirty paths |
| --- | --- | ---: |
| PipelineGen (`refactored`) | `dc354ab22b2b46d74b97b23d9433fdc4b567da0d` | 7 (three corrected docs, audit, overlay model, migration 291 and its test) |
| RenderingGen | `0b659e87a646426cd8ee0cde76b1ebdc2bdb407d` | 8 (six generated demo logs, worker drain change and test) |
| Chronon3d | `0ac4ef26a6e97e067b5f9a2f8e9b3a98601e3e7c` | 16 (worktree) |
| Monorepo | `a043a2b55a74ee9b86a0975a742ff23b0eb6ba41` | 56 (submodule gitlinks, retention policy/tool and ops artifacts) |

Toolchain observed: Go `1.26.10` resolved through the module's `toolchain`
directive, ffprobe/ffmpeg `7.0.2`, 62 GiB free (80% used), GPU devices
`/dev/dri/card0`, `card1`, `renderD128`. `govulncheck`, `staticcheck` and
`golangci-lint` are not installed on this host.

### 10.2 Gates actually run in this pass

| Repository / surface | Command | Result |
| --- | --- | --- |
| PipelineGen | `make verify-main` | **PASS**, exit 0 (`verify_main_exit=0`) |
| PipelineGen | `bash scripts/tests/submit-and-poll_test.sh` | **PASS**, six transport misses recovered on the same job id; deadline exits 4 |
| PipelineGen | `make verify-rust-contract` | **PASS**, 3 shared goldens validated |
| PipelineGen | `make verify-rust-muscles`, `make verify-rust-fetch` | **PASS**; fetch fixture installs BrainRust v9.9.9 with ICU 72 |
| PipelineGen | `go test ./internal/platform/sqlite -run 'TestMigrations_291\|TestMigrationHistory_OnlyTablesWithALiveClaimAreReported\|TestMigrations_DeclaredTables\|TestMigrations_Smoke_Baseline'` | **PASS**, migration 291 append-only trigger test |
| RenderingGen | `go test ./renderinggen/cmd/renderinggen` | **PASS**, shutdown drain registration test |
| RenderingGen | `make verify` | **PASS**, exit 0 (`verify_exit=0`) |
| Chronon3d | `chronon3d_media_video_tests` | **PASS**, 153/153 cases, 22,232/22,232 assertions |
| PipelineGen | shared conformance + five-image chain tests | **PASS** |
| RenderingGen | five-image lowering + map rejection tests | **PASS** |
| PipelineGen | four motion-catalog parity tests | **PASS** |
| PipelineGen | explicit-pin fanout test | **PASS** |
| PipelineGen | audio, voiceover service, atomicwrite, workerruntime, lifecycle | **PASS** |
| RenderingGen | queue service + server (replay/dedup/artifact validation) | **PASS** |
| RenderingGen | delivery manifest verifier tests | **PASS**, 6/6 |
| Monorepo | `python3 ops/tools/reconcile_tickets.py --check` | **PASS**, 87 sheets |
| Monorepo | `python3 ops/tools/retention_inventory.py --check-policy` | **PASS**, 16/16 jobs classified, 0 approved for deletion |
| Both | `git diff --check` | clean |

### 10.3 The 26-criterion checklist

Statuses are `PASS` only where an executed command or an inspected artifact
proves the criterion. `OPERATOR`, `NOT VERIFIED` and `PRODUCT DECISION` are not
passes.

| # | Criterion | Status | Evidence / remaining gap |
| ---: | --- | --- | --- |
| 1 | Credenziali esposte ruotate e dump bonificati | **OPERATOR** | No tracked `.env` or private key. Two ignored transcripts each contained one GitHub token; both were redacted in place on 2026-11-12 and a rescan finds zero matches. Rotation and host access-log audit remain external. |
| 2 | CI verde e branch protection configurata | **PARTIAL** | Both local gates PASS. Remote workflows and branch protection need an admin token. |
| 3 | Vulnerabilità e crash del linter risolti | **NOT VERIFIED** | CI pins `govulncheck v1.1.4` and `golangci-lint v2.13.2`; neither tool is installed here, so `GO-2026-6859` and SA4023 cannot be reproduced locally. |
| 4 | Toolchain Go/Rust/ICU/ffmpeg riproducibile | **PARTIAL** | Go pinned to `1.26.10`; ffmpeg/ffprobe `7.0.2`. `verify-rust-contract` validates 3 shared goldens and `verify-rust-muscles` / `verify-rust-fetch` PASS (the fetch fixture installs ICU 72). The host still has ICU 70 and no `rustc`, so the production Rust/ICU runtime is not certified here. |
| 5 | Preflight disco e risorse GPU verificato | **PASS** | `workerruntime` preflight tests PASS; 35 GiB free; `/dev/dri` present. |
| 6 | Nessun composer immagini legacy con caller di produzione | **PASS** | `composeNearbyEntityImages` / `AutoImageGroups` appear only in the historical note of the corrected doc; no Go caller. |
| 7 | Unico processo di ammissione, deduplica e quote | **PASS** | Planner/budget suites inside `make verify-main`; no second admission path added. |
| 8 | Pin immagine esclusivi e chapter_id stabili | **PARTIAL** | `TestVidRushFanoutPlanExplicitImageQueriesAreExclusive` PASS. The tree has no separate `chapter_id` field; scene IDs are the stable join key, so a rename is a contract change, not a test fix. |
| 9 | Golden 5 immagini → 5 segmenti → 5 motion | **PASS** | Producer and consumer tests PASS; shared fixture SHA-256 `c8b8195d…`. |
| 10 | Schema `chain_segments` validato sui due repository | **PASS** | Byte-identical fixture validated by PipelineGen and compiled by RenderingGen; both copies hash-match. |
| 11 | Mappe off per default e separate dalle chain miste | **PRODUCT DECISION** | Map-in-chain rejection PASS. `overlayCategoryEnabled` keeps the established default enabled when the category is omitted; turning maps off by default changes product behaviour and needs approval. |
| 12 | Background decoder sicuro e senza workaround permanenti | **PASS** | `chronon3d_media_video_tests` 153/153, including clamp, authorized loop and `source_start` cases. |
| 13 | Unica edit map con trasformazione applicata una volta | **PASS** | `internal/capabilities/audio` suite PASS. |
| 14 | Test 73,3 s → 64,7 s certificato | **PASS** | `TestRemapTiming_Garlasco73_3To64_7` PASS inside the audio suite. |
| 15 | Cache TTS versionata per provider, voce, velocità e silenzi | **PASS** | `internal/capabilities/voiceover/service` PASS (fingerprint V4). |
| 16 | Watchdog zero frame e stale RUNNING automatico | **PASS** | Lifecycle sweeper tests PASS; queue artifact validation rejects `frame_count <= 0`. |
| 17 | SIGTERM drain e resume automatico | **PARTIAL** | Pools drain on context cancellation, and shutdown now re-registers the full worker identity as `draining` (`TestMarkWorkerDrainingPreservesIdentity` PASS). No signal-delivery test yet proves SIGTERM interrupts an in-flight render and the same job resumes. |
| 18 | Retry M2M e riconciliazione sul Master | **PASS** | `submit-and-poll_test.sh` injects six consecutive transport failures (curl exit 7), reports `POLLING_UNAVAILABLE`, recovers on the same job id with `POLLING_RECOVERED`, and a deadline exits 4 instead of 1. This is a replay at the script boundary; a live Master partition was not exercised. |
| 19 | Notifiche task/attempt deduplicate | **PASS** | Queue replay/duplicate-submit tests PASS. |
| 20 | Prepared media atomici con quarantena | **PASS** | `pkg/atomicwrite` plus enforcement/publish tests PASS. |
| 21 | Nessun `SUCCEEDED` senza SHA, probe e frame | **PASS** | Queue artifact validation and delivery-manifest verifier reject missing/zero facts. |
| 22 | MP4 derivati trasferiti fuori da Git | **PASS** | 259 files / 225,168,502 bytes removed from the index; 16 reviewed MP4 remain; manifest present. |
| 23 | Dump e cache sotto retention controllata | **PASS** | Chat dumps are ignored and the two token-bearing transcripts were redacted. `retention_inventory.py` measured `ops/jobs` at 5.89 GB / 8,599 files, and 35.5 GB of regenerable TTS weights and scratch were removed. `policy.json` classifies all 16 job directories and blocks deletion; `--check-policy` PASS (16/16, 0 approved). No job was deleted: this closes control, not a deletion approval. |
| 24 | Stato e ticket allineati e storici archiviati | **PASS** | Ticket index regenerated to 87 sheets; `--check` exit 0; stale composition docs corrected. |
| 25 | Artefatto canonico verificato per commessa | **OPERATOR** | Garlasco r151 master was not fetched/frame-checked; Kaufmann r12 is `CANCELLED` with no artifact. |
| 26 | Report immutabile di ogni run disponibile | **PASS** | Migration 291 adds `run_report_snapshots` plus INSERT/UPDATE triggers on `run_observability`: every `StartReport`, `SaveReport` and `RecoverAbandoned` version is copied before the live row changes. `TestMigrations_291_RunReportSnapshotsAreAppendOnly` PASS (insert captured, changed body appended, unchanged body not duplicated, snapshot UPDATE rejected). The live `report_json` remains the current projection; the snapshot table is the immutable history. |

Tally: **18 PASS, 4 PARTIAL, 2 OPERATOR, 1 PRODUCT DECISION, 1 NOT VERIFIED**
(26 total).

### 10.4 Changes landed in this pass

- Promoted criterion 18 to PASS after the M2M transport-failure replay, and
  recorded the Rust contract/muscles/fetch results without promoting the ICU
  runtime.
- Redacted one GitHub token from each of two ignored transcripts and rescanned
  the transcript set to zero matching secrets; rotation remains external.
- Added shutdown drain registration to the RenderingGen worker: it re-sends the
  full worker identity with `status=draining` on a fresh context after the pools
  exit, with a unit test proving identity and versions are preserved.
- Added migration 291: append-only `run_report_snapshots` fed by INSERT and
  `report_json` UPDATE triggers, with an append-only guard and a focused test.
- Added `ops/jobs/_retention/policy.json` and `retention_inventory.py
  --check-policy`: every job directory must be classified, and any policy
  entry marked `approved` fails the check unless separately reviewed.
- Regenerated the canonical ticket index (86 → 87 sheets) and confirmed
  `reconcile_tickets.py --check` exits 0.
- Corrected three operations documents that still described automatic
  entity-image grouping as a production path:
  `docs/operations/entity-overlay-composition.md`,
  `docs/operations/script-generate-payload-schema.md` and
  `docs/entity-image-catalog-state-policy.md`. The corrected contract is one
  identity per item, chains keep one segment per image, and composites exist
  only for explicitly authored `MultiEntityGroups`.
- Recorded this closeout in the canonical state file and linked it from
  `docs/stato/README.md`.

### 10.5 Operator-only next steps, in order

1. Rotate the exposed sudo password and audit the affected host's access logs.
   Never commit either value.
2. Commit the two worktrees, push the remediation SHAs, rerun both CI workflows
   on that exact commit, then enable branch protection with required checks.
3. Install the pinned `govulncheck v1.1.4` and `golangci-lint v2.13.2`; rerun
   both jobs. If `GO-2026-6859` persists, move the module to the patched Go
   toolchain; reproduce SA4023 on `artifactsinfra` before changing the linter.
4. Fetch the Garlasco r151 master by SHA-256
   `e877d90423c0f5a6bf7fdd80964e18165386b5a7bb2d16173e5eeef52adbdfed`,
   probe it, hash the first/middle/last frames, then issue the delivery
   manifest. Reconcile Kaufmann r12 from `CANCELLED` or authorize a new
   generation; do not fabricate a `SUCCEEDED` row.
5. Deletion remains blocked by `ops/jobs/_retention/policy.json`. Approve a
   per-job deletion only after resolving its manifest and recording the
   approval in a new policy version; the read-only inventory is
   `ops/tools/retention_inventory.py` and the classification is in
   `ops/jobs/_retention/2026-11-12/REPORT.md`.
6. Decide whether maps must be off by default. If yes, change
   `overlayCategoryEnabled` and its tests as a product change; do not sneak it
   into the decoder.
7. Add a signal-delivery SIGTERM test that interrupts an in-flight render and
   proves the same job resumes. The drain status update itself is covered.
8. Decide whether a separate `chapter_id` belongs to the result contract. If
   yes, add it to `ChapterManifestEntry` and the Rust consumer together; if no,
   record scene IDs as the sole authority.
9. Treat a Git history rewrite for the 225 MB of removed media blobs as a
   separate coordinated operation: backup, rewrite, force-push, re-clone.

### 10.6 Explicit non-claims

Local green tests do not certify remote CI, branch protection, credential
rotation, GPU delivery frames, or a history rewrite. Redacting a token from a
local dump is not rotation: the exposed token must still be revoked. The
retention PASS means every retained job is classified and deletion is blocked;
it is not approval to delete. `PARTIAL`, `OPERATOR`, `PRODUCT DECISION` and
`NOT VERIFIED` remain open; none is reported as `PASS`.
