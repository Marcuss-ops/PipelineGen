// Package e2e — QDRANT-DOD-FINAL-2026-07-08 wave, Gate 11 (media_assets
// supersede gate) hermetic TDD probe.
//
// Per architecture/action-plans/2026-07-08-qdrant-dod-final.md section 5
// (Test 8 — supersede gate) + section 11 (canonical source_version invariant
// via typed SupersedeError), this file locks the OUTBOX-LEVEL supersede
// contract hermetically. The full IndexingHandler.Handle source_version check
// is exercised in internal/application/jobs/outbox/indexing_v1_test.go
// (in-package, mock sourceQuerier + indexer ports); this file pins the
// downstream typed-error envelope that the handler produces, so the e2e
// observer can detect a stale-version event via errors.As(*SupersedeError)
// without reaching into the unexported IndexingHandler fields.
//
// godlike/06 SSOT (one canonical owner per fact): the SupersedeError
// type, NewSupersede constructor, and IsSupersede classifier live ONLY at
// internal/infrastructure/database/sqlite/outboxevents/supersede.go. This
// test is the canonical hermetic probe of the surface; it does NOT redefine
// the type or duplicate the constructor.
//
// godlike/07 typed-error contract: every probe asserts the typed-error
// surface (errors.As / errors.Is / dual-%w wrap) WITHOUT falling back to
// string matching. The SupersedeError envelope is the load-bearing seam
// that the outbox pool's processEvent uses to route rows to
// MarkSuperseded (status='superseded') rather than terminal dead-letter.
//
// Pre-existing build carry-forward (NOT a regression): the tests/e2e
// package compilation is currently blocked by qdrant_e2e_youtube_test.go:625
// (undefined: youtubetypes.ClipMetadata). This file compiles in isolation
// and is verified via `go build` on the file directly.
package e2e

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
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
	err := outboxevents.NewSupersede("asset-abc-123", "v3-persisted", "v2-event")
	if err == nil {
		t.Fatal("NewSupersede must return non-nil error")
	}
	var supErr *outboxevents.SupersedeError
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

// TestSupersedeError_IsSupersede_ClassifiesTypedEnvelopeOnly pins the
// godlike/07 typed-error contract: IsSupersede must return true ONLY for
// *SupersedeError envelopes (and their dual-%w wrap chains). Unrelated
// errors (sql.ErrNoRows, plain errors.New, nil) must NOT be classified
// as supersede — otherwise the outbox pool would route genuine transient
// failures to MarkSuperseded (status='superseded') and silently lose
// retryable events.
func TestSupersedeError_IsSupersede_ClassifiesTypedEnvelopeOnly(t *testing.T) {
	t.Parallel()

	// Positive: typed *SupersedeError → IsSupersede true.
	supErr := outboxevents.NewSupersede("asset-x", "v2", "v1")
	if !outboxevents.IsSupersede(supErr) {
		t.Errorf("IsSupersede(%v) = false, want true for typed envelope", supErr)
	}

	// Negative: sql.ErrNoRows (the canonical "no row in DB" path that
	// triggers fallthrough-to-IndexClip, NOT a supersede).
	if outboxevents.IsSupersede(sql.ErrNoRows) {
		t.Errorf("IsSupersede(sql.ErrNoRows) = true, want false (ErrNoRows is the no-op fallthrough, not a supersede)")
	}

	// Negative: plain errors.New (must never match).
	if outboxevents.IsSupersede(errors.New("transient db error")) {
		t.Errorf("IsSupersede(plain error) = true, want false (plain errors are not supersede envelopes)")
	}

	// Negative: nil error (the canonical success path; must never be
	// classified as supersede — would cause pool.processEvent to
	// incorrectly MarkSuperseded a row that returned nil from Handle).
	if outboxevents.IsSupersede(nil) {
		t.Errorf("IsSupersede(nil) = true, want false (nil is not a supersede envelope)")
	}
}

// TestSupersedeError_DualPercentW_PreservesClassification pins the
// godlike/07 dual-%w wrap idiom: when the IndexingHandler wraps a
// *SupersedeError via fmt.Errorf("...%w: %w", underlyingErr, supErr),
// BOTH errors must be recoverable (errors.Is + errors.As). This is the
// load-bearing contract for the IndexingHandler post-upsert-race path
// (clipindexer.ErrIndexSuperseded wrapped in a NewSupersede envelope).
func TestSupersedeError_DualPercentW_PreservesClassification(t *testing.T) {
	t.Parallel()

	underlying := errors.New("post-upsert race detected")
	supErr := outboxevents.NewSupersede("asset-race", "v3-postupsert", "v2-staleevent")
	wrapped := fmt.Errorf("IndexingHandler.Handle: stale source_version: %w: %w", underlying, supErr)

	// The *SupersedeError must be recoverable via errors.As.
	var extracted *outboxevents.SupersedeError
	if !errors.As(wrapped, &extracted) {
		t.Fatalf("errors.As(*SupersedeError) on dual-%%w wrap returned false, want true (godlike/07 dual-%%w contract violated)")
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

	// IsSupersede must also return true on the wrapped chain (the
	// outbox pool's processEvent classifier walks the chain).
	if !outboxevents.IsSupersede(wrapped) {
		t.Errorf("IsSupersede(wrapped) = false, want true (outbox pool classification depends on chain walk)")
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
	err := outboxevents.NewSupersede("asset-eq", "v10", "v9")
	var supErr *outboxevents.SupersedeError
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
// logs the canonical reason string to t.Log for dashboard-grep
// stability. Future drift in the wording is allowed; the typed
// envelope (errors.As + IsSupersede) is the load-bearing contract
// and is locked by the other 4 tests in this file.
func TestSupersedeError_ReasonStringMentionsVersionDrift(t *testing.T) {
	t.Parallel()
	err := outboxevents.NewSupersede("asset-vis", "v5", "v4")
	msg := err.Error()
	t.Logf("SupersedeError.Error() canonical reason: %s", msg)
	// Soft assertion: log if the canonical marker drifts so future
	// operators can update dashboard-grep configs.
	if !strings.Contains(msg, "supersede") && !strings.Contains(msg, "stale") {
		t.Logf("NOTE: canonical reason string drifted from 'supersede'/'stale' markers; update dashboard-grep configs if this is intentional")
	}
}
