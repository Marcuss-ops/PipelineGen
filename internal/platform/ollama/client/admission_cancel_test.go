package client

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
)

// TestAdmissionCanceledWaitIsTyped pins the queue fact: a request that never
// reached the model gets ErrAdmissionCanceled AND keeps the context cause, so
// the caller can retry it as a deadline while the breaker stays untouched.
func TestAdmissionCanceledWaitIsTyped(t *testing.T) {
	t.Parallel()

	limiter := admissionFor("http://admission-typed.test:11434")
	for i := 0; i < limiter.Limit(); i++ {
		if _, err := limiter.acquire(context.Background()); err != nil {
			t.Fatalf("fill slot %d: %v", i, err)
		}
	}
	defer func() {
		for i := 0; i < limiter.Limit(); i++ {
			limiter.release()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := limiter.acquire(ctx)
	if err == nil {
		limiter.release()
		t.Fatal("a saturated budget must not grant a slot once the context is dead")
	}
	if !errors.Is(err, ErrAdmissionCanceled) {
		t.Fatalf("err = %v, want ErrAdmissionCanceled", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, must keep the context cause", err)
	}
}

// TestSaturatedAdmissionDoesNotOpenTheModelBreaker is the regression pin for
// the r24-r26 cascade: three saturated admission waits opened the model's
// breaker, and the next calls failed with "all models skipped by circuit
// breaker" although no model was ever asked.
func TestSaturatedAdmissionDoesNotOpenTheModelBreaker(t *testing.T) {
	t.Parallel()

	const baseURL = "http://admission-breaker.test:11434"
	c := NewClient(baseURL, "admission-breaker-model", 1)
	limiter := c.admission
	if limiter == nil {
		t.Fatal("a real client must own its endpoint's admission limiter")
	}
	for i := 0; i < limiter.Limit(); i++ {
		if _, err := limiter.acquire(context.Background()); err != nil {
			t.Fatalf("fill slot %d: %v", i, err)
		}
	}
	defer func() {
		for i := 0; i < limiter.Limit(); i++ {
			limiter.release()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := c.ChatOnce(ctx, []types.Message{{Role: "user", Content: "ciao"}}, nil, nil)
	if !errors.Is(err, ErrAdmissionCanceled) {
		t.Fatalf("ChatOnce err = %v, want ErrAdmissionCanceled (the request must never reach the model)", err)
	}

	breaker := c.breakerFor("admission-breaker-model")
	breaker.mu.Lock()
	failures := breaker.failureCount
	breaker.mu.Unlock()
	if failures != 0 {
		t.Fatalf("breaker failures = %d, want 0: a queued request is not a model failure", failures)
	}
	if !breaker.AllowRequest() {
		t.Fatal("the model breaker must stay closed after a saturated admission wait")
	}
}
