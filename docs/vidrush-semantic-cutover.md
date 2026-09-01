# VidRush Semantic Cutover — Script → SceneIR → VisualNER → MediaSampler → Local Stock → Bindings → MediaCert

## Status

All new components are built, gated, and green on `main`. The cutover
sequence below is the safe, incremental wiring into the live VidRush
`Runner` orchestration path. It follows the Fase plan's rule:

> Non sostituirei tutto nello stesso commit. Prima inseriamo SceneIR,
> poi VisualNER, poi MediaSampler, poi Local Stock, infine MediaCert.
> A quel punto eliminiamo definitivamente i vecchi resolver/extractor/
> sampler duplicati.

## Components built (all green)

| Fase  | Component             | Location                                              | Make target                     |
| ----- | --------------------- | ----------------------------------------------------- | ------------------------------- |
| 1     | SceneIR compiler      | `internal/kernel/sceneir/`                            | `make verify-sceneir`           |
| 2     | MediaCert certifier   | `internal/capabilities/mediacert/` + `cmd/mediacert/` | `make verify-vidrush-semantic`  |
| 3     | VisualNER (Rust)      | `rust/visualner/`                                     | `make verify-visualner`          |
| 4     | MediaSampler (Rust)  | `rust/mediasampler/`                                  | `make verify-mediasampler`       |
| 5     | Local Stock Intel.    | `internal/capabilities/stockintelligence/`            | `make verify-stockintelligence`  |
| —     | Aggregate gate        | —                                                     | `make verify-media-intelligence` |
| —     | Pre-final cert.       | —                                                     | `make vidrush-pre-final`         |

`make vidrush-pre-final` prints `VIDRUSH_PRE_FINAL = TRUE`.

## Live orchestration insertion points

The VidRush `Runner` (in `internal/capabilities/scripts/`) owns the
canonical orchestration path. The insertion points for the new chain are:

1. **Script → SceneIR Compiler**
   - Insert `sceneir.Compile` at the segment-boundary in
     `runner.go::runVidRushJoinAndPrepare` / `beginVidRush`, after the
     script segments are canonicalized and before the legacy planner reads
     them. The SceneIR's immutable `SourceText` + `SegmentID` replace the
     raw segment text the legacy planner consumes.
   - Gate: `make verify-sceneir` (identity, immutability, profiles).

2. **SceneIR → VisualNER (Rust)**
   - Replace the legacy entity extractor call with the VisualNER FFI/JSON
     call, feeding `SceneIR.SourceText` and populating
     `SceneIR.Entities` with the source-grounded `VisualEntity` results.
   - Gate: `make verify-visualner` (NO EVIDENCE → NO ENTITY, exactly 3,
     Greek salad / hummus source-grounded).

3. **VisualNER → MediaSampler (Rust)**
   - Replace the legacy chooser with the MediaSampler call, feeding
     `SceneIR.Profile` (subject + terms) and the candidate set. The
     sampler rejects subject mismatches (boxing for Greek Salad) and
     cross-scene reuse.
   - Gate: `make verify-mediasampler` (subject mismatch, cross-scene
     reuse, determinism, image fanout).

4. **MediaSampler → Local Stock Resolver**
   - Wire `internal/capabilities/stockintelligence.Service` ahead of the
     live Artlist browser path. LOCAL FIRST PROVIDER SECOND: Qdrant local
     search → SQLite hydrate → MediaSampler; provider live only when
     `local_candidates < threshold` or `best_score < minimum_quality`.
   - Gate: `make verify-stockintelligence` (0 requests on local-first,
     exactly 1 on fallback).

5. **Local Stock → Bindings → MediaCert**
   - Run `mediacert.Certify(spec, result)` on the completed run before
     marking the job `SUCCEEDED`. A `CERTIFIED=false` report must fail the
     job (even when `JobStatus=SUCCEEDED`).
   - Gate: `make verify-vidrush-semantic` + `TestMediaCertRejectsTechnicallySuccessfulButWrongRun`.

## Legacy removal (after every gate above is green on the live path)

Once the new chain carries production traffic and `make vidrush-pre-final`
stays green across N runs, delete the legacy duplicate surfaces:

- legacy resolver / extractor / sampler adapters in
  `internal/capabilities/scripts/adapters/`
- any parallel entity-extraction / chooser path that the new chain made
  redundant.

Keep `SceneIR` + `VisualNER` + `MediaSampler` + `stockintelligence` +
`mediacert` as the sole owners of their respective concepts.

## Safe commit sequence

1. Insert SceneIR at the segment boundary (gates: `verify-sceneir`).
2. Switch entity extraction to VisualNER (gates: `verify-visualner`).
3. Switch the chooser to MediaSampler (gates: `verify-mediasampler`).
4. Wire Local Stock ahead of Artlist live (gates: `verify-stockintelligence`).
5. Add `mediacert.Certify` to the job-completion path
   (gates: `verify-vidrush-semantic`).
6. Delete legacy resolver/extractor/sampler duplicates only after 1–5
   are green on the live path.

Each step is its own commit. Do NOT combine them.
