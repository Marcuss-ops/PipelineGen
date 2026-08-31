package scheduling

import "time"

// BackoffConfig controls the worker polling backoff state machine.
type BackoffConfig struct {
	MaxBackoff                time.Duration
	JitterFraction            float64
	ConsecutiveEmptyThreshold int
}

// PollingState is the pure state of one worker's polling machine. It contains
// no persistence, logging, metrics, timers, or worker references.
type PollingState struct {
	CurrentBackoff   time.Duration
	ConsecutiveEmpty int
}

// NewPollingState starts a polling machine at the configured base interval.
func NewPollingState(base time.Duration) PollingState {
	return PollingState{CurrentBackoff: base}
}

// RecordEmpty advances the state after an empty claim. It returns whether the
// backoff was escalated; callers own logging and metrics for that transition.
func (s PollingState) RecordEmpty(base time.Duration, config BackoffConfig) (PollingState, bool) {
	if base <= 0 {
		base = s.CurrentBackoff
	}
	if s.CurrentBackoff <= 0 {
		s.CurrentBackoff = base
	}
	s.ConsecutiveEmpty++
	if config.ConsecutiveEmptyThreshold <= 0 || s.ConsecutiveEmpty <= config.ConsecutiveEmptyThreshold {
		return s, false
	}

	previous := s.CurrentBackoff
	next := previous * 2
	if config.MaxBackoff > 0 && next > config.MaxBackoff {
		next = config.MaxBackoff
	}
	if next <= previous {
		return s, false
	}
	s.CurrentBackoff = next
	return s, true
}

// ResetAfterClaim restores the base cadence after a successful claim.
func (s PollingState) ResetAfterClaim(base time.Duration) PollingState {
	return PollingState{CurrentBackoff: base}
}
