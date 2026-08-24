// Package app — TDD coverage for the WireAssets dep-getter type-classification
// (PR-WIRE-ASSETS-NIL-CLASSIFICATION, July 2026).
//
// Tests cover the 3-class taxonomy (DepRequired / DepOptionalDisabled /
// DepTestOnly) + the 3 typed sentinels + the ClassifyDepGet helper across
// the canonical call patterns:
//  1. nil check, class=DepRequired → returns ErrRequiredDepMissing wrapped
//  2. nil check, class=DepOptionalDisabled → logs Warn + returns nil
//  3. nil check, class=DepTestOnly → returns ErrTestOnlyDepMissing wrapped
//  4. non-nil check, all classes → returns nil (no error, no log)
//  5. cross-class boundary: errors.Is probes for typed sentinel
//  6. zap observer-based log capture for the OptionalDisabled Warn
//  7. nil-log tolerance for OptionalDisabled (no panic on nil log)
package wiring

import (
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// TestClassifyDepGet_RequiredNil_ReturnsTypedError pins the godlike/07
// typed-error contract for DepRequired. The returned error MUST be
// errors.Is-comparable with ErrRequiredDepMissing so callers can pattern-match
// without string-matching.
func TestClassifyDepGet_RequiredNil_ReturnsTypedError(t *testing.T) {
	err := ClassifyDepGet("WireAssets: searchFanOut", true, DepRequired, zap.NewNop())
	if err == nil {
		t.Fatal("expected error for DepRequired + isNil=true, got nil")
	}
	if !errors.Is(err, ErrRequiredDepMissing) {
		t.Fatalf("expected errors.Is(err, ErrRequiredDepMissing) to be true, got err=%v", err)
	}
	// Verify the canonical dep name is in the error message (operator-side diagnostic).
	if got := err.Error(); !strings.Contains(got, "WireAssets: searchFanOut") {
		t.Fatalf("expected error message to contain dep name 'WireAssets: searchFanOut', got %q", got)
	}
}

// TestClassifyDepGet_TestOnlyNil_ReturnsTypedError pins the godlike/07
// typed-error contract for DepTestOnly. Same errors.Is contract as DepRequired
// but with a distinct sentinel.
func TestClassifyDepGet_TestOnlyNil_ReturnsTypedError(t *testing.T) {
	err := ClassifyDepGet("WireAssets: testStub", true, DepTestOnly, zap.NewNop())
	if err == nil {
		t.Fatal("expected error for DepTestOnly + isNil=true, got nil")
	}
	if !errors.Is(err, ErrTestOnlyDepMissing) {
		t.Fatalf("expected errors.Is(err, ErrTestOnlyDepMissing) to be true, got err=%v", err)
	}
}

// TestClassifyDepGet_OptionalDisabledNil_LogsAndReturnsNil pins the
// DepOptionalDisabled contract: capability degraded, server stays up.
func TestClassifyDepGet_OptionalDisabledNil_LogsAndReturnsNil(t *testing.T) {
	core, recorded := observer.New(zapcore.WarnLevel)
	log := zap.New(core)

	err := ClassifyDepGet("WireAssets: optionalDep", true, DepOptionalDisabled, log)
	if err != nil {
		t.Fatalf("expected nil error for DepOptionalDisabled, got %v", err)
	}

	// Verify the Warn log was emitted with the canonical dep + class fields.
	entries := recorded.All()
	if len(entries) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(entries))
	}
	entry := entries[0]
	if entry.Level != zapcore.WarnLevel {
		t.Fatalf("expected Warn level, got %v", entry.Level)
	}
	if !strings.Contains(entry.Message, "optional dep disabled") {
		t.Fatalf("expected log message to contain 'optional dep disabled', got %q", entry.Message)
	}
	// Verify the typed context fields are populated.
	fields := entry.ContextMap()
	if fields["dep"] != "WireAssets: optionalDep" {
		t.Fatalf("expected log field 'dep' = 'WireAssets: optionalDep', got %v", fields["dep"])
	}
	if fields["class"] != "optional-disabled" {
		t.Fatalf("expected log field 'class' = 'optional-disabled', got %v", fields["class"])
	}
}

// TestClassifyDepGet_OptionalDisabledNil_NilLogTolerated pins the
// partial-deploy safety net: a nil log (e.g. test fixture) MUST NOT panic
// the helper. The Warn log is skipped silently.
func TestClassifyDepGet_OptionalDisabledNil_NilLogTolerated(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("expected no panic on nil log, got %v", r)
		}
	}()
	err := ClassifyDepGet("WireAssets: optionalDep", true, DepOptionalDisabled, nil)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

// TestClassifyDepGet_NonNil_AllClasses_ReturnNil pins the non-nil happy path:
// all 3 classes MUST return nil for isNil=false (the dep is present + non-nil).
func TestClassifyDepGet_NonNil_AllClasses_ReturnNil(t *testing.T) {
	for _, class := range []DepClassification{DepRequired, DepOptionalDisabled, DepTestOnly} {
		t.Run(class.String(), func(t *testing.T) {
			err := ClassifyDepGet("WireAssets: dep", false, class, zap.NewNop())
			if err != nil {
				t.Fatalf("expected nil error for isNil=false (class=%v), got %v", class, err)
			}
		})
	}
}

// TestClassifyDepGet_CrossClassBoundary_TypedSentinelIsolated pins the
// invariant that each class has a DISTINCT typed sentinel. A DepRequired
// error MUST NOT match DepTestOnly's sentinel (and vice versa).
func TestClassifyDepGet_CrossClassBoundary_TypedSentinelIsolated(t *testing.T) {
	reqErr := ClassifyDepGet("WireAssets: req", true, DepRequired, zap.NewNop())
	testErr := ClassifyDepGet("WireAssets: test", true, DepTestOnly, zap.NewNop())

	// Sanity: each error matches its own sentinel.
	if !errors.Is(reqErr, ErrRequiredDepMissing) {
		t.Fatalf("expected reqErr to match ErrRequiredDepMissing, got %v", reqErr)
	}
	if !errors.Is(testErr, ErrTestOnlyDepMissing) {
		t.Fatalf("expected testErr to match ErrTestOnlyDepMissing, got %v", testErr)
	}

	// Cross-class: required error must NOT match test-only sentinel.
	if errors.Is(reqErr, ErrTestOnlyDepMissing) {
		t.Fatalf("reqErr MUST NOT match ErrTestOnlyDepMissing (typed-sentinel isolation violation), got err=%v", reqErr)
	}
	// Cross-class: test-only error must NOT match required sentinel.
	if errors.Is(testErr, ErrRequiredDepMissing) {
		t.Fatalf("testErr MUST NOT match ErrRequiredDepMissing (typed-sentinel isolation violation), got err=%v", testErr)
	}
}

// TestClassifyDepGet_DepClassificationString pins the canonical lowercase
// String() representation used in log fields + error messages.
func TestClassifyDepGet_DepClassificationString(t *testing.T) {
	cases := []struct {
		class DepClassification
		want  string
	}{
		{DepRequired, "required"},
		{DepOptionalDisabled, "optional-disabled"},
		{DepTestOnly, "test-only"},
	}
	for _, c := range cases {
		if got := c.class.String(); got != c.want {
			t.Fatalf("class.String() = %q, want %q", got, c.want)
		}
	}
}

// TestClassifyDepGet_UnknownClass_DefaultsToNil pins the defensive fallback:
// an unknown DepClassification value MUST return nil rather than panic or
// silently degrade. This protects against enum drift if a future agent adds
// a new class without updating the switch.
func TestClassifyDepGet_UnknownClass_DefaultsToNil(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("expected no panic on unknown class, got %v", r)
		}
	}()
	unknown := DepClassification(99)
	err := ClassifyDepGet("WireAssets: dep", true, unknown, zap.NewNop())
	if err != nil {
		t.Fatalf("expected nil error for unknown class, got %v", err)
	}
}

// TestClassifyDepGet_OptionalDisabled_NoLogWhenNonNil pins the inverse of
// TestClassifyDepGet_OptionalDisabledNil_LogsAndReturnsNil: a present
// (non-nil) optional dep MUST NOT log the disabled-Warn. The helper
// short-circuits before the log call.
func TestClassifyDepGet_OptionalDisabled_NoLogWhenNonNil(t *testing.T) {
	core, recorded := observer.New(zapcore.WarnLevel)
	log := zap.New(core)

	err := ClassifyDepGet("WireAssets: optionalDep", false, DepOptionalDisabled, log)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if got := len(recorded.All()); got != 0 {
		t.Fatalf("expected 0 log entries for non-nil optional dep, got %d", got)
	}
}
