// Package script — model_output_strict_test.go
//
// Pins the strict envelope behaviour as a behavioural contract. Drift
// here MUST fail the build before reaching prod (godlike/06 SSOT +
// godlike/07 NO-FAKE-AVAILABILITY + godlike/08 forward-prevention).
package script_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// validRefsFixture returns the canonical 3-slot valid-ref set for
// most tests in this file. Each test that exercises a different set
// builds its own.
func validRefsFixture() map[string]struct{} {
	return map[string]struct{}{
		"slot-1": {},
		"slot-2": {},
		"slot-3": {},
	}
}

// ── Happy paths ──────────────────────────────────────────────────────────

func TestParseModelOutputStrict_OneSegment_HappyPath(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"segments":[{"ref":"slot-1","text":"opening narration"}]}`)

	out, err := script.ParseModelOutputStrict(raw, validRefsFixture())
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if got, want := len(out.Segments), 1; got != want {
		t.Fatalf("segments len = %d, want %d", got, want)
	}
	if got := out.Segments[0].Ref; got != "slot-1" {
		t.Fatalf("Segments[0].Ref = %q, want %q", got, "slot-1")
	}
	if got := out.Segments[0].Text; !strings.Contains(got, "opening") {
		t.Fatalf("Segments[0].Text = %q, want substring %q", got, "opening")
	}
}

func TestParseModelOutputStrict_ThreeSegments_AllValidRefs(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"segments":[
		{"ref":"slot-1","text":"round 1 narration"},
		{"ref":"slot-2","text":"round 7 surge"},
		{"ref":"slot-3","text":"closing"}
	]}`)

	out, err := script.ParseModelOutputStrict(raw, validRefsFixture())
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if got, want := len(out.Segments), 3; got != want {
		t.Fatalf("segments len = %d, want %d", got, want)
	}
	for i, seg := range out.Segments {
		wantRef := []string{"slot-1", "slot-2", "slot-3"}[i]
		if seg.Ref != wantRef {
			t.Fatalf("Segments[%d].Ref = %q, want %q", i, seg.Ref, wantRef)
		}
	}
}

// ── ErrModelOutputExtraField paths (godlike/08 forward-prevention) ────

func TestParseModelOutputStrict_ExtraTopLevelField_Rejected(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"schema_version":1,"segments":[{"ref":"slot-1","text":"x"}]}`)

	_, err := script.ParseModelOutputStrict(raw, validRefsFixture())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, script.ErrModelOutputExtraField) {
		t.Fatalf("expected ErrModelOutputExtraField, got %T: %v", err, err)
	}
	// Defensive: surface the offending key name in the error message
	// so operators diagnose the upstream contract drift.
	if !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("error MUST name the offender: %v", err)
	}
}

func TestParseModelOutputStrict_LegacyV1SpecScene_Rejected(t *testing.T) {
	t.Parallel()
	// The pre-LLM-COMPACT-CONTRACT V1 envelope. The strict contract
	// MUST reject it loudly — silent "compat" is a godlike/07 violation.
	raw := []byte(`{"schema_version":1,"text":"narration","specscene":{"version":1,"scenes":[{"id":"scene-0","index":0,"text":"narration","kind":"narration","bindings":{}}]}}`)

	_, err := script.ParseModelOutputStrict(raw, validRefsFixture())
	if err == nil {
		t.Fatal("expected error, got nil — V1 envelope MUST be rejected by the strict contract")
	}
	if !errors.Is(err, script.ErrModelOutputExtraField) {
		t.Fatalf("expected ErrModelOutputExtraField, got %T: %v", err, err)
	}
}

func TestParseModelOutputStrict_ExtraSegmentField_Rejected(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"segments":[{"ref":"slot-1","text":"x","clip_id":"clip-a"}]}`)

	_, err := script.ParseModelOutputStrict(raw, validRefsFixture())
	if err == nil {
		t.Fatal("expected error (extra segment field)")
	}
	if !errors.Is(err, script.ErrModelOutputExtraField) {
		t.Fatalf("expected ErrModelOutputExtraField, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "clip_id") {
		t.Fatalf("error MUST name the extra segment field: %v", err)
	}
}

func TestParseModelOutputStrict_EmptyExtraSegment_Rejected(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"segments":[{}]}`)

	_, err := script.ParseModelOutputStrict(raw, validRefsFixture())
	if err == nil {
		t.Fatal("expected error (segment missing both ref and text)")
	}
	// 0 keys → caught by the "missing required key(s)" branch
	// (mapped to ErrModelOutputExtraField via the validator's
	// fail-closed contract).
	if !errors.Is(err, script.ErrModelOutputExtraField) {
		t.Fatalf("expected ErrModelOutputExtraField, got %T: %v", err, err)
	}
}

func TestParseModelOutputStrict_MissingSegmentText_Rejected(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"segments":[{"ref":"slot-1"}]}`)

	_, err := script.ParseModelOutputStrict(raw, validRefsFixture())
	if err == nil {
		t.Fatal("expected error (segment missing required `text`)")
	}
	if !errors.Is(err, script.ErrModelOutputExtraField) {
		t.Fatalf("expected ErrModelOutputExtraField, got %T: %v", err, err)
	}
}

func TestParseModelOutputStrict_TwoExtraTopLevelKeys_Rejected(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"ok":true,"schema_version":1,"segments":[{"ref":"slot-1","text":"x"}]}`)

	_, err := script.ParseModelOutputStrict(raw, validRefsFixture())
	if err == nil {
		t.Fatal("expected error (two extra top-level keys)")
	}
	if !errors.Is(err, script.ErrModelOutputExtraField) {
		t.Fatalf("expected ErrModelOutputExtraField, got %T: %v", err, err)
	}
}

// ── ErrModelOutputRefNotInPlan paths (godlike/07 fail closed) ─────────

func TestParseModelOutputStrict_RefNotInPlan_Rejected(t *testing.T) {
	t.Parallel()
	// slot-4 is not in validRefsFixture() {slot-1, slot-2, slot-3}.
	raw := []byte(`{"segments":[{"ref":"slot-4","text":"x"}]}`)

	_, err := script.ParseModelOutputStrict(raw, validRefsFixture())
	if err == nil {
		t.Fatal("expected error (ref slot-4 not in plan)")
	}
	if !errors.Is(err, script.ErrModelOutputRefNotInPlan) {
		t.Fatalf("expected ErrModelOutputRefNotInPlan, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "slot-4") {
		t.Fatalf("error MUST name the offending ref: %v", err)
	}
}

func TestParseModelOutputStrict_RefPartialMatch_Rejected(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"segments":[{"ref":"slot-2","text":"x"},{"ref":"slot-99","text":"y"}]}`)

	_, err := script.ParseModelOutputStrict(raw, validRefsFixture())
	if err == nil {
		t.Fatal("expected error (segment 1 ref slot-99 not in plan)")
	}
	if !errors.Is(err, script.ErrModelOutputRefNotInPlan) {
		t.Fatalf("expected ErrModelOutputRefNotInPlan, got %T: %v", err, err)
	}
}

// ── ErrModelOutputEmptySegments paths ──────────────────────────────────

func TestParseModelOutputStrict_EmptySegmentsArray_Rejected(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"segments":[]}`)

	_, err := script.ParseModelOutputStrict(raw, validRefsFixture())
	if err == nil {
		t.Fatal("expected error (empty segments array)")
	}
	if !errors.Is(err, script.ErrModelOutputEmptySegments) {
		t.Fatalf("expected ErrModelOutputEmptySegments, got %T: %v", err, err)
	}
}

func TestParseModelOutputStrict_EmptyRawBytes_Rejected(t *testing.T) {
	t.Parallel()

	_, err := script.ParseModelOutputStrict([]byte(""), validRefsFixture())
	if err == nil {
		t.Fatal("expected error (empty raw bytes)")
	}
	if !errors.Is(err, script.ErrModelOutputEmptySegments) {
		t.Fatalf("expected ErrModelOutputEmptySegments, got %T: %v", err, err)
	}
}

func TestParseModelOutputStrict_WhitespaceOnlyRawBytes_Rejected(t *testing.T) {
	t.Parallel()
	_, err := script.ParseModelOutputStrict([]byte("   \n\t  "), validRefsFixture())
	if err == nil {
		t.Fatal("expected error (whitespace-only raw bytes)")
	}
	if !errors.Is(err, script.ErrModelOutputEmptySegments) {
		t.Fatalf("expected ErrModelOutputEmptySegments (whitespace collapses to empty), got %T: %v", err, err)
	}
}

// ── Additional defensive checks ───────────────────────────────────────

func TestParseModelOutputStrict_NonJSONBytes_Rejected(t *testing.T) {
	t.Parallel()
	raw := []byte(`not-json-at-all`)

	_, err := script.ParseModelOutputStrict(raw, validRefsFixture())
	if err == nil {
		t.Fatal("expected decode error (bytes are not JSON)")
	}
	// The error class here is wrap-of-json-error, NOT a typed
	// sentinel (the bytes didn't even get to the policy gates).
	if errors.Is(err, script.ErrModelOutputExtraField) {
		t.Fatalf("non-JSON must NOT be classified as extra-field, got %v", err)
	}
}

func TestParseModelOutputStrict_EmptySegmentText_Rejected(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"segments":[{"ref":"slot-1","text":"   "}]}`)

	_, err := script.ParseModelOutputStrict(raw, validRefsFixture())
	if err == nil {
		t.Fatal("expected error (whitespace-only text)")
	}
	// Empty text fails the in-segment godlike/07 guard — not a
	// typed sentinel, but a defensive wrap.
	if !strings.Contains(err.Error(), "empty") {
		t.Fatalf("error MUST mention empty text: %v", err)
	}
}

func TestParseModelOutputStrict_EmptyValidRefsEmptySegments(t *testing.T) {
	t.Parallel()
	// Edge case: caller passes an empty validRefs set. Any
	// non-empty envelope MUST be rejected (no refs allowed).
	raw := []byte(`{"segments":[{"ref":"slot-1","text":"x"}]}`)

	_, err := script.ParseModelOutputStrict(raw, map[string]struct{}{})
	if err == nil {
		t.Fatal("expected error (slot-1 not in EMPTY validRefs set)")
	}
	if !errors.Is(err, script.ErrModelOutputRefNotInPlan) {
		t.Fatalf("expected ErrModelOutputRefNotInPlan, got %T: %v", err, err)
	}
}

func TestParseModelOutputStrict_MissingSegmentsTopLevelKey_Rejected(t *testing.T) {
	t.Parallel()
	raw := []byte(`{}`)

	_, err := script.ParseModelOutputStrict(raw, validRefsFixture())
	if err == nil {
		t.Fatal("expected error (no top-level `segments`)")
	}
	// Missing `segments` is logically equivalent to "extra field
	// of zero allowed fields" → ErrModelOutputExtraField.
	if !errors.Is(err, script.ErrModelOutputExtraField) {
		t.Fatalf("expected ErrModelOutputExtraField, got %T: %v", err, err)
	}
}
