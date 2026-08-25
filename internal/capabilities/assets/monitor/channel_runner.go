// Package monitor — channel_runner.go: per-channel goroutine fan-out
// with semaphore, timeout, and panic recovery.
//
// God-object decomposition (PR-GODOBJ-2, July 2026): extracted from
// scheduler.go per the action-plan split topology. This file owns:
//   - checkDueChannels: bounded goroutine fan-out with per-channel timeout.
//   - safeCheckChannel: panic-recovery wrapper that converts panics to errors.
package monitor

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	channels "github.com/Marcuss-ops/PipelineGen/internal/capabilities/channels"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// checkDueChannels spawns bounded goroutines (one per channel), each of
// which runs safeCheckChannel + records the outcome via recordCheckOutcome.
//
// Commit A (June 2026, P1 #9): the per-goroutine recover-and-log defer
// previously LOGGED the panic but did NOT call recordCheckOutcome. The
// lease was held until expiry (typically 30 min). The fix is to route
// every per-channel panic through safeCheckChannel, which converts the
// panic into a typed error that recordCheckOutcome always sees — so the
// channel ends up Success=false with a synthesized panic message and the
// exponential backoff can apply. The bound is policy.MaxConcurrentChannels
// from MonitorRuntimePolicy (governed by m.globalSem, the per-monitor
// rate-limiter semaphore). The per-channel ctx timeout is
// policy.PerChannelTimeout.
//
// Blocco 3c (July 2026): validateChannelConfig runs BEFORE the first
// yt-dlp call. A channel with malformed Keywords/SemanticKeywords JSON
// is rejected immediately — no video listing, no Ollama calls, no
// wasted cycle budget.
func (m *ChannelMonitor) checkDueChannels(ctx context.Context, chs []channels.Channel) {
	policy := m.policyOrDefault()
	for _, ch := range chs {
		ch := ch

		// Blocco 3c: validate JSON config BEFORE spawning the goroutine.
		// A malformed Keywords/SemanticKeywords field is a hard error —
		// skip the check entirely and record the failure immediately.
		if valErr := m.validateChannelConfig(ch); valErr != nil {
			m.log.Warn("skipping channel with invalid config",
				zap.String("channel_id", ch.ID),
				zap.Error(valErr))
			if recErr := m.recordCheckOutcome(ctx, ch, valErr); recErr != nil {
				m.log.Error("failed to record validation failure",
					zap.String("channel_id", ch.ID),
					zap.Error(recErr))
			}
			continue
		}

		m.globalSem <- struct{}{}
		concurrent.SafeGo("monitor-channel-check", func() {
			defer func() { <-m.globalSem }()

			checkCtx, cancel := context.WithTimeout(ctx, policy.PerChannelTimeout)
			defer cancel()

			result, checkErr := m.safeCheckChannel(checkCtx, ch)
			m.log.Info("channel check completed",
				zap.String("channel_id", ch.ID),
				zap.Bool("success", checkErr == nil),
				zap.Int("videos_discovered", result.VideosDiscovered),
				zap.Int("videos_enqueued", result.VideosEnqueued),
				zap.Int("videos_skipped", result.VideosSkipped),
				zap.Int("infra_failures", result.InfraFailures))

			if recErr := m.recordCheckOutcome(ctx, ch, checkErr); recErr != nil {
				m.log.Error("Failed to mark channel as checked",
					zap.String("channel_id", ch.ID),
					zap.Error(recErr))
			}
		})
	}
}

// safeCheckChannel wraps checkChannel with panic-recovery. The
// previous in-goroutine `defer recover()` swallowed panics into a
// log line and let the lease sit idle until expiry; safeCheckChannel
// instead converts the panic into a regular Go error so the caller's
// recordCheckOutcome always fires and the backoff path is taken.
//
// Return shape is identical to checkChannel: (ChannelCheckResult,
// error). On panic the result is the zero ChannelCheckResult so a
// panic in processVideo doesn't pollute the per-channel counters.
func (m *ChannelMonitor) safeCheckChannel(ctx context.Context, ch channels.Channel) (result ChannelCheckResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			m.log.Error("panic in channel check goroutine (safeCheckChannel)",
				zap.Any("recover", r),
				zap.String("channel_id", ch.ID))
			// Go's panic payload is `any`; %w requires an error.
			// Two cases: (1) panic was raised with an error value —
			// propagate it as-is via %w so callers can errors.Is.
			// (2) panic was raised with a non-error value (string,
			// int, struct) — fall back to %v rendering so the operator
			// still sees the payload in the wrapped error message.
			if e, ok := r.(error); ok {
				err = fmt.Errorf("channel check panicked for %s: %w", ch.ID, e)
			} else {
				err = fmt.Errorf("channel check panicked for %s: %v", ch.ID, r)
			}
		}
	}()
	return m.checkChannel(ctx, ch)
}
