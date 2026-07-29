// Package stockpipeline — orchestrator_steps.go
// (PR-STOCK-ORCHESTRATOR-SPLIT, July 2026).
//
// SLIM orchestrator-steps surface: package doc + Step interface +
// 6 canonical step key constants. The 6 step implementations
// live in 1-file-per-Step single-purpose capability files
// (godlike/06 SSOT one-canonical-owner-per-fact) per AGENTS.md
// Pattern 5:
//
//   - StockPlanStep           → step_plan_clips.go
//   - StockStageSourcesStep   → step_stage_sources.go
//   - StockExtractClipsStep   → step_extract_clips.go
//   - StockComposeChunksStep  → step_compose_chunks.go
//   - StockPublishStep        → step_publish.go
//   - StockFinalizeStep       → step_finalize.go
//
// The 6 step-level typed sentinels (ErrStockPublishArtifactFailed,
// ErrStockFinalizeSpineFailed, ErrStockFinalizeLeaseMissing,
// ErrStockFnRequired, ErrStockStageSourcesAllFailed,
// ErrStockComposeChunksAllFailed) live in
// orchestrator_step_errors.go.
//
// godlike/06 SSOT (one canonical owner per fact):
//   - 6 step key constants:     THIS FILE (orchestrator_steps.go)
//   - Step interface:           THIS FILE (orchestrator_steps.go)
//   - 6 step implementations:   step_plan_clips.go +
//     step_stage_sources.go +
//     step_extract_clips.go +
//     step_compose_chunks.go +
//     step_publish.go +
//     step_finalize.go
//   - 6 step-level sentinels:   orchestrator_step_errors.go
//   - DefaultStockSteps() +
//     compile-time assertions:  orchestrator_defaults.go
//   - StepRunner interface +
//     RunState + 6 accessors:   step_runner.go
//   - Artifact ID helpers:      orchestrator_fingerprint.go
//   - Metadata helpers:         orchestrator_metadata.go
//
// PR-STOCK-ORCHESTRATOR-SPLIT extracted the 6 step impls + 6
// sentinels from this file on 2026-07-04. The pre-split file
// was 874 LoC (the user spec referenced 949 LoC; the spec's
// "slim RunResilient ladder ~140 LoC" sub-file would have been
// empty per godlike/07 no-fake-availability — RunResilient lives
// in orchestrator.go today, not in orchestrator_steps.go; the 7
// step file names in the spec implied splitting StockFinalizeStep
// into 3+ sub-step files which is an aggressive split of a single
// Step type rather than the natural 1-file-per-Step unit; the
// minimum-ripple 1-file-per-Step split (6 step files + sentinels
// + slimmed orchestrator_steps.go = 8 files) is the canonical
// interpretation; see the commit body for the full honest scope
// disclosure).
package stockpipeline

import "context"

// ── Canonical step keys (PipelineGen §12-5) ────────────────────────────
//
// SSOT: changing any of these constants is a wire-format break for the
// canonical §12-3 step store. New stages MUST add new constants;
// existing ones MUST NOT be renamed without a migration forward-pointer
// via architecture/current.yaml.
const (
	StepKeyStockPlan          = "stock.plan"
	StepKeyStockStageSources  = "stock.stage_sources"
	StepKeyStockExtractClips  = "stock.extract_clips"
	StepKeyStockComposeChunks = "stock.compose_chunks"
	StepKeyStockPublish       = "stock.publish"
	StepKeyStockFinalize      = "stock.finalize"
)

// Step is the canonical typed contract for a single orchestrator-side
// step. The Orchestrator iterates over a typed []Step slice and
// dispatches each step's Run with a StepRunner (defined in step_runner.go).
//
// Name() returns the canonical step_key (one of the
// StepKeyStockXxx constants above) — used to build the
// steps.StepKey triple on checkpoint rows. The Name() output MUST
// match the typed slice's position; changing it is a pipeline-order
// break.
//
// Run() executes the step body. Returns nil error on success
// (orchestrator dispatches MarkCompleted); returns non-nil error
// on failure (orchestrator dispatches MarkFailed + aborts the
// run with the typed error). Optional non-fatal outcomes (like
// the projection-resilience INDEX_PENDING flip) MUST be expressed
// via state mutation (e.g. State.FinalStatus = StatusIndexPending)
// rather than returning error — error means abort.
type Step interface {
	Name() string
	Run(ctx context.Context, runner StepRunner) error
}
