// Package scripts — generation_outcome.go: PURE outcome classification
// for script.generate jobs. Only ClassifySingleOutcome remains
// (multi-item classification was removed with the inline Execute path).
package jobs

import (
	"context"
	"errors"
	"fmt"

	domainScript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
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
