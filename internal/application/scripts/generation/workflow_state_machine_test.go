package generation

import (
	"errors"
	"testing"
)

// ── Test 1: Legal forward transitions ──────────────────────────────────

func TestWorkflowState_LegalForwardTransitions(t *testing.T) {
	tests := []struct {
		from WorkflowState
		to   WorkflowState
	}{
		{StateScriptReady, StateImagesPending},
		{StateImagesPending, StateImagesGenerated},
		{StateImagesGenerated, StateDocumentCreated},
	}
	for _, tt := range tests {
		if !tt.from.IsValidTransition(tt.to) {
			t.Errorf("expected %s -> %s to be valid", tt.from, tt.to)
		}
	}
}

// ── Test 2: Illegal backward transitions → typed error ─────────────────

func TestWorkflowState_IllegalBackwardTransitions(t *testing.T) {
	tests := []struct {
		from WorkflowState
		to   WorkflowState
	}{
		{StateImagesPending, StateScriptReady},
		{StateImagesGenerated, StateImagesPending},
		{StateImagesGenerated, StateScriptReady},
		{StateDocumentCreated, StateImagesGenerated},
		{StateDocumentCreated, StateImagesPending},
		{StateDocumentCreated, StateScriptReady},
	}
	for _, tt := range tests {
		if tt.from.IsValidTransition(tt.to) {
			t.Errorf("expected %s -> %s to be ILLEGAL (backward)", tt.from, tt.to)
		}
		err := tt.from.ValidateAndTransition(tt.to)
		if err == nil {
			t.Fatalf("ValidateAndTransition(%s, %s): expected typed error, got nil", tt.from, tt.to)
		}
		if !errors.Is(err, ErrIllegalWorkflowStateTransition) {
			t.Errorf("ValidateAndTransition(%s, %s): expected ErrIllegalWorkflowStateTransition, got %v", tt.from, tt.to, err)
		}
		var ite *IllegalWorkflowTransitionError
		if !errors.As(err, &ite) {
			t.Errorf("ValidateAndTransition(%s, %s): expected *IllegalWorkflowTransitionError, got %T", tt.from, tt.to, err)
		} else {
			if ite.From != tt.from || ite.To != tt.to {
				t.Errorf("IllegalWorkflowTransitionError.From=%s, .To=%s; want %s, %s",
					ite.From, ite.To, tt.from, tt.to)
			}
		}
	}
}

// ── Test 3: Retry idempotency (self-loop) ──────────────────────────────

func TestWorkflowState_SelfLoopIdempotency(t *testing.T) {
	for _, s := range CanonicalWorkflowStateValues() {
		if !s.IsValidTransition(s) {
			t.Errorf("expected self-loop %s -> %s to be valid (idempotent)", s, s)
		}
		if err := s.ValidateAndTransition(s); err != nil {
			t.Errorf("ValidateAndTransition(%s, %s): expected nil (idempotent), got %v", s, s, err)
		}
	}
}

// ── Test 4: Sticky-terminal sinks (FAILED, DEAD_LETTERED) ──────────────

func TestWorkflowState_StickyTerminalSinks(t *testing.T) {
	terminals := []WorkflowState{StateWorkflowFailed, StateWorkflowDeadLettered}
	nonTerminals := []WorkflowState{
		StateScriptReady, StateImagesPending,
		StateImagesGenerated, StateDocumentCreated,
	}

	for _, term := range terminals {
		// Self-loop is idempotent.
		if !term.IsValidTransition(term) {
			t.Errorf("expected self-loop %s -> %s to be valid", term, term)
		}
		// No transition out to any NON-terminal state.
		for _, nt := range nonTerminals {
			if term.IsValidTransition(nt) {
				t.Errorf("expected sticky-terminal %s -> %s to be ILLEGAL", term, nt)
			}
		}
		// No transition out to the OTHER terminal.
		other := StateWorkflowFailed
		if term == StateWorkflowFailed {
			other = StateWorkflowDeadLettered
		}
		if term.IsValidTransition(other) {
			t.Errorf("expected sticky-terminal %s -> %s to be ILLEGAL", term, other)
		}
	}

	// Any non-terminal CAN transition to a terminal.
	for _, nt := range nonTerminals {
		for _, term := range terminals {
			if !nt.IsValidTransition(term) {
				t.Errorf("expected %s -> %s to be valid", nt, term)
			}
		}
	}
}

// ── Test 5: Zero-value is invalid ──────────────────────────────────────

func TestWorkflowState_ZeroValueInvalid(t *testing.T) {
	var zero WorkflowState
	if zero.Valid() {
		t.Error("expected zero-value WorkflowState to be invalid")
	}
	if zero.IsValidTransition(StateScriptReady) {
		t.Error("expected zero-value -> SCRIPT_READY to be ILLEGAL")
	}
	err := zero.ValidateAndTransition(StateScriptReady)
	if err == nil {
		t.Fatal("expected ValidateAndTransition(zero, SCRIPT_READY) to error")
	}
	// ValidateAndTransition must fail at the Valid() gate, NOT the
	// transition gate — so ErrIllegalWorkflowStateTransition must NOT
	// be in the chain.
	if errors.Is(err, ErrIllegalWorkflowStateTransition) {
		t.Error("expected Valid() gate to reject BEFORE IsValidTransition is consulted")
	}
}

// ── Test 6: ValidateAndTransition rejects invalid target ────────────────

func TestWorkflowState_ValidateAndTransition_InvalidTarget(t *testing.T) {
	err := StateScriptReady.ValidateAndTransition("BOGUS_STATE")
	if err == nil {
		t.Fatal("expected ValidateAndTransition(SCRIPT_READY, BOGUS_STATE) to error")
	}
	// Should fail at the target Valid() gate.
	if errors.Is(err, ErrIllegalWorkflowStateTransition) {
		t.Error("expected target Valid() gate to reject BEFORE IsValidTransition is consulted")
	}
}

// ── Test 7: Canonical values completeness ──────────────────────────────

func TestWorkflowState_CanonicalValuesComplete(t *testing.T) {
	vals := CanonicalWorkflowStateValues()
	if len(vals) != 6 {
		t.Fatalf("expected 6 canonical values, got %d: %v", len(vals), vals)
	}
	seen := make(map[WorkflowState]bool)
	for _, v := range vals {
		if seen[v] {
			t.Errorf("duplicate canonical value: %s", v)
		}
		seen[v] = true
		if !v.Valid() {
			t.Errorf("canonical value %s is not Valid()", v)
		}
	}
}

// ── Test 8: DOCUMENT_CREATED is terminal success (no forward) ────────────

func TestWorkflowState_DocumentCreatedTerminalSuccess(t *testing.T) {
	// DOCUMENT_CREATED is terminal success — no forward progression
	// to any non-terminal state (backward edges are rejected).
	// It CAN still transition to FAILED/DEAD_LETTERED via the
	// early-return in IsValidTransition (post-document-creation
	// failure is legitimate).
	nonSelfTargets := []WorkflowState{
		StateScriptReady, StateImagesPending, StateImagesGenerated,
	}
	for _, to := range nonSelfTargets {
		if StateDocumentCreated.IsValidTransition(to) {
			t.Errorf("expected DOCUMENT_CREATED -> %s to be ILLEGAL (terminal success)", to)
		}
	}
	// DOCUMENT_CREATED CAN legitimately transition to FAILED / DEAD_LETTERED.
	if !StateDocumentCreated.IsValidTransition(StateWorkflowFailed) {
		t.Error("expected DOCUMENT_CREATED -> FAILED to be VALID (post-creation failure)")
	}
	if !StateDocumentCreated.IsValidTransition(StateWorkflowDeadLettered) {
		t.Error("expected DOCUMENT_CREATED -> DEAD_LETTERED to be VALID (post-creation failure)")
	}
}

// ── Test 9: Forward chain skips are rejected ──────────────────────────

func TestWorkflowState_SkipForbidden(t *testing.T) {
	// SCRIPT_READY → IMAGES_GENERATED is a skip (must go through IMAGES_PENDING).
	if StateScriptReady.IsValidTransition(StateImagesGenerated) {
		t.Error("expected SCRIPT_READY -> IMAGES_GENERATED to be ILLEGAL (skip)")
	}
	// SCRIPT_READY → DOCUMENT_CREATED is a skip.
	if StateScriptReady.IsValidTransition(StateDocumentCreated) {
		t.Error("expected SCRIPT_READY -> DOCUMENT_CREATED to be ILLEGAL (skip)")
	}
	// IMAGES_PENDING → DOCUMENT_CREATED is a skip.
	if StateImagesPending.IsValidTransition(StateDocumentCreated) {
		t.Error("expected IMAGES_PENDING -> DOCUMENT_CREATED to be ILLEGAL (skip)")
	}
}
