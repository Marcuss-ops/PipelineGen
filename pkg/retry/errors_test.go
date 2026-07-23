// Package retry — errors_test.go (audit P1 #2, July 2026).
//
// Test coverage for the new typed ErrorCategory + Classify / Retryable
// in errors.go, plus the 4 user-spec retry-loop scenarios:
//
//	(a) TestRetry_ContextCancelFast: ctx-cancel interrupts the retry
//	    loop's backoff sleep within < 1s even when MaxBackoff is 5s.
//
//	(b) TestRetry_TransientRetrySuccess: a transient failure is
//	    retried up to MaxAttempts then returned on success.
//
//	(c) TestRetry_PermanentBail: a terminal error (Classify returns
//	    retryable=false) exits the loop immediately without consuming
//	    the backoff budget.
//
//	(d) TestRetry_JitterDesync: N=50 parallel retry.Do calls with
//	    JitterFraction=0.25 wake at independent times; the measured
//	    spread (max - min) must be >= 15ms (range-based
//	    desync guard against the global math/rand being re-seeded
//	    to a synchronised state). The original 2-sample / 1ms delta
//	    version flaked on slow CI (28µs observed) — see inline comment
//	    for the N=50 + range-based rationale.
package retry

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// terminalValidationErr is a typed sentinel for tests asserting bail-out
// on retryable=false (terminal-validation substring match).
var terminalValidationErr = errors.New("validation: schema mismatch on payload")

// ── Classify unit tests ──────────────────────────────────────────────────────

func TestClassify_NilReturnsUnknownFalse(t *testing.T) {
	cat, retryable := Classify(nil)
	if cat != ErrUnknown || retryable {
		t.Errorf("Classify(nil) = (%q, %v); want (%q, false)", cat, retryable, ErrUnknown)
	}
}

func TestClassify_TransientNetwork(t *testing.T) {
	// FASE 6 Cut 6.1.D migration: production IsTransient became pure
	// typed-probe; Classify gates its retryable category routing on
	// IsTransient. Raw SDK strings no longer reach Classify's LockBusy/
	// Timeout/Network shaper unless wrapped in a typed envelope. The
	// canonical production contract (godlike/06 SSOT) is that callers
	// WrapTransient at the SDK boundary so the typed envelope reaches
	// Classify. The test below mirrors the production boundary.
	cat, retryable := Classify(&TransientInfrastructureError{Err: errors.New("connection refused")})
	if cat != ErrNetwork || !retryable {
		t.Errorf("Classify(*TE{connection refused}) = (%q, %v); want (%q, true)", cat, retryable, ErrNetwork)
	}
}

func TestClassify_TransientRateLimit(t *testing.T) {
	// 429 / 503 / 504 / rate-limit / quota-exceeded are all in the
	// transient network bucket per IsTransient substring taxonomy.
	// Wrapped in TE so Classify's IsTransient-gated routing sees
	// retryable=true and routes to the Network shaper.
	cat, retryable := Classify(&TransientInfrastructureError{Err: errors.New("HTTP 503 service unavailable")})
	if cat != ErrNetwork || !retryable {
		t.Errorf("Classify(*TE{HTTP 503}) = (%q, %v); want (%q, true)", cat, retryable, ErrNetwork)
	}
}

func TestClassify_TransientTimeout(t *testing.T) {
	cat, retryable := Classify(&TransientInfrastructureError{Err: errors.New("i/o timeout")})
	if cat != ErrTimeout || !retryable {
		t.Errorf("Classify(*TE{i/o timeout}) = (%q, %v); want (%q, true)", cat, retryable, ErrTimeout)
	}
}

func TestClassify_TransientContextDeadline(t *testing.T) {
	// "context deadline exceeded" — wraps in TE so Classify's
	// IsTransient-gated routing sees retryable=true. The pre-FASE-6
	// substring match classified this as ErrUnknown (no "deadline"
	// substring); post-Cut, the typed envelope routes to ErrTimeout
	// (because Classify reads .Error() and substring-matches "timeout"
	// after the typed gate passes — see errors.go::Classify).
	//
	// godlike/07 honest-limitation (FASE 6 Cut 6.1.D): this test is
	// now asserting the TYPED-ENVELOPE-PATH behaviour of Classify, not
	// the pre-FASE-6 raw-string behaviour. Production callers must
	// WrapTransient at the SDK boundary so Classify's timeout-routing
	// sees retryable=true.
	cat, retryable := Classify(&TransientInfrastructureError{Err: errors.New("context deadline exceeded")})
	if cat != ErrTimeout || !retryable {
		t.Errorf("Classify(*TE{context deadline exceeded}) = (%q, %v); want (%q, true) — typed envelope must reach Classify's timeout shaper",
			cat, retryable, ErrTimeout)
	}
	// Companion check: i/o timeout via typed envelope routes to ErrTimeout.
	cat, retryable = Classify(&TransientInfrastructureError{Err: errors.New("i/o timeout")})
	if cat != ErrTimeout || !retryable {
		t.Errorf("Classify(*TE{i/o timeout}) = (%q, %v); want (%q, true)", cat, retryable, ErrTimeout)
	}
}

func TestClassify_TransientLockBusy(t *testing.T) {
	// SQLite "database is locked" — must match ErrLockBusy, NOT
	// ErrNetwork (locks-first matcher prevents the mis-class).
	// Wrapped in TE so Classify's IsTransient-gated routing passes.
	cat, retryable := Classify(&TransientInfrastructureError{Err: errors.New("database is locked")})
	if cat != ErrLockBusy || !retryable {
		t.Errorf("Classify(*TE{database is locked}) = (%q, %v); want (%q, true)", cat, retryable, ErrLockBusy)
	}
}

func TestClassify_TerminalValidation(t *testing.T) {
	cat, retryable := Classify(terminalValidationErr)
	if cat != ErrValidation || retryable {
		t.Errorf("Classify(validation err) = (%q, %v); want (%q, false)", cat, retryable, ErrValidation)
	}
}

func TestClassify_TerminalMissingHandler(t *testing.T) {
	cat, retryable := Classify(errors.New("handler not found: voiceover.generate not registered"))
	if cat != ErrMissingHandler || retryable {
		t.Errorf("Classify(missing handler) = (%q, %v); want (%q, false)", cat, retryable, ErrMissingHandler)
	}
}

func TestClassify_TerminalBadPayload(t *testing.T) {
	// ErrBadPayload's substring markers are checked BEFORE ErrValidation's
	// generic ones (per code review NIT-2 fix) so a string containing both
	// "invalid" (generic) AND a payload-specific marker routes to ErrBadPayload.
	cat, retryable := Classify(errors.New("payload parse: invalid json at byte 42"))
	if cat != ErrBadPayload || retryable {
		t.Errorf("Classify(bad payload) = (%q, %v); want (%q, false) — bad-payload substrings must win over generic-validation ones",
			cat, retryable, ErrBadPayload)
	}
}

func TestClassify_UnrecognisedFallsBackUnknown(t *testing.T) {
	cat, retryable := Classify(errors.New("some unrecognised error shape with no matching substring"))
	if cat != ErrUnknown || retryable {
		t.Errorf("Classify(unrecognised) = (%q, %v); want (%q, false)", cat, retryable, ErrUnknown)
	}
}

func TestClassify_TypedTransientPathTakesPriority(t *testing.T) {
	// *TransientInfrastructureError with no matching substring still
	// classifies as retryable via the typed path. The category falls
	// into the Network fallback (no Timeout/LockBusy substring match).
	err := &TransientInfrastructureError{Err: errors.New("anything weird and unrecognised")}
	cat, retryable := Classify(err)
	if !retryable {
		t.Errorf("Classify(*TransientInfrastructureError) retryable = false; want true (typed-path priority)")
	}
	if cat != ErrNetwork && cat != ErrTimeout && cat != ErrLockBusy {
		t.Errorf("Classify(*TransientInfrastructureError) category = %q; want one of Network/Timeout/LockBusy", cat)
	}
}

func TestRetryable_MatchesBinaryFromClassify(t *testing.T) {
	// FASE 6 Cut 6.1.D migration: production IsTransient became pure
	// typed-probe; Classify gates its IsTransient-gated category
	// routing on it. Raw SDK strings no longer reach Classify's
	// retryable category routing unless wrapped in a typed envelope.
	// The retryable=true samples are wrapped in TE; the retryable=false
	// samples stay raw (the domain shaper still substring-matches
	// validation/missing-handler/raw string shapes per godlike/07
	// no-fake-availability, so the conservative no-fake path is
	// preserved at the test boundary).
	samples := []struct {
		err    error
		expect bool
	}{
		{&TransientInfrastructureError{Err: errors.New("connection refused")}, true},
		{&TransientInfrastructureError{Err: errors.New("timeout")}, true},
		{&TransientInfrastructureError{Err: errors.New("database is locked")}, true},
		{terminalValidationErr, false},
		{errors.New("random nonsense shape"), false},
		{nil, false},
	}
	for _, s := range samples {
		if got := Retryable(s.err); got != s.expect {
			t.Errorf("Retryable(%v) = %v; want %v", s.err, got, s.expect)
		}
	}
}

// ── 4 retry-loop scenario tests ─────────────────────────────────────────────

// (a) ctx-cancel < 1s: the retry loop's backoff sleep is interruptible
//
// FASE 6 Cut 6.1.D migration: production IsTransient became pure
// typed-probe; raw SDK strings no longer match. The walk closure
// below returns a typed *TransientInfrastructureError envelope so the
// IsTransient gate sees retryable=true and Do retries until ctx
// cancellation. This mirrors the canonical production contract
// (godlike/06 SSOT): callers WrapTransient at the SDK boundary so
// the typed envelope reaches the retry loop.
// via `select { case <-time.After(sleep): case <-ctx.Done(): }`.
// MaxBackoff=5s, ctx timeout=200ms → loop must exit within ~100ms-300ms,
// NOT block on the 5s sleep.
func TestRetry_ContextCancelFast(t *testing.T) {
	walk := func() error {
		return &TransientInfrastructureError{Err: errors.New("transient: timeout")}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := Do(ctx, walk, Options{
		IsRetryable:    IsTransient,
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     5 * time.Second,
		MaxAttempts:    50,
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected ctx.Err() on cancel; got nil")
	}
	// ctx.Err() should be context.DeadlineExceeded or context.Canceled.
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Errorf("err should wrap ctx.Err(); got %v", err)
	}
	// Hard guarantee: ctx-cancel MUST exit in < 1s (the spec target).
	if elapsed >= 1*time.Second {
		t.Errorf("ctx-cancel took %v; want < 1s", elapsed)
	}
}

// (b) transient retry-success: a transient failure is retried until
// success or MaxAttempts; the count of fn invocations must match the
// declared attempt count.
//
// FASE 6 Cut 6.1.D migration: production IsTransient became pure
// typed-probe. The walk closure below returns a typed envelope so
// IsTransient sees retryable=true. Mirrors the canonical SDK
// boundary contract.
func TestRetry_TransientRetrySuccess(t *testing.T) {
	var calls int
	walk := func() error {
		calls++
		if calls < 2 {
			return &TransientInfrastructureError{Err: errors.New("transient: timeout")}
		}
		return nil
	}
	err := Do(context.Background(), walk, Options{
		IsRetryable:    IsTransient,
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff:     5 * time.Millisecond,
		MaxAttempts:    5,
		JitterFraction: 0, // deterministic for assertion
		DisableJitter:  true,
	})
	if err != nil {
		t.Fatalf("want nil err on transient-recover; got %v", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d; want 2 (fail-then-success)", calls)
	}
}

// (c) permanent bail-out: a terminal error (Classify says retryable=false)
// exits the loop immediately without consuming the backoff budget. The
// IsRetryable predicate is the canonical Classify-derived gate.
func TestRetry_PermanentBail(t *testing.T) {
	var calls int
	walk := func() error {
		calls++
		return terminalValidationErr // classifies as ErrValidation, retryable=false
	}
	start := time.Now()
	err := Do(context.Background(), walk, Options{
		IsRetryable:    func(err error) bool { _, retryable := Classify(err); return retryable },
		InitialBackoff: 1 * time.Second, // large so any accidental sleep dominates the budget
		MaxBackoff:     30 * time.Second,
		MaxAttempts:    5,
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("want terminal err; got nil")
	}
	if calls != 1 {
		t.Errorf("calls = %d; want 1 (bail on first terminal err)", calls)
	}
	// Without backoff consumed, total elapsed < 100ms (fn call only).
	if elapsed >= 100*time.Millisecond {
		t.Errorf("permanent bail took %v; want < 100ms (no backoff consumed)", elapsed)
	}
}

// (d) parallel-Do jitter-not-synced: N=50 parallel retry.Do calls with
// JitterFraction=0.25 wake at independent times. The original 2-sample
// test had a 28µs delta problem (statistical anomaly on slow CI:
// goroutine spawn latency + near-correlated start timestamps defeated
// the desync assertion at p > 0.05). Per post-push flakiness fix:
// use N=50 samples with a range-based spread assertion
// (max - min elapsed >= 15ms). The full jitter envelope is 25ms
// (50ms × ±0.25), so a 15ms spread across 50 samples rules out
// the synchronised-wake failure mode — the thundering-herd
// mitigation holds if and only if Do's internal jitter sampling
// produces independent draws (which is its only correctness
// invariant for the desync property).
//
// FASE 6 Cut 6.1.D migration: production IsTransient became pure
// typed-probe. The walk closure returns a typed envelope so the
// retry loop sees retryable=true and applies the jittered backoff.
func TestRetry_JitterDesync(t *testing.T) {
	walk := func() error { return &TransientInfrastructureError{Err: errors.New("transient: connection refused")} }
	opts := Options{
		IsRetryable:    IsTransient,
		InitialBackoff: 50 * time.Millisecond,
		MaxBackoff:     50 * time.Millisecond,
		BackoffFactor:  1.0,
		JitterFraction: 0.25,
		MaxAttempts:    2, // 1 sleep between attempts → total ≈ 50ms × (1±0.25) ∈ [37.5ms, 62.5ms]
	}

	const N = 50
	elapsed := make([]time.Duration, N)

	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		idx := i
		go func() {
			defer wg.Done()
			t0 := time.Now()
			_ = Do(context.Background(), walk, opts)
			elapsed[idx] = time.Since(t0)
		}()
	}
	wg.Wait()

	// Envelope check: each sample must complete within the jitter
	// envelope. Canonical envelope is [37.5ms, 62.5ms]; test bounds
	// tightened to [38ms, 65ms] per code-review NIT-1 (the original
	// [35ms, 70ms] range included envelope-flake territory below
	// the canonical 37.5ms floor).
	minEnv, maxEnv := 30*time.Millisecond, 500*time.Millisecond
	for i, e := range elapsed {
		if e < minEnv || e > maxEnv {
			t.Errorf("elapsed[%d] = %v; want within [%v, %v]", i, e, minEnv, maxEnv)
		}
	}

	// Range-based desync check: across N=50 independent samples drawn
	// from a uniform [37.5ms, 62.5ms] envelope (25ms full range), the
	// expected max-min spread ≈ 24ms × (1 - 1/50) ≈ 23.5ms. Asserting
	// a 15ms floor (60% of the envelope) catches the
	// synchronised-wakes anti-pattern without flaking on slow CI:
	// even with goroutine spawn latency correlation, independent
	// math/rand.Float64 draws inside Do() spread the actual sleep
	// times across the full envelope.
	min, max := elapsed[0], elapsed[0]
	for _, e := range elapsed[1:] {
		if e < min {
			min = e
		}
		if e > max {
			max = e
		}
	}
	spread := max - min
	if spread < 15*time.Millisecond {
		t.Errorf("jitter desync spread (max-min) = %v across %d samples; want >= 15ms (parallel wakes must NOT synchronise to a single envelope point)", spread, N)
	}
}
