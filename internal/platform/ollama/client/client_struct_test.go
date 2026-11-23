package client

import (
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
