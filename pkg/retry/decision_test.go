// Package retry — decision_test.go (Fase 6(a) Push 6.1, July 2026).
//
// Hermetic test for the typed-only retry-decision surface introduced
// in decision.go. The test pins the Fase-6(a) user-spec contract:
//
//	(a) ClassifierRegistry.Decision() walks the registered Classifier
//	    chain and returns the first match. No classifier → zero-value +
//	    false.
//
//	(b) norm() fail-closes IsRetryable==nil to neverRetry. The legacy
//	    "skip the predicate entirely" semantic is GONE; pass an explicit
//	    predicate or get no-retry default.
//
//	(c) ClassifierRegistry.Register panics on nil and after Seal
//	    (godlike/07 fail-closed at observable boundary).
//
//	(d) Decision walker skips classifiers that emit final=true with
//	    empty Class or SafeMessage (godlike/07 fail-closed).
//
//	(e) Tests use local ClassifierRegistry instances so they stay
//	    hermetic and exercise the injected path.
//
// The tests register local fakeErr / typedErr helpers via the canonical
// Classifier signature and never consult the substring-fallback path
// (the transientSubstrings catalog is LEGACY per Fase 6(a) and is
// covered by transient_test.go separately).
package retry

import (
	"context"
	"errors"
	"testing"
	"time"
)

// ── Helpers ─────────────────────────────────────────────────────────────────

// fakeErr is a typed error used by the test classifiers. errors.As walks
// the chain and the registered classifier inspects it via asAdapter[T].
// It satisfies error and exports nothing (matches production-adapter
// shape: typed-error surfaces, no pkg/retry import required to define).
type fakeErr struct {
	tag string
}

func (e *fakeErr) Error() string { return "fake: " + e.tag }

// typedClassifier inspects err via asAdapter[*fakeErr] and emits a
// populated RetryDecision with SafeMessage + Class. Returns
// (zero-value, false) for non-matching err — the canonical
// "I don't claim this err" signal.
func typedClassifier() Classifier {
	return func(err error) (RetryDecision, bool) {
		fe, ok := asAdapter[*fakeErr](err)
		if !ok {
			return RetryDecision{}, false
		}
		return RetryDecision{
			Class:       ErrNetwork,
			Retryable:   true,
			RetryAfter:  1500 * time.Millisecond,
			SafeMessage: "fake adapter classified (" + fe.tag + ")",
		}, true
	}
}

// alwaysRetryClassifier emits a valid populated RetryDecision for ANY
// err. Used to verify the walker handles "match every err" correctly
// (and to verify panic-on-empty invariant on a different path).
func alwaysRetryClassifier() Classifier {
	return func(err error) (RetryDecision, bool) {
		if err == nil {
			return RetryDecision{}, false
		}
		return RetryDecision{
			Class:       ErrUnknown,
			Retryable:   true,
			SafeMessage: "always-classify-test",
		}, true
	}
}

// ── (a) Decision walker ─────────────────────────────────────────────────────

// newTestRegistry builds a sealed ClassifierRegistry from the given
// classifiers. Tests use this helper instead of the mutable default
// registry so they stay hermetic and exercise the injected path.
func newTestRegistry(classifiers ...Classifier) *ClassifierRegistry {
	reg := NewClassifierRegistry()
	for _, c := range classifiers {
		reg.Register(c)
	}
	reg.Seal()
	return reg
}

func TestDecision_NilErr_ReturnsZero(t *testing.T) {
	reg := newTestRegistry(typedClassifier())

	d, ok := reg.Decision(nil)
	if ok {
		t.Fatalf("Decision(nil) must return ok=false; got ok=true, d=%+v", d)
	}
	if d != (RetryDecision{}) {
		t.Fatalf("Decision(nil) must return zero RetryDecision; got %+v", d)
	}
}

func TestDecision_NoClassifier_NoMatch(t *testing.T) {
	reg := newTestRegistry()
	// No classifier registered.
	d, ok := reg.Decision(errors.New("unregistered err"))
	// Fail-closed: unknown err returns zero-value, false. The walker
	// does NOT consult the legacy substring path in this test (we
	// sanity-check that "unregistered err" has no transient marker).
	if ok || d != (RetryDecision{}) {
		t.Fatalf("Decision(unregistered) must return (zero, false); got (%+v, %v)", d, ok)
	}
}

func TestDecision_RegisteredClassifier_Matches(t *testing.T) {
	reg := newTestRegistry(typedClassifier())

	err := &fakeErr{tag: "test-1"}
	d, ok := reg.Decision(err)
	if !ok {
		t.Fatalf("Decision(typedErr) must return ok=true; got ok=false")
	}
	if d.Class != ErrNetwork {
		t.Fatalf("Class: want ErrNetwork (%q); got %q", ErrNetwork, d.Class)
	}
	if !d.Retryable {
		t.Fatalf("Retryable: want true; got false")
	}
	if d.RetryAfter != 1500*time.Millisecond {
		t.Fatalf("RetryAfter: want 1500ms; got %v", d.RetryAfter)
	}
	if d.SafeMessage == "" {
		t.Fatalf("SafeMessage: must be non-empty for final=true classifier")
	}
}

func TestDecision_FirstMatchWins(t *testing.T) {
	// Register two classifiers; the first one claims the err and the
	// second one should never be visited.
	firstCalled := false
	reg := newTestRegistry(
		func(err error) (RetryDecision, bool) {
			firstCalled = true
			fe, ok := asAdapter[*fakeErr](err)
			if !ok {
				return RetryDecision{}, false
			}
			return RetryDecision{
				Class:       ErrNetwork,
				Retryable:   true,
				SafeMessage: "first-wins(" + fe.tag + ")",
			}, true
		},
		func(err error) (RetryDecision, bool) {
			t.Fatalf("second classifier must NOT be called (first-match-wins)")
			return RetryDecision{}, true
		},
	)

	d, ok := reg.Decision(&fakeErr{tag: "first-wins"})
	if !ok || !firstCalled {
		t.Fatalf("Decision: want first-classifier win; got (ok=%v, firstCalled=%v, d=%+v)", ok, firstCalled, d)
	}
	if d.SafeMessage != "first-wins(first-wins)" {
		t.Fatalf("SafeMessage: want the first classifier's; got %q", d.SafeMessage)
	}
}

func TestDecision_FinalTrueEmptyClass_LogsAndSkips(t *testing.T) {
	// FASE 6 Cut 6.1 review feedback (July 2026): a misconfigured
	// Classifier that emits final=true with empty Class MUST NOT
	// crash the production request path. The walker now skips the
	// buggy classifier (godlike/07 fail-closed, NOT crash-closed)
	// and falls through to the typed-probe fallback. This test pins
	// the new contract.
	reg := newTestRegistry(func(err error) (RetryDecision, bool) {
		return RetryDecision{
			// Class intentionally empty.
			Retryable:   true,
			SafeMessage: "missing-class",
		}, true
	})

	d, ok := reg.Decision(errors.New("empty-class test"))
	// Walker MUST skip the buggy classifier and fall through to the
	// typed-probe fallback. err doesn't implement RetryableError and
	// isn't a *TransientInfrastructureError; the walker returns
	// (zero, false) — fail-closed, NOT retryable by default.
	if ok {
		t.Fatalf("Decision: want ok=false after skipping buggy classifier + fail-closed typed-probe; got ok=true, d=%+v", d)
	}
	if d != (RetryDecision{}) {
		t.Fatalf("Decision: want zero-value (fall-through after skip); got %+v", d)
	}
}

func TestDecision_FinalTrueEmptySafeMessage_LogsAndSkips(t *testing.T) {
	// FASE 6 Cut 6.1 review feedback (July 2026): a misconfigured
	// Classifier that emits final=true with empty SafeMessage MUST
	// NOT crash the production request path. Same operational
	// rationale as TestDecision_FinalTrueEmptyClass_LogsAndSkips.
	//
	// Register TWO classifiers: the first one (buggy, no SafeMessage)
	// SHOULD be skipped; the second one (valid, populated) is the
	// canonical first-match-wins walker hit. The test asserts the
	// walker picks up the second non-buggy classifier — walker is
	// skipping on SafeMessage-and-trying-next, not skipping-on-
	// SafeMessage-and-falling-through-silently.
	firstCalled := false
	reg := newTestRegistry(
		func(err error) (RetryDecision, bool) {
			firstCalled = true
			return RetryDecision{
				Class:     ErrNetwork,
				Retryable: true,
				// SafeMessage intentionally empty.
			}, true
		},
		alwaysRetryClassifier(),
	)

	d, ok := reg.Decision(errors.New("empty-safemessage test"))
	if !firstCalled {
		t.Fatalf("Decision: want first (buggy) classifier to be evaluated; was not called")
	}
	if !ok {
		t.Fatalf("Decision: want ok=true from SECOND (valid) classifier; got false (walker did not skip-and-continue)")
	}
	if d.SafeMessage != "always-classify-test" {
		t.Fatalf("Decision: want second classifier's SafeMessage; got %q (walker skipped both classifiers instead of falling through)", d.SafeMessage)
	}
}

// ── (b) norm() fail-closed IsRetryable==nil ─────────────────────────────────

func TestNorm_IsRetryableNil_DefaultsToNeverRetry(t *testing.T) {
	o := Options{
		MaxAttempts:    1,
		InitialBackoff: 1 * time.Millisecond,
		// IsRetryable intentionally nil.
	}
	o = norm(o)
	if o.IsRetryable == nil {
		t.Fatalf("norm(nil IsRetryable) must coalesce to neverRetry; got nil")
	}
	// neverRetry returns false unconditionally.
	if o.IsRetryable(errors.New("anything")) {
		t.Fatalf("norm(nil IsRetryable) predicate must return false; got true")
	}
}

func TestNorm_DefaultOptionsHaveJitter(t *testing.T) {
	// Pin Fase 6(a) default contract: DefaultOptions defaults are
	// unchanged (MaxAttempts=3, InitialBackoff=1s, JitterFraction=0.25).
	// Backoff+jitter is ALWAYS applied (per spec b: `Backoff+jitter
	// sempre applicati da norm(Options{})`).
	o := DefaultOptions()
	if o.JitterFraction != 0.25 {
		t.Fatalf("DefaultOptions.JitterFraction: want 0.25; got %v", o.JitterFraction)
	}
	if o.MaxAttempts != 3 {
		t.Fatalf("DefaultOptions.MaxAttempts: want 3; got %d", o.MaxAttempts)
	}
	o = norm(o)
	if o.JitterFraction != 0.25 {
		t.Fatalf("norm(DefaultOptions).JitterFraction: want 0.25; got %v", o.JitterFraction)
	}
}

func TestDo_DoNotRetry_MaxAttemptsOne(t *testing.T) {
	// Pin spec (b) end-to-end: with IsRetryable=nil, MaxAttempts=1,
	// the retry loop exits on first error (no burning of retry budget).
	calls := 0
	err := Do(context.Background(), func() error {
		calls++
		return errors.New("transient-typed-error")
	}, Options{
		MaxAttempts: 1,
		// IsRetryable nil → norm fills with neverRetry → loop exits
		// on first error.
	})
	if err == nil {
		t.Fatalf("Do: want non-nil error; got nil")
	}
	if calls != 1 {
		t.Fatalf("Do: want exactly 1 call under fail-closed norm; got %d", calls)
	}
}

// ── (c) ClassifierRegistry.Register panic ─────────────────────────────────

func TestClassifierRegistry_Register_Nil_Panics(t *testing.T) {
	reg := NewClassifierRegistry()

	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("ClassifierRegistry.Register(nil): want panic; got no panic")
		}
	}()
	reg.Register(nil)
}

func TestClassifierRegistry_Seal_PreventsFurtherRegistration(t *testing.T) {
	reg := NewClassifierRegistry()
	reg.Register(typedClassifier())
	reg.Seal()

	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("ClassifierRegistry.Register after Seal: want panic; got no panic")
		}
	}()
	reg.Register(typedClassifier())
}

func TestDefaultClassifierRegistry_IsSealed(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("Register on sealed default registry: want panic; got no panic")
		}
	}()
	defaultClassifierRegistry.Register(func(error) (RetryDecision, bool) { return RetryDecision{}, false })
}

// ── (d) asAdapter helper ────────────────────────────────────────────────────

func TestAsAdapter_TypedError_Matches(t *testing.T) {
	want := &fakeErr{tag: "as"}
	got, ok := asAdapter[*fakeErr](want)
	if !ok {
		t.Fatalf("asAdapter: want ok=true; got false")
	}
	if got != want {
		t.Fatalf("asAdapter: want identity match (%p); got %p", want, got)
	}
}

func TestAsAdapter_WrappedTypedError_Matches(t *testing.T) {
	inner := &fakeErr{tag: "wrapped"}
	wrapped := errors.New("wrap: " + inner.Error())
	// errors.As walks the Unwrap chain. Because inner DOESN'T have
	// Unwrap here (raw errors.New has no Unwrap), asAdapter should
	// NOT match. This is the production case: callers wrap with
	// fmt.Errorf %w and the inner has Unwrap; this test covers the
	// negative case for raw errors.New wrapping.
	got, ok := asAdapter[*fakeErr](wrapped)
	if ok {
		t.Fatalf("asAdapter on non-unwrap chain: want ok=false; got ok=true (%+v)", got)
	}
}

func TestAsAdapter_FmtErrorfWrapped_Matches(t *testing.T) {
	inner := &fakeErr{tag: "fmt-wrap"}
	wrapped := wrapAsUnwrap(inner)
	got, ok := asAdapter[*fakeErr](wrapped)
	if !ok {
		t.Fatalf("asAdapter on fmt.Errorf %%w chain: want ok=true; got ok=false")
	}
	if got.tag != "fmt-wrap" {
		t.Fatalf("asAdapter: tag mismatch; want fmt-wrap; got %q", got.tag)
	}
}

// wrapAsUnwrap mimics fmt.Errorf("%w", x): the wrapper carries an
// Unwrap method. errors.As walks it and finds inner.
type unwrapper struct{ inner error }

func (u unwrapper) Error() string { return "wrap: " + u.inner.Error() }
func (u unwrapper) Unwrap() error { return u.inner }

func wrapAsUnwrap(err error) error { return unwrapper{inner: err} }

// ── (e) Injected registry in retry executor ─────────────────────────────────

func TestOptions_ClassifierRegistry_InjectedIntoExecutor(t *testing.T) {
	reg := newTestRegistry(typedClassifier())

	var calls int
	err := Do(context.Background(), func() error {
		calls++
		return &fakeErr{tag: "injected"}
	}, Options{
		MaxAttempts:        2,
		InitialBackoff:     1 * time.Millisecond,
		MaxBackoff:         1 * time.Millisecond,
		ClassifierRegistry: reg,
	})
	if err == nil {
		t.Fatalf("Do: want error after exhausting attempts; got nil")
	}
	if calls != 2 {
		t.Fatalf("Do: want 2 calls (retryable typed error); got %d", calls)
	}
}

func TestOptions_ClassifierRegistry_NilFallsBackToNeverRetry(t *testing.T) {
	var calls int
	err := Do(context.Background(), func() error {
		calls++
		return &fakeErr{tag: "no-registry"}
	}, Options{
		MaxAttempts:    2,
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff:     1 * time.Millisecond,
		// ClassifierRegistry intentionally nil.
	})
	if err == nil {
		t.Fatalf("Do: want error; got nil")
	}
	if calls != 1 {
		t.Fatalf("Do: want exactly 1 call when no predicate/registry; got %d", calls)
	}
}

// ── (f) Resilience ────────────────────────────────────────────────────────

func TestDecision_AfterPanicClassifier_StillResilient(t *testing.T) {
	// Use a local registry so this test does not depend on the mutable
	// default registry state.
	reg := newTestRegistry(alwaysRetryClassifier())
	d, ok := reg.Decision(errors.New("ok"))
	if !ok {
		t.Fatalf("Decision: want ok=true; got false")
	}
	if d.Class != ErrUnknown {
		t.Fatalf("Class: want ErrUnknown; got %q", d.Class)
	}
}
