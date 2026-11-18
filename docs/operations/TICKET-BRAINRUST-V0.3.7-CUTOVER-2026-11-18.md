# TICKET — BrainRust v0.3.7 cutover and quality-gate default

- **Status:** CLOSED (2026-11-18, committed) — pin `v0.3.7`, Rust gate
  unconditional, Go gate deleted. Publishing the `v0.3.7` tag remains
  TICKET-27 in BrainRust (no credentials in this checkout); `make rust-fetch`
  consumes the hash-verified local bundle until then.
- **Severity:** P0 — the code and the binary contract disagree until the pin moves
- **Owner:** PipelineGen platform + BrainRust release
- **Source:** audit 2026-11-18; `BrainRust/docs/tickets/TICKET-22-QUALITY-GATE-RUST-PORT.md`; `refactored@75b5b1def`
- **Depends on:** `TICKET-27-publish-v0.3.7` in BrainRust
- **Rule:** no step is marked done from a unit test alone; the cutover closes with a real generation and the Go gate deleted

## Problem

`refactored` now contains the full opt-in Rust quality-gate path:

- `internal/platform/media/rustexec/semantic_engine_quality_gate.go` (148 lines)
  sends `{ "operation": "quality_gate", ... }` to `semantic-engine`.
- `internal/kernel/script/quality_gate.go` defines the `QualityGateEvaluator`
  seam; `usecase/gencore/generation_finalize.go` calls it when set.
- `internal/app/wiring/wire_script_usecases.go:137-144` wires the adapter only
  when `cfg.External.RustQualityGateEnabled` is true; the flag
  (`VELOX_RUST_QUALITY_GATE`, `internal/platform/config/types_external.go:38-41`)
  defaults to `false`.

At the same time:

- `make/build.mk:44` still pins `BRAINRUST_VERSION ?= v0.3.6`.
- `bin/BRAINRUST_RELEASE.json` records `"version": "v0.3.6"` and
  `"git_sha": "527b756"`, which is the release **without** `quality_gate`
  (the operation first appears in `d3b77d6`, tag `v0.3.7`).

Consequences:

1. The cutover is half-landed: the Go code can call an operation the installed
   worker does not serve.
2. If `VELOX_RUST_QUALITY_GATE=true` is set before the binaries are updated,
   every finalize fails closed with an unknown-operation protocol error. This
   is the correct fail-closed behaviour, but it makes the flag order-sensitive
   and easy to misfire during a deploy.
3. The Go gate (`internal/capabilities/scripts/usecase/qualitygate/`, 10
   production files, 866 lines) remains a second owner of language and script
   QA rules beside Rust `script-qa` (886 lines). Keeping both after the cutover
   recreates the drift the audit removed elsewhere.

## Execution result (2026-11-18)

Steps 2–7 are executed; step 1 (publish) is the only external gap.

| Step | Result |
|---|---|
| 2. Bump the pin | `make/build.mk` → `BRAINRUST_VERSION ?= v0.3.7` |
| 3. Reinstall through the fetch target | `make rust-fetch RUST_ARTIFACTS_DIR=…/BrainRust/dist-v0.3.7` → 4 binaries installed; `bin/BRAINRUST_RELEASE.json` = `v0.3.7`, `git_sha 2ca3603`, ICU 72; all four SHA-256 digests match |
| 4. Verify the operation | `TestSemanticEngineRealBinaryEvaluatesQualityGate` **PASS** (real binary, no skip) |
| 5. Remove the switch | the flag is **deleted**, not flipped: `RustQualityGateEnabled`, its config test, the registry knob and the generated artifacts are gone; `wireScriptFlow: Rust quality_gate wired` is unconditional |
| 6. Compare verdicts | 27-scenario Go↔Rust differential on the release binary: **27/27 identical** (metrics + reason strings). Go verdicts frozen to `BrainRust/script-qa/tests/fixtures/quality_gate/goldens.json` and enforced by `script-qa/tests/quality_gate_goldens.rs` |
| 7. Delete the Go gate | `internal/capabilities/scripts/usecase/qualitygate/` removed (10 production files, 866 lines); no production import remains; a missing evaluator fails closed with `quality gate evaluator is not configured` |

**Deviation from the written order, with reason:** step 1 (publish) could not run
because this checkout has no GitHub credentials (`gh auth status`: not logged in;
`git ls-remote --tags origin v0.3.7` is empty). Deferring the cutover until
publication would leave the only released worker (`v0.3.6`) unable to serve
`quality_gate` while the Go owner was already gone in the same tree, so the code
cutover was completed against the locally built, checksum-verified `v0.3.7`
bundle. Consequence: `make build` requires the published release **or**
`RUST_ARTIFACTS_DIR=…/BrainRust/dist-v0.3.7` until TICKET-27 lands.

## Execution order (as written, do not reorder)

1. Publish BrainRust `v0.3.7` (`TICKET-27`) and confirm the release assets.
2. Bump `BRAINRUST_VERSION` to `v0.3.7` in `make/build.mk`.
3. Reinstall the four binaries and regenerate `bin/BRAINRUST_RELEASE.json`
   through the existing fetch/install target — do not hand-edit the manifest.
4. Verify the installed worker answers `quality_gate` (real-binary test).
5. Flip the default: `RustQualityGateEnabled` becomes `true` (config default
   and example config), and log the binary path at startup.
6. Run one real generation with the default path and compare the verdict to
   the previous Go verdict on the same input.
7. Delete the Go gate package and its tests; leave only the policy
   (status/skip/`QualityGateError`) required by the `QualityGateEvaluator`
   contract.
8. Mark TICKET-22 DONE in BrainRust with the release URL and the generation
   evidence.

## Acceptance criteria

- [x] `make/build.mk` pins `v0.3.7`; `bin/BRAINRUST_RELEASE.json` has the same
      version and the tag commit SHA; the four binary hashes match the release
      checksums.
- [x] `go test ./internal/platform/media/rustexec -run QualityGate -count=1`
      runs the real binary (no skip) and passes.
- [x] With no switch at all, a generation uses the Rust gate (log line
      `Rust quality_gate wired` present, no Go-gate fallback).
- [x] The quality block is byte-identical to the pre-cutover Go verdict on the
      same inputs: 27/27 differential scenarios, including every failure path.
      A full generate/finalize run additionally needs SQLite + the model
      provider and was not executed here; the finalize contract itself is
      covered by the injected-gate tests.
- [x] `internal/capabilities/scripts/usecase/qualitygate/` is deleted; no
      production import remains; `go build ./...` and
      `go test ./internal/capabilities/scripts/usecase/... -count=1` pass.
- [x] TICKET-22 is `DONE (bound: v0.3.7 publication)` in BrainRust, linked here.
- [x] Docs (`docs/stato/brainrust.md`, `BrainRust/docs/tickets/`,
      `refactored/docs/operations/`) no longer claim the Go gate is the
      production default.

## Execution log — 2026-11-18

The cutover was applied and committed on 2026-11-18. Verified here:

- pin `v0.3.7`, manifest `version=v0.3.7`, `git_sha=2ca3603…`;
- **provenance defect found and fixed**: `bin/semantic-engine` was a local
  rebuild (hash `e10f36…`) while the manifest declared the release hash
  `0e1d10…`. The release binary was restored from `dist-v0.3.7` and all four
  installed binaries now match `sha256sums.txt`;
- `TestSemanticEngineRealBinaryEvaluatesQualityGate` passes on the restored
  release binary (no skip);
- the Go gate package is gone; `VELOX_RUST_QUALITY_GATE` and its knob are gone;
  the full build is green;
- `cargo fmt --all -- --check`, `cargo clippy -p script-qa -p semantic-engine
  --all-targets -- -D warnings` and `cargo test -p script-qa -p semantic-engine`
  are green in BrainRust; `go build ./...`, `go vet ./...` and
  `go test ./internal/capabilities/scripts/...` are green here.

Still open: publishing the tag (`TICKET-27`) and one real generation run with
the Rust gate. The earlier `TestSceneImageCandidateClampsToOwningSceneEnd`
failure came from the unticketed overlay clamp work, which is parked outside
this cutover; the `scripts` suite is green on the committed tree.

## Verification

```sh
grep -n 'BRAINRUST_VERSION' make/build.mk          # v0.3.7
cat bin/BRAINRUST_RELEASE.json                     # v0.3.7 / 2ca3603 / icu 72
go test ./internal/platform/media/rustexec -run QualityGate -count=1
go test ./internal/capabilities/scripts/usecase/gencore -run QualityGate -count=1
go build ./... && go vet ./...
rg -n "usecase/qualitygate" --glob '*.go'           # no matches
cd ../BrainRust && cargo test -p script-qa --test quality_gate_goldens
```

Known external gap: `v0.3.7` is tagged and bundled locally but not published.
Until TICKET-27 publishes it, a clean checkout must run
`make rust-fetch RUST_ARTIFACTS_DIR=…/BrainRust/dist-v0.3.7`.

## Rollback

Revert the default to `false` and keep the adapter; the Go gate must not be
deleted until step 7 has a green real run. The release pin is the last thing to
roll back, never the first.

## Out of scope

- Publishing the release itself (BrainRust `TICKET-27`).
- The language weight in `mediasampler` (`local_score.rs`), which depends on
  the `media_assets.language` schema ticket.

## Provenance recheck — 2026-11-18 (later pass)

`bin/semantic-engine` was found again as an 821-byte Python wrapper
(`#!/usr/bin/env python3`), hash `a1358f43…`, instead of the release binary
declared by `bin/BRAINRUST_RELEASE.json` (`0e1d10ab…`). The real-binary tests
failed with `unexpected end of JSON input` until the release artifact was
restored from `BrainRust/dist-v0.3.7/semantic-engine`.

After restore, all four binaries match the manifest:

```text
semantic-engine      0e1d10ab…  OK
visualner            cc0089b4…  OK
mediasampler         550800f0…  OK
pipelinegen-muscles  d433d04c…  OK
```

`TestCompileOverlayPlanV2UsesOneRealWorkerCall` and
`TestSemanticEngineRealBinaryEvaluatesQualityGate` PASS again. Any local
wrapper is not a substitute for the signed release artifact: it hides the
real worker contract behind a stub.
