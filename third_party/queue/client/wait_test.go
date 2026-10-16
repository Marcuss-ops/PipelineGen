package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// waitServer serves a job-status long poll that answers with `state` for any
// known job and 404 otherwise.
func waitServer(t *testing.T, state State, withWaitRoute bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	job := func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Job{ID: "job-1", State: state})
	}
	if withWaitRoute {
		mux.HandleFunc("GET /jobs/{id}/wait", func(w http.ResponseWriter, r *http.Request) {
			if r.PathValue("id") == "missing" {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			job(w)
		})
	}
	mux.HandleFunc("GET /jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") == "missing" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		job(w)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func TestWaitTerminalUsesEventRoute(t *testing.T) {
	ts := waitServer(t, StateCompleted, true)
	c := New(ts.URL)

	start := time.Now()
	job, err := c.WaitTerminal(context.Background(), "job-1")
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if job.State != StateCompleted {
		t.Fatalf("state = %q, want completed", job.State)
	}
	if elapsed := time.Since(start); elapsed > 1*time.Second {
		t.Fatalf("event-driven wait took %s; expected an immediate return", elapsed)
	}
}

func TestWaitTerminalFallsBackWhenRouteAbsent(t *testing.T) {
	// An older queue server has no /jobs/{id}/wait route: the client must
	// detect it and degrade to the polling Wait loop instead of failing.
	ts := waitServer(t, StateCompleted, false)
	c := New(ts.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	job, err := c.WaitTerminal(ctx, "job-1")
	if err != nil {
		t.Fatalf("fallback wait: %v", err)
	}
	if job.State != StateCompleted {
		t.Fatalf("state = %q, want completed", job.State)
	}
}

func TestWaitTerminalFallsBackAfterTransientWaitFailure(t *testing.T) {
	var gets int
	mux := http.NewServeMux()
	mux.HandleFunc("GET /jobs/job-1/wait", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
	})
	mux.HandleFunc("GET /jobs/job-1", func(w http.ResponseWriter, _ *http.Request) {
		gets++
		state := StateRunning
		if gets > 1 {
			state = StateCompleted
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Job{ID: "job-1", State: state})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	job, err := New(ts.URL).WaitTerminal(ctx, "job-1")
	if err != nil {
		t.Fatalf("wait after transient long-poll failure: %v", err)
	}
	if job.State != StateCompleted {
		t.Fatalf("state = %q, want completed", job.State)
	}
	if gets < 2 {
		t.Fatalf("status fallback reads = %d, want at least 2", gets)
	}
}

func TestWaitTerminalUnknownJobIsNotFound(t *testing.T) {
	ts := waitServer(t, StateCompleted, true)
	c := New(ts.URL)

	if _, err := c.WaitTerminal(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestWaitTerminalHonorsContext(t *testing.T) {
	ts := waitServer(t, StateRunning, true)
	c := New(ts.URL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.WaitTerminal(ctx, "job-1"); err == nil {
		t.Fatal("cancelled context must surface as an error")
	}
}

func TestIsTerminalStateExcludesRendered(t *testing.T) {
	if IsTerminalState(StateRendered) {
		t.Fatal("rendered is not terminal: publication is still pending")
	}
	for _, state := range []State{StateCompleted, StateFailed, StateCancelled} {
		if !IsTerminalState(state) {
			t.Fatalf("%s must be terminal", state)
		}
	}
}
