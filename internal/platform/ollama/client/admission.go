package client

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	logger "github.com/Marcuss-ops/PipelineGen/internal/platform/logging"

	"go.uber.org/zap"
)

// Ollama runs a bounded number of requests against the resident model
// (OLLAMA_NUM_PARALLEL, 3 on this host). Everything that overshoots that
// ceiling does not run faster: it queues INSIDE the server, and because the
// queue is FIFO, the client-side pools that queued first make every later pool
// wait for them. Measured on the translation path: with translation, cue and
// NLP fan-outs each sized independently, `work/wall` collapsed towards 1.0 and
// the pools serialized against each other.
//
// The pools cannot see each other — they are built by different wiring bundles
// and never share a semaphore. The one place they all funnel through is the
// Ollama client, so the budget lives here: one limiter per Ollama endpoint,
// shared by every client instance pointing at it (the LLM client, the stock
// enrichment client, the creator client, ...). Each pool keeps its own width
// (that is a scheduling choice about its own work); this is the hard ceiling on
// requests actually outstanding against the server.
const (
	// DefaultMaxInFlightRequests mirrors the certified OLLAMA_NUM_PARALLEL.
	// It is only a fallback: the value is resolved from the environment when
	// the operator tells us what the server was started with.
	DefaultMaxInFlightRequests = 3

	// maxAdmissionLimit bounds an operator override so a typo cannot turn the
	// budget into "unbounded" (which is the bug this code exists to prevent).
	maxAdmissionLimit = 64

	// admissionLimitEnvVar is the explicit operator knob.
	admissionLimitEnvVar = "VELOX_OLLAMA_MAX_INFLIGHT"

	// serverParallelEnvVar is the server-side knob (systemd drop-in for
	// ollama.service). Reading it keeps the client aligned with the server
	// without a second place to edit.
	serverParallelEnvVar = "OLLAMA_NUM_PARALLEL"
)

// AdmissionStats is a snapshot of one endpoint's admission limiter. Peak and
// Deferred are the evidence that the budget is actually binding: Peak must
// never exceed Limit.
type AdmissionStats struct {
	Endpoint string `json:"endpoint"`
	Limit    int    `json:"limit"`
	InFlight int64  `json:"in_flight"`
	Peak     int64  `json:"peak"`
	Deferred int64  `json:"deferred"`
	Granted  int64  `json:"granted"`
}

// ollamaAdmission is the shared limiter for one Ollama endpoint.
type ollamaAdmission struct {
	endpoint string
	limit    int
	slots    chan struct{}

	inFlight atomic.Int64
	peak     atomic.Int64
	deferred atomic.Int64
	granted  atomic.Int64
	observer atomic.Pointer[admissionObserverBox]
}

// admissionLimiters is keyed by normalized endpoint so two Client instances
// built by different wiring bundles for the same Ollama URL share one budget.
var (
	admissionMu       sync.Mutex
	admissionLimiters = map[string]*ollamaAdmission{}
)

// normalizeOllamaEndpoint makes the limiter key insensitive to the trailing
// slash and host casing that appear across config files.
func normalizeOllamaEndpoint(baseURL string) string {
	normalized := strings.TrimSpace(baseURL)
	normalized = strings.TrimRight(normalized, "/")
	if normalized == "" {
		return "http://localhost:11434"
	}
	return strings.ToLower(normalized)
}

// String returns the limiter's normalized endpoint.
func (a *ollamaAdmission) String() string {
	if a == nil {
		return ""
	}
	return a.endpoint
}

// Limit returns the configured maximum number of in-flight requests.
func (a *ollamaAdmission) Limit() int {
	if a == nil {
		return 0
	}
	return a.limit
}

// stats snapshots the limiter counters.
func (a *ollamaAdmission) stats() AdmissionStats {
	if a == nil {
		return AdmissionStats{}
	}
	return AdmissionStats{
		Endpoint: a.endpoint,
		Limit:    a.limit,
		InFlight: a.inFlight.Load(),
		Peak:     a.peak.Load(),
		Deferred: a.deferred.Load(),
		Granted:  a.granted.Load(),
	}
}

// recordPeak publishes the running high-water mark of concurrent requests.
func (a *ollamaAdmission) recordPeak(value int64) {
	for {
		peak := a.peak.Load()
		if value <= peak {
			return
		}
		if a.peak.CompareAndSwap(peak, value) {
			return
		}
	}
}

// acquire takes one slot, blocking until the server has room. The returned
// flag reports whether the caller had to queue for its slot. It is
// cancellation-aware: a pool that is shutting down must not hold the budget
// hostage.
func (a *ollamaAdmission) acquire(ctx context.Context) (bool, error) {
	if a == nil {
		return false, nil
	}

	waited := false
	select {
	case a.slots <- struct{}{}:
		// Fast path: the server had room, no queueing at all.
	default:
		// The budget is saturated. Record it, because this counter is what
		// distinguishes "the server is the bottleneck" from "the pool is".
		waited = true
		a.deferred.Add(1)
		select {
		case a.slots <- struct{}{}:
		case <-ctx.Done():
			return false, fmt.Errorf("ollama admission wait cancelled: %w", ctx.Err())
		}
	}

	inFlight := a.inFlight.Add(1)
	a.recordPeak(inFlight)
	a.granted.Add(1)
	if waited {
		a.notifyAdmissionDeferred()
	}
	a.notifyAdmissionChanged()
	return waited, nil
}

// release returns the slot taken by acquire. Safe on a limiter that never
// granted one (zero-value clients built by tests), so callers can defer it
// unconditionally.
func (a *ollamaAdmission) release() {
	if a == nil {
		return
	}
	a.inFlight.Add(-1)
	<-a.slots
	a.notifyAdmissionChanged()
}

// resolveAdmissionLimit reads the operator override, then the server-side
// knob, then falls back to the certified default.
func resolveAdmissionLimit() int {
	if limit, ok := positiveIntFromEnv(admissionLimitEnvVar); ok {
		return limit
	}
	if limit, ok := positiveIntFromEnv(serverParallelEnvVar); ok {
		return limit
	}
	return DefaultMaxInFlightRequests
}

// positiveIntFromEnv parses a positive integer, clamped to a sane maximum.
// Zero, negative and unparsable values are rejected rather than silently
// meaning "unbounded" or "serialize everything".
func positiveIntFromEnv(name string) (int, bool) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0, false
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, false
	}
	if value > maxAdmissionLimit {
		value = maxAdmissionLimit
	}
	return value, true
}

// admissionFor returns the process-wide limiter for an Ollama endpoint,
// creating it on first use. The limit is resolved at creation time from the
// environment, which is stable for the process lifetime.
func admissionFor(baseURL string) *ollamaAdmission {
	endpoint := normalizeOllamaEndpoint(baseURL)

	admissionMu.Lock()
	defer admissionMu.Unlock()

	if existing, ok := admissionLimiters[endpoint]; ok {
		return existing
	}

	limiter := &ollamaAdmission{
		endpoint: endpoint,
		limit:    resolveAdmissionLimit(),
		slots:    make(chan struct{}, resolveAdmissionLimit()),
	}
	admissionLimiters[endpoint] = limiter
	return limiter
}

// acquireOllamaSlot takes one unit of the endpoint's in-flight budget. It is a
// no-op for the vLLM/NVIDIA backends, which own their own batching and live on
// a different endpoint.
func (c *Client) acquireOllamaSlot(ctx context.Context) error {
	if c == nil || c.admission == nil {
		return nil
	}
	if c.useVLLM || c.useNvidiaForLLM {
		return nil
	}
	waited, err := c.admission.acquire(ctx)
	if err != nil {
		return err
	}
	if waited {
		// One line per queued request, at debug level: this is the trace that
		// shows a pool was throttled by the shared budget instead of by its own
		// width.
		stats := c.admission.stats()
		logger.Debug("ollama admission budget saturated, request queued",
			zap.String("endpoint", stats.Endpoint),
			zap.Int("limit", stats.Limit),
			zap.Int64("deferred_total", stats.Deferred),
		)
	}
	return nil
}

// releaseOllamaSlot returns the unit taken by acquireOllamaSlot.
func (c *Client) releaseOllamaSlot() {
	if c == nil || c.admission == nil {
		return
	}
	if c.useVLLM || c.useNvidiaForLLM {
		return
	}
	c.admission.release()
}

// AdmissionStats reports this client's endpoint budget. It is the diagnostic
// that answers "was the server the bottleneck?" without guessing: Peak is the
// highest number of requests ever outstanding, Deferred counts the calls that
// had to queue for a slot.
func (c *Client) AdmissionStats() AdmissionStats {
	if c == nil || c.admission == nil {
		return AdmissionStats{}
	}
	return c.admission.stats()
}

// AdmissionLimit returns the resolved in-flight ceiling for this client's
// endpoint (0 when the client has no budget, e.g. a zero-value Client).
func (c *Client) AdmissionLimit() int {
	if c == nil || c.admission == nil {
		return 0
	}
	return c.admission.Limit()
}
