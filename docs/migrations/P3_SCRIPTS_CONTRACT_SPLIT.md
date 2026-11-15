# P3 contract-first split: `capabilities/scripts` (2026-11-15)

## Current binding facts

- 384 production Go files, 88,519 production LOC.
- The documented dependency graph contains a 117-file strongly connected
  component and a 68-file floor at the former 600-LOC cap. With the cap now at
  800, file moves alone still cannot reduce the SCC: the imports, not the file
  lengths, bind the closure.
- The package is a capability, not a utility bag. Splitting it by directory
  first would create import cycles and move the same shared model types around.

## Phase 1 executed (2026-11-15)

- Canonical contract: `internal/capabilities/scripts/contracts/scene`.
- Moved `Language`, `ResolvedScene`, and `ResolvedVoiceover` into the contract.
- Root compatibility aliases live in the existing `model_values.go` and
  `resolved_scene.go`; no new root file was added, so the `scripts` hotspot
  count stays at its 137 baseline.
- The contract imports only `internal/capabilities/audio` and carries no
  root-package dependency.
- Next: migrate generation/audio/overlay consumers to import the contract
  directly, then remove the root aliases. Do not move resolver policy into the
  contract; it consumes the contract and stays with its owner.

## Phase 2 executed (2026-11-15)

- Canonical contract: `internal/capabilities/scripts/contracts/audio`.
- Moved `FinalAudioReference` and `AudioCompileTimings` into the contract.
- The delivery-audio port surface in `ports_execution.go` now names the
  contract types directly; existing root consumers keep compatibility aliases.
- The contract has no root-package dependency.
- Next: migrate the remaining audio compiler/runner consumers to the contract,
  then remove the root aliases.

## Required order

Extract contracts before moving implementations. Each phase owns a narrow
contract package under `internal/capabilities/scripts/contracts/<phase>` and
must not import another phase's implementation package.

| Phase | Contract to freeze first | Producers | Consumers | Exit criterion |
|---|---|---|---|---|
| Generation | plan identity, resolved scene, unit/candidate IDs, generation result envelope | semantic planner, incremental coordinator | audio, overlay, final-job | generation implementation imports only `contracts/generation` plus kernel/platform |
| Audio | `audio.VideoSegment`, timeline offsets, freeze-tail policy, audio intent | generation, voiceover | overlay, final-job | no audio implementation imports overlay/final-job |
| Overlay | overlay plan, entity card, asset/materialization reference | generation, entity catalog | final-job | overlay contract has no SQLite/provider imports |
| Final-job | compiled plan assets, materialization request/result, completion declaration | generation, overlay, stock | delivery/rendering | final-job implementation imports contracts only |
| VidRush | segment result, provider fanout request, cache identity | generation, media resolver | final-job | VidRush imports no sibling implementation package |

## Phase 3 executed (2026-11-15)

- Canonical contract: `internal/capabilities/scripts/contracts/generation`.
- Moved the serializable DTOs: production unit, planned identity, resolved
  image, coverage, deferred fact, metrics, search query/component and retry.
- `SemanticProductionPlan` remains in the root as the planner wrapper because
  it carries private revocation state (`factsByCandidate`, `coveredFacts`,
  `identityByID`); its public DTO fields now use the contract types.
- Existing constructors and tests stay source-compatible through type aliases.

## Existing canonical owners (no new package needed)

The phase map is not five greenfield packages; three phases already have a
single owner that the root must consume rather than duplicate:

| Phase | Canonical owner | State |
|---|---|---|
| Overlay | `internal/capabilities/overlays/model` | already separate from `scriptgeneration`; keep it as the overlay contract |
| Final-job | `internal/capabilities/scripts/finaljob` | owns the versioned PREPARE/FINALIZE/CompiledPlan V2 contract; root aliases it in `final_job_payload.go` |
| VidRush | `internal/kernel/script` (`VidRushSegmentResult`) | kernel-owned segment contract |
| Generation | `internal/kernel/script` (`ResolvedGenerationPlan`) | kernel-owned resolved plan; the remaining root-owned cluster is the semantic production plan |

The only unresolved contract cluster is the semantic production plan
(`SemanticProductionPlan`, its identities, coverage and search feedback). It is
intentionally not moved in this change: its private planner state and methods
still depend on the root planner. The next extraction must split the serializable
DTO from that private state before moving it.

## Sequence

1. **Freeze the generation contract.** Move only the model types and
   interfaces named in the table into `contracts/generation`; keep behavior in
   place. Update the existing callers to the contract package. Build and run the
   scripts tests before touching the next phase.
2. **Freeze audio.** Move the timeline/freeze contract. Delete the old aliases
   only after every producer and consumer imports the contract package.
3. **Freeze overlay.** The overlay contract must not depend on SQLite or media
   providers; those remain ports owned by the implementation.
4. **Freeze final-job.** This is the convergence point; it may depend on the
   first three contracts, never on their implementations.
5. **Freeze VidRush.** Move the segment/fanout contract last because it is the
   most provider-coupled phase.
6. **Move implementations one phase at a time.** After each move, verify the
   import graph: no reverse edge into the moved phase, no new SCC edge, and no
   `capabilities/scripts` root import from a contract package.

## Acceptance gates per step

- `go build ./...`.
- `go run ./cmd/archcheck --strict` (zero violations).
- Package import check: contract packages import only kernel/platform/contracts.
- The affected scripts tests pass; a failing pre-existing WIP test must be
  recorded separately and must not be masked by the split.
- The 117-file SCC is measured again; the step is rejected if the SCC does not
  shrink or if a new reverse edge appears.

## Why this is not a file-move PR

The 117-file SCC and the 68-file floor mean a same-directory split can increase
file count without reducing coupling. The contract extraction above is the
smallest sequence that can actually reduce the closure. A full split is a
multi-step migration, not a single reviewable change; each step above is the
unit of review.
