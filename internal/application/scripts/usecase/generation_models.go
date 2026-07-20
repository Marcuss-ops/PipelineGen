// Package usecase — generation_models.go is the doc-only carrier-data-flow
// index for single-item script generation. It catalogs the typed
// carriers that flow through the GenerateOneUseCase 4-phase orchestrator
// (Preparer -> EngineRunner -> Postprocessor -> Finalizer) and points
// the reader to each carrier's canonical owner file.
//
// godlike/06 SSOT: ONE canonical owner per fact. This file carries
// NO type declarations — every typed carrier below lives in exactly
// one phase file. A newcomer facing the pipeline for the first time
// can land here, see the canonical data flow, and grep-hop to the
// phase file that owns each carrier, instead of hunting across four
// files for the right struct to extend.
//
// Carrier data flow (single item path):
//
//	GenerationItemV2 (input)
//	    │
//	    ▼
//	PreparedGeneration   (phase 1 of 4 — Preparation)
//	    │
//	    ▼
//	GeneratedDraft       (phase 2 of 4 — Engine)
//	    │
//	    ▼
//	ProcessedGeneration  (phase 3 of 4 — Postprocess)
//	    │  + FinalizeInputs (aggregated at the orchestrator boundary)
//	    ▼
//	GenerationResult     (phase 4 of 4 — Finalize; terminal output)
//
// Where each carrier lives (the canonical-owner map):
//
//	PreparedGeneration     -> internal/application/scripts/usecase/generation_prepare.go
//	GeneratedDraft         -> internal/application/scripts/usecase/generation_engine.go
//	ProcessedGeneration    -> internal/application/scripts/usecase/generation_postprocess.go
//	FinalizeInputs         -> internal/application/scripts/usecase/generation_finalize.go
//	GenerationResult       -> internal/domain/script/generation_result.go   (domain layer)
//	GenerationItemV2       -> internal/domain/script/generation_item.go     (domain layer, input)
//
// Why a doc-only index and not a re-export? godlike/06 SSOT forbids
// dual ownership: declaring a type in BOTH this file AND its phase
// file would create a `compatibility alias` debt caught by the
// canonical archcheck guard `percheck_no_domain_job_compatibility_aliases`
// (migrations/api/archcheck-strict-baseline.json). A doc file with
// zero type declarations is the only safe shape — newcomers land here,
// see the flow, and jump to the canonical file to read the actual
// definitions. Add NEW carriers here WITHOUT declaring them locally:
// extend the canonical phase file and update this index pointer list
// in the same commit.
//
// Reading order for newcomers:
//
//  1. internal/application/scripts/usecase/generate_one_usecase.go
//     (the 4-phase orchestrator wiring — read the Execute body FIRST).
//  2. This file (the typed-carrier flow across the 4 phases).
//  3. Each phase file in order: generation_prepare.go, generation_engine.go,
//     generation_postprocess.go, generation_finalize.go.
//
// godlike/06 SSOT reference: architecture/current.yaml#typed-carriers-map
// (the canonical index entries for the pipeline carriers are mirrored
// from this doc to the YAML surface so a YAML tooling consumer can
// navigate without reading Go).
package usecase
