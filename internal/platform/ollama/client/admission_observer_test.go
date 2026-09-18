package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// recordingAdmissionObserver captures the samples the limiter publishes.
type recordingAdmissionObserver struct {
	mu       sync.Mutex
	changed  []admissionSample
	deferred int64
}

type admissionSample struct {
	endpoint string
	limit    int
	inFlight int64
}

func (r *recordingAdmissionObserver) AdmissionChanged(endpoint string, limit int, inFlight int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.changed = append(r.changed, admissionSample{endpoint: endpoint, limit: limit, inFlight: inFlight})
}

func (r *recordingAdmissionObserver) AdmissionDeferred(string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deferred++
}

func (r *recordingAdmissionObserver) samples() ([]admissionSample, int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]admissionSample(nil), r.changed...), r.deferred
}

// TestAdmissionObserverSeesCeilingAndInFlight pins the telemetry contract: the
// ceiling is published on wiring, the in-flight count tracks real admissions,
// and a queued request is counted as deferred.
func TestAdmissionObserverSeesCeilingAndInFlight(t *testing.T) {
	const budget = 2
	t.Setenv(admissionLimitEnvVar, "2")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"ok"},"done":true}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "gemma4:e4b", 30)
	observer := &recordingAdmissionObserver{}
	c.SetAdmissionObserver(observer)

	if !c.hasAdmissionObserver() {
		t.Fatal("hasAdmissionObserver = false after SetAdmissionObserver")
	}

	initial, deferredInitial := observer.samples()
	if len(initial) != 1 {
		t.Fatalf("samples after wiring = %d, want 1 (the ceiling is published immediately)", len(initial))
	}
	if initial[0].limit != budget || initial[0].inFlight != 0 {
		t.Fatalf("wiring sample = %+v, want limit=%d inFlight=0", initial[0], budget)
	}
	if initial[0].endpoint == "" {
		t.Fatal("wiring sample has an empty endpoint")
	}
	if deferredInitial != 0 {
		t.Fatalf("deferred after wiring = %d, want 0", deferredInitial)
	}

	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.Chat(context.Background(), testMessages(), nil, nil)
		}()
	}
	wg.Wait()

	samples, deferred := observer.samples()
	var peak int64
	for _, sample := range samples {
		if sample.inFlight > peak {
			peak = sample.inFlight
		}
		if sample.limit != budget {
			t.Fatalf("sample limit = %d, want %d on every sample", sample.limit, budget)
		}
	}
	if peak != budget {
		t.Fatalf("peak reported in-flight = %d, want %d", peak, budget)
	}
	if samples[len(samples)-1].inFlight != 0 {
		t.Fatalf("last sample in-flight = %d, want 0 (the budget must drain)", samples[len(samples)-1].inFlight)
	}
	if deferred == 0 {
		t.Fatal("deferred = 0: a 6-request fan-out against a 2-slot budget must report queueing")
	}
}

// TestAdmissionObserverCanBeDisabled keeps telemetry optional: clearing the
// observer must stop the samples without touching the budget's behavior.
func TestAdmissionObserverCanBeDisabled(t *testing.T) {
	t.Setenv(admissionLimitEnvVar, "1")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"ok"},"done":true}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "gemma4:e4b", 30)
	observer := &recordingAdmissionObserver{}
	c.SetAdmissionObserver(observer)
	c.SetAdmissionObserver(nil)

	if c.hasAdmissionObserver() {
		t.Fatal("hasAdmissionObserver = true after clearing the observer")
	}
	before, _ := observer.samples()

	if _, err := c.Chat(context.Background(), testMessages(), nil, nil); err != nil {
		t.Fatalf("Chat with telemetry disabled: %v", err)
	}

	after, deferred := observer.samples()
	if len(after) != len(before) || deferred != 0 {
		t.Fatalf("samples grew after clearing the observer: before=%d after=%d deferred=%d", len(before), len(after), deferred)
	}
	if got := c.AdmissionStats().Granted; got != 1 {
		t.Fatalf("Granted = %d, want 1: the budget must keep working without telemetry", got)
	}
}

// TestAdmissionObserverIsSharedWithSameEndpointClients documents that the
// observer installed on one client also covers the other clients of the same
// endpoint (the limiter is shared, the observer lives on the limiter).
func TestAdmissionObserverIsSharedWithSameEndpointClients(t *testing.T) {
	t.Setenv(admissionLimitEnvVar, "3")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"ok"},"done":true}`))
	}))
	defer server.Close()

	primary := NewClient(server.URL, "gemma4:e4b", 30)
	observer := &recordingAdmissionObserver{}
	primary.SetAdmissionObserver(observer)

	other := NewClient(server.URL, "nomic-embed-text", 30)
	if !other.hasAdmissionObserver() {
		t.Fatal("a second client for the same endpoint did not inherit the observer")
	}

	before, _ := observer.samples()
	if _, err := other.Chat(context.Background(), testMessages(), nil, nil); err != nil {
		t.Fatalf("Chat on the second client: %v", err)
	}
	after, _ := observer.samples()
	if len(after) <= len(before) {
		t.Fatalf("samples did not grow for the second client: before=%d after=%d", len(before), len(after))
	}

	var last atomic.Int64
	last.Store(after[len(after)-1].inFlight)
	if last.Load() != 0 {
		t.Fatalf("last sample in-flight = %d, want 0", last.Load())
	}
}
