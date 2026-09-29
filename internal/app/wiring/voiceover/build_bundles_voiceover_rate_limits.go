// Package app — voiceover rate-limit adapters (FASE 8 VO-OPERATIONAL-READINESS, July 2026).
//
// Three thin adapter wrappers that add bounded concurrency (per-owner-fair
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
//
// rateLimitedTranslator was DELETED from this file on 2026-09-20: it had no
// construction site anywhere in the tree. Removing it changes no runtime
// behaviour, because the canonical path already passes the un-wrapped
// translation.NewOllamaTranslator to translation.TranslationPort; what it does
// remove is the illusion that translation calls are semaphore-bounded and
// per-call-timeout-bounded. If that protection is wanted, it has to be wired as
// a real injection site next to the TTS and publisher adapters above, not kept
// as a satisfied-looking port implementation that nothing constructs.
//
// Gate acquire happens BEFORE timeout-derivation so the per-call timeout
// budget covers execution only, and the wait itself is recorded as a typed
// WaitSemaphore interval on the bound run (kernel/observability). That keeps
// the timeout budget predictable AND keeps the queue wait out of the call's
// work time: a task that queues for 4 minutes still gets its full 2-minute TTS
// timeout once it acquires the slot, and the 4 minutes appear as blocked time
// instead of an inflated inference/upload duration. Cancellation while queued
// returns ctx.Err() without holding a slot.
//
// godlike/06 SSOT: each adapter is the SOLE owner of its semaphore and
// its timeout/retry policy. The composition root (build_bundles_voiceover.go)
// constructs them and injects them into the voiceover.Service.
// godlike/07 minimum-blast-radius: zero changes to the voiceover package.
package voiceover

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	voiceover "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
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

func NewRetryableTTSProvider(inner voiceover.TTSProvider, vcfg config.VoiceoverConcurrencyConfig, log *zap.Logger) *retryableTTSProvider {
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
// concurrency gate and per-call timeout. The gate capacity is clamped to
// [1, 16] at construction; zero or negative values default to 1.
//
// The gate is a per-owner-FAIR semaphore, and the time spent waiting on it is
// recorded as a WaitSemaphore interval on the bound run: a job whose scenes
// queue behind another job's synthesis now sees the queue wait as blocked time
// instead of an inflated TTS work time (2026-09-28: the shared gate was
// invisible in the timing report).
//
// Compile-time assertion: the adapter satisfies the TTSProvider port.
var _ voiceover.TTSProvider = (*rateLimitedTTSProvider)(nil)

type rateLimitedTTSProvider struct {
	inner   voiceover.TTSProvider
	sem     *concurrent.FairSemaphore
	timeout time.Duration
	log     *zap.Logger
}

func NewRateLimitedTTSProvider(inner voiceover.TTSProvider, vcfg config.VoiceoverConcurrencyConfig, log *zap.Logger) *rateLimitedTTSProvider {
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
		sem:     concurrent.NewFairSemaphore(cap),
		timeout: timeout,
		log:     log,
	}
}

func (r *rateLimitedTTSProvider) Synthesize(ctx context.Context, input voiceover.TTSInput) (voiceover.TTSOutput, error) {
	release, err := kernobs.AcquireFairSlot(ctx, r.sem, kernobs.WaitOwner(ctx), kernobs.ComponentTTS, kernobs.WaitSemaphore)
	if err != nil {
		return voiceover.TTSOutput{}, err
	}
	defer release()
	timedCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	return r.inner.Synthesize(timedCtx, input)
}

// ── rateLimitedPublisher ──────────────────────────────────────────────────

// rateLimitedPublisher wraps a voiceover.VoiceoverPublisher with a bounded
// concurrency gate, per-call timeout, and Drive-upload retry via pkg/retry.Do.
// The gate capacity is clamped to [1, 16]; zero or negative defaults to 3.
// Retry uses exponential backoff starting at the configured
// DriveUploadRetryBackoffMs, capped at 10s.
//
// The gate is a per-owner-FAIR semaphore and it is shared by EVERY job in the
// process (it is constructed once at composition). The 2026-09-28 measurement
// caught a single final_audio upload waiting 85.7 s for ~5 s of work because
// two other jobs' publication phases held all three slots; fairness plus the
// recorded WaitSemaphore interval make that queue wait both impossible to
// monopolize and visible in the report.
//
// Compile-time assertion: the adapter satisfies VoiceoverPublisher.
var _ voiceover.VoiceoverPublisher = (*rateLimitedPublisher)(nil)

type rateLimitedPublisher struct {
	inner       voiceover.VoiceoverPublisher
	sem         *concurrent.FairSemaphore
	timeout     time.Duration
	maxRetries  int
	initialWait time.Duration
	log         *zap.Logger
}

// NewDriveUploadGate builds the Drive-upload gate a process shares across every
// publisher that talks to Drive. One authority, one capacity: the configured
// `max_concurrent_drive_uploads` is a process-wide ceiling ("limits parallel
// Google Drive upload calls"), so callers that each built their own gate would
// multiply the ceiling by the number of publishers instead of enforcing it.
//
// Capacity is clamped to [1, 16]; zero or negative defaults to 3, matching the
// historical adapter default.
func NewDriveUploadGate(vcfg config.VoiceoverConcurrencyConfig) *concurrent.FairSemaphore {
	capacity := vcfg.MaxConcurrentDriveUploads
	if capacity < 1 {
		capacity = 3
	}
	if capacity > 16 {
		capacity = 16
	}
	return concurrent.NewFairSemaphore(capacity)
}

// NewRateLimitedPublisher builds the adapter with a gate of its own. Prefer
// NewRateLimitedPublisherWithGate at composition sites that also publish other
// Drive artifacts (final audio, docs), so every Drive upload in the process
// shares one fair gate.
func NewRateLimitedPublisher(inner voiceover.VoiceoverPublisher, vcfg config.VoiceoverConcurrencyConfig, log *zap.Logger) *rateLimitedPublisher {
	return NewRateLimitedPublisherWithGate(inner, NewDriveUploadGate(vcfg), vcfg, log)
}

// NewRateLimitedPublisherWithGate is NewRateLimitedPublisher with an
// externally-owned gate (see NewDriveUploadGate).
func NewRateLimitedPublisherWithGate(inner voiceover.VoiceoverPublisher, gate *concurrent.FairSemaphore, vcfg config.VoiceoverConcurrencyConfig, log *zap.Logger) *rateLimitedPublisher {
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
		sem:         gate,
		timeout:     timeout,
		maxRetries:  maxRetries,
		initialWait: initialWait,
		log:         log,
	}
}

func (r *rateLimitedPublisher) Publish(ctx context.Context, cmd voiceover.VoiceoverPublishCommand) (string, error) {
	// Acquire the fair gate BEFORE deriving the timeout so the budget is
	// execution-only: the queue wait is charged to the run's typed wait, never
	// to this call's work time. ctx cancellation is honoured without a slot.
	release, err := kernobs.AcquireFairSlot(ctx, r.sem, kernobs.WaitOwner(ctx), kernobs.ComponentDrive, kernobs.WaitSemaphore)
	if err != nil {
		return "", err
	}
	defer release()

	// Retry loop: each attempt gets its own timeout context so a
	// single slow upload doesn't consume the retry budget.
	var fileID string
	err = retry.Do(ctx, func() error {
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

// rateLimitedTranslator lived here until 2026-09-20. See the file header for
// why it was deleted and what would have to exist before the protection it
// described could be claimed.
