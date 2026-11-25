package client

import (
	"context"
	"testing"
	"time"
)

func TestBreakerIsPerModel(t *testing.T) {
	c := &Client{}

	breakerA := c.breakerFor("gemma:2b")
	breakerA.RecordFailure()
	breakerA.RecordFailure()
	breakerA.RecordFailure()

	if breakerA.AllowRequest() {
		t.Fatal("expected breaker for failed model to be open")
	}

	breakerB := c.breakerFor("gemma4:e2b")
	if !breakerB.AllowRequest() {
		t.Fatal("expected breaker for a different model to remain closed")
	}
}

func TestHalfOpenConcurrentRequestWaitsForProbe(t *testing.T) {
	cb := NewCircuitBreaker(1, time.Millisecond)
	if !cb.AllowRequest() {
		t.Fatal("closed breaker should admit initial request")
	}
	cb.RecordFailure()
	time.Sleep(5 * time.Millisecond)
	if !cb.AllowRequest() {
		t.Fatal("breaker should admit half-open probe after cooldown")
	}

	result := make(chan struct {
		allowed bool
		err     error
	}, 1)
	go func() {
		allowed, err := cb.AllowRequestContext(context.Background())
		result <- struct {
			allowed bool
			err     error
		}{allowed: allowed, err: err}
	}()

	select {
	case got := <-result:
		t.Fatalf("concurrent half-open request returned before probe completed: %+v", got)
	case <-time.After(20 * time.Millisecond):
	}

	cb.RecordSuccess()
	select {
	case got := <-result:
		if got.err != nil || !got.allowed {
			t.Fatalf("waiter after successful probe = %+v, want admitted", got)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter did not resume after probe completed")
	}
}

func TestCanceledHalfOpenProbeDoesNotReopenBreaker(t *testing.T) {
	cb := NewCircuitBreaker(1, time.Millisecond)
	if !cb.AllowRequest() {
		t.Fatal("closed breaker should admit initial request")
	}
	cb.RecordFailure()
	time.Sleep(5 * time.Millisecond)
	if !cb.AllowRequest() {
		t.Fatal("breaker should admit half-open probe after cooldown")
	}

	cb.RecordCancellation()
	cb.mu.Lock()
	state, probeInFlight, failureCount := cb.state, cb.probeInFlight, cb.failureCount
	cb.mu.Unlock()
	if state != "half-open" || probeInFlight || failureCount != 1 {
		t.Fatalf("after canceled probe state=%q probeInFlight=%v failures=%d; want half-open, released, unchanged", state, probeInFlight, failureCount)
	}
	if !cb.AllowRequest() {
		t.Fatal("breaker should admit a replacement probe after cancellation")
	}
}

// TestCircuitBreakerRecoversAfterCooldown pins the r17-r21 failure mode: the
// breaker was reported as permanently open while Ollama was healthy again. A
// failed model must allow exactly one half-open probe after its cooldown, and
// a successful probe must close it.
func TestCircuitBreakerRecoversAfterCooldown(t *testing.T) {
	cb := NewCircuitBreaker(3, 10*time.Millisecond)
	for i := 0; i < 3; i++ {
		if !cb.AllowRequest() {
			t.Fatalf("attempt %d: expected closed breaker to allow the request", i)
		}
		cb.RecordFailure()
	}
	if cb.AllowRequest() {
		t.Fatal("breaker must stay open before the cooldown expires")
	}
	time.Sleep(30 * time.Millisecond)
	if !cb.AllowRequest() {
		t.Fatal("breaker must allow one half-open probe after the cooldown")
	}
	if cb.AllowRequest() {
		t.Fatal("breaker must allow only one probe while the half-open request is in flight")
	}
	cb.RecordSuccess()
	if !cb.AllowRequest() {
		t.Fatal("breaker must close after a successful half-open probe")
	}
}
