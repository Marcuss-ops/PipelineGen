// Package app — FASE 6 TTS retry + circuit breaker tests (July 2026).
package voiceover

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	voiceover "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// concurrencyProbe is a TTSProvider that holds every call until released and
// records the maximum observed concurrency. It has no retry/timeout logic of
// its own, so it measures exactly the rate-limited gate in front of it.
type concurrencyProbe struct {
	entered chan struct{}
	release chan struct{}
	current atomic.Int64
	max     atomic.Int64
}

func (p *concurrencyProbe) Synthesize(ctx context.Context, _ voiceover.TTSInput) (voiceover.TTSOutput, error) {
	n := p.current.Add(1)
	for {
		m := p.max.Load()
		if n <= m || p.max.CompareAndSwap(m, n) {
			break
		}
	}
	p.entered <- struct{}{}
	select {
	case <-p.release:
	case <-ctx.Done():
	}
	p.current.Add(-1)
	return voiceover.TTSOutput{}, nil
}

// TestRateLimitedTTSProvider_HonorsNetworkAndLocalSlots pins the 2026-10-29
// TTS ceiling fixes: the former 16-slot clamp silently ignored a configured
// voiceover.max_concurrent_tts of 24 (benchmark: 120 chunks, 10.765 s at 12
// vs 5.465 s at 24), and the outer gate must not spend its network slots on
// local probe/loudness work. The processor still bounds HTTP requests to 24;
// this gate adds the local allowance on top so the stream stays saturated.
func TestRateLimitedTTSProvider_HonorsNetworkAndLocalSlots(t *testing.T) {
	want := maxTTSProviderSlots + ttsLocalWorkAllowance
	probe := &concurrencyProbe{entered: make(chan struct{}, want), release: make(chan struct{})}
	provider := NewRateLimitedTTSProvider(probe, config.VoiceoverConcurrencyConfig{MaxConcurrentTTS: maxTTSProviderSlots}, zap.NewNop())

	var wg sync.WaitGroup
	for i := 0; i < want; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = provider.Synthesize(context.Background(), voiceover.TTSInput{})
		}()
	}
	for i := 0; i < want; i++ {
		select {
		case <-probe.entered:
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d/%d calls reached the provider; the configured width was clamped", i, want)
		}
	}
	if got := probe.max.Load(); got != int64(want) {
		t.Fatalf("max concurrency = %d, want %d", got, want)
	}
	close(probe.release)
	wg.Wait()
}

// ── retryableTTSProvider tests ────────────────────────────────────────────

// ttsCallRecorder records each Synthesize call and returns canned responses
// from a pre-configured sequence.
type ttsCallRecorder struct {
	calls    atomic.Int64
	results  []ttsCallResult // index = call number (0-based)
	fallback ttsCallResult   // used when results exhausted
}

type ttsCallResult struct {
	out voiceover.TTSOutput
	err error
}

func (r *ttsCallRecorder) Synthesize(_ context.Context, _ voiceover.TTSInput) (voiceover.TTSOutput, error) {
	n := int(r.calls.Add(1)) - 1 // 0-based
	if n < len(r.results) {
		res := r.results[n]
		// Wrap non-transient errors with TransientInfrastructureError
		// so retry.IsTransient (and thus retry.Do) recognizes them.
		// retry.WrapTransient only wraps already-transient errors, so
		// we must use the concrete type directly for test simulation.
		if res.err != nil && !retry.IsTransient(res.err) {
			return res.out, &retry.TransientInfrastructureError{Err: res.err}
		}
		return res.out, res.err
	}
	fallback := r.fallback
	if fallback.err != nil && !retry.IsTransient(fallback.err) {
		return fallback.out, &retry.TransientInfrastructureError{Err: fallback.err}
	}
	return fallback.out, fallback.err
}

func (r *ttsCallRecorder) Calls() int { return int(r.calls.Load()) }

// TestRetryableTTSProvider_RetriesThenSucceeds verifies the retry contract:
// 3 failures → retry → success on the 4th attempt. This is the canonical
// test the user spec asked for.
func TestRetryableTTSProvider_RetriesThenSucceeds(t *testing.T) {
	recorder := &ttsCallRecorder{
		results: []ttsCallResult{
			{err: errors.New("TTS error 1")},
			{err: errors.New("TTS error 2")},
			{err: errors.New("TTS error 3")},
			{out: voiceover.TTSOutput{LocalPath: "/tmp/audio.mp3", ContentSHA256: "abc123"}},
		},
	}

	provider := NewRetryableTTSProvider(recorder, config.VoiceoverConcurrencyConfig{
		TTSMaxRetries:     4,  // 4 attempts total (1 initial + 3 retries)
		TTSRetryBackoffMs: 10, // tiny backoff for fast test
	}, zap.NewNop())

	out, err := provider.Synthesize(context.Background(), voiceover.TTSInput{
		Filename: "test.mp3",
	})
	require.NoError(t, err)
	require.Equal(t, "/tmp/audio.mp3", out.LocalPath)
	require.Equal(t, "abc123", out.ContentSHA256)
	require.Equal(t, 4, recorder.Calls(), "expected 4 calls: 3 failures + 1 success")
}

// TestRetryableTTSProvider_AllRetriesExhausted verifies that when all
// attempts fail, the error is propagated and the consecutive-failure
// counter is incremented.
func TestRetryableTTSProvider_AllRetriesExhausted(t *testing.T) {
	recorder := &ttsCallRecorder{
		results: []ttsCallResult{
			{err: errors.New("fail 1")},
			{err: errors.New("fail 2")},
			{err: errors.New("fail 3")},
		},
	}

	provider := NewRetryableTTSProvider(recorder, config.VoiceoverConcurrencyConfig{
		TTSMaxRetries:     3,
		TTSRetryBackoffMs: 10,
	}, zap.NewNop())

	_, err := provider.Synthesize(context.Background(), voiceover.TTSInput{
		Filename: "test.mp3",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "all 3 attempts failed")
	require.Equal(t, 3, recorder.Calls())
	// consecutiveFailures increments once per failed Synthesize call
	// (not per retry attempt). The test only calls Synthesize once,
	// so the counter is 1 even though the inner provider was called
	// 3 times by the retry loop.
	require.Equal(t, int64(1), provider.consecutiveFailures.Load())
}

// TestRetryableTTSProvider_CircuitBreakerOpensAfterThreshold verifies the
// circuit breaker opens after N consecutive failures and rejects subsequent
// calls with ErrTTSCircuitBreakerOpen.
func TestRetryableTTSProvider_CircuitBreakerOpensAfterThreshold(t *testing.T) {
	recorder := &ttsCallRecorder{
		results: []ttsCallResult{
			{err: errors.New("fail")},
			{err: errors.New("fail")},
			{err: errors.New("fail")},
			{err: errors.New("fail")},
			{err: errors.New("fail")},
		},
		fallback: ttsCallResult{err: errors.New("should not be called — circuit open")},
	}

	// Threshold = 3, so the circuit opens after the 3rd failure.
	provider := NewRetryableTTSProvider(recorder, config.VoiceoverConcurrencyConfig{
		TTSMaxRetries:               1, // no retries — each call fails once
		TTSRetryBackoffMs:           10,
		TTSCircuitBreakerThreshold:  3,
		TTSCircuitBreakerCooldownMs: 60000, // 60s cooldown
	}, zap.NewNop())

	// 3 consecutive failures — circuit should open.
	for i := 0; i < 3; i++ {
		_, err := provider.Synthesize(context.Background(), voiceover.TTSInput{Filename: "test.mp3"})
		require.Error(t, err)
	}

	// 4th call — circuit is open, should reject immediately.
	_, err := provider.Synthesize(context.Background(), voiceover.TTSInput{Filename: "test.mp3"})
	require.ErrorIs(t, err, ErrTTSCircuitBreakerOpen)
	require.Equal(t, 3, recorder.Calls(), "4th call should be rejected without calling inner")

	// Verify counter is at 3 (only the 3 failed calls incremented it;
	// the rejected call did NOT touch the inner provider).
	require.Equal(t, int64(3), provider.consecutiveFailures.Load())
}

// TestRetryableTTSProvider_CircuitBreakerResetsOnSuccess verifies that
// a successful call resets the consecutive-failure counter to zero.
func TestRetryableTTSProvider_CircuitBreakerResetsOnSuccess(t *testing.T) {
	recorder := &ttsCallRecorder{
		results: []ttsCallResult{
			{err: errors.New("fail 1")},
			{err: errors.New("fail 2")},
			{out: voiceover.TTSOutput{LocalPath: "/tmp/ok.mp3"}},
		},
	}

	provider := NewRetryableTTSProvider(recorder, config.VoiceoverConcurrencyConfig{
		TTSMaxRetries:     1,
		TTSRetryBackoffMs: 10,
	}, zap.NewNop())

	// 2 failures.
	_, err := provider.Synthesize(context.Background(), voiceover.TTSInput{Filename: "f1.mp3"})
	require.Error(t, err)
	_, err = provider.Synthesize(context.Background(), voiceover.TTSInput{Filename: "f2.mp3"})
	require.Error(t, err)
	require.Equal(t, int64(2), provider.consecutiveFailures.Load())

	// 3rd call succeeds → counter resets to 0.
	_, err = provider.Synthesize(context.Background(), voiceover.TTSInput{Filename: "ok.mp3"})
	require.NoError(t, err)
	require.Equal(t, int64(0), provider.consecutiveFailures.Load())
}

// TestRetryableTTSProvider_CircuitBreakerHalfOpenProbe verifies the
// half-open → closed transition: after the cooldown expires, the next
// call probes the inner provider. If it succeeds, the circuit closes.
func TestRetryableTTSProvider_CircuitBreakerHalfOpenProbe(t *testing.T) {
	recorder := &ttsCallRecorder{
		results: []ttsCallResult{
			{err: errors.New("fail")},
			{err: errors.New("fail")},
			// 3rd result: the probe call (after cooldown).
			// The 3rd Synthesize call is rejected at the gate
			// (circuit open), so it never reaches inner.
			{out: voiceover.TTSOutput{LocalPath: "/tmp/probe_ok.mp3"}},
		},
	}

	provider := NewRetryableTTSProvider(recorder, config.VoiceoverConcurrencyConfig{
		TTSMaxRetries:               1,
		TTSRetryBackoffMs:           10,
		TTSCircuitBreakerThreshold:  2,
		TTSCircuitBreakerCooldownMs: 50, // 50ms cooldown — safe margin for CI
	}, zap.NewNop())

	// 2 failures → circuit opens (threshold=2).
	_, err := provider.Synthesize(context.Background(), voiceover.TTSInput{Filename: "f1.mp3"})
	require.Error(t, err)
	_, err = provider.Synthesize(context.Background(), voiceover.TTSInput{Filename: "f2.mp3"})
	require.Error(t, err)

	// Verify circuit is open — 3rd call rejected at the gate.
	_, err = provider.Synthesize(context.Background(), voiceover.TTSInput{Filename: "rejected.mp3"})
	require.ErrorIs(t, err, ErrTTSCircuitBreakerOpen)
	require.Equal(t, 2, recorder.Calls(), "2 inner calls (fail, fail); 3rd rejected at gate")

	// Wait for cooldown to expire.
	time.Sleep(100 * time.Millisecond)

	// Probe call — should succeed and close the circuit.
	out, err := provider.Synthesize(context.Background(), voiceover.TTSInput{Filename: "probe.mp3"})
	require.NoError(t, err)
	require.Equal(t, "/tmp/probe_ok.mp3", out.LocalPath)
	require.Equal(t, 3, recorder.Calls(), "3 inner calls: fail, fail, success")
	require.Equal(t, int64(0), provider.consecutiveFailures.Load())
	require.Equal(t, int64(0), provider.openedAt.Load(), "circuit should be closed after successful probe")
}

// TestRetryableTTSProvider_CircuitBreakerHalfOpenProbeFails verifies the
// half-open → open transition: after the cooldown expires, a probe call
// that fails causes the circuit to re-open (cooldown restarts) and the
// next call is rejected with ErrTTSCircuitBreakerOpen.
func TestRetryableTTSProvider_CircuitBreakerHalfOpenProbeFails(t *testing.T) {
	recorder := &ttsCallRecorder{
		results: []ttsCallResult{
			{err: errors.New("fail")},
			{err: errors.New("fail")},
			// 3rd result: the probe call that also fails.
			{err: errors.New("probe failed")},
		},
		fallback: ttsCallResult{err: errors.New("should not be called — circuit re-opened")},
	}

	provider := NewRetryableTTSProvider(recorder, config.VoiceoverConcurrencyConfig{
		TTSMaxRetries:               1,
		TTSRetryBackoffMs:           10,
		TTSCircuitBreakerThreshold:  2,
		TTSCircuitBreakerCooldownMs: 50, // 50ms cooldown
	}, zap.NewNop())

	// 2 failures → circuit opens (threshold=2).
	_, err := provider.Synthesize(context.Background(), voiceover.TTSInput{Filename: "f1.mp3"})
	require.Error(t, err)
	_, err = provider.Synthesize(context.Background(), voiceover.TTSInput{Filename: "f2.mp3"})
	require.Error(t, err)

	// Verify circuit is open.
	_, err = provider.Synthesize(context.Background(), voiceover.TTSInput{Filename: "rejected.mp3"})
	require.ErrorIs(t, err, ErrTTSCircuitBreakerOpen)

	// Wait for cooldown to expire.
	time.Sleep(100 * time.Millisecond)

	// Probe call fails → circuit should re-open.
	_, err = provider.Synthesize(context.Background(), voiceover.TTSInput{Filename: "probe.mp3"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "all 1 attempts failed")
	require.Equal(t, 3, recorder.Calls())

	// Circuit should be open again — next call rejected.
	_, err = provider.Synthesize(context.Background(), voiceover.TTSInput{Filename: "rejected2.mp3"})
	require.ErrorIs(t, err, ErrTTSCircuitBreakerOpen)
	require.Equal(t, 3, recorder.Calls(), "rejected call should NOT touch inner")
	// consecutiveFailures should equal the total number of failed Synthesize
	// calls (2 initial failures + 1 probe failure = 3). The counter is never
	// reset until a success.
	require.Equal(t, int64(3), provider.consecutiveFailures.Load())
	// openedAt should be non-zero (circuit is open).
	require.NotEqual(t, int64(0), provider.openedAt.Load(), "circuit should be open")
}

// TestRetryableTTSProvider_CompileTimePin verifies the adapter satisfies
// the voiceover.TTSProvider interface at compile time.
func TestRetryableTTSProvider_CompileTimePin(t *testing.T) {
	// This test exists so the test file imports the type and the
	// compile-time var _ pin in the production file catches drift.
	_ = (*retryableTTSProvider)(nil)
	t.Log("retryableTTSProvider type compiles — var _ pin guards interface conformance")
}
