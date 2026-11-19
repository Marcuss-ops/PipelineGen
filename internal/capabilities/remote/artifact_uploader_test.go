// Package remote — artifact_uploader_test.go (P0 Commit 6, July 2026).
//
// State-machine + idempotency-key test surface. Locks the canonical
// contract that downstream adapters (creator.Adapter, jobbrokerclient
// HTTP-commands) depend on. Order-independent: tests do not rely on
// any package-level singleton state.
package remote_test

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/remote"
)

// ── UploadState.Valid() / canonical-list tests ─────────────────────────

// TestUploadState_Valid_AllCanonical — every canonical UploadState
// value passes Valid().
func TestUploadState_Valid_AllCanonical(t *testing.T) {
	for _, s := range []remote.UploadState{
		remote.StateUploadPreparing,
		remote.StateUploadUploading,
		remote.StateUploadUploaded,
		remote.StateUploadVerified,
		remote.StateUploadFinalized,
		remote.StateUploadFailed,
	} {
		if !s.Valid() {
			t.Errorf("canonical %q must be Valid() == true", s)
		}
	}
}

// TestUploadState_Valid_ZeroValueInvalid — the zero-value (s == "")
// is intentionally INVALID so a half-wired UploadSession cannot slip
// through Valid() as empty-but-valid (godlike/07 no-fake-availability).
func TestUploadState_Valid_ZeroValueInvalid(t *testing.T) {
	var s remote.UploadState
	if s.Valid() {
		t.Error("zero-value UploadState must be Valid() == false (godlike/07 no-fake-availability)")
	}
	if s.IsValidTransition(remote.StateUploadPreparing) {
		t.Error("zero-value UploadState must reject any IsValidTransition call")
	}
}

// TestUploadState_Valid_RandomStringInvalid — non-canonical strings
// are rejected.
func TestUploadState_Valid_RandomStringInvalid(t *testing.T) {
	invalid := []remote.UploadState{
		"preparing", // lowercase
		"PREPARED",  // typo
		"UNKNOWN",
		"   PREPARING",  // whitespace
		"PREPARING\000", // null byte
	}
	for _, s := range invalid {
		if s.Valid() {
			t.Errorf("non-canonical %q must be Valid() == false", s)
		}
	}
}

// ── UploadState.IsValidTransition() tests ─────────────────────────────

// TestUploadState_ForwardChain — the canonical forward chain is legal.
// PREPARING → UPLOADING → UPLOADED → VERIFIED → FINALIZED.
func TestUploadState_ForwardChain(t *testing.T) {
	chain := []remote.UploadState{
		remote.StateUploadPreparing,
		remote.StateUploadUploading,
		remote.StateUploadUploaded,
		remote.StateUploadVerified,
		remote.StateUploadFinalized,
	}
	for i := 0; i < len(chain)-1; i++ {
		if !chain[i].IsValidTransition(chain[i+1]) {
			t.Errorf("forward edge %s → %s must be valid", chain[i], chain[i+1])
		}
	}
}

// TestUploadState_SelfLoopAllowed — re-running the same-state handler
// is a no-op rather than a fatal transition error. Mirrors the
// LifecycleState convention in domain/asset.
func TestUploadState_SelfLoopAllowed(t *testing.T) {
	for _, s := range []remote.UploadState{
		remote.StateUploadPreparing,
		remote.StateUploadUploading,
		remote.StateUploadUploaded,
		remote.StateUploadVerified,
		remote.StateUploadFinalized,
		remote.StateUploadFailed,
	} {
		if !s.IsValidTransition(s) {
			t.Errorf("self-loop on %s must be valid (idempotent)", s)
		}
	}
}

// TestUploadState_BackwardEdgeRejected — every backward edge in the
// forward chain is rejected (e.g. UPLOADING → PREPARING).
func TestUploadState_BackwardEdgeRejected(t *testing.T) {
	backwardEdges := [][2]remote.UploadState{
		{remote.StateUploadUploading, remote.StateUploadPreparing},
		{remote.StateUploadUploaded, remote.StateUploadUploading},
		{remote.StateUploadVerified, remote.StateUploadUploaded},
		{remote.StateUploadFinalized, remote.StateUploadVerified},
		{remote.StateUploadFinalized, remote.StateUploadPreparing},
	}
	for _, edge := range backwardEdges {
		if edge[0].IsValidTransition(edge[1]) {
			t.Errorf("backward edge %s → %s must be REJECTED", edge[0], edge[1])
		}
	}
}

// TestUploadState_SkipAheadPartial — the partial-skip PREPARING →
// UPLOADED is rejected (must go through UPLOADING first).
func TestUploadState_SkipAheadPartial(t *testing.T) {
	skipEdges := [][2]remote.UploadState{
		{remote.StateUploadPreparing, remote.StateUploadUploaded}, // skip UPLOADING
		{remote.StateUploadPreparing, remote.StateUploadVerified}, // skip UPLOADING + UPLOADED
		{remote.StateUploadPreparing, remote.StateUploadFinalized},
		{remote.StateUploadUploading, remote.StateUploadVerified}, // skip UPLOADED
		{remote.StateUploadUploading, remote.StateUploadFinalized},
		{remote.StateUploadUploaded, remote.StateUploadFinalized}, // skip VERIFIED
	}
	for _, edge := range skipEdges {
		if edge[0].IsValidTransition(edge[1]) {
			t.Errorf("skip-ahead edge %s → %s must be REJECTED", edge[0], edge[1])
		}
	}
}

// TestUploadState_UploadedToVerifiedLegal — the skip-ahead UPLOADED →
// VERIFIED is LEGAL because the remote-side's finalize command
// atomically verifies + transitions to VERIFIED + FINALIZED in one
// call (Creator adapter's Finalize impl).
func TestUploadState_UploadedToVerifiedLegal(t *testing.T) {
	if !remote.StateUploadUploaded.IsValidTransition(remote.StateUploadVerified) {
		t.Error("skip-ahead UPLOADED → VERIFIED must be valid (atomic finalize on remote-side)")
	}
}

// TestUploadState_NonTerminalStatesToFAILED — every NON-terminal
// state can transition to FAILED (the Creator adapter's error path
// / the remote-side's mid-call failure handler). The sticky-terminal
// states (FAILED itself, FINALIZED) are explicitly EXCLUDED:
//   - FAILED -> FAILED is allowed via self-loop only.
//   - FINALIZED is closed — once an artifact is finalized, a
//     post-finalize failure CANNOT reset the session to FAILED;
//     the row stays FINALIZED (operator-visible on dashboards) and
//     a retry requires a NEW session.
func TestUploadState_NonTerminalStatesToFAILED(t *testing.T) {
	preFailed := []remote.UploadState{
		remote.StateUploadPreparing,
		remote.StateUploadUploading,
		remote.StateUploadUploaded,
		remote.StateUploadVerified,
	}
	for _, s := range preFailed {
		if !s.IsValidTransition(remote.StateUploadFailed) {
			t.Errorf("non-terminal edge %s → FAILED must be valid (error-path sink)", s)
		}
	}

	// Sticky-terminal assertions: FINALIZED and FAILED cannot route
	// INTO FAILED via the cross-state edge (self-loop is handled
	// separately by TestUploadState_SelfLoopAllowed).
	if remote.StateUploadFinalized.IsValidTransition(remote.StateUploadFailed) {
		t.Error("FINALIZED → FAILED must be REJECTED (sticky-terminal — closes the row)")
	}
}

// TestUploadState_StickyTerminalSinks — FAILED and FINALIZED are
// sticky-terminal sinks: no transition OUT of either state is legal.
// Callers must invent a NEW session to retry from a sticky-terminal.
func TestUploadState_StickyTerminalSinks(t *testing.T) {
	terminals := []remote.UploadState{remote.StateUploadFailed, remote.StateUploadFinalized}
	targets := []remote.UploadState{
		remote.StateUploadPreparing,
		remote.StateUploadUploading,
		remote.StateUploadUploaded,
		remote.StateUploadVerified,
		remote.StateUploadFinalized,
		remote.StateUploadFailed,
	}
	for _, term := range terminals {
		for _, to := range targets {
			if term == to {
				continue // self-loop is allowed (idempotent)
			}
			if term.IsValidTransition(to) {
				t.Errorf("sticky-terminal %s must NOT transition out to %s", term, to)
			}
		}
	}
}
