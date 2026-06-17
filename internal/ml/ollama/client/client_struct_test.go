package client

import "testing"

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
