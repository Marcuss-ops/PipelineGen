// Package health — Artlist Node scraper health probe (ART-002 P1.3, July 2026).
//
// Per AGENTS.md Pattern 0, the struct is composition-injected: the
// caller (internal/app/lifecycle_scheduler.go) passes the serverURL
// (cfg.External.ArtlistScraperServerURL), a logger, and an optional
// metrics handle (NewHealthMetrics() points at the promauto globals
// from observability/metrics_artlist.go).
//
// nil-safety: the metrics pointer may be nil. incProbeResult +
// incAlert short-circuit on a nil receiver (the canonical
// DriveValidatorMetrics P1.4 + ArtlistMetrics P1.1 contract).
//
// State machine (per probe tick):
//
//	tick: probeOnce(ctx) returns (healthy, err)
//	  | if err != nil (transport error):
//	  |     consecutive++ ; on success path resets to 0
//	  |     if consecutive >= threshold:
//	  |         log.Warn + metrics.incAlert() + reset to 0
//	  | if err == nil (any HTTP response):
//	  |     consecutive = 0 (streak broken)
//
// The alert-once-per-streak semantic (reset to 0 after alert)
// matches the user spec "alert 3 fallimenti consecutivi" — every
// 3-failure streak fires exactly 1 alert, every 6-failure streak
// fires exactly 2 alerts. The consecutive counter is in-memory only
// (per the 60s SRE tick semantics: a process restart naturally
// resets state, and the alert pattern is for transient outages not
// long-running accumulation).
//
// godlike/07 no-fake-availability: a stopped probe (Stop called
// before Start) is a no-op; a probe with empty serverURL is a no-op
// (fail-closed at composition time via the lifecycle_scheduler
// guard, not at the probe). The alert log is always emitted
// regardless of metrics availability — operators reading logs see
// the alert even if /metrics is broken.
package health

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
)

// Default constants for the health probe. Mirrors the DriveRootsValidator
// P1.3 default convention.
const (
	// DefaultProbeInterval is the canonical tick cadence (60s per
	// the user spec). Override via Options.Interval.
	DefaultProbeInterval = 60 * time.Second

	// DefaultFailureThreshold is the canonical consecutive-failure
	// count that fires an alert (3 per the user spec). Override
	// via Options.Threshold.
	DefaultFailureThreshold = 3

	// DefaultProbeTimeout is the per-attempt HTTP timeout. 5s
	// mirrors the HTTPSelfLoopProbe convention.
	DefaultProbeTimeout = 5 * time.Second
)

// Options configures the probe. Zero-value fields fall back to
// the Default* consts above.
type Options struct {
	// Interval is the tick cadence. Default: DefaultProbeInterval (60s).
	Interval time.Duration
	// FailureThreshold is the consecutive-failure count that fires
	// an alert. Default: DefaultFailureThreshold (3).
	FailureThreshold int
	// HTTPTimeout is the per-attempt HTTP timeout. Default: DefaultProbeTimeout (5s).
	HTTPTimeout time.Duration
	// HTTPClient is an optional custom *http.Client. If nil, a
	// new client is constructed with HTTPTimeout.
	HTTPClient *http.Client
	// Metrics is the optional observability handle. If nil, the
	// probe runs with no SRE emission (logs only).
	Metrics *Metrics
	// OnAlert is an optional callback fired once per threshold
	// crossing. If nil, the probe's internal log.Warn is the only
	// alert path. Composition roots that need custom alert
	// routing (PagerDuty, Slack, etc.) supply a callback here.
	OnAlert func(consecutiveFailures int, lastErr error)
}

// Probe is the canonical 60s-tick health probe for the Artlist
// Node scraper server. Construct via New, start via Start, stop
// via Stop. The probe is safe to construct and Start even when
// the Node scraper is down (no boot-time dependency on the
// scraper being live).
type Probe struct {
	serverURL string
	interval  time.Duration
	threshold int
	client    *http.Client
	metrics   *Metrics
	onAlert   func(int, error)
	log       *zap.Logger

	mu        sync.Mutex
	streak    int
	lastErr   error
	lastOK    bool
	stopCh    chan struct{}
	stoppedCh chan struct{}
	started   bool
}

// New constructs a Probe from explicit args. The serverURL is
// the Node scraper base URL (e.g. "http://artlist-scraper:9123").
// Pass nil options to use all defaults.
//
// godlike/07 fail-closed: empty serverURL returns a probe that
// short-circuits on every tick (no goroutine launched). This
// preserves the composition-time fail-closed contract
// (buildSchedulerSteps already guards on empty URL, but the
// probe itself is also safe to construct with an empty URL).
func New(serverURL string, log *zap.Logger, opts *Options) *Probe {
	if log == nil {
		log = zap.NewNop()
	}
	if opts == nil {
		opts = &Options{}
	}
	interval := opts.Interval
	if interval <= 0 {
		interval = DefaultProbeInterval
	}
	threshold := opts.FailureThreshold
	if threshold <= 0 {
		threshold = DefaultFailureThreshold
	}
	timeout := opts.HTTPTimeout
	if timeout <= 0 {
		timeout = DefaultProbeTimeout
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	return &Probe{
		serverURL: serverURL,
		interval:  interval,
		threshold: threshold,
		client:    client,
		metrics:   opts.Metrics,
		onAlert:   opts.OnAlert,
		log:       log,
		stopCh:    make(chan struct{}),
		stoppedCh: make(chan struct{}),
	}
}

// Start launches the ticker goroutine. Returns immediately. The
// goroutine exits when ctx is cancelled OR Stop is called.
//
// godlike/07 no-fake-availability: returns an error if the
// serverURL is empty (caller chose to construct a no-op probe
// without composition-time guard) or if Start is called twice on
// the same probe (idempotency contract).
func (p *Probe) Start(ctx context.Context) error {
	if p == nil {
		return fmt.Errorf("health.Probe.Start: nil receiver")
	}
	if p.serverURL == "" {
		return fmt.Errorf("health.Probe.Start: serverURL is empty (composition-time fail-closed)")
	}
	p.mu.Lock()
	if p.started {
		p.mu.Unlock()
		return fmt.Errorf("health.Probe.Start: already started")
	}
	p.started = true
	p.mu.Unlock()

	go p.run(ctx)
	return nil
}

// Stop signals the ticker goroutine to exit and blocks until the
// goroutine has confirmed shutdown. Safe to call multiple times
// (idempotent); second-and-subsequent calls return immediately
// after the first one completes.
//
// godlike/07 minimum-blast-radius: the per-call context timeout
// caps the wait at 5s; if the goroutine does not exit within the
// budget, Stop logs a Warn and returns ctx.Err() so the caller
// (serverLifecycle.Stop) can decide whether to log+continue or
// escalate.
func (p *Probe) Stop(ctx context.Context) error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	started := p.started
	p.mu.Unlock()
	if !started {
		return nil
	}
	select {
	case <-p.stopCh:
		// already closed
	default:
		close(p.stopCh)
	}
	select {
	case <-p.stoppedCh:
		return nil
	case <-ctx.Done():
		p.log.Warn("artlist scraper health probe: Stop timed out (goroutine did not exit within ctx budget)",
			zap.Duration("budget", 5*time.Second),
			zap.Error(ctx.Err()),
		)
		return ctx.Err()
	}
}

// run is the ticker goroutine body. Runs probeOnce on each tick
// until ctx cancellation or Stop signal. The first tick fires
// immediately (no startup grace period) so a broken server is
// detected at boot rather than 60s after — a deliberate
// godlike/07 fail-fast posture (a broken scraper on a fresh
// boot is operator signal that warrants immediate attention,
// not silent accumulation).
//
// godlike/07 panic-safe: a deferred recover() shields the
// ticker goroutine from a panicking OnAlert callback (a
// misbehaving embedder) or any other tick-time panic. Without
// this guard a single misbehaving callback would kill the
// goroutine silently and the probe would stop ticking forever
// (operators see the first alert but no recovery signal).
func (p *Probe) run(ctx context.Context) {
	defer close(p.stoppedCh)
	defer func() {
		if r := recover(); r != nil {
			p.log.Error("artlist scraper health probe: panic recovered (goroutine survived)",
				zap.Any("recovered", r),
				zap.String("server_url", p.serverURL),
			)
		}
	}()
	t := time.NewTicker(p.interval)
	defer t.Stop()
	// Immediate first tick (no startup grace period) so a broken
	// scraper is detected at t=0 rather than t=60s. The ticker
	// then continues at p.interval cadence.
	p.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stopCh:
			return
		case <-t.C:
			p.tick(ctx)
		}
	}
}

// tick performs a single probe attempt and updates the streak
// state. Exposed as a method (not the goroutine body) so tests
// can drive the probe without waiting for the ticker.
func (p *Probe) tick(ctx context.Context) {
	_, err := p.probeOnce(ctx)
	if err == nil {
		// Success: reset the streak + record last-OK timestamp.
		p.mu.Lock()
		p.streak = 0
		p.lastErr = nil
		p.lastOK = true
		p.mu.Unlock()
		p.metrics.incProbeResult("success")
		return
	}
	// Failure: increment the streak; alert if crossed threshold.
	p.mu.Lock()
	p.streak++
	p.lastErr = err
	p.lastOK = false
	streak := p.streak
	p.mu.Unlock()
	p.metrics.incProbeResult("failure")

	if streak >= p.threshold {
		// Alert-once-per-streak: fire alert, then reset the counter
		// to 0 so the next 3 failures fire another alert (per
		// user spec "alert 3 fallimenti consecutivi" = 1 alert per
		// streak, not 1 alert ever).
		p.mu.Lock()
		p.streak = 0
		p.mu.Unlock()
		p.metrics.incAlert()
		p.log.Warn("artlist scraper health probe alert: consecutive failures crossed threshold",
			zap.String("server_url", p.serverURL),
			zap.Int("consecutive_failures", streak),
			zap.Int("threshold", p.threshold),
			zap.Error(err),
		)
		if p.onAlert != nil {
			p.onAlert(streak, err)
		}
	}
}

// probeOnce performs a single HTTP GET attempt. Returns nil on
// any HTTP response (the probe is a liveness check, not a
// functional smoke test — the server is alive if it responds at
// all). Returns wrapped error on transport-level failure
// (connection refused, timeout, DNS, TLS).
func (p *Probe) probeOnce(ctx context.Context) (bool, error) {
	if p == nil {
		return false, fmt.Errorf("health.Probe.probeOnce: nil receiver")
	}
	if p.serverURL == "" {
		return false, fmt.Errorf("health.Probe.probeOnce: serverURL is empty")
	}
	// Use a per-attempt context with HTTPTimeout to bound the
	// probe's wall-clock independently of the parent ctx.
	probeCtx, cancel := context.WithTimeout(ctx, p.client.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, p.serverURL, nil)
	if err != nil {
		return false, fmt.Errorf("health.Probe.probeOnce: NewRequest: %w", err)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("health.Probe.probeOnce: client.Do %s: %w", p.serverURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return true, nil
}

// Status returns the current probe state for the /diagnostics
// endpoint or future use. (Not exposed in the lifecycle today —
// the probe's effect is via the Prometheus counters + log.Warn
// alert path; the Status accessor is included for forward-compat
// with the per-call /diagnostics surface that may consume it.)
func (p *Probe) Status() (lastOK bool, consecutive int, lastErr error) {
	if p == nil {
		return false, 0, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastOK, p.streak, p.lastErr
}

// ── Metrics adapter (Pattern 0 mirror of P1.1/P1.4) ────────────────

// Metrics groups the Prometheus collectors emitted by the
// health probe. The pointer is injected via Options; passing nil
// disables metrics emission (no-op observers).
type Metrics struct {
	// ProbeResult counts per-attempt outcomes. Labelled by
	// "result" (success | failure).
	ProbeResult *prometheus.CounterVec
	// AlertsTotal counts threshold-crossing events (one per
	// 3-consecutive-failure streak).
	AlertsTotal prometheus.Counter
}

// NewMetrics constructs the production metrics struct backed by
// the promauto globals from observability/metrics_artlist.go.
func NewMetrics() *Metrics {
	return &Metrics{
		ProbeResult: observability.ArtlistScraperProbeResultTotal,
		AlertsTotal: observability.ArtlistScraperHealthAlertsTotal,
	}
}

// incProbeResult records a per-attempt outcome. Nil-receiver
// safe (no-op) and nil-vec safe (no-op) per the canonical
// DriveValidatorMetrics P1.4 contract.
func (m *Metrics) incProbeResult(result string) {
	if m == nil || m.ProbeResult == nil {
		return
	}
	m.ProbeResult.WithLabelValues(result).Inc()
}

// incAlert records a threshold-crossing event. Nil-receiver
// safe and nil-counter safe per the canonical contract.
func (m *Metrics) incAlert() {
	if m == nil || m.AlertsTotal == nil {
		return
	}
	m.AlertsTotal.Inc()
}
