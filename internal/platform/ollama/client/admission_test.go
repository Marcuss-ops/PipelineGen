package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
)

// admissionProbe is an Ollama-shaped test server that records how many
// requests it is handling at the same time. The peak it observes is the
// property under test: the client budget must never let more requests reach
// the server than the configured limit.
type admissionProbe struct {
	server *httptest.Server

	inFlight atomic.Int64
	peak     atomic.Int64
	total    atomic.Int64

	// hold, when non-nil, blocks a handler until the channel is closed. It
	// makes queueing deterministic instead of timing-dependent.
	hold chan struct{}
	// failFirst makes exactly the first request fail with HTTP 500.
	failFirst atomic.Bool
}

func newAdmissionProbe(t *testing.T) *admissionProbe {
	t.Helper()

	probe := &admissionProbe{}
	probe.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inFlight := probe.inFlight.Add(1)
		for {
			peak := probe.peak.Load()
			if inFlight <= peak || probe.peak.CompareAndSwap(peak, inFlight) {
				break
			}
		}
		defer probe.inFlight.Add(-1)
		probe.total.Add(1)

		if probe.hold != nil {
			select {
			case <-probe.hold:
			case <-r.Context().Done():
				return
			}
		}

		if probe.failFirst.CompareAndSwap(true, false) {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/api/chat"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"message": map[string]any{"role": "assistant", "content": "ok"},
				"done":    true,
			})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"response": "ok",
				"done":     true,
			})
		}
	}))
	t.Cleanup(probe.server.Close)
	return probe
}

// TestAdmissionBoundsConcurrentRequests is the core contract: no matter how
// many pools call at once, the server never sees more than the budgeted number
// of simultaneous requests.
func TestAdmissionBoundsConcurrentRequests(t *testing.T) {
	t.Setenv(admissionLimitEnvVar, "3")

	probe := newAdmissionProbe(t)
	probe.hold = make(chan struct{})

	c := NewClient(probe.server.URL, "gemma4:e4b", 30)
	if got := c.AdmissionLimit(); got != 3 {
		t.Fatalf("AdmissionLimit() = %d, want 3", got)
	}

	const callers = 9
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.Chat(context.Background(), testMessages(), nil, nil)
			errs <- err
		}()
	}

	// Let the client saturate the budget, then verify it plateaued at the
	// limit instead of dribbling requests through one at a time.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && probe.inFlight.Load() < 3 {
		time.Sleep(2 * time.Millisecond)
	}
	if got := probe.inFlight.Load(); got != 3 {
		close(probe.hold)
		wg.Wait()
		t.Fatalf("in-flight requests = %d, want the budget of 3 to be saturated", got)
	}
	time.Sleep(50 * time.Millisecond)
	if got := probe.peak.Load(); got > 3 {
		close(probe.hold)
		wg.Wait()
		t.Fatalf("peak concurrent requests = %d, want <= 3", got)
	}

	close(probe.hold)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("chat call failed: %v", err)
		}
	}

	stats := c.AdmissionStats()
	if stats.Peak > int64(stats.Limit) {
		t.Fatalf("stats.Peak = %d exceeds stats.Limit = %d", stats.Peak, stats.Limit)
	}
	if stats.Deferred == 0 {
		t.Fatal("stats.Deferred = 0: the queueing that the budget creates was not recorded")
	}
	if stats.InFlight != 0 {
		t.Fatalf("stats.InFlight = %d after all calls returned, want 0", stats.InFlight)
	}
	if probe.peak.Load() != 3 {
		t.Fatalf("probe peak = %d, want 3 (the budget must still allow real concurrency)", probe.peak.Load())
	}
}

// TestAdmissionSharesBudgetAcrossClientsOnSameEndpoint pins the reason the
// limiter is keyed by endpoint: the translation, cue, NLP and materializer
// pools are built by different wiring bundles and each gets its own *Client.
// They must still share one ceiling.
func TestAdmissionSharesBudgetAcrossClientsOnSameEndpoint(t *testing.T) {
	t.Setenv(admissionLimitEnvVar, "2")

	probe := newAdmissionProbe(t)
	probe.hold = make(chan struct{})

	first := NewClient(probe.server.URL, "gemma4:e4b", 30)
	// A trailing slash is the same endpoint, not a new budget.
	second := NewClient(strings.TrimRight(probe.server.URL, "/")+"/", "gemma4:e4b", 30)

	const callers = 6
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		client := second
		if i%2 == 0 {
			client = first
		}
		wg.Add(1)
		go func(client *Client) {
			defer wg.Done()
			_, _ = client.Chat(context.Background(), testMessages(), nil, nil)
		}(client)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && probe.inFlight.Load() < 2 {
		time.Sleep(2 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	peak := probe.peak.Load()
	close(probe.hold)
	wg.Wait()

	if peak > 2 {
		t.Fatalf("peak = %d across two clients sharing one endpoint, want <= 2", peak)
	}
	if got := second.AdmissionLimit(); got != 2 {
		t.Fatalf("second client limit = %d, want the shared 2", got)
	}
}

// TestAdmissionReleasesSlotOnServerError guards the failure path: a 5xx must
// not leak a slot, or the budget would degrade to zero capacity over time.
func TestAdmissionReleasesSlotOnServerError(t *testing.T) {
	t.Setenv(admissionLimitEnvVar, "1")

	probe := newAdmissionProbe(t)
	probe.failFirst.Store(true)

	c := NewClient(probe.server.URL, "gemma4:e4b", 30)

	if _, err := c.Chat(context.Background(), testMessages(), nil, nil); err == nil {
		t.Fatal("first call: want an error from the 500 response")
	}
	if got := c.AdmissionStats().InFlight; got != 0 {
		t.Fatalf("in-flight after failure = %d, want 0 (slot leaked)", got)
	}
	if _, err := c.Chat(context.Background(), testMessages(), nil, nil); err != nil {
		t.Fatalf("second call after a failed one: %v (the slot was not released)", err)
	}
}

// TestAdmissionCancelledWaiterDoesNotConsumeSlot checks the shutdown path: a
// pool that cancels while queued must return an error and leave the budget
// intact for the callers that are still running.
func TestAdmissionCancelledWaiterDoesNotConsumeSlot(t *testing.T) {
	t.Setenv(admissionLimitEnvVar, "1")

	probe := newAdmissionProbe(t)
	probe.hold = make(chan struct{})

	c := NewClient(probe.server.URL, "gemma4:e4b", 30)

	busy := make(chan error, 1)
	go func() {
		_, err := c.Chat(context.Background(), testMessages(), nil, nil)
		busy <- err
	}()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && probe.inFlight.Load() < 1 {
		time.Sleep(2 * time.Millisecond)
	}

	ctx, cancel := context.WithCancel(context.Background())
	queued := make(chan error, 1)
	go func() {
		_, err := c.Chat(ctx, testMessages(), nil, nil)
		queued <- err
	}()
	time.Sleep(30 * time.Millisecond)
	cancel()

	if err := <-queued; err == nil || !strings.Contains(err.Error(), "admission wait") {
		t.Fatalf("cancelled waiter error = %v, want an admission wait cancellation", err)
	}

	close(probe.hold)
	if err := <-busy; err != nil {
		t.Fatalf("first call: %v", err)
	}

	if got := c.AdmissionStats().InFlight; got != 0 {
		t.Fatalf("in-flight = %d after both callers finished, want 0", got)
	}
	// The budget must still have capacity: a leaked slot would hang here.
	done := make(chan error, 1)
	go func() {
		_, err := c.Chat(context.Background(), testMessages(), nil, nil)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("call after a cancelled waiter: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("call after a cancelled waiter blocked: the cancelled waiter consumed the only slot")
	}
}

// TestAdmissionBypassesRemoteBackends documents that vLLM and NVIDIA own their
// own batching on a different endpoint, so the Ollama budget must not gate
// them.
func TestAdmissionBypassesRemoteBackends(t *testing.T) {
	t.Setenv(admissionLimitEnvVar, "1")

	c := NewClient("http://127.0.0.1:1", "gemma4:e4b", 5)
	c.useVLLM = true

	if err := c.acquireOllamaSlot(context.Background()); err != nil {
		t.Fatalf("acquire with vLLM enabled: %v", err)
	}
	// A second acquire would block forever if the budget applied.
	acquired := make(chan error, 1)
	go func() { acquired <- c.acquireOllamaSlot(context.Background()) }()
	select {
	case err := <-acquired:
		if err != nil {
			t.Fatalf("second acquire with vLLM enabled: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("vLLM path was gated by the Ollama admission budget")
	}
	c.releaseOllamaSlot()
	c.releaseOllamaSlot()
	if got := c.AdmissionStats().InFlight; got != 0 {
		t.Fatalf("in-flight = %d for a bypassed backend, want 0", got)
	}
}

// TestAdmissionLimitResolution pins the precedence and the clamps: an explicit
// operator value wins, the server-side knob is the fallback, and nonsense can
// neither disable the budget nor serialize everything.
func TestAdmissionLimitResolution(t *testing.T) {
	tests := []struct {
		name     string
		explicit string
		server   string
		want     int
	}{
		{name: "default", want: DefaultMaxInFlightRequests},
		{name: "server parallel", server: "6", want: 6},
		{name: "explicit wins", explicit: "5", server: "6", want: 5},
		{name: "explicit one", explicit: "1", want: 1},
		{name: "explicit zero ignored", explicit: "0", server: "4", want: 4},
		{name: "explicit negative ignored", explicit: "-3", want: DefaultMaxInFlightRequests},
		{name: "explicit garbage ignored", explicit: "many", server: "2", want: 2},
		{name: "server garbage ignored", server: "auto", want: DefaultMaxInFlightRequests},
		{name: "clamped to ceiling", explicit: "9999", want: maxAdmissionLimit},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(admissionLimitEnvVar, tc.explicit)
			t.Setenv(serverParallelEnvVar, tc.server)
			if got := resolveAdmissionLimit(); got != tc.want {
				t.Fatalf("resolveAdmissionLimit() = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestAdmissionStatsReportEndpointKeepsDiagnosticsActionable ensures the
// evidence points at a real endpoint, so an operator can correlate the budget
// with the server it protects.
func TestAdmissionStatsReportEndpointKeepsDiagnosticsActionable(t *testing.T) {
	t.Setenv(admissionLimitEnvVar, "2")

	probe := newAdmissionProbe(t)
	c := NewClient(probe.server.URL+"/", "gemma4:e4b", 30)

	if _, err := c.Chat(context.Background(), testMessages(), nil, nil); err != nil {
		t.Fatalf("chat: %v", err)
	}

	stats := c.AdmissionStats()
	if stats.Endpoint == "" {
		t.Fatal("stats.Endpoint is empty")
	}
	if !strings.HasPrefix(stats.Endpoint, "http://127.0.0.1:") || strings.HasSuffix(stats.Endpoint, "/") {
		t.Fatalf("stats.Endpoint = %q, want a normalized host:port", stats.Endpoint)
	}
	if stats.Granted != 1 {
		t.Fatalf("stats.Granted = %d, want 1", stats.Granted)
	}
}

// TestAdmissionBoundsLegacyGeneratePath pins that the budget is not a chat-only
// detail: every legacy /api/generate caller (entity extraction, important
// phrases, batch extraction) draws from the same ceiling.
func TestAdmissionBoundsLegacyGeneratePath(t *testing.T) {
	t.Setenv(admissionLimitEnvVar, "2")

	probe := newAdmissionProbe(t)
	probe.hold = make(chan struct{})

	c := NewClient(probe.server.URL, "gemma4:e4b", 30)

	const callers = 6
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.GenerateWithOptions(context.Background(), "gemma4:e4b", "prompt", nil)
		}()
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && probe.inFlight.Load() < 2 {
		time.Sleep(2 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	peak := probe.peak.Load()
	close(probe.hold)
	wg.Wait()

	if peak > 2 {
		t.Fatalf("peak = %d on the legacy generate path, want <= 2", peak)
	}
	if got := c.AdmissionStats().InFlight; got != 0 {
		t.Fatalf("in-flight = %d after all calls returned, want 0", got)
	}
}

// TestAdmissionSharedWithEmbedClient covers the second Client instance the
// wiring builds for the same server: embeddings and generation must not be
// able to add up past the ceiling.
func TestAdmissionSharedWithEmbedClient(t *testing.T) {
	t.Setenv(admissionLimitEnvVar, "1")

	probe := newAdmissionProbe(t)
	probe.hold = make(chan struct{})

	sameServerEmbedClient := NewClient(probe.server.URL, "nomic-embed-text", 30)

	embedDone := make(chan error, 1)
	go func() {
		_, err := sameServerEmbedClient.Embed(context.Background(), "text")
		embedDone <- err
	}()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && probe.inFlight.Load() < 1 {
		time.Sleep(2 * time.Millisecond)
	}
	if got := probe.inFlight.Load(); got != 1 {
		close(probe.hold)
		t.Fatalf("in-flight = %d, want the embedding call to hold the only slot", got)
	}

	// A chat call from a different Client for the same endpoint must queue,
	// not arrive at the server alongside the embedding.
	chatStarted := make(chan struct{})
	go func() {
		close(chatStarted)
		_, _ = NewClient(probe.server.URL, "gemma4:e4b", 30).Chat(context.Background(), testMessages(), nil, nil)
	}()
	<-chatStarted
	time.Sleep(50 * time.Millisecond)

	if got := probe.inFlight.Load(); got != 1 {
		close(probe.hold)
		t.Fatalf("in-flight = %d, want 1 while the chat call queues behind the embed", got)
	}

	close(probe.hold)
	if err := <-embedDone; err != nil {
		t.Fatalf("embed: %v", err)
	}
}

// TestAdmissionUnconfiguredClientIsNilSafe keeps the zero-value Client usable
// in unit tests: no budget configured means no gating and no panic.
func TestAdmissionUnconfiguredClientIsNilSafe(t *testing.T) {
	c := &Client{model: "gemma4:e4b"}

	if err := c.acquireOllamaSlot(context.Background()); err != nil {
		t.Fatalf("acquire on a client without a budget: %v", err)
	}
	c.releaseOllamaSlot()

	if got := c.AdmissionLimit(); got != 0 {
		t.Fatalf("AdmissionLimit() = %d, want 0 for an unconfigured client", got)
	}
	if stats := c.AdmissionStats(); stats.Limit != 0 || stats.Peak != 0 {
		t.Fatalf("stats = %+v, want a zero record", stats)
	}
}

// testMessages is the canonical one-message payload for these tests.
func testMessages() []types.Message {
	return []types.Message{{Role: "user", Content: "translate"}}
}
