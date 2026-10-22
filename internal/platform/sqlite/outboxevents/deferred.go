package outboxevents

import (
	"errors"
	"fmt"
	"time"
)

// DeferredError means the event is valid but its downstream precondition is
// not ready yet. Unlike a retryable failure, deferral does not consume an
// attempt or increment the transient-retry metric.
type DeferredError struct {
	Cause error
	Delay time.Duration
}

func (e *DeferredError) Error() string {
	if e == nil || e.Cause == nil {
		return "outbox event deferred"
	}
	return fmt.Sprintf("outbox event deferred: %v", e.Cause)
}

func (e *DeferredError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// NewDeferredError schedules a non-failure deferral. A non-positive delay
// uses the conservative one-minute default.
func NewDeferredError(cause error, delay time.Duration) error {
	if cause == nil {
		cause = errors.New("downstream precondition is not ready")
	}
	if delay <= 0 {
		delay = time.Minute
	}
	return &DeferredError{Cause: cause, Delay: delay}
}

func deferredDelay(err error) (time.Duration, bool) {
	var deferred *DeferredError
	if !errors.As(err, &deferred) || deferred == nil {
		return 0, false
	}
	if deferred.Delay <= 0 {
		return time.Minute, true
	}
	return deferred.Delay, true
}
