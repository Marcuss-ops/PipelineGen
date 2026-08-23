// Package usecase — engine_invoke.go: engine-phase error helpers.
//
// Extracted from generate_one_usecase_execute.go (July 2026).
// Owns: logPhaseError, preConstructError, generateOnePreConstructError.
package usecase

import (
	"fmt"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

	"go.uber.org/zap"
)

// ── SCRIPT-T03-USECASE (P0, 2026-07-15) godlike/07 typed-error gate ──

// logPhaseError is the canonical usecase-boundary error-logging helper
// for the single-item orchestrator. Per godlike/07 typed-error contract
// + NO_FAKE_AVAILABILITY, every `return nil, err` at the orchestrator
// boundary MUST log the diagnostic context (item_id, phase, error)
// BEFORE returning the typed error. The typed error remains the
// propagation surface (handler reads it via errors.Is for HTTP status
// mapping) but operators now have a log trail for every failure.
//
// The structured log fields let operators correlate the typed error
// (which surfaces to the client as a 4xx/5xx) with the diagnostic log
// entry (which carries the full error chain + item_id + phase). This
// is the canonical "log+typed-propagate" pattern per godlike/07.
//
// PR-ERROR-SURFACING (2026-07-04): the return value is a TERTIARY
// wrapped error (umbrella + phase-sentinel + inner) so callers can
// errors.Is walk to ANY of the three layers:
//
//	errors.Is(err, scriptpkg.ErrScriptGenerationFailed)        → umbrella (script.generate capability)
//	errors.Is(err, scriptpkg.ErrPlanInvalid|ErrSourceResolutionFailed|ErrPostprocessFailed)  → phase
//	errors.Is(err, errAlreadyWrapped)                          → fine-grained (e.g. ErrEntityExtractorUnavailable)
//
// The umbrella wrap is canonical SSOT for "any script.generate failure";
// the phase + fine-grained sentinels preserve granular classification.
//
// PR-ERROR-SURFACING commit-5 (2026-07-04): umbrella coverage = 7/7 paths.
// Every error-return path inside `Execute` routes through one of three
// canonical wrap helpers so callers can `errors.Is(err,
// scriptpkg.ErrScriptGenerationFailed)` uniformly:
//   - 6 phase paths → logPhaseError (Phase 2 validate, Phase 3
//     source_resolve, Phase 4 voiceover_resolve, Phase 4
//     registry_validate, Phase 5 engine, Phase 6 postprocess).
//   - 2 pre-construction paths (counted as 1 "pre-construction"
//     category in 7/7: uc=nil + engine=nil) → preConstructError /
//     generateOnePreConstructError.
//
// All three helpers emit `fmt.Errorf("%w: ...: %w: %w",
// ErrScriptGenerationFailed, <label>, phaseSentinel, innerErr)` so the
// chain reaches the umbrella + the phase sentinel + the inner error
// (Go 1.20+ N-ary `%w` semantics — errors.Is walks boolean OR). godlike/07
// typed-error contract: NO path emits a plain unwrapped error.
// Inner errors may carry their own typed structs (GenerationError,
// PostprocessError) so `errors.As` continues to work for fine-grained
// classification alongside the umbrella.
//
// Returns the wrapped error so callers can write
//
//	return nil, uc.logPhaseError(item, "validate", scriptpkg.ErrPlanInvalid, err)
//
// for compactness.
func (uc *GenerateOneUseCase) logPhaseError(
	item scriptpkg.GenerationItemV2,
	phase string,
	sentinel error,
	err error,
	tracker *ProgressTracker,
) error {
	if uc.log != nil {
		uc.log.Warn("generate-one: phase failed",
			zap.String("item_id", item.ID),
			zap.String("phase", phase),
			zap.Error(err))
	}
	if tracker != nil {
		tracker.TrackEvent("stage.failed", "Pipeline stage failed", map[string]any{
			"item_id": item.ID,
			"phase":   phase,
			"error":   err.Error(),
		})
	}
	return fmt.Errorf("%w: %w: %w", scriptpkg.ErrScriptGenerationFailed, sentinel, err)
}

// ── PR-ERROR-SURFACING commit-5 pre-construction helpers ──

// preConstructError wraps a non-nil uc's pre-construction failure
// through the canonical umbrella + phase-sentinel chain. Mirrors the
// logPhaseError helper above but WITHOUT the per-item log fields
// (these paths run BEFORE any item is parsed in, so there is no item
// correlation). The receiver remains non-nil — the engine=nil check
// runs AFTER the uc=nil test in Execute, so by the time this helper
// is invoked for engine=nil, uc is guaranteed non-nil.
func (uc *GenerateOneUseCase) preConstructError(
	reason string,
	sentinel error,
	err error,
) error {
	if uc.log != nil {
		uc.log.Warn("generate-one: construction failed",
			zap.String("reason", reason))
	}
	return fmt.Errorf("%w: %s: %w: %w", scriptpkg.ErrScriptGenerationFailed, reason, sentinel, err)
}

// generateOnePreConstructError is the package-level wrapper used when
// the receiver uc is NIL itself (the first pre-construction check in
// Execute). Cannot log because no logger accessor exists without a
// receiver; the typed-error chain is the canonical propagation
// surface for handlers / dashboards.
func generateOnePreConstructError(
	_ *GenerateOneUseCase,
	reason string,
	sentinel error,
	err error,
) error {
	return fmt.Errorf("%w: %s: %w: %w", scriptpkg.ErrScriptGenerationFailed, reason, sentinel, err)
}
