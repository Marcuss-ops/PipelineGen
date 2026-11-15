# P3 contract-first split: `capabilities/scripts` (2026-11-15)

## Current binding facts

- 384 production Go files, 88,519 production LOC.
- The documented dependency graph contains a 117-file strongly connected
  component and a 68-file floor at the former 600-LOC cap. With the cap now at
  800, file moves alone still cannot reduce the SCC: the imports, not the file
  lengths, bind the closure.
- The package is a capability, not a utility bag. Splitting it by directory
  first would create import cycles and move the same shared model types around.

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
