// Package qdrant — health.go (QDRANT-005, June 2026)
//
// HealthProbe is the canonical readiness check for the Qdrant capability.
// The /ready HTTP handler maps "DB ok, Drive ok, Qdrant ok" → 200
// READY; a single failure maps to 503 NOT_READY. The probe lives in
// the qdrant package (next to Client) so the wiring in
// BuildProcessBundle can hand a single concrete value to the
// lifecycle readiness barrier via AddProbe(name="qdrant", fn=...).
//
// The probe is intentionally lightweight: a single GET on
// /collections with a short timeout. Listing collections is the
// cheapest Qdrant endpoint that exercises HTTP plumbing AND schema
// resolution; using a heavier check (e.g. ScrollPoints) would stall
// the readiness barrier under transient Qdrant slowness without
// adding signal.
package qdrant

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// HealthProbe is the canonical Qdrant readiness check used by the
// lifecycle barrier. Construct via NewHealthProbe; the qdrant
// package's own Client holds the configured base URL + API key
// (apiKey is sent as the X-Api-Key header on every request).
type HealthProbe struct {
	client  *Client
	http    *http.Client
	probeTO time.Duration
}

// NewHealthProbe wires a probe against an existing Client. The
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
func NewHealthProbe(client *Client) *HealthProbe {
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
// on every request via the X-Api-Key header so an api_key-protected
// Qdrant server doesn't return 401 on a healthy deployment.
func (h *HealthProbe) Probe(ctx context.Context) error {
	if h == nil || h.client == nil {
		return fmt.Errorf("qdrant: health probe not configured (nil client)")
	}
	if h.http == nil {
		h.http = &http.Client{Timeout: h.probeTO}
	}
	url := h.client.BaseURL() + "/collections"
	pCtx, cancel := context.WithTimeout(ctx, h.probeTO)
	defer cancel()
	req, err := http.NewRequestWithContext(pCtx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("qdrant health: build request: %w", err)
	}
	if key := h.client.APIKey(); key != "" {
		req.Header.Set("X-Api-Key", key)
	}
	resp, err := h.http.Do(req)
	if err != nil {
		return fmt.Errorf("qdrant health: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("qdrant health: HTTP %d", resp.StatusCode)
	}
	return nil
}

// Compile-time assertion that HealthProbe satisfies the canonical
// readiness-probe contract (`Probe(context.Context) error`). Drift is
// caught at compile time, not on first /ready call.
var _ interface{ Probe(context.Context) error } = (*HealthProbe)(nil)
