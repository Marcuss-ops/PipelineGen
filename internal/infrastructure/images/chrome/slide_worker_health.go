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
package chrome

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

// HealthDeepResult mirrors the JSON shape the Python worker returns
// for `{"action": "health_deep"}`. The probe verifies that the Nano
// Banana panel is open, the prompt textarea is interactable, and the
// Immagine/Image mode is selectable (NOT collapsed/hidden/disabled).
// Any one failing makes the post-generation image-mode-actively-ok
// check return an error.
//
// P2 (July 2026): HealthDeep is invoked AFTER a successful Generate
// to confirm the next request will also flow through the image-mode
// pipeline (no silent regression to text-only mode). The basic
// `health` action stays the low-cost probe used by ensureStarted;
// HealthDeep is the heavier post-write consistency check carried
// out by the diagnostics + smoke paths.
type HealthDeepResult struct {
	Status              string `json:"status"`
	PanelOK             bool   `json:"panel_ok"`
	TextareaOK          bool   `json:"textarea_ok"`
	ImageModeSelectable bool   `json:"image_mode_selectable"`
	URL                 string `json:"url,omitempty"`
	ProfileHealthy      bool   `json:"profile_healthy"`
	FailureReason       string `json:"failure_reason,omitempty"`
}

// HealthDeep issues a deeper health probe to the worker that exercises
// the Nano Banana UI surface (panel + textarea + Immagine mode) so
// post-Generate callers can verify the next-request pipeline is
// reachable. Returns nil iff all three round-trip checks pass AND
// the basic profile health is still ok.
//
// PR-CHROME-PROVIDER-SPLIT + P2 (July 2026): HealthDeep complements
// the existing Health() probe (worker alive + responsive). Health()
// stays the lightweight kick-the-tires probe used by ensureStarted;
// HealthDeep is the heavier post-write DOM readiness check that
// catches silent regressions (e.g. Immagine tab closes between
// generations because of a Google Slides UI refactor). Must be
// called while p.mu is held.
func (p *ChromeImageProvider) HealthDeep() error {
	if p.stdin == nil {
		return fmt.Errorf("health deep: worker stdin is nil (process may have exited)")
	}
	if err := p.writeJSON(map[string]any{"action": "health_deep"}); err != nil {
		return fmt.Errorf("health deep: write failed: %w", err)
	}
	raw, err := p.readRawResponse()
	if err != nil {
		return fmt.Errorf("health deep: read failed: %w", err)
	}
	var result HealthDeepResult
	if err := mapToStruct(raw, &result); err != nil {
		return fmt.Errorf("health deep: parse failed: %w (raw=%v)", err, raw)
	}
	if result.Status != "ok" {
		reason := result.FailureReason
		if reason == "" {
			reason = "unknown (worker returned status != ok)"
		}
		return fmt.Errorf("health deep: worker reported unhealthy: %s", reason)
	}
	if !result.PanelOK {
		return fmt.Errorf("health deep: Nano Banana panel is not open (URL=%s)", result.URL)
	}
	if !result.TextareaOK {
		return fmt.Errorf("health deep: prompt textarea is not interactable (URL=%s)", result.URL)
	}
	if !result.ImageModeSelectable {
		return fmt.Errorf("health deep: Immagine/Image mode is not selectable (URL=%s)", result.URL)
	}
	return nil
}
