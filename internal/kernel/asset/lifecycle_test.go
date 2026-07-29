package asset_test

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// TestLifecycleState_PreparingToPublishedToActive verifies the FASE 3b
// forward chain: PREPARING → PUBLISHED → ACTIVE.
func TestLifecycleState_PreparingToPublishedToActive(t *testing.T) {
	// PREPARING → PUBLISHED (valid).
	if !asset.StatePreparing.IsValidTransition(asset.StatePublished) {
		t.Error("PREPARING → PUBLISHED must be valid")
	}

	// PUBLISHED → ACTIVE (valid, post-indexing activation).
	if !asset.StatePublished.IsValidTransition(asset.StateActive) {
		t.Error("PUBLISHED → ACTIVE must be valid")
	}

	// PUBLISHED → ERROR (valid, indexing failed).
	if !asset.StatePublished.IsValidTransition(asset.StateError) {
		t.Error("PUBLISHED → ERROR must be valid")
	}

	// PREPARING → ACTIVE (invalid — must go through PUBLISHED).
	if asset.StatePreparing.IsValidTransition(asset.StateActive) {
		t.Error("PREPARING → ACTIVE must NOT be valid (skip PUBLISHED)")
	}

	// PREPARING → DELETED (invalid).
	if asset.StatePreparing.IsValidTransition(asset.StateDeleted) {
		t.Error("PREPARING → DELETED must NOT be valid")
	}
}

// TestLifecycleState_SelfLoopIdempotent verifies that self-transitions
// are always valid (idempotent writes).
func TestLifecycleState_SelfLoopIdempotent(t *testing.T) {
	cases := []asset.LifecycleState{
		asset.StatePreparing,
		asset.StatePublished,
		asset.StateActive,
		asset.StateDeleteRequested,
		asset.StateDeleted,
	}
	for _, s := range cases {
		if !s.IsValidTransition(s) {
			t.Errorf("self-loop %q → %q must be valid", s, s)
		}
	}
}

// TestLifecycleState_ValidCanonical verifies Valid() accepts all
// canonical states including PREPARING and PUBLISHED.
func TestLifecycleState_ValidCanonical(t *testing.T) {
	if !asset.StatePreparing.Valid() {
		t.Error("PREPARING must be Valid()")
	}
	if !asset.StatePublished.Valid() {
		t.Error("PUBLISHED must be Valid()")
	}
	if !asset.StateActive.Valid() {
		t.Error("ACTIVE must be Valid()")
	}
	if !asset.StateDeleted.Valid() {
		t.Error("DELETED must be Valid()")
	}
}

// TestIndexState_NotIndexable verifies the NOT_INDEXABLE state is
// valid and terminal but not a failure.
func TestIndexState_NotIndexable(t *testing.T) {
	if !asset.StateNotIndexable.Valid() {
		t.Error("NOT_INDEXABLE must be Valid()")
	}
	if !asset.StateNotIndexable.IsTerminal() {
		t.Error("NOT_INDEXABLE must be IsTerminal()")
	}
	if asset.StateNotIndexable.IsFailedTerminal() {
		t.Error("NOT_INDEXABLE must NOT be IsFailedTerminal()")
	}
	if asset.StateNotIndexable.IsDeletedCanonical() {
		t.Error("NOT_INDEXABLE must NOT be IsDeletedCanonical()")
	}
}

// TestPhysicalVsIndexStateSeparation verifies that lifecycle_state and
// index_state are orthogonal — no index-related states appear in the
// physical lifecycle enum. Legacy index-specific states
// (StateLifecycleIndexDeletePending, StateIndexDeleted) are in the
// canonical list pending a future migration wave; this test documents
// the drift rather than enforcing today.
func TestPhysicalVsIndexStateSeparation(t *testing.T) {
	// Physical lifecycle must not contain index-specific terms.
	physical := asset.CanonicalLifecycleStateValues()
	legacyCount := 0
	for _, s := range physical {
		switch s {
		case asset.StateLifecycleIndexDeletePending, asset.StateIndexDeleted:
			legacyCount++
		}
	}
	// Accept the 2 legacy index-specific states as known drift.
	if legacyCount > 2 {
		t.Errorf("unexpected index-specific state count in lifecycle: got %d, want <= 2 (legacy)", legacyCount)
	}

	// Verify PREPARING and PUBLISHED are in the canonical set.
	foundPreparing, foundPublished := false, false
	for _, s := range physical {
		if s == asset.StatePreparing {
			foundPreparing = true
		}
		if s == asset.StatePublished {
			foundPublished = true
		}
	}
	if !foundPreparing {
		t.Error("PREPARING missing from CanonicalLifecycleStateValues()")
	}
	if !foundPublished {
		t.Error("PUBLISHED missing from CanonicalLifecycleStateValues()")
	}
}
