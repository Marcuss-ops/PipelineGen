// Package jobs — worker_backoff.go (PR7 split, June 2026).
//
// Backoff configuration + retry policy extracted from worker.go. Owns:
//
//  1. type BackoffConfig — per-Worker polling backoff sub-struct with
//     MaxBackoff / JitterFraction / ConsecutiveEmptyThreshold.
//  2. func (w *Worker) effectiveSleep — applies JitterFraction as
//     full-jitter on a base sleep duration (the canonical AWS jitter
//     pattern).
//  3. func jitterDuration — package-level free helper that produces
//     rand(0, base*jitter) clamped into [base*0, base*jitter]. Used by
//     both effectiveSleep and Start's initial-spread jitter.
//
// NOTE: The backoff ESCALATION state-machine logic INSIDE Start() (the
// "if consecutiveEmpty > threshold: double currentBackoff capped at
// MaxBackoff" block) is NOT extracted here — per the PR7 spec NO new
// abstractions are allowed. The escalation math stays inline in
// worker_execution.go's caller (currently worker.go::Start).
//
// Mechanical split, zero behavior change. ONLY relocated + import-redistributed.
package jobs

import (
	"math/rand"
	"time"
)

// BackoffConfig is the per-Worker polling backoff sub-struct
// (PR-Polling / ADR-0002 §D6.5, June 2026). All fields are config-driven
// from JobsConfig.PollMaxBackoff / PollJitterFraction /
// PollConsecutiveEmptyBeforeBackoff.
type BackoffConfig struct {
	// MaxBackoff caps the exponential-backoff curve. Worker poll
	// intervals grow up to this cap and stay there until a wake
	// arrives or a non-empty claim resets to BaseInterval. Default
	// 60s — matches the qdrant-stale-cleaner historical cadence
	// and bounds the worst-case latency between Wake → Claim.
	MaxBackoff time.Duration

	// JitterFraction is the FULL-JITTER factor (AWS pattern).
	// actualSleep = rand(0, currentBackoff) every iteration, which
	// spreads thundering-herd wake-ups across the worker pool when a
	// burst of Enqueues lands. 0.0 = no jitter (deterministic burn
	// of full backoff); 1.0 = uniform [0, currentBackoff] jitter.
	// Default 0.5.
	JitterFraction float64

	// ConsecutiveEmptyThreshold is the number of CONSECUTIVE empty
	// Claims Workers tolerate before escalating the backoff curve.
	// Below the threshold: stay at BaseInterval. Above: backoff
	// doubles every subsequent empty claim (capped at MaxBackoff).
	// 0 disables the escalation entirely (workers stay at
	// BaseInterval forever — the legacy behaviour, useful as an
	// emergency unblock toggle).
	ConsecutiveEmptyThreshold int
}

// effectiveSleep applies the JitterFraction as full-jitter on the base
// sleep duration. 0.0 jitter ⇒ deterministic burn; 1.0 ⇒ uniform
// rand(0, base). Negative or >1 are clamped to keep the math safe.
func (w *Worker) effectiveSleep(base time.Duration) time.Duration {
	if base <= 0 {
		base = w.pollEvery
	}
	return jitterDuration(base, w.backoff.JitterFraction)
}

// jitterDuration adds full-jitter to a base duration: actual = rand(0, base).
// AWS-style full-jitter is the canonical exponential-backoff spread
// (https://aws.amazon.com/blogs/architecture/exponential-backoff-and-jitter/);
// on a saturated queue the spread prevents each Worker from burning the
// next full Backoff identical-interval sleeping in lockstep.
func jitterDuration(base time.Duration, jitter float64) time.Duration {
	if base <= 0 {
		return base
	}
	if jitter < 0 {
		jitter = 0
	} else if jitter > 1 {
		jitter = 1
	}
	delta := int64(float64(base) * jitter)
	if delta <= 0 {
		return base
	}
	return time.Duration(rand.Int63n(delta + 1))
}
