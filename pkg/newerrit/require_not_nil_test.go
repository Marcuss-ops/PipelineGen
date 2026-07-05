// TDD tests for pkg/newerrit, the typed-sentinel helper that
// backs the constructor fail-fast migration forward-pointer
// PR-RUNTIME-CONSTRUCTOR-TYPED-ERRORS (target 2026-08-15).
//
// These tests pin the godlike/07 typed-error contract:
//
//	-1 Sentinel probe: errors.Is(err, ErrNilDependency) returns true
//	-2 Typed probe:   errors.As(err, &NilDependencyError{}) recovers
//	                 the Name field for instrumentation
//	-3 Wrap probe:    fmt.Errorf("...%w", typedErr) chains still pass errors.Is
//	-4 Typed-nil:     (*T)(nil) wrapped in any is detected, not silently accepted
//	-5 Non-nil nil:   per godlike/07 minimum-blast-radius, scalar non-nil
//	                 values (empty string, zero int) are NOT nil at the
//	                 typed level — caller layers non-empty checks if needed
package newerrit

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// ── createNull returns a *T typed nil wrapped as `any` — the canonical
// "silently-succeeds" anti-pattern this helper MUST detect. ──────────
type fakeService struct{ id int }

func nilAny() any {
	var p *fakeService
	return p // typed-nil pointer wrapped as interface{}
}

// ── RequireNotNil: happy path ──────────────────────────────────────────
//
// Per godlike/07 minimum-blast-radius: every non-nil value (including
// scalars and zero-value structs) passes the helper without modification.
func TestRequireNotNil_NonNilPointer_ReturnsNilError(t *testing.T) {
	if err := RequireNotNil("svc.deps.Logger", &fakeService{id: 1}); err != nil {
		t.Fatalf("expected nil for non-nil pointer, got: %v", err)
	}
}

func TestRequireNotNil_NonNilString_ReturnsNilError(t *testing.T) {
	if err := RequireNotNil("svc.deps.Name", "alice"); err != nil {
		t.Fatalf("expected nil for non-empty string, got: %v", err)
	}
}

func TestRequireNotNil_EmptyString_ReturnsNilError(t *testing.T) {
	// Per-helper-spec: nil-empty-string conflation is documented.
	// Empty string is NOT nil at the typed layer. Caller layers
	// non-empty checks if it cares.
	if err := RequireNotNil("svc.deps.Name", ""); err != nil {
		t.Fatalf("expected nil for empty string (NOT typed-nil), got: %v", err)
	}
}

func TestRequireNotNil_ZeroInt_ReturnsNilError(t *testing.T) {
	if err := RequireNotNil("svc.deps.Timeout", 0); err != nil {
		t.Fatalf("expected nil for zero int (NOT typed-nil), got: %v", err)
	}
}

func TestRequireNotNil_ZeroStruct_ReturnsNilError(t *testing.T) {
	if err := RequireNotNil("svc.Deps", struct{ Log *fakeService }{}); err != nil {
		t.Fatalf("expected nil for zero-value struct (NOT nil at typed layer), got: %v", err)
	}
}

// ── RequireNotNil: typed-nil detection (the load-bearing edge case) ──
//
// Per godlike/07 NO-FAKE-AVAILABILITY: the typed-nil pointer wrapped
// in `any` MUST be detected. If it weren't, the constructor would
// silently accept a nil dep and panic later at callsite (worse
// diagnostic surface than failing at construction).

func TestRequireNotNil_PlainNil_ReturnsTypedError(t *testing.T) {
	err := RequireNotNil("svc.deps.Logger", nil)
	if err == nil {
		t.Fatal("expected error for plain-nil, got nil")
	}
	probe := &NilDependencyError{Name: "svc.deps.Logger"}
	if !errors.Is(err, probe) {
		t.Fatalf("expected errors.Is(*NilDependencyError) match, got: %v", err)
	}
	if !strings.Contains(err.Error(), "svc.deps.Logger") {
		t.Fatalf("expected name in error, got: %v", err.Error())
	}
}

func TestRequireNotNil_TypedNilPointer_ReturnsTypedError(t *testing.T) {
	err := RequireNotNil("svc.Log", nilAny())
	if err == nil {
		t.Fatal("expected typed-nil pointer detection, got nil")
	}
	typed := &NilDependencyError{}
	if !errors.As(err, &typed) {
		t.Fatalf("expected errors.As *NilDependencyError, got: %v", err)
	}
	if typed.Name != "svc.Log" {
		t.Fatalf("expected Name=svc.Log, got: %q", typed.Name)
	}
}

func TestRequireNotNil_TypedNilSlice_ReturnsTypedError(t *testing.T) {
	var s []int
	err := RequireNotNil("svc.Slice", s)
	if err == nil {
		t.Fatal("expected typed-nil slice detection, got nil")
	}
}

func TestRequireNotNil_TypedNilMap_ReturnsTypedError(t *testing.T) {
	var m map[string]int
	err := RequireNotNil("svc.Map", m)
	if err == nil {
		t.Fatal("expected typed-nil map detection, got nil")
	}
}

func TestRequireNotNil_TypedNilChan_ReturnsTypedError(t *testing.T) {
	var c chan int
	err := RequireNotNil("svc.Chan", c)
	if err == nil {
		t.Fatal("expected typed-nil chan detection, got nil")
	}
}

func TestRequireNotNil_TypedNilFunc_ReturnsTypedError(t *testing.T) {
	var f func()
	err := RequireNotNil("svc.Func", f)
	if err == nil {
		t.Fatal("expected typed-nil func detection, got nil")
	}
}

// ── NilDependencyError: typed-error contract pins ──────────────────────

func TestNilDependencyError_Is_SentinelMatch(t *testing.T) {
	err := &NilDependencyError{Name: "x"}
	if !errors.Is(err, ErrNilDependency) {
		t.Fatal("errors.Is(*NilDependencyError, ErrNilDependency) MUST be true")
	}
}

func TestNilDependencyError_Is_TargetValueMatch(t *testing.T) {
	a := &NilDependencyError{Name: "x"}
	b := &NilDependencyError{Name: "x"}
	if !errors.Is(a, b) {
		t.Fatal("errors.Is(a, b) MUST match on equal-value *NilDependencyError")
	}
}

func TestNilDependencyError_Is_NameMismatch_NoMatch(t *testing.T) {
	a := &NilDependencyError{Name: "x"}
	b := &NilDependencyError{Name: "y"}
	if errors.Is(a, b) {
		t.Fatal("errors.Is(a, b) MUST NOT match on different Name values")
	}
}

func TestNilDependencyError_Unwrap_ReturnsSentinel(t *testing.T) {
	err := &NilDependencyError{Name: "x"}
	if uw := errors.Unwrap(err); uw != ErrNilDependency {
		t.Fatalf("Unwrap() MUST return ErrNilDependency, got: %v", uw)
	}
}

func TestNilDependencyError_Error_QuotedName(t *testing.T) {
	err := &NilDependencyError{Name: "svc.deps.Logger"}
	got := err.Error()
	// godlike/07 instrumentation contract: Name is quoted via strconv.Quote
	// so log scanners keyed on the prefix "newerrit:" + quoted name don't break.
	if !strings.HasPrefix(got, "newerrit:") {
		t.Fatalf("expected 'newerrit:' prefix, got: %q", got)
	}
	if !strings.Contains(got, strconvQuote("svc.deps.Logger")) {
		t.Fatalf("expected quoted Name in error, got: %q", got)
	}
}

// strconvQuote re-exports strconv.Quote for the test (avoid importing strconv twice).
func strconvQuote(s string) string { return fmt.Sprintf("%q", s) }

// ── NilDependencyError: wrap-chain compatibility (godlike/07) ──────────

func TestRequireNotNil_FmtErrorfWrap_PreservesErrorsIs(t *testing.T) {
	typed := &NilDependencyError{Name: "svc.deps.Logger"}
	wrapped := fmt.Errorf("NewChannelMonitor: %w", typed)

	// both probes must pass through the wrap chain:
	if !errors.Is(wrapped, ErrNilDependency) {
		t.Fatal("errors.Is wrapped→ErrNilDependency MUST be true")
	}
	if !errors.Is(wrapped, typed) {
		t.Fatal("errors.Is wrapped→typed-pointer MUST be true")
	}
	probe := &NilDependencyError{}
	if !errors.As(wrapped, &probe) {
		t.Fatal("errors.As wrapped→*NilDependencyError MUST be true")
	}
	if probe.Name != "svc.deps.Logger" {
		t.Fatalf("errors.As MUST recover Name, got: %q", probe.Name)
	}
}

// ── godlike/06 SSOT: stable error-message shape ───────────────────────
//
// Any future PR that changes the error message format must update the
// test (which is the gate). Three canonical substrings MUST persist:
//
//  1. "newerrit:" prefix  — log-scanner key
//  2. "constructor dependency" — class identifier
//  3. "is nil" — ergonomic message
func TestNilDependencyError_MessageFormat_StableSubstrings(t *testing.T) {
	err := &NilDependencyError{Name: "X"}
	msg := err.Error()
	wantSubs := []string{
		"newerrit:",
		"constructor dependency",
		"is nil",
	}
	for _, sub := range wantSubs {
		if !strings.Contains(msg, sub) {
			t.Errorf("error message MUST contain %q, got: %q", sub, msg)
		}
	}
}

// ── godlike/07 NO-FAKE-AVAILABILITY: helper accepts a name + any value ─
//
// Sanity: signature is accept-any-name, accept-any-value. The
// constructor code's existing arg names ("svc.deps.Logger" etc.)
// MUST round-trip verbatim into the error message.
func TestRequireNotNil_NameRoundTrip(t *testing.T) {
	names := []string{
		"ChannelMonitor.deps.Logger",
		"ParentAggregator.dispatcher",
		"ExtractionService.repos.Clips",
		"Finalizer.registry",
		"", // edge case: empty name is a code smell but helper should not crash
	}
	for _, n := range names {
		err := RequireNotNil(n, nil)
		if err == nil {
			t.Fatalf("expected error for nil val (name=%q), got nil", n)
		}
		if n != "" && !strings.Contains(err.Error(), n) {
			t.Errorf("error MUST contain name %q, got: %v", n, err)
		}
	}
}

// ── reflect sanity: the helper does not accidentally lose type info ────
func TestRequireNotNil_ReturnsPointerType(t *testing.T) {
	err := RequireNotNil("x", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	// must be *NilDependencyError (typed-A path), not just a sentinel.
	if reflect.TypeOf(err).String() != "*newerrit.NilDependencyError" {
		t.Fatalf("expected *newerrit.NilDependencyError, got: %T", err)
	}
}
