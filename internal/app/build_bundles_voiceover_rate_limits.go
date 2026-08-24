// Package app — voiceover rate-limit adapters (FASE 8 VO-OPERATIONAL-READINESS, July 2026).
//
// Three thin adapter wrappers that add bounded concurrency (channel-based
// semaphore), per-call timeouts (context.WithTimeout), and Drive-upload
// retry (pkg/retry.Do) to the voiceover pipeline. Each adapter satisfies
// exactly one voiceover port (Pattern 0) so the composition root can
// swap the rate-limited version in-place without the voiceover package
// knowing about concurrency.
//
// Adapter topology:
//
//	rateLimitedTTSProvider   wraps voiceover.TTSProvider
//	rateLimitedPublisher     wraps voiceover.VoiceoverPublisher
//	rateLimitedTranslator    wraps translation.TranslationPort
//
// Semaphore acquire happens BEFORE timeout-derivation so the per-call
// timeout budget covers execution only. Queue-wait is bounded by the
// caller's ctx cancellation (select on sem+ctx.Done). This keeps the
// timeout budget predictable: a task that queues for 4 minutes still
// gets its full 2-minute TTS timeout once it acquires the slot. The caller's ctx
// cancellation is also honoured (select on sem+ctx.Done).
//
// godlike/06 SSOT: each adapter is the SOLE owner of its semaphore and
// its timeout/retry policy. The composition root (build_bundles_voiceover.go)
// constructs them and injects them into the voiceover.Service.
// godlike/07 minimum-blast-radius: zero changes to the voiceover package.
package app

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// ── retryableTTSProvider (FASE 6, July 2026) ──────────────────────────────

// retryableTTSProvider wraps a voiceover.TTSProvider with exponential-backoff
// retry (pkg/retry.Do) and a circuit breaker that opens after N consecutive
// failures, rejecting calls for a cooldown period.
//
// Circuit breaker states:
//
//	closed   – normal operation; calls pass through to inner.
//	open     – threshold exceeded; calls are rejected immediately with
//	           ErrTTSCircuitBreakerOpen until the cooldown expires.
//	half-open – cooldown expired; the NEXT call probes the inner provider.
//	           If it succeeds → closed (counter reset). If it fails →
//	           open again (cooldown restarts).
//
// Concurrency: atomic.Int64 for the failure counter + atomic.Int64 for the
// opened-at timestamp. No mutex — the circuit breaker tolerates transient
// over-count (a few extra failures past the threshold) because the cooldown
// timer is the authoritative gate.
//
// Compile-time assertion: satisfies TTSProvider.
var _ voiceover.TTSProvider = (*retryableTTSProvider)(nil)

var (
	// ErrTTSCircuitBreakerOpen is returned when the circuit breaker is
	// open and the cooldown has not yet expired.
	ErrTTSCircuitBreakerOpen = fmt.Errorf("voiceover TTS circuit breaker is open")
)

type retryableTTSProvider struct {
	inner voiceover.TTSProvider

	// Retry config.
	maxRetries  int
	initialWait time.Duration

	// Circuit breaker config.
	cbThreshold int           // consecutive failures before opening
	cbCooldown  time.Duration // how long the breaker stays open

	// Circuit breaker state (atomic, lock-free).
	consecutiveFailures atomic.Int64
	openedAt            atomic.Int64 // unix nano timestamp; 0 = closed

	log *zap.Logger
}

func newRetryableTTSProvider(inner voiceover.TTSProvider, vcfg config.VoiceoverConcurrencyConfig, log *zap.Logger) *retryableTTSProvider {
	maxRetries := vcfg.TTSMaxRetries
	if maxRetries < 1 {
		maxRetries = 3
	}
	initialWait := time.Duration(vcfg.TTSRetryBackoffMs) * time.Millisecond
	if initialWait <= 0 {
		initialWait = 500 * time.Millisecond
	}
	cbThreshold := vcfg.TTSCircuitBreakerThreshold
	if cbThreshold < 1 {
		cbThreshold = 5
	}
	cbCooldown := time.Duration(vcfg.TTSCircuitBreakerCooldownMs) * time.Millisecond
	if cbCooldown <= 0 {
		cbCooldown = 30 * time.Second
	}
	return &retryableTTSProvider{
		inner:       inner,
		maxRetries:  maxRetries,
		initialWait: initialWait,
		cbThreshold: cbThreshold,
		cbCooldown:  cbCooldown,
		log:         log,
	}
}

func (r *retryableTTSProvider) Synthesize(ctx context.Context, input voiceover.TTSInput) (voiceover.TTSOutput, error) {
	// Circuit breaker gate: if open and cooldown not expired, reject immediately.
	if opened := r.openedAt.Load(); opened != 0 {
		if elapsed := time.Since(time.Unix(0, opened)); elapsed < r.cbCooldown {
			r.log.Warn("voiceover TTS circuit breaker open — rejecting call",
				zap.Duration("elapsed", elapsed),
				zap.Duration("cooldown", r.cbCooldown),
				zap.Int64("remaining_ms", (r.cbCooldown-elapsed).Milliseconds()),
			)
			return voiceover.TTSOutput{}, ErrTTSCircuitBreakerOpen
		}
		// Cooldown expired → half-open: allow one probe call.
		r.log.Info("voiceover TTS circuit breaker half-open — probing")
	}

	// Retry loop with exponential backoff.
	var out voiceover.TTSOutput
	err := retry.Do(ctx, func() error {
		var attemptErr error
		out, attemptErr = r.inner.Synthesize(ctx, input)
		if attemptErr != nil {
			r.log.Warn("voiceover TTS synthesis attempt failed (will retry)",
				zap.String("filename", input.Filename),
				zap.Error(attemptErr),
			)
		}
		return attemptErr
	}, retry.Options{
		MaxAttempts:    r.maxRetries,
		InitialBackoff: r.initialWait,
		MaxBackoff:     10 * time.Second,
		BackoffFactor:  2.0,
		IsRetryable:    retry.IsTransient,
	})

	if err != nil {
		// All retries exhausted — increment circuit breaker counter.
		failures := r.consecutiveFailures.Add(1)
		r.log.Warn("voiceover TTS synthesis failed after all retries",
			zap.Int("max_retries", r.maxRetries),
			zap.Int64("consecutive_failures", failures),
			zap.Error(err),
		)
		if failures >= int64(r.cbThreshold) {
			// Open (or re-open) the circuit breaker. Store
			// (not CompareAndSwap) because openedAt may already
			// be non-zero from a prior open (half-open → open
			// transition). CAS would silently fail when openedAt
			// is non-zero, leaving the circuit stuck half-open
			// forever. Store unconditionally resets the cooldown
			// timer correctly for both closed→open and
			// half-open→open transitions.
			prev := r.openedAt.Swap(time.Now().UnixNano())
			if prev == 0 {
				r.log.Error("voiceover TTS circuit breaker OPEN",
					zap.Int64("consecutive_failures", failures),
					zap.Duration("cooldown", r.cbCooldown),
				)
			} else {
				r.log.Error("voiceover TTS circuit breaker remains OPEN after probe failure",
					zap.Int64("consecutive_failures", failures),
					zap.Duration("cooldown", r.cbCooldown),
				)
			}
		}
		return voiceover.TTSOutput{}, fmt.Errorf("retryableTTSProvider.Synthesize: all %d attempts failed: %w", r.maxRetries, err)
	}

	// Success — reset circuit breaker state.
	r.consecutiveFailures.Store(0)
	if r.openedAt.Swap(0) != 0 {
		r.log.Info("voiceover TTS circuit breaker closed (probe succeeded)")
	}
	return out, nil
}

// ── rateLimitedTTSProvider ────────────────────────────────────────────────

// rateLimitedTTSProvider wraps a voiceover.TTSProvider with a bounded
// concurrency semaphore and per-call timeout. The semaphore capacity is
// clamped to [1, 16] at construction; zero or negative values default to 1.
//
// Compile-time assertion: the adapter satisfies the TTSProvider port.
var _ voiceover.TTSProvider = (*rateLimitedTTSProvider)(nil)

type rateLimitedTTSProvider struct {
	inner   voiceover.TTSProvider
	sem     chan struct{}
	timeout time.Duration
	log     *zap.Logger
}

func newRateLimitedTTSProvider(inner voiceover.TTSProvider, vcfg config.VoiceoverConcurrencyConfig, log *zap.Logger) *rateLimitedTTSProvider {
	_ = log // reserved for future timeout/queue-wait observability
	cap := vcfg.MaxConcurrentTTS
	if cap < 1 {
		cap = 1
	}
	if cap > 16 {
		cap = 16
	}
	timeout := time.Duration(vcfg.TTSTimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return &rateLimitedTTSProvider{
		inner:   inner,
		sem:     make(chan struct{}, cap),
		timeout: timeout,
		log:     log,
	}
}

func (r *rateLimitedTTSProvider) Synthesize(ctx context.Context, input voiceover.TTSInput) (voiceover.TTSOutput, error) {
	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	case <-ctx.Done():
		return voiceover.TTSOutput{}, ctx.Err()
	}
	timedCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	return r.inner.Synthesize(timedCtx, input)
}

// ── rateLimitedPublisher ──────────────────────────────────────────────────

// rateLimitedPublisher wraps a voiceover.VoiceoverPublisher with a bounded
// concurrency semaphore, per-call timeout, and Drive-upload retry via
// pkg/retry.Do. The semaphore capacity is clamped to [1, 16]; zero or
// negative defaults to 3. Retry uses exponential backoff starting at the
// configured DriveUploadRetryBackoffMs, capped at 10s.
//
// Compile-time assertion: the adapter satisfies VoiceoverPublisher.
var _ voiceover.VoiceoverPublisher = (*rateLimitedPublisher)(nil)

type rateLimitedPublisher struct {
	inner       voiceover.VoiceoverPublisher
	sem         chan struct{}
	timeout     time.Duration
	maxRetries  int
	initialWait time.Duration
	log         *zap.Logger
}

func newRateLimitedPublisher(inner voiceover.VoiceoverPublisher, vcfg config.VoiceoverConcurrencyConfig, log *zap.Logger) *rateLimitedPublisher {
	cap := vcfg.MaxConcurrentDriveUploads
	if cap < 1 {
		cap = 3
	}
	if cap > 16 {
		cap = 16
	}
	timeout := time.Duration(vcfg.DriveUploadTimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 300 * time.Second
	}
	maxRetries := vcfg.DriveUploadMaxRetries
	if maxRetries < 1 {
		maxRetries = 3
	}
	initialWait := time.Duration(vcfg.DriveUploadRetryBackoffMs) * time.Millisecond
	if initialWait <= 0 {
		initialWait = 1 * time.Second
	}
	return &rateLimitedPublisher{
		inner:       inner,
		sem:         make(chan struct{}, cap),
		timeout:     timeout,
		maxRetries:  maxRetries,
		initialWait: initialWait,
		log:         log,
	}
}

func (r *rateLimitedPublisher) Publish(ctx context.Context, cmd voiceover.VoiceoverPublishCommand) (string, error) {
	// Acquire semaphore BEFORE deriving the timeout so the budget
	// includes queue-wait. ctx cancellation also honoured.
	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	case <-ctx.Done():
		return "", ctx.Err()
	}

	// Retry loop: each attempt gets its own timeout context so a
	// single slow upload doesn't consume the retry budget.
	var fileID string
	err := retry.Do(ctx, func() error {
		timedCtx, cancel := context.WithTimeout(ctx, r.timeout)
		defer cancel()
		var attemptErr error
		fileID, attemptErr = r.inner.Publish(timedCtx, cmd)
		if attemptErr != nil {
			r.log.Warn("voiceover Drive upload attempt failed (will retry)",
				zap.String("id", cmd.ID),
				zap.String("filename", cmd.Filename),
				zap.Error(attemptErr),
			)
		}
		return attemptErr
	}, retry.Options{
		MaxAttempts:    r.maxRetries,
		InitialBackoff: r.initialWait,
		MaxBackoff:     10 * time.Second,
		BackoffFactor:  2.0,
		IsRetryable:    retry.IsTransient,
	})
	if err != nil {
		return "", fmt.Errorf("rateLimitedPublisher.Publish: all %d attempts failed: %w", r.maxRetries, err)
	}
	return fileID, nil
}

// ── rateLimitedTranslator ─────────────────────────────────────────────────

// rateLimitedTranslator wraps a translation.TranslationPort with a bounded
// concurrency semaphore and per-call timeout for Ollama inference calls
// originating from the voiceover pipeline (promo translation).
//
// Compile-time assertion: the adapter satisfies TranslationPort.
var _ translation.TranslationPort = (*rateLimitedTranslator)(nil)

type rateLimitedTranslator struct {
	inner   translation.TranslationPort
	sem     chan struct{}
	timeout time.Duration
	log     *zap.Logger
}

func (r *rateLimitedTranslator) Translate(ctx context.Context, cmd translation.TranslationCommand) (translation.TranslationResult, error) {
	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	case <-ctx.Done():
		return translation.TranslationResult{}, ctx.Err()
	}
	timedCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	return r.inner.Translate(timedCtx, cmd)
}
