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
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

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

func newRateLimitedTranslator(inner translation.TranslationPort, vcfg config.VoiceoverConcurrencyConfig, log *zap.Logger) *rateLimitedTranslator {
	_ = log // reserved for future timeout/queue-wait observability
	// Ollama concurrency: use 1 by default (single-GPU safety).
	// The global ConcurrencyConfig.MaxConcurrentOllamaCalls is the
	// system-wide cap; the voiceover pipeline mirrors that here.
	cap := 1
	timeout := time.Duration(vcfg.OllamaTimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return &rateLimitedTranslator{
		inner:   inner,
		sem:     make(chan struct{}, cap),
		timeout: timeout,
		log:     log,
	}
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
