// Package scripts — generation_outcome.go is the canonical owner of the
// job-handler outcome classification (godlike/06 SSOT: one owner per
// fact). The Outcome enum + Diagnostic struct + the two Classify
// functions are pure: no I/O, no log writes, no DB calls, no manifest
// construction, no filesystem ops. The handler calls these to decide
// the (mapped_envelope, dispatch_err) shape that the worker broker
// interprets as FAILED vs COMPLETED-with-ok=false.
//
// PR-GODOBJ-4 KILL list applied (per user spec, July 2026):
//
//	(1) ClassifyGenerationOutcome + ClassifySingleOutcome are PURE
//	    functions — they classify a (result, err) pair into a typed
//	    Outcome + Diagnostic. NO filesystem ops in this file.
//	(2) Single + batch outcomes are classified via SEPARATE functions
//	    (ClassifySingleOutcome, ClassifyGenerationOutcome). The
//	    handler dispatch (generation_handler.go) routes the work; the
//	    classification is shape-typed, not branched on len(items).
//
// godlike/07 typed-error contract: every Outcome carries a Diagnostic
// with a typed `Err` field. Handler callers propagate Diagnostic.Err
// via fmt.Errorf("%w", diagnostic.Err) so errors.Is + errors.As probes
// remain reliable for retry-vs-no-retry decisions in the worker.
//
// godlike/07 honest-limitation disclosure (AGENTS.md Check 44 LoC cap):
// This file exceeds the 66-LoC transitional cap (~170 LoC) because
// it owns 7 typed Outcome enum values + 2 ClassifyFns + the
// OutcomeDiagnostic struct. The classify logic for context.Canceled
// pre-emption is verbose by design (errors.Is check + phase label).
// Forward-pointer linked_issue (zero-baseline rule):
// PR-GODOBJ-4d-OUTCOME-SLIM extracts the single-vs-multi
// classification branches into per-path helper files (≤30 LoC each).
// Deadline 2026-08-15.
package jobs

import (
	"context"
	"errors"
	"fmt"

	usecase "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// Outcome is the canonical classification of a script.generate job
// run. The handler maps each Outcome to a (mapped_envelope,
// dispatch_err) pair so the worker broker can route FAILED vs
// COMPLETED-with-ok=false correctly (godlike/07 no-fake-availability:
// a multi-item all-failed run MUST surface as FAILED so retry kicks
// in).
type Outcome string

const (
	// Single-mode outcomes (one item, len(env.Items)==1).
	OutcomeSingleSuccess Outcome = "SINGLE_SUCCESS"
	OutcomeSingleFailure Outcome = "SINGLE_FAILURE"

	// Multi-mode outcomes (multiple items, len(env.Items)>1).
	OutcomeMultiInfraFailure Outcome = "MULTI_INFRA_FAILURE"
	OutcomeMultiAllFailed    Outcome = "MULTI_ALL_FAILED"
	OutcomeMultiPartial      Outcome = "MULTI_PARTIAL"
	OutcomeMultiFullSuccess  Outcome = "MULTI_FULL_SUCCESS"

	// Cancellation observed at any phase (Issue 6 / P1).
	OutcomeCanceled Outcome = "CANCELED"
)

// Diagnostic carries the context the handler needs to project onto
// the (mapped_envelope, dispatch_err) return shape. Err is propagated
// through fmt.Errorf("%w", diagnostic.Err) so the worker broker's
// errors.Is probe stays intact.
type Diagnostic struct {
	// Outcome is the typed classification.
	Outcome Outcome
	// Err is the canonical error to surface to the dispatch broker
	// (nil for non-failure outcomes). Typed errors via fmt.Errorf
	// %w-wrap.
	Err error
	// Total / Failed / Succeeded carry the summary counts for multi
	// outcomes; both are 0 for single outcomes (the single path
	// uses outcome.Summary counts from the typed envelope instead).
	Total     int
	Succeeded int
	Failed    int
	// Phase records the cancel observation phase (handler-entry,
	// single-item-pre-execute, multi-item-pre-execute) when the
	// diagnostic surfaces cancellation. Empty otherwise.
	Phase string
}

// ClassifySingleOutcome classifies a single-item (result, err) pair.
// Cancellation is checked first (errors.Is err == context.Canceled).
// On an infra error a SINGLE_FAILURE outcome is surfaced with the
// typed error wrapped. On success the outcome is SINGLE_SUCCESS.
// Result may be nil on a SINGLE_FAILURE — the handler does not call
// PersistGeneratedArtifacts for failed single runs.
func ClassifySingleOutcome(result *domainScript.GenerationResult, err error) Diagnostic {
	if errors.Is(err, context.Canceled) {
		return Diagnostic{
			Outcome: OutcomeCanceled,
			Err:     fmt.Errorf("script.generate cancelled: %w", err),
			Phase:   "single-item",
		}
	}
	if err != nil {
		return Diagnostic{
			Outcome: OutcomeSingleFailure,
			Err:     fmt.Errorf("script.generate single failed: %w", err),
		}
	}
	return Diagnostic{Outcome: OutcomeSingleSuccess}
}

// ClassifyGenerationOutcome classifies a multi-item (manyResult, err)
// pair. Cancellation is checked first. A nil manyResult with no
// error surfaces as MULTI_INFRA_FAILURE with a synthesised error
// (defensive — current GenerateManyUseCase always returns non-nil,
// but maintains the godlike/07 fail-closed shape). Failed==Total
// (Total>0) → MULTI_ALL_FAILED; Failed>0 && Failed<Total →
// MULTI_PARTIAL; Failed==0 → MULTI_FULL_SUCCESS. Total==0 is treated
// as MULTI_INFRA_FAILURE (a zero-item batch is a malformed input).
func ClassifyGenerationOutcome(manyResult *usecase.GenerateManyResult, err error) Diagnostic {
	if manyResult != nil && errors.Is(err, context.Canceled) {
		return Diagnostic{
			Outcome:   OutcomeCanceled,
			Err:       fmt.Errorf("script.generate cancelled mid-multi-item: %w", err),
			Total:     manyResult.Summary.Total,
			Succeeded: manyResult.Summary.Succeeded,
			Failed:    manyResult.Summary.Failed,
			Phase:     "multi-item",
		}
	}
	if manyResult == nil {
		synth := err
		if synth == nil {
			synth = fmt.Errorf("script.generate: use case returned nil result without error")
		} else {
			synth = fmt.Errorf("script.generate: use-case nil result: %w", err)
		}
		return Diagnostic{
			Outcome: OutcomeMultiInfraFailure,
			Err:     synth,
		}
	}
	summary := manyResult.Summary
	if summary.Total > 0 && summary.Failed == summary.Total {
		wrapped := err
		if wrapped == nil {
			wrapped = fmt.Errorf("script generation: all %d items failed", summary.Total)
		} else {
			wrapped = fmt.Errorf("script generation: all %d items failed: %w", summary.Total, err)
		}
		return Diagnostic{
			Outcome:   OutcomeMultiAllFailed,
			Err:       wrapped,
			Total:     summary.Total,
			Succeeded: summary.Succeeded,
			Failed:    summary.Failed,
		}
	}
	if summary.Failed > 0 {
		return Diagnostic{
			Outcome:   OutcomeMultiPartial,
			Err:       nil,
			Total:     summary.Total,
			Succeeded: summary.Succeeded,
			Failed:    summary.Failed,
		}
	}
	if summary.Total == 0 {
		return Diagnostic{
			Outcome: OutcomeMultiInfraFailure,
			Err:     fmt.Errorf("script.generate: zero-item batch is malformed"),
		}
	}
	return Diagnostic{
		Outcome:   OutcomeMultiFullSuccess,
		Err:       nil,
		Total:     summary.Total,
		Succeeded: summary.Succeeded,
		Failed:    summary.Failed,
	}
}
