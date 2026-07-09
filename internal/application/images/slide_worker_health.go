// Package images — slide_worker_health.go (PR-CHROME-PROVIDER-SPLIT, 2026-07-04):
// health probes for the slide_worker.py subprocess.
//
// PR-CHROME-PROVIDER-SPLIT (2026-07-04, godlike/06 + AGENTS.md Pattern 5):
// extracted from the pre-split ~260-LoC god file into a single-purpose
// capability file in the same package. Owns the health probe surface:
//   - healthCheck() — raw JSON probe via the "health" action (low-level,
//     called by ensureStarted to verify the worker is alive after a
//     successful start)
//   - Health() — public health probe used by DiagnosticsService
//   - ActiveCooldownProfiles() — DEPRECATED counter preserved on the
//     diagnostics surface (always returns 0; the per-profile cooldown
//     tracker is RETIRED per godlike/07 no-fake-availability)
//
// Imports needed by this file (single-purpose slice per Pattern 5):
// stdlib fmt + the canonical ChromeImageProvider fields (p.started)
// + the JSON protocol helpers (p.writeJSON, p.readRawResponse) declared
// in slide_worker_protocol.go.
package images

import (
	"fmt"
)

// healthCheck sends a health action to the worker.
// Must be called while p.mu is held.
func (p *ChromeImageProvider) healthCheck() error {
	if p.stdin == nil {
		return fmt.Errorf("health check: worker stdin is nil (process may have exited)")
	}
	if err := p.writeJSON(map[string]any{"action": "health"}); err != nil {
		return fmt.Errorf("health check: write failed: %w", err)
	}
	resp, err := p.readRawResponse()
	if err != nil {
		return fmt.Errorf("health check: read failed: %w", err)
	}
	if resp["status"] != "ok" {
		return fmt.Errorf("worker unhealthy: %v", resp["error"])
	}
	return nil
}

// Health reports whether the persistent worker is alive.
// Returns nil (healthy) or an error describing the problem. Used by
// DiagnosticsService (FASE 10, June 2026).
//
// PR-CHROME-PROVIDER-SPLIT (2026-07-04): the pre-split implementation
// walked `p.cooldowns` and surfaced "X profiles in cooldown" warnings —
// the cooldowns field is RETIRED per godlike/07 no-fake-availability.
// The post-split Health() returns a clean health signal: nil if the
// worker is alive + responsive, error otherwise.
func (p *ChromeImageProvider) Health() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.started {
		return fmt.Errorf("worker not started")
	}
	return p.healthCheck()
}

// ActiveCooldownProfiles returns the count of profiles currently in cooldown.
//
// PR-CHROME-PROVIDER-SPLIT (2026-07-04): this method is preserved on the
// public surface (consumed by DiagnosticsService.Diagnostics() in
// diagnostics_service.go to populate the ImageGenCooldownProfiles
// diagnostics field) but always returns 0. The per-profile cooldown
// tracker (the `cooldowns map[int]int64` field) is RETIRED per
// godlike/07 no-fake-availability — single-profile is the canonical
// policy (no per-profile routing to spread the load onto), so the
// counter would be tracked-but-never-actionable.
//
// godlike/07 no-fake-availability: the counter reports the truth
// (0 profiles in cooldown, by policy), not a tracked-but-never-actionable
// value. Diagnostics consumers should treat ImageGenCooldownProfiles=0
// as the canonical state for the single-profile policy.
func (p *ChromeImageProvider) ActiveCooldownProfiles() int {
	return 0
}
