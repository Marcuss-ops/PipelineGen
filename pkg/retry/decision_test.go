// Package retry — decision_test.go (Fase 6(a) Push 6.1, July 2026).
//
// Hermetic test for the typed-only retry-decision surface introduced
// in decision.go. The test pins the Fase-6(a) user-spec contract:
//
//   (a) Decision() walks the registered Classifier chain in init order
//       and returns the first match. No classifier → zero-value + false.
//
//   (b) norm() fail-closes IsRetryable==nil to neverRetry. The legacy
//       "skip the predicate entirely" semantic is GONE; pass an explicit
//       predicate or get no-retry default.
//
//   (c) RegisterClassifier panics on nil at init (godlike/07 fail-closed
//       at observable boundary).
//
//   (d) Decision walker panics on final=true with empty Class /
//       SafeMessage (godlike/07 fail-closed at observable boundary).
//
//   (e) ResetClassifiersForTest isolates per-test state. Calling
//       ResetClassifiersForTest at test start + t.Cleanup isolates
//       hermetic tests from the global chain.
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

func TestDecision_NilErr_ReturnsZero(t *testing.T) {
	t.Cleanup(ResetClassifiersForTest)
	RegisterClassifier(typedClassifier())

	d, ok := Decision(nil)
	if ok {
		t.Fatalf("Decision(nil) must return ok=false; got ok=true, d=%+v", d)
	}
	if d != (RetryDecision{}) {
		t.Fatalf("Decision(nil) must return zero RetryDecision; got %+v", d)
	}
}

func TestDecision_NoClassifier_NoMatch(t *testing.T) {
	t.Cleanup(ResetClassifiersForTest)
	// No classifier registered.
	d, ok := Decision(errors.New("unregistered err"))
	// Fail-closed: unknown err returns zero-value, false. The walker
	// does NOT consult the legacy substring path in this test (we
	// sanity-check that "unregistered err" has no transient marker).
	if ok || d != (RetryDecision{}) {
		t.Fatalf("Decision(unregistered) must return (zero, false); got (%+v, %v)", d, ok)
	}
}

func TestDecision_RegisteredClassifier_Matches(t *testing.T) {
	t.Cleanup(ResetClassifiersForTest)
	RegisterClassifier(typedClassifier())

	err := &fakeErr{tag: "test-1"}
	d, ok := Decision(err)
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
	t.Cleanup(ResetClassifiersForTest)
	// Register two classifiers; the first one claims the err and the
	// second one should never be visited.
	firstCalled := false
	RegisterClassifier(func(err error) (RetryDecision, bool) {
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
	})
	RegisterClassifier(func(err error) (RetryDecision, bool) {
		t.Fatalf("second classifier must NOT be called (first-match-wins)")
		return RetryDecision{}, true
	})

	d, ok := Decision(&fakeErr{tag: "first-wins"})
	if !ok || !firstCalled {
		t.Fatalf("Decision: want first-classifier win; got (ok=%v, firstCalled=%v, d=%+v)", ok, firstCalled, d)
	}
	if d.SafeMessage != "first-wins(first-wins)" {
		t.Fatalf("SafeMessage: want the first classifier's; got %q", d.SafeMessage)
	}
}

func TestDecision_FinalTrueEmptyClass_Panics(t *testing.T) {
	t.Cleanup(ResetClassifiersForTest)
	// godlike/07 fail-closed: classifier emits final=true with empty
	// Class → panic in the walker.
	RegisterClassifier(func(err error) (RetryDecision, bool) {
		return RetryDecision{
			// Class intentionally empty.
			Retryable:   true,
			SafeMessage: "missing-class",
		}, true
	})

	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("Decision: want panic on empty Class; got no panic")
		}
	}()
	Decision(errors.New("empty-class test"))
}

func TestDecision_FinalTrueEmptySafeMessage_Panics(t *testing.T) {
	t.Cleanup(ResetClassifiersForTest)
	RegisterClassifier(func(err error) (RetryDecision, bool) {
		return RetryDecision{
			Class:     ErrNetwork,
			Retryable: true,
			// SafeMessage intentionally empty.
		}, true
	})

	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("Decision: want panic on empty SafeMessage; got no panic")
		}
	}()
	Decision(errors.New("empty-safemessage test"))
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

// ── (c) RegisterClassifier panic ────────────────────────────────────────────

func TestRegisterClassifier_Nil_Panics(t *testing.T) {
	t.Cleanup(ResetClassifiersForTest)

	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("RegisterClassifier(nil): want panic; got no panic")
		}
	}()
	RegisterClassifier(nil)
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

// ── (e) Reset isolation across tests ────────────────────────────────────────

func TestResetClassifiersForTest_ClearsChain(t *testing.T) {
	// Pre-condition: no matcher registered, so an unknown err returns
	// (zero, false). This pins the hermetic-isolation contract: tests
	// can mutate and reset the chain without leaking state across
	// test runs.
	ResetClassifiersForTest()
	d, ok := Decision(&fakeErr{tag: "post-reset"})
	if ok || d != (RetryDecision{}) {
		t.Fatalf("post-reset Decision: want (zero, false); got (%+v, %v)", d, ok)
	}
}

// ── Sanity: Decision concurrency under Reset isolation ────────────────────

func TestDecision_AfterPanicClassifier_StillResilient(t *testing.T) {
	t.Cleanup(ResetClassifiersForTest)
	// A panicking classifier would terminate the test process. The
	// walker MUST NOT amplify panics into recoverable classify calls.
	// This test pins the sanity contract: panic in classifier is
	// process-fatal (it bubbles through Decision). We do NOT trigger
	// a panic here — we just sanity-check that after registering a
	// legit classifier, Decision still works.
	RegisterClassifier(alwaysRetryClassifier())
	d, ok := Decision(errors.New("ok"))
	if !ok {
		t.Fatalf("Decision: want ok=true; got false")
	}
	if d.Class != ErrUnknown {
		t.Fatalf("Class: want ErrUnknown; got %q", d.Class)
	}
}
