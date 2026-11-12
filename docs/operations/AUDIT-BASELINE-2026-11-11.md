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
