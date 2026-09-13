package outboxevents

// supersede_envelope_test.go pins the typed SupersedeError envelope produced by
// the outbox pool's stale-version path. The type, its constructor and its
// classifier live in supersede.go (godlike/06: one canonical owner per fact);
// this file does NOT redefine them.
//
// PROVENANCE (2026-09-13): these tests were written as
// tests/e2e/qdrant_dod_supersede_gate_test.go, which was named after the Qdrant
// media chain but in fact exercised ONLY this package. That placement was
// load-bearing in the worst way: `verify-main` does not run ./tests/... (only
// `verify-integration` does), so a live typed-error contract was pinned by a
// test the pre-push gate never executed. The genuinely unique assertions moved
// here; the file's `IsSupersede` classification cases were dropped because
// supersede_test.go already covers nil/typed/plain/terminal classification.
//
// godlike/07 typed-error contract: every probe asserts the typed surface
// (errors.As / errors.Is / dual-%w wrap) WITHOUT falling back to string
// matching. The envelope is the load-bearing seam the outbox pool's
// processEvent uses to route rows to MarkSuperseded (status='superseded')
// instead of terminal dead-letter.

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestSupersedeError_NewConstructor_PopulatesFields pins the typed-error
// surface: NewSupersede must populate AssetID + Current + Expected from
// the caller's input arguments, in that order, with no reordering or
// sentinel injection. Future drift in the field-population order would
// break every downstream errors.As consumer that reads .Expected vs
// .Current (the canonical reading direction is "the version we have vs
// the version the event claims").
func TestSupersedeError_NewConstructor_PopulatesFields(t *testing.T) {
	t.Parallel()
	err := NewSupersede("asset-abc-123", "v3-persisted", "v2-event")
	if err == nil {
		t.Fatal("NewSupersede must return non-nil error")
	}
	var supErr *SupersedeError
	if !errors.As(err, &supErr) {
		t.Fatalf("NewSupersede must return *SupersedeError, got %T: %v", err, err)
	}
	if supErr.AssetID != "asset-abc-123" {
		t.Errorf("AssetID = %q, want %q", supErr.AssetID, "asset-abc-123")
	}
	if supErr.Current != "v3-persisted" {
		t.Errorf("Current = %q, want %q (the version we have in DB)", supErr.Current, "v3-persisted")
	}
	if supErr.Expected != "v2-event" {
		t.Errorf("Expected = %q, want %q (the version the event claims)", supErr.Expected, "v2-event")
	}
}

// TestSupersedeError_DualPercentW_PreservesClassification pins the
// godlike/07 dual-%w wrap idiom: when the stale-version path wraps a
// *SupersedeError via fmt.Errorf("...%w: %w", underlyingErr, supErr),
// BOTH errors must be recoverable (errors.Is + errors.As). The outbox pool
// classifies a row by walking the chain, so a wrap that hid the envelope
// would send a genuinely superseded event to the retry/dead-letter path.
func TestSupersedeError_DualPercentW_PreservesClassification(t *testing.T) {
	t.Parallel()

	underlying := errors.New("post-upsert race detected")
	supErr := NewSupersede("asset-race", "v3-postupsert", "v2-staleevent")
	wrapped := fmt.Errorf("stale source_version: %w: %w", underlying, supErr)

	// The *SupersedeError must be recoverable via errors.As.
	var extracted *SupersedeError
	if !errors.As(wrapped, &extracted) {
		t.Fatalf("errors.As(*SupersedeError) on dual-%%w wrap returned false, want true (dual-%%w contract violated)")
	}
	if extracted.AssetID != "asset-race" {
		t.Errorf("AssetID = %q, want %q", extracted.AssetID, "asset-race")
	}
	if extracted.Current != "v3-postupsert" {
		t.Errorf("Current = %q, want %q", extracted.Current, "v3-postupsert")
	}
	if extracted.Expected != "v2-staleevent" {
		t.Errorf("Expected = %q, want %q", extracted.Expected, "v2-staleevent")
	}

	// The underlying error must survive the same wrap.
	if !errors.Is(wrapped, underlying) {
		t.Error("errors.Is(underlying) on dual-%w wrap returned false; the dual-wrap lost the first operand")
	}

	// IsSupersede must also return true on the wrapped chain (the
	// outbox pool's processEvent classifier walks the chain).
	if !IsSupersede(wrapped) {
		t.Error("IsSupersede(wrapped) = false, want true (outbox pool classification depends on chain walk)")
	}
}

// TestSupersedeError_AllFieldsPreservedOnEqualityLock pins the
// non-emptiness invariant: AssetID, Current, Expected must all be
// non-empty after NewSupersede. An empty AssetID would cause the
// outbox MarkSuperseded UPDATE to fail (no asset_id to write to);
// empty Current or Expected would defeat the diagnostic value of
// the envelope (operators reading the supersede event want to see
// the version drift).
func TestSupersedeError_AllFieldsPreservedOnEqualityLock(t *testing.T) {
	t.Parallel()
	err := NewSupersede("asset-eq", "v10", "v9")
	var supErr *SupersedeError
	if !errors.As(err, &supErr) {
		t.Fatalf("expected *SupersedeError, got %T: %v", err, err)
	}
	if supErr.AssetID == "" {
		t.Error("AssetID is empty after NewSupersede — MarkSuperseded would fail")
	}
	if supErr.Current == "" {
		t.Error("Current is empty — supersede event loses its diagnostic value (no version we had)")
	}
	if supErr.Expected == "" {
		t.Error("Expected is empty — supersede event loses its diagnostic value (no version the event claimed)")
	}
}

// TestSupersedeError_ReasonStringMentionsVersionDrift is an
// INFORMATIONAL operator-visibility probe (NOT a hard contract): it
// logs the canonical reason string for dashboard-grep stability. Future
// drift in the wording is allowed; the typed envelope (errors.As +
// IsSupersede) is the load-bearing contract and is locked by the other
// tests in this file.
func TestSupersedeError_ReasonStringMentionsVersionDrift(t *testing.T) {
	t.Parallel()
	err := NewSupersede("asset-vis", "v5", "v4")
	msg := err.Error()
	t.Logf("SupersedeError.Error() canonical reason: %s", msg)
	// Soft assertion: log if the canonical marker drifts so operators
	// can update dashboard-grep configs.
	if !strings.Contains(msg, "supersede") && !strings.Contains(msg, "stale") {
		t.Log("NOTE: canonical reason string drifted from 'supersede'/'stale' markers; update dashboard-grep configs if this is intentional")
	}
}
