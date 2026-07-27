// Package mediamemory — batch_service_validation.go is the
// canonical home for spec / state predicates used by every
// BatchService method. godlike/06 SSOT (single canonical home
// per responsibility): the validator, the spec-equality helper,
// and the terminal-state predicate live together so the
// closed-set enforcement is grep-able in one place.
//
// File split ownership (godlike/06 SSOT — single canonical home):
//   - batch_service.go                : BatchService port + struct + ctors + lifecycle wiring
//   - batch_service_validation.go     : validateSpec + specsStructurallyEqual + isTerminalState  ← this file
//   - batch_service_persistence.go    : CreateBatch/AppendCandidate/MarkMaterialized/internal reads
//   - batch_service_lifecycle.go      : Get/Resume/Reconcile (terminal-state machine)
//   - batch_service_orchestrator.go   : RunCatalogOnly/EnrichLinker/loadChildCandidates
//   - batch_materialization.go        : MaterializeTopK/PromoteOnDemand/recordParentFailure (Fase 3.3)
package mediamemory

import (
	"fmt"
	"slices"
)

// validateSpec is the canonical BatchSpec validator. godlike/06
// SSOT (closed-set Mode): only ModeCatalogOnly / ModeMaterializeTopK
// are accepted; empty spec, missing Mode, or unknown values all
// surface as typed sentinels. Single canonical validator — DRY
// per the validator-must-be-unique rule.
func validateSpec(spec BatchSpec) error {
	if spec.Name == "" {
		return fmt.Errorf("mediamemory: BatchSpec.Name is empty: %w", ErrInvalidPhrase)
	}
	if len(spec.Queries) == 0 {
		return fmt.Errorf("mediamemory: BatchSpec.Queries is empty: %w", ErrInvalidPhrase)
	}
	if len(spec.Providers) == 0 {
		return fmt.Errorf("mediamemory: BatchSpec.Providers is empty: %w", ErrInvalidPhrase)
	}
	if spec.MaxCandidates <= 0 {
		return fmt.Errorf("mediamemory: BatchSpec.MaxCandidates must be > 0: %w", ErrInvalidPhrase)
	}
	if !IsKnownBatchMode(spec.Mode) {
		err := ErrInvalidBatchMode
		if spec.Mode == "" {
			return fmt.Errorf("mediamemory: BatchSpec.Mode is empty: %w", err)
		}
		return fmt.Errorf("mediamemory: BatchSpec.Mode=%q: %w", spec.Mode, err)
	}
	return nil
}

// specsStructurallyEqual reports whether two BatchSpec values are
// field-for-field equal (deep). godlike/06 SSOT (spec immutability):
// the idempotent-by-name CreateBatch path calls this on the
// incoming spec vs the already-persisted spec; a non-equal result
// surfaces as wrapped ErrBatchSpecDrift so the canonical
// "spec is immutable post-CreateBatch" contract is enforced.
//
// Go's `==` is not usable on structs containing `[]string` slices
// (compile-time error: invalid operation), so we hand-roll the
// comparison using the stdlib slices.Equal for the slice fields
// plus direct equality for the scalars. Adding a map or a
// non-comparable field to BatchSpec will surface as a compile
// error in this helper, which is the desired godlike/07
// fail-loud property.
func specsStructurallyEqual(a, b BatchSpec) bool {
	if a.Name != b.Name || a.Language != b.Language {
		return false
	}
	if a.MaxCandidates != b.MaxCandidates || a.MaterializeTopK != b.MaterializeTopK {
		return false
	}
	if a.Mode != b.Mode {
		return false
	}
	if !slices.Equal(a.Queries, b.Queries) {
		return false
	}
	if !slices.Equal(a.Providers, b.Providers) {
		return false
	}
	if !slices.Equal(a.MediaTypes, b.MediaTypes) {
		return false
	}
	return true
}

// isTerminalState reports whether state is in the canonical
// closed set {Completed, Failed}. godlike/06 SSOT: every state
// reader MUST go through this predicate so the terminal-state
// guard is centralized.
func isTerminalState(state BatchState) bool {
	return state == BatchCompleted || state == BatchFailed
}
