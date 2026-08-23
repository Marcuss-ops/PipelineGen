// Package scripts — workflow_state_machine.go
//
// Azione 11 (P1, CUTOVER-COMPLETE-WITH-ARTIFACTS, July 2026):
// typed 6-state machine for the script.generate workflow lifecycle.
//
// States (6 closed values):
//
//	SCRIPT_READY    →  IMAGES_PENDING  →  IMAGES_GENERATED  →  DOCUMENT_CREATED
//	   │                    │                   │                    │
//	   ├──→ FAILED          ├──→ FAILED         ├──→ FAILED          ├──→ FAILED
//	   └──→ DEAD_LETTERED   └──→ DEAD_LETTERED  └──→ DEAD_LETTERED   └──→ DEAD_LETTERED
//
// Forward-only progression via Validated() gate; FAILED and DEAD_LETTERED
// are sticky-terminal sinks (no transition out). Self-loops are idempotent.
//
// godlike/06 SSOT: this file is the SINGLE canonical owner of the
// "workflow lifecycle state" fact for script.generate jobs.
//
// godlike/07 typed-error contract: every illegal transition returns
// a typed sentinel reachable via errors.Is + errors.As traversal,
// mirroring the domain/remote/artifact_uploader.go UploadState pattern.
package generation

import (
	"errors"
	"fmt"
)

// ── WorkflowState (closed enum, 6 values) ────────────────────────────────

// WorkflowState is the canonical state of a script.generate workflow.
// The 6 values form a directed progression with FAILED and DEAD_LETTERED
// as sticky-terminal sinks.
//
// Migration invariants:
//   - String values are UPPERCASE (mirrors LifecycleState convention in
//     internal/kernel/asset/asset_types.go).
//   - The canonical list is locked at 6 values; adding a new value
//     requires a godlike/07 4-phase migration.
//   - Self-loops are idempotent (s.IsValidTransition(s) returns true).
//   - FAILED and DEAD_LETTERED are sticky-terminal sinks (no transition
//     out of either; callers must invent a NEW workflow to retry).
//   - The zero-value (s == "") is INVALID.
type WorkflowState string

const (
	// StateScriptReady — script text has been generated and reviewed.
	// Entry state for the scene-image phase.
	StateScriptReady WorkflowState = "SCRIPT_READY"

	// StateImagesPending — child image.generate jobs have been emitted
	// but not all have completed.
	StateImagesPending WorkflowState = "IMAGES_PENDING"

	// StateImagesGenerated — all child image.generate jobs have
	// completed successfully; images are staged in Drive.
	StateImagesGenerated WorkflowState = "IMAGES_GENERATED"

	// StateDocumentCreated — the Google Doc has been assembled from
	// rendered scenes + finalized images; terminal success state.
	StateDocumentCreated WorkflowState = "DOCUMENT_CREATED"

	// StateFailed — workflow terminated with an unrecoverable error.
	// Sticky-terminal: any further transitions are rejected.
	// Callers must invent a NEW workflow to retry.
	StateWorkflowFailed WorkflowState = "FAILED"

	// StateDeadLettered — workflow moved to dead-letter queue after
	// exhausting retries or encountering a token/auth failure that
	// cannot self-heal. Sticky-terminal: any further transitions are
	// rejected. Callers must manually triage the dead-letter queue.
	StateWorkflowDeadLettered WorkflowState = "DEAD_LETTERED"
)

// CanonicalWorkflowStateValues returns the closed enumeration of
// canonical WorkflowState strings. Callers use this as the
// single-source-of-truth list for migrations, dashboards, and
// Qdrant-payload validation (parallels CanonicalUploadStateValues
// in domain/remote and CanonicalLifecycleStateValues in domain/asset).
func CanonicalWorkflowStateValues() []WorkflowState {
	return []WorkflowState{
		StateScriptReady,
		StateImagesPending,
		StateImagesGenerated,
		StateDocumentCreated,
		StateWorkflowFailed,
		StateWorkflowDeadLettered,
	}
}

// Valid returns true if s is a known canonical WorkflowState. The
// zero-value (s == "") is intentionally invalid — callers must
// initialise via a typed constant.
func (s WorkflowState) Valid() bool {
	switch s {
	case StateScriptReady, StateImagesPending, StateImagesGenerated,
		StateDocumentCreated, StateWorkflowFailed, StateWorkflowDeadLettered:
		return true
	}
	return false
}

// IsValidTransition reports whether moving from `from` to `to` is
// one of the allowed edges of the workflow state machine.
//
// Legal progression (forward chain):
//
//	SCRIPT_READY    → IMAGES_PENDING → IMAGES_GENERATED → DOCUMENT_CREATED
//	   │                  │                 │                  │
//	   ├──→ FAILED        ├──→ FAILED       ├──→ FAILED        ├──→ FAILED
//	   └──→ DEAD_LETTERED └──→ DEAD_LETTERED└──→ DEAD_LETTERED └──→ DEAD_LETTERED
//
// Self-loops are IDEMPOTENT (s.IsValidTransition(s) returns true).
//
// FAILED and DEAD_LETTERED are sticky-terminal sinks — no transition
// out of either. Callers must invent a NEW workflow to retry.
//
// All backward edges are REJECTED (e.g. IMAGES_PENDING → SCRIPT_READY
// returns false with a typed error).
func (s WorkflowState) IsValidTransition(to WorkflowState) bool {
	// Idempotent self-loop (mirrors UploadState.IsValidTransition).
	if s == to {
		return true
	}

	// FAILED and DEAD_LETTERED are sticky-terminal sinks.
	if s == StateWorkflowFailed || s == StateWorkflowDeadLettered {
		return false
	}

	// Any non-terminal state can transition to FAILED or DEAD_LETTERED.
	if to == StateWorkflowFailed || to == StateWorkflowDeadLettered {
		return true
	}

	switch s {
	case StateScriptReady:
		return to == StateImagesPending
	case StateImagesPending:
		return to == StateImagesGenerated
	case StateImagesGenerated:
		return to == StateDocumentCreated
	case StateDocumentCreated:
		// Terminal success — no forward progression.
		return false
	}

	// Zero-value ("") or any non-canonical state: no transitions.
	return false
}

// ── Typed-error contract (godlike/07) ──────────────────────────────────

// ErrIllegalWorkflowStateTransition is the typed sentinel for
// IsValidTransition rejections. Callers errors.Is against the
// sentinel AND errors.As against *IllegalWorkflowTransitionError to
// extract the {From, To} pair — mirroring the UploadState pattern
// in domain/remote/artifact_uploader.go.
var ErrIllegalWorkflowStateTransition = errors.New("illegal workflow state transition")

// IllegalWorkflowTransitionError is the typed-error-data envelope that
// exposes From + To state at the seam so callers can route on the
// specific (from → to) pair.
type IllegalWorkflowTransitionError struct {
	From WorkflowState
	To   WorkflowState
}

// Error implements the error interface (godlike/07 typed-error contract).
func (e *IllegalWorkflowTransitionError) Error() string {
	return fmt.Sprintf("illegal workflow state transition: %s -> %s", e.From, e.To)
}

// Is enables errors.Is(err, ErrIllegalWorkflowStateTransition) on
// chains wrapping *IllegalWorkflowTransitionError. Per godlike/07, both
// the sentinel AND the typed-data error are reachable in a single
// probe path.
func (e *IllegalWorkflowTransitionError) Is(target error) bool {
	return target == ErrIllegalWorkflowStateTransition
}

// NewIllegalWorkflowTransitionError is the canonical constructor for
// *IllegalWorkflowTransitionError at rejection sites.
func NewIllegalWorkflowTransitionError(from, to WorkflowState) error {
	return &IllegalWorkflowTransitionError{From: from, To: to}
}

// ValidateAndTransition performs a Validated() gate check followed by
// an IsValidTransition check. Returns nil if the transition is legal,
// or a typed error if either check fails.
//
// This is the single canonical entry point for all workflow state
// transitions — callers do NOT call IsValidTransition directly in
// production code; they call ValidateAndTransition so every transition
// is logged + validated at the same seam.
//
// The zero-value (s == "") is rejected by the Valid() gate BEFORE
// IsValidTransition is consulted, so a half-wired workflow cannot
// slip through as empty-but-valid (godlike/07 no-fake-availability).
func (s WorkflowState) ValidateAndTransition(to WorkflowState) error {
	if !s.Valid() {
		return fmt.Errorf("workflow state transition: source state %q is not a valid WorkflowState", s)
	}
	if !to.Valid() {
		return fmt.Errorf("workflow state transition: target state %q is not a valid WorkflowState", to)
	}
	if !s.IsValidTransition(to) {
		return NewIllegalWorkflowTransitionError(s, to)
	}
	return nil
}
