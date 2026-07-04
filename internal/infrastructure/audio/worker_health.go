// Package audioasset — worker_health.go (VO-DECOMPOSITION P0 #1, July 2026):
// health probes for the tts_edge_server.py subprocess.
//
// godlike/06 + AGENTS.md Pattern 5: single-purpose capability file in
// the same package. Owns the health probe surface:
//   - healthCheck() — low-level GET /health probe, called by ensureStarted
//     to verify the worker is alive after startup.
//   - Health() — public health probe for diagnostics.
//
// Mirrors the precedent in internal/application/images/slide_worker_
// health.go (PR-CHROME-PROVIDER-SPLIT, 2026-07-04).
package audioasset

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// healthCheck sends a GET /health to the persistent worker.
// Must be called while p.mu is held.
func (p *Processor) healthCheck() error {
	if p.baseURL == "" || p.httpClient == nil {
		return fmt.Errorf("tts worker not started (no baseURL)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		p.baseURL+"/health", nil)
	if err != nil {
		return fmt.Errorf("health check: create request: %w", err)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("health check: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("worker unhealthy: status %d", resp.StatusCode)
	}

	return nil
}

// Health reports whether the persistent TTS worker is alive and responsive.
// Returns nil (healthy) or an error describing the problem.
//
// VO-DECOMPOSITION P0 #1 (July 2026): the public health surface for
// diagnostics and monitoring. The worker is lazily started on the first
// call to Generate(); Health() before the first synthesis reports the
// worker as not started (not an error — it's a lazy-init contract).
func (p *Processor) Health() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.started {
		return fmt.Errorf("tts worker not started")
	}
	return p.healthCheck()
}
