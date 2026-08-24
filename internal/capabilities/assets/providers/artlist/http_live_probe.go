// Package artlist — http_live_probe.go: concrete *HTTPSelfLoopProbe
// adapter for the IsLiveProbe port (PR-ARTLIST-LIVE-WIRE, July 2026).
//
// godlike/06 SSOT: the composition root at internal/app/build_bundles_artlist.go
// constructs this adapter on-the-fly with 4 DIRECT args (baseURL, path,
// timeout, log) — no shim layer between the composition root and the
// concrete receiver (Pattern 0 + AGENTS.md §Pattern 4). Drift in the
// IsLiveProbe port signature surfaces as a build failure at the
// compile-time pin below rather than as a runtime panic on first dispatch.
//
// godlike/07 no-fake-availability: returns (false, nil) on non-2xx after
// timeout — caller decides retry policy; (false, wrapped err) on transport
// failure (DNS/TCP/timeout/connection-refused) to distinguish from
// semantic 4xx/5xx. Probe never caches across calls — compositional
// callers wrap with their own LRU if desired (godlike/07 audit-pinning).
package assets

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// HTTPSelfLoopProbe is the concrete adapter for the canonical IsLiveProbe
// port. It performs an HTTP GET against the configured baseURL + path
// ("/api/artlist/stats" by default at the composition root) with an
// explicit per-request timeout and reports liveness via the
// (bool, error) return contract.
//
// Self-loop semantics: at composition time, the WireArtlist function
// constructs this adapter with a baseURL pointing at the same PipelineGen
// server being booted (typically http://localhost:8080 from cfg or
// fallback). At runtime the probe is fired by handlers/diagnostics asking
// "are we live?" — by then the routes are registered and the handler at
// /api/artlist/stats replies with 2xx.
type HTTPSelfLoopProbe struct {
	baseURL string
	path    string
	client  *http.Client
	log     *zap.Logger
}

// NewHTTPSelfLoopProbe builds the concrete *HTTPSelfLoopProbe from
// explicit args (Pattern 0 + AGENTS.md §Pattern 4: no shim, no
// setter, single constructor entry-point). The caller (WireArtlist
// composition root) owns timeout-defaulting: if timeout <= 0 the
// adapter falls back to DefaultProbeTimeout (5s — mirrors
// internal/platform/delivery/startup_validator.go::ProbeTimeout
// per P1.4 closure).
//
// baseURL is the server root (e.g. http://localhost:8080 or
// https://api.example.com). path is "/api/artlist/stats" (canonical
// liveness endpoint per internal/api/assets/artlist/artlist_handlers.go::Stats).
// The probe concatenates baseURL + path internally at Probe time.
func NewHTTPSelfLoopProbe(baseURL string, path string, timeout time.Duration, log *zap.Logger) *HTTPSelfLoopProbe {
	if timeout <= 0 {
		timeout = DefaultProbeTimeout
	}
	return &HTTPSelfLoopProbe{
		baseURL: baseURL,
		path:    path,
		client: &http.Client{
			Timeout: timeout,
		},
		log: log,
	}
}

// DefaultProbeTimeout is the canonical fallback when the composition
// root passes timeout <= 0. 5s matches the precedent in
// internal/platform/delivery/startup_validator.go::ProbeTimeout
// (drive-side validator default per P1.4 closure).
const DefaultProbeTimeout = 5 * time.Second

// Probe implements the IsLiveProbe port:
//   - HTTP 2xx → (true, nil) — server is live
//   - HTTP 4xx/5xx → (false, nil) — server reachable but unhealthy
//     (caller decides retry policy; godlike/07 does NOT surface
//     transient details — diagnostic layer escalates)
//   - transport failure (DNS/TCP/timeout/connection-refused) →
//     (false, wrapped err) — caller decides retry policy
//
// Probe honors ctx cancellation: if ctx is cancelled before the HTTP
// roundtrip completes, the http.Request is short-circuited and the
// resulting error is wrapped with the underlying cause contextually
// preserved (context.Canceled / context.DeadlineExceeded).
func (h *HTTPSelfLoopProbe) Probe(ctx context.Context) (bool, error) {
	if h == nil {
		return false, fmt.Errorf("HTTPSelfLoopProbe.Probe: nil receiver")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.baseURL+h.path, nil)
	if err != nil {
		return false, fmt.Errorf("HTTPSelfLoopProbe.Probe: NewRequestWithContext: %w", err)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("HTTPSelfLoopProbe.Probe: client.Do %s: %w", h.baseURL+h.path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return true, nil
	}
	if h.log != nil {
		h.log.Debug("HTTPSelfLoopProbe: non-2xx status; reporting not-live",
			zap.String("url", h.baseURL+h.path),
			zap.Int("status_code", resp.StatusCode),
		)
	}
	return false, nil
}

// Compile-time pin (Pattern 0 + AGENTS.md): the concrete receiver must
// satisfy the canonical port. Drift in the IsLiveProbe signature surfaces
// as a build failure here rather than as a runtime panic on first dispatch.
var _ IsLiveProbe = (*HTTPSelfLoopProbe)(nil)
