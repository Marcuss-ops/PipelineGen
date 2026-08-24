// Package qdrant — health.go (QDRANT-005, June 2026)
//
// HealthProbe is the canonical readiness check for the Qdrant capability.
// The /ready HTTP handler maps "DB ok, Drive ok, Qdrant ok" → 200
// READY; a single failure maps to 503 NOT_READY. The probe lives in
// the qdrant package (next to transport.Client) so the wiring in
// BuildProcessBundle can hand a single concrete value to the
// lifecycle readiness barrier via AddProbe(name="qdrant", fn=...).
//
// The probe is intentionally lightweight: a single GET on
// /collections with a short timeout. Listing collections is the
// cheapest Qdrant endpoint that exercises HTTP plumbing AND schema
// resolution; using a heavier check (e.g. ScrollPoints) would stall
// the readiness barrier under transient Qdrant slowness without
// adding signal.
package disasterrecovery

import (
	"context"
	"fmt"
	"net/http"
	"time"

	healthport "github.com/Marcuss-ops/PipelineGen/internal/capabilities/system/health"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
)

// HealthProbe is the canonical Qdrant readiness check used by the
// lifecycle barrier.
//
// PR1 — fix/qdrant-wire-contracts: the probe now calls the configured
// transport.Client via transport.Client.doRequest so the api-key header is set by the SINGLE
// shared transport (was previously duplicated here as X-Api-Key). The
// probe still holds its own *http.Client for a per-call Timeout ceiling
// — the defense-in-depth invariant from earlier versions is intact.
type HealthProbe struct {
	client  *transport.Client
	http    *http.Client
	probeTO time.Duration
}

// NewHealthProbe wires a probe against an existing transport.Client. The
// probe timeout is per-call; the lifecycle barrier wraps the call
// in an additional context.WithTimeout(probeTimeout) (5s) so the
// effective ceiling is min(probeTimeout, barrierTimeout).
//
// The HTTP client is a dedicated, fresh *http.Client (NOT
// http.DefaultClient) so per-probe Timeout fires even if a caller
// forgets to wrap the call in context.WithTimeout — defense in
// depth: the lifecycle barrier times out from above, but the
// internal client Timeout enforces a second ceiling so a future
// operator wiring AddProbe(...) without a context wrapper cannot
// stall the barrier indefinitely.
func NewHealthProbe(client *transport.Client) *HealthProbe {
	probeTO := 5 * time.Second
	if client == nil {
		return &HealthProbe{
			probeTO: probeTO,
			http:    &http.Client{Timeout: probeTO},
		}
	}
	return &HealthProbe{
		client:  client,
		probeTO: probeTO,
		http:    &http.Client{Timeout: probeTO},
	}
}

// Probe issues a single GET {baseURL}/collections request against
// the configured Qdrant base. Returns nil on HTTP 200, an error
// otherwise. The probe NEVER reads SQLite — qdrant liveness is the
// only thing under check. The configured API key (if any) is sent
// on every request by transport.Client.doRequest via the api-key header so an
// api_key-protected Qdrant server doesn't return 401 on a healthy
// deployment.
//
// PR1: the probe delegates to transport.Client.doRequest (rather than building
// its own http.NewRequest) so there is exactly ONE place that sets
// the api-key header. Previously the probe reimplemented auth as
// `req.Header.Set("X-Api-Key", key)`, which used a different header
// capitalization than the transport.Client and could drift apart under future
// changes.
func (h *HealthProbe) Probe(ctx context.Context) error {
	if h == nil || h.client == nil {
		return fmt.Errorf("qdrant: health probe not configured (nil client)")
	}
	if h.http == nil {
		h.http = &http.Client{Timeout: h.probeTO}
	}
	pCtx, cancel := context.WithTimeout(ctx, h.probeTO)
	defer cancel()
	url := h.client.BaseURL() + "/collections"
	resp, err := h.client.DoWithHTTPClient(pCtx, h.http, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("qdrant health: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &transport.APIError{
			Operation: "HealthProbe.Probe",
			Status:    resp.StatusCode,
			Message:   fmt.Sprintf("qdrant health: HTTP %d", resp.StatusCode),
			Retryable: resp.StatusCode == 0 || resp.StatusCode == 408 || resp.StatusCode == 429 || (resp.StatusCode >= 500 && resp.StatusCode <= 599),
		}
	}
	return nil
}

// CheckQdrant implements healthport.QdrantChecker — the /health endpoint
// contract. Delegates to Probe and translates the result into a
// healthport.CheckResult map. This makes HealthProbe the SINGLE Qdrant
// liveness check implementation, eliminating the duplicated
// infrahealth.QdrantChecker (QDRANT-005 Blocker 3, June 2026).
func (h *HealthProbe) CheckQdrant(ctx context.Context) healthport.CheckResult {
	start := time.Now()
	if err := h.Probe(ctx); err != nil {
		return healthport.CheckResult{
			"ok":          false,
			"duration_ms": time.Since(start).Milliseconds(),
			"error":       err.Error(),
		}
	}
	return healthport.CheckResult{
		"ok":          true,
		"duration_ms": time.Since(start).Milliseconds(),
		"configured":  true,
	}
}

// Compile-time assertions. HealthProbe satisfies both:
//   - the readiness-barrier Probe contract (`Probe(context.Context) error`)
//   - the /health endpoint QdrantChecker contract (`CheckQdrant(context.Context) CheckResult`)
var (
	_ interface{ Probe(context.Context) error } = (*HealthProbe)(nil)
	_ healthport.QdrantChecker                  = (*HealthProbe)(nil)
)
