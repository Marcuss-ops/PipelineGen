// Package health — Probe TDD tests (ART-002 P1.3, July 2026).
//
// 3 canonical-surface tests for the Artlist Node scraper health
// probe. The test surface is the Probe.tick() method (called per
// ticker fire) — integration tests for the goroutine lifecycle
// are deferred to a follow-up PR (the canonical pattern is to
// drive the tick directly + assert on the post-tick state).
//
// Test isolation discipline: each test constructs a fresh
// httptest.NewServer (or uses a deliberately-closed port for the
// transport-error case) + a fresh Metrics struct backed by
// private prometheus.NewCounterVec instances (NOT promauto), so
// the test never registers against prometheus.DefaultRegisterer.
// The same discipline is used in
// internal/platform/observability/metrics_adapter_test.go
// (FASE 3.7 Commit 2 canonical precedent) + P1.1
// downloader_metrics_test.go.
package health

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.uber.org/zap"
)

// newTestMetrics returns a fresh Metrics struct backed by
// private (non-promauto) CounterVec/Counter instances. The struct
// is wired into the probe via Options.Metrics.
func newTestMetrics() (*Metrics, *prometheus.CounterVec, prometheus.Counter) {
	cv := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "test_artlist_scraper_probe_total",
		Help: "test-only counter",
	}, []string{"result"})
	c := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "test_artlist_scraper_health_alerts_total",
		Help: "test-only counter",
	})
	return &Metrics{ProbeResult: cv, AlertsTotal: c}, cv, c
}

// TestProbe_Healthy pins the happy-path: when the Node scraper
// returns any HTTP response (even 4xx/5xx — the probe is a
// liveness check, not a functional smoke test), the streak stays
// at 0 and no alert fires.
func TestProbe_Healthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	metrics, cv, alerts := newTestMetrics()
	alertCount := int32(0)
	p := New(srv.URL, zap.NewNop(), &Options{
		Metrics: metrics,
		OnAlert: func(_ int, _ error) { atomic.AddInt32(&alertCount, 1) },
	})

	// Drive 5 ticks to simulate 5 minutes of probe activity.
	for i := 0; i < 5; i++ {
		p.tick(context.Background())
	}

	if got := testutil.ToFloat64(cv.WithLabelValues("success")); got != 5 {
		t.Errorf("success count: want 5, got %v", got)
	}
	if got := testutil.ToFloat64(cv.WithLabelValues("failure")); got != 0 {
		t.Errorf("failure count: want 0, got %v", got)
	}
	if got := testutil.ToFloat64(alerts); got != 0 {
		t.Errorf("alerts count: want 0, got %v", got)
	}
	if got := atomic.LoadInt32(&alertCount); got != 0 {
		t.Errorf("OnAlert callback fired %d times, want 0", got)
	}
	lastOK, streak, _ := p.Status()
	if !lastOK {
		t.Errorf("Status.lastOK: want true, got false")
	}
	if streak != 0 {
		t.Errorf("Status.streak: want 0, got %d", streak)
	}
}

// TestProbe_TransportError_IncrementsStreak pins the failure
// state machine: when the Node scraper is unreachable (transport
// error), the streak counter increments by 1 per tick. No alert
// fires until the streak reaches the threshold (covered in
// TestProbe_ThreeConsecutiveFailures_TriggersAlert).
func TestProbe_TransportError_IncrementsStreak(t *testing.T) {
	// Reserve a port, immediately close it, use it for the probe
	// so the connection always refused. Lighter than
	// httptest.NewServer + srv.Close() (which can leave a
	// TIME_WAIT socket that a quick reconnect might still hit).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	url := "http://127.0.0.1:" + strconv.Itoa(port)

	metrics, cv, alerts := newTestMetrics()
	p := New(url, zap.NewNop(), &Options{
		Metrics:     metrics,
		HTTPTimeout: 500 * time.Millisecond, // bound the test
	})

	// 2 ticks = 2 transport errors, streak 2, no alert yet.
	p.tick(context.Background())
	p.tick(context.Background())

	if got := testutil.ToFloat64(cv.WithLabelValues("success")); got != 0 {
		t.Errorf("success count: want 0, got %v", got)
	}
	if got := testutil.ToFloat64(cv.WithLabelValues("failure")); got != 2 {
		t.Errorf("failure count: want 2, got %v", got)
	}
	if got := testutil.ToFloat64(alerts); got != 0 {
		t.Errorf("alerts count: want 0 (streak 2 < threshold 3), got %v", got)
	}
	_, streak, lastErr := p.Status()
	if streak != 2 {
		t.Errorf("Status.streak: want 2, got %d", streak)
	}
	if lastErr == nil {
		t.Errorf("Status.lastErr: want non-nil transport error, got nil")
	}
}

// TestProbe_ThreeConsecutiveFailures_TriggersAlert pins the
// alert-once-per-streak contract: 3 consecutive transport errors
// fires exactly 1 alert (the canonical user spec "alert 3
// fallimenti consecutivi"). The streak counter resets to 0 after
// the alert, so the NEXT 3 failures fire another alert (verified
// in the same test for forward-compat coverage).
func TestProbe_ThreeConsecutiveFailures_TriggersAlert(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	url := "http://127.0.0.1:" + strconv.Itoa(port)

	metrics, cv, alerts := newTestMetrics()
	alertCount := int32(0)
	lastAlertStreak := int32(0)
	p := New(url, zap.NewNop(), &Options{
		Metrics:     metrics,
		HTTPTimeout: 500 * time.Millisecond,
		OnAlert: func(streak int, _ error) {
			atomic.AddInt32(&alertCount, 1)
			atomic.StoreInt32(&lastAlertStreak, int32(streak))
		},
	})

	// 3 ticks = 3 transport errors = 1 alert.
	p.tick(context.Background())
	p.tick(context.Background())
	p.tick(context.Background())

	if got := testutil.ToFloat64(cv.WithLabelValues("failure")); got != 3 {
		t.Errorf("failure count after first streak: want 3, got %v", got)
	}
	if got := testutil.ToFloat64(alerts); got != 1 {
		t.Errorf("alerts count after first streak: want 1, got %v", got)
	}
	if got := atomic.LoadInt32(&alertCount); got != 1 {
		t.Errorf("OnAlert callback count: want 1, got %d", got)
	}
	if got := atomic.LoadInt32(&lastAlertStreak); got != 3 {
		t.Errorf("OnAlert streak: want 3, got %d", got)
	}

	// After the alert, streak resets to 0. 3 more failures = 1
	// more alert (alert-once-per-streak semantic).
	p.tick(context.Background())
	p.tick(context.Background())
	p.tick(context.Background())

	if got := testutil.ToFloat64(alerts); got != 2 {
		t.Errorf("alerts count after 2nd streak: want 2, got %v", got)
	}
	if got := atomic.LoadInt32(&alertCount); got != 2 {
		t.Errorf("OnAlert callback count after 2nd streak: want 2, got %d", got)
	}
	if got := testutil.ToFloat64(cv.WithLabelValues("failure")); got != 6 {
		t.Errorf("failure count after 2 streaks: want 6, got %v", got)
	}
	_, streak, _ := p.Status()
	if streak != 0 {
		t.Errorf("Status.streak after 2nd alert: want 0 (post-alert reset), got %d", streak)
	}
}
