// Package remote — artifact_uploader_test.go (P0 Commit 6, July 2026).
//
// State-machine + idempotency-key test surface. Locks the canonical
// contract that downstream adapters (creator.Adapter, jobbrokerclient
// HTTP-commands) depend on. Order-independent: tests do not rely on
// any package-level singleton state.
package remote_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/remote"
)

// ── UploadState.Valid() / canonical-list tests ─────────────────────────

// TestUploadState_Valid_AllCanonical — every canonical UploadState
// value passes Valid().
func TestUploadState_Valid_AllCanonical(t *testing.T) {
	for _, s := range remote.CanonicalUploadStateValues() {
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
	for _, s := range remote.CanonicalUploadStateValues() {
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
	targets := remote.CanonicalUploadStateValues()
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

// ── IllegalTransitionError tests ──────────────────────────────────────

// TestIllegalTransitionError_ErrorsIsAs — the typed-error-data
// envelope is reachable via BOTH errors.Is(sentinel) AND
// errors.As(*IllegalTransitionError) per godlike/07 dual-traversal
// pattern.
func TestIllegalTransitionError_ErrorsIsAs(t *testing.T) {
	base := remote.NewIllegalTransitionError(remote.StateUploadUploaded, remote.StateUploadPreparing)

	// Wrap with %w (typical call-site pattern).
	wrapped := fmt.Errorf("creator adapter: backward-edge rejected: %w", base)

	if !errors.Is(wrapped, remote.ErrIllegalUploadStateTransition) {
		t.Error("errors.Is must return true for sentinel probe")
	}

	var ite *remote.IllegalTransitionError
	if !errors.As(wrapped, &ite) {
		t.Fatal("errors.As must return true for typed-data extraction")
	}

	if ite.From != remote.StateUploadUploaded || ite.To != remote.StateUploadPreparing {
		t.Errorf("extracted (From, To) = (%s, %s); want (UPLOADED, PREPARING)", ite.From, ite.To)
	}

	// Error() string includes both From and To for human-readable logs.
	expected := "illegal upload state transition: UPLOADED -> PREPARING"
	if ite.Error() != expected {
		t.Errorf("Error() = %q; want %q", ite.Error(), expected)
	}
}

// ── UploadSession tests ───────────────────────────────────────────────

// TestUploadSession_NewUploadSession_HappyPath — valid fields yield
// an initialised UploadSession at StateUploadPreparing.
func TestUploadSession_NewUploadSession_HappyPath(t *testing.T) {
	sess, err := remote.NewUploadSession("sess-001", "lease-001", "job-001:script_json")
	if err != nil {
		t.Fatal(err)
	}
	if sess.ID != "sess-001" {
		t.Errorf("ID = %q; want sess-001", sess.ID)
	}
	if sess.LeaseID != "lease-001" {
		t.Errorf("LeaseID = %q; want lease-001", sess.LeaseID)
	}
	if sess.ArtifactID != "job-001:script_json" {
		t.Errorf("ArtifactID = %q; want job-001:script_json", sess.ArtifactID)
	}
	if sess.State != remote.StateUploadPreparing {
		t.Errorf("initial State = %q; want PREPARING", sess.State)
	}

	// State must be Valid.
	if !sess.State.Valid() {
		t.Error("initial State must be Valid()")
	}
}

// TestUploadSession_NewUploadSession_EmptyFieldsRejected — every
// required field is rejected when empty (godlike/07 no-fake-availability).
func TestUploadSession_NewUploadSession_EmptyFieldsRejected(t *testing.T) {
	cases := []struct {
		name     string
		id       string
		leaseID  string
		artifact string
	}{
		{"empty id", "", "lease-001", "job-001:script_json"},
		{"empty lease", "sess-001", "", "job-001:script_json"},
		{"empty artifact", "sess-001", "lease-001", ""},
		{"all empty", "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sess, err := remote.NewUploadSession(c.id, c.leaseID, c.artifact)
			if err == nil {
				t.Fatalf("expected error for case %q; got session %+v", c.name, sess)
			}
			if sess != nil {
				t.Errorf("session must be nil on rejection; got %+v", sess)
			}
		})
	}
}

// TestUploadSession_NewUploadSession_NilCheck — when caller passes
// no fields, the error message references all three required fields
// (for diagnostic clarity under godlike/07 typed-error contract).
func TestUploadSession_NewUploadSession_DiagnosticClarity(t *testing.T) {
	_, err := remote.NewUploadSession("", "", "")
	if err == nil {
		t.Fatal("expected error on all-empty fields")
	}
	msg := err.Error()
	// Diagnostic must mention each missing field for operators.
	if !contains(msg, "id") || !contains(msg, "leaseID") || !contains(msg, "artifactID") {
		t.Errorf("error message must mention all 3 required fields; got: %q", msg)
	}
}

// contains is a local helper since strings.Contains would also work
// but we want the diagnostic to flag at the right call site.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		indexOf(s, substr) >= 0)
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// ── ArtifactIdempotencyKey tests ───────────────────────────────────────

// TestArtifactIdempotencyKey_Deterministic — same inputs yield
// byte-stable output across N invocations.
func TestArtifactIdempotencyKey_Deterministic(t *testing.T) {
	const jobID = "job-001"
	const artifactID = "job-001:script_json"
	const sha256 = "abc1234567890def"

	first := remote.ArtifactIdempotencyKey(jobID, artifactID, sha256)
	for i := 0; i < 10; i++ {
		got := remote.ArtifactIdempotencyKey(jobID, artifactID, sha256)
		if got != first {
			t.Errorf("invocation %d: key changed: %q vs %q", i, got, first)
		}
	}
}

// TestArtifactIdempotencyKey_DifferentInputsDifferentOutputs — three
// perturbation variants (different jobID, different artifactID,
// different sha256) yield distinct keys.
func TestArtifactIdempotencyKey_DifferentInputsDifferentOutputs(t *testing.T) {
	base := remote.ArtifactIdempotencyKey("job-001", "job-001:script_json", "abc1234567890def")

	v1 := remote.ArtifactIdempotencyKey("job-002", "job-001:script_json", "abc1234567890def")
	v2 := remote.ArtifactIdempotencyKey("job-001", "job-001:metadata", "abc1234567890def")
	v3 := remote.ArtifactIdempotencyKey("job-001", "job-001:script_json", "def4567890abc")

	if v1 == base || v2 == base || v3 == base {
		t.Errorf("any perturbed input should yield a distinct key; got base=%q v1=%q v2=%q v3=%q", base, v1, v2, v3)
	}
	if v1 == v2 || v1 == v3 || v2 == v3 {
		t.Errorf("all 3 perturbed keys should be pairwise distinct")
	}
}

// TestArtifactIdempotencyKey_RetrySameKey — calling the function
// repeatedly with the same triple (simulating a retry loop) yields
// the same key (idempotency-on-retry invariant).
func TestArtifactIdempotencyKey_RetrySameKey(t *testing.T) {
	const triple = "job-001|job-001:script_json|sha256-deadbeefcafef00d"
	// Triple expanded: same triple, called 50 times, all 50 keys must match.

	var first string
	for i := 0; i < 50; i++ {
		got := remote.ArtifactIdempotencyKey("job-001", "job-001:script_json", "sha256-deadbeefcafef00d")
		if first == "" {
			first = got
		}
		if got != first {
			t.Errorf("retry %d: key diverged: %q vs %q", i, got, first)
		}
	}
	_ = triple // the compilation-time expansion
}

// TestArtifactIdempotencyKey_KeyFormat — output is 64-char lowercase
// hex per Property 3 (header-safe + URL-safe + canonical lowercase).
func TestArtifactIdempotencyKey_KeyFormat(t *testing.T) {
	const samples = 20
	for i := 0; i < samples; i++ {
		key := remote.ArtifactIdempotencyKey(
			fmt.Sprintf("job-%03d", i),
			fmt.Sprintf("job-%03d:script", i),
			fmt.Sprintf("sha256-%064d", i),
		)
		if !remote.IsValidIdempotencyKey(key) {
			t.Errorf("sample %d: key %q failed IsValidIdempotencyKey", i, key)
		}
		if len(key) != 64 {
			t.Errorf("sample %d: key length = %d; want 64", i, len(key))
		}
	}
}

// TestArtifactIdempotencyKey_EmptyHandling — empty input triple
// yields the deterministic empty marker (NOT a hash digest), per
// godlike/07 no-fake-availability.
func TestArtifactIdempotencyKey_EmptyHandling(t *testing.T) {
	emptyCases := []struct{ j, a, s string }{
		{"", "a", "s"},
		{"j", "", "s"},
		{"j", "a", ""},
		{"", "", ""},
	}
	for _, c := range emptyCases {
		got := remote.ArtifactIdempotencyKey(c.j, c.a, c.s)
		if got != "" {
			t.Errorf("empty input (%q, %q, %q): got %q; want empty marker", c.j, c.a, c.s, got)
		}
		// Markers should still pass IsValidIdempotencyKey (see docs)
		// so the caller can probe both.
		if !remote.IsValidIdempotencyKey(got) {
			t.Errorf("empty-marker should pass IsValidIdempotencyKey; got: %q", got)
		}
	}
} // TestIsValidIdempotencyKey_FalseCases — malformed keys are rejected.
// Each entry is provably not a 64-char lowercase / uppercase hex
// digest (validation rejects it via length mismatch OR non-hex
// character), OR is the empty marker (which the helper accepts as
// valid so callers can probe BOTH the empty-marker handler and
// the malformed-key handler).
func TestIsValidIdempotencyKey_FalseCases(t *testing.T) {
	invalid := []string{
		"abc",                         // too short (3 chars)
		"abc" + repeat("0", 60) + "g", // non-hex 'g' at the tail
		" abc" + repeat("0", 61),      // leading space
		repeat("z", 64),               // non-hex 'z'
		repeat("-", 64),               // non-hex '-'
		repeat("0", 64) + " ",         // trailing space
		repeat("0", 65),               // length 65 (1 over)
	}
	for i, k := range invalid {
		if remote.IsValidIdempotencyKey(k) {
			t.Errorf("invalid key[%d] = %q should NOT pass IsValidIdempotencyKey", i, k)
		}
	}
}

// repeat returns s repeated n times (used by TestIsValidIdempotencyKey_FalseCases).
func repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
