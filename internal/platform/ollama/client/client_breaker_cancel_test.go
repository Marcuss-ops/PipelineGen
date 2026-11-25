package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
)

func TestCanceledHalfOpenChatReleasesProbeWithoutModelFailure(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer func() {
		close(release)
		server.Close()
	}()

	c := NewClient(server.URL, "gemma4:e2b", 5)
	breaker := c.breakerFor("gemma4:e2b")
	for i := 0; i < types.CircuitBreakerFailures; i++ {
		breaker.RecordFailure()
	}
	breaker.mu.Lock()
	breaker.timeout = time.Millisecond
	breaker.mu.Unlock()
	time.Sleep(5 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := c.ChatOnce(ctx, []types.Message{{Role: "user", Content: "probe"}}, nil, nil)
		result <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("half-open probe did not reach Ollama")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ChatOnce error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ChatOnce did not return after caller cancellation")
	}
	breaker.mu.Lock()
	state, inFlight, failures := breaker.state, breaker.probeInFlight, breaker.failureCount
	breaker.mu.Unlock()
	if state != "half-open" || inFlight || failures != types.CircuitBreakerFailures {
		t.Fatalf("after canceled probe state=%q inFlight=%v failures=%d; want half-open, released, unchanged", state, inFlight, failures)
	}
}
