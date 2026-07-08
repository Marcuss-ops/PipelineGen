// Package script — postprocessor_preflight_test.go is the hermetic
// TDD test surface for the SCRIPTCONTRACT-2026-07-08 PR-2
// composition-time preflight (canonical impl in
// postprocessor_preflight.go; typed-error contract in
// internal/domain/script/errors_preflight.go).
//
// godlike/06 SSOT: this test is the canonical hermetic probe of
// the preflight surface. No re-implementation of the predicate
// lives elsewhere in the codebase.
//
// godlike/07 NO-FAKE-AVAILABILITY: every assertion locks a typed
// or behavioural contract that future refactors MUST preserve.
// A pre-PR-2 refactor that introduced silent graceful-degradation
// would surface as a test failure (the canonical regression guard).
package script

import (
	"errors"
	"testing"

	"go.uber.org/zap"

	domainScript "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// makeCaps is a test-only convenience constructor for the
// canonical PreflightCaps (3 bool fields). godlike/06 SSOT:
// PreflightCaps is defined in postprocessor_preflight.go and has
// no exported constructor; this helper is the test-only pattern.
func makeCaps(voiceover, images, document bool) PreflightCaps {
	return PreflightCaps{
		VoiceoverEnabled: voiceover,
		ImagesEnabled:    images,
		DocumentEnabled:  document,
	}
}

// makeEnvelopeWithOutput builds a single-item envelope with the
// supplied OutputSpec flags. The Source field is a minimal
// text-shape (Title + Topic + SourceText) so envelope.Validate
// would pass if called (the preflight is called AFTER
// envelope.Validate in enqueueEnvelopeFn; the test only exercises
// the preflight predicate directly, so envelope.Validate is
// not invoked here — the test isolates the preflight surface
// from the envelope validation surface).
func makeEnvelopeWithOutput(voiceover, images, document bool) *domainScript.GenerationEnvelopeV2 {
	return &domainScript.GenerationEnvelopeV2{
		Version: 2,
		Preset:  "custom",
		Items: []domainScript.GenerationItemV2{
			{
				ID:    "test-item",
				Title: "Test Item",
				Source: domainScript.SourceSpec{
					Type:       domainScript.SourceText,
					Topic:      "test",
					SourceText: "test",
				},
				Output: domainScript.OutputSpec{
					GenerateVoiceover:   voiceover,
					GenerateSceneImages: images,
					GenerateDocument:    document,
				},
			},
		},
	}
}

// TestRequireRequestedProcessors_NilEnvelope verifies the preflight
// returns a clear error (not a panic) when the envelope is nil.
// godlike/07 minimum-blast-radius: nil-args are an explicit edge
// case the test pins (defensive against future call-site drift).
func TestRequireRequestedProcessors_NilEnvelope(t *testing.T) {
	t.Parallel()

	err := requireRequestedProcessors(makeCaps(true, true, true), nil, zap.NewNop())
	if err == nil {
		t.Fatal("expected error for nil envelope, got nil")
	}
	if !contains(err.Error(), "nil envelope") {
		t.Errorf("expected error to mention 'nil envelope', got: %v", err)
	}
}

// TestRequireRequestedProcessors_NoItemsRequested_AllCapsDisabled
// is the godlike/07 conservative-default contract: zero-value
// OutputSpec (no processors requested) passes the preflight even
// when all caps are disabled. The preflight ONLY fires on
// user-EXPLICITLY-requested processors; a "happy path" envelope
// with no special requirements is always allowed.
func TestRequireRequestedProcessors_NoItemsRequested_AllCapsDisabled(t *testing.T) {
	t.Parallel()

	// Zero-value caps (all false) + zero-value Output (all false)
	// = preflight passes (no user-requested processors to check).
	err := requireRequestedProcessors(makeCaps(false, false, false), makeEnvelopeWithOutput(false, false, false), zap.NewNop())
	if err != nil {
		t.Errorf("expected no error for zero-value envelope+caps (no processors requested), got: %v", err)
	}
}

// TestRequireRequestedProcessors_VoiceoverRequestedButDisabled is
// the godlike/07 NO-FAKE-AVAILABILITY canonical contract: when
// the user envelope explicitly requests `GenerateVoiceover=true`
// but `caps.VoiceoverEnabled=false` (composition not wired), the
// preflight MUST fail-closed with the typed error envelope. No
// silent skip. No panic. No degraded-but-works path.
func TestRequireRequestedProcessors_VoiceoverRequestedButDisabled(t *testing.T) {
	t.Parallel()

	caps := makeCaps(false, true, true)               // VoiceoverEnabled=false
	env := makeEnvelopeWithOutput(true, false, false) // user wants voiceover

	err := requireRequestedProcessors(caps, env, zap.NewNop())
	if err == nil {
		t.Fatal("expected preflight failure (voiceover requested but disabled at composition), got nil")
	}

	// Probe 1: errors.Is recovers the canonical sentinel.
	if !errors.Is(err, domainScript.ErrPreflightProcessorMissing) {
		t.Errorf("expected errors.Is(err, ErrPreflightProcessorMissing) = true, got err: %v", err)
	}

	// Probe 2: errors.As recovers the typed envelope with the
	// processor field populated.
	var typedErr *domainScript.PreflightProcessorMissingError
	if !errors.As(err, &typedErr) {
		t.Errorf("expected errors.As(err, &PreflightProcessorMissingError) = true, got err: %v", err)
	} else {
		if typedErr.Processor != "voiceover" {
			t.Errorf("expected Processor='voiceover', got %q", typedErr.Processor)
		}
		if typedErr.Reason == "" {
			t.Errorf("expected Reason to be populated for diagnosability, got empty")
		}
	}
}

// TestRequireRequestedProcessors_VoiceoverRequestedAndEnabled
// verifies the happy-path: when the user requests voiceover AND
// the composition has VoiceoverService wired (caps.VoiceoverEnabled=true),
// the preflight passes.
func TestRequireRequestedProcessors_VoiceoverRequestedAndEnabled(t *testing.T) {
	t.Parallel()

	caps := makeCaps(true, true, true)                // all wired
	env := makeEnvelopeWithOutput(true, false, false) // user wants voiceover

	err := requireRequestedProcessors(caps, env, zap.NewNop())
	if err != nil {
		t.Errorf("expected no error (voiceover requested and enabled), got: %v", err)
	}
}

// TestRequireRequestedProcessors_ImagesRequestedButDisabled is
// the scene-image variant of the fail-closed contract.
func TestRequireRequestedProcessors_ImagesRequestedButDisabled(t *testing.T) {
	t.Parallel()

	caps := makeCaps(true, false, true)               // ImagesEnabled=false
	env := makeEnvelopeWithOutput(false, true, false) // user wants scene images

	err := requireRequestedProcessors(caps, env, zap.NewNop())
	if err == nil {
		t.Fatal("expected preflight failure (images requested but disabled), got nil")
	}

	var typedErr *domainScript.PreflightProcessorMissingError
	if !errors.As(err, &typedErr) {
		t.Errorf("expected errors.As to recover typed envelope, got err: %v", err)
	} else if typedErr.Processor != "images" {
		t.Errorf("expected Processor='images', got %q", typedErr.Processor)
	}
}

// TestRequireRequestedProcessors_DocumentRequestedButDisabled is
// the document variant of the fail-closed contract.
func TestRequireRequestedProcessors_DocumentRequestedButDisabled(t *testing.T) {
	t.Parallel()

	caps := makeCaps(true, true, false)               // DocumentEnabled=false
	env := makeEnvelopeWithOutput(false, false, true) // user wants document

	err := requireRequestedProcessors(caps, env, zap.NewNop())
	if err == nil {
		t.Fatal("expected preflight failure (document requested but disabled), got nil")
	}

	var typedErr *domainScript.PreflightProcessorMissingError
	if !errors.As(err, &typedErr) {
		t.Errorf("expected errors.As to recover typed envelope, got err: %v", err)
	} else if typedErr.Processor != "document" {
		t.Errorf("expected Processor='document', got %q", typedErr.Processor)
	}
}

// TestRequireRequestedProcessors_NilLogger_DoesNotPanic pins the
// nil-tolerance contract: the preflight is safe to call without a
// logger (composition-root misuse case). godlike/07 minimum-blast-
// radius: nil-args must never panic.
func TestRequireRequestedProcessors_NilLogger_DoesNotPanic(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("preflight panicked with nil logger: %v", r)
		}
	}()

	caps := makeCaps(false, true, true)
	env := makeEnvelopeWithOutput(true, false, false)
	err := requireRequestedProcessors(caps, env, nil) // nil logger
	if err == nil {
		t.Fatal("expected preflight failure (voiceover requested but disabled), got nil")
	}
}

// TestRequireRequestedProcessors_MultipleItems_FailsOnFirstInvalid
// verifies the preflight short-circuits on the FIRST invalid item.
// The canonical loop walks Items in order; a fail on item[0] must
// return immediately (no further checks for items[1..N]).
//
// This is the canonical fail-fast-at-input contract: a batch with
// a single invalid item is rejected with the FIRST violation's
// diagnostic, not the last. Operators can fix the first item
// without distraction.
func TestRequireRequestedProcessors_MultipleItems_FailsOnFirstInvalid(t *testing.T) {
	t.Parallel()

	caps := makeCaps(false, true, true) // VoiceoverEnabled=false
	env := &domainScript.GenerationEnvelopeV2{
		Version: 2,
		Items: []domainScript.GenerationItemV2{
			// item[0]: invalid (voiceover requested but disabled)
			{
				ID:    "item-0-bad",
				Title: "Item 0 Bad",
				Source: domainScript.SourceSpec{
					Type:       domainScript.SourceText,
					Topic:      "test",
					SourceText: "test",
				},
				Output: domainScript.OutputSpec{GenerateVoiceover: true},
			},
			// item[1]: valid (no processors requested) — must NOT
			// be reached because item[0] short-circuits.
			{
				ID:    "item-1-ok",
				Title: "Item 1 OK",
				Source: domainScript.SourceSpec{
					Type:       domainScript.SourceText,
					Topic:      "test",
					SourceText: "test",
				},
				Output: domainScript.OutputSpec{},
			},
		},
	}

	err := requireRequestedProcessors(caps, env, zap.NewNop())
	if err == nil {
		t.Fatal("expected preflight failure on item[0], got nil (would imply item[1] was checked first)")
	}
	if !errors.Is(err, domainScript.ErrPreflightProcessorMissing) {
		t.Errorf("expected sentinel match, got err: %v", err)
	}
}

// TestRequireRequestedProcessors_MultipleItems_AllValid verifies
// the multi-item happy path: a batch with all-valid items passes
// the preflight in one call.
func TestRequireRequestedProcessors_MultipleItems_AllValid(t *testing.T) {
	t.Parallel()

	caps := makeCaps(true, true, true) // all wired
	env := &domainScript.GenerationEnvelopeV2{
		Version: 2,
		Items: []domainScript.GenerationItemV2{
			{
				ID:    "item-0",
				Title: "Item 0",
				Source: domainScript.SourceSpec{
					Type:       domainScript.SourceText,
					Topic:      "test",
					SourceText: "test",
				},
				Output: domainScript.OutputSpec{GenerateVoiceover: true},
			},
			{
				ID:    "item-1",
				Title: "Item 1",
				Source: domainScript.SourceSpec{
					Type:       domainScript.SourceText,
					Topic:      "test",
					SourceText: "test",
				},
				Output: domainScript.OutputSpec{GenerateSceneImages: true},
			},
			{
				ID:    "item-2",
				Title: "Item 2",
				Source: domainScript.SourceSpec{
					Type:       domainScript.SourceText,
					Topic:      "test",
					SourceText: "test",
				},
				Output: domainScript.OutputSpec{GenerateDocument: true},
			},
		},
	}

	err := requireRequestedProcessors(caps, env, zap.NewNop())
	if err != nil {
		t.Errorf("expected no error for all-valid batch, got: %v", err)
	}
}

// contains is a minimal test-only substring helper (avoids
// pulling in strings.Contains dependency on this file).
func contains(s, substr string) bool {
	if len(substr) > len(s) {
		return false
	}
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
