// Package images (application/images) — service_provider_health.go owns the
// run-time PREREQUISITE check that the per-provider Diagnostics() surface
// (/api/system/doctor) already exposed but the job never consulted: a degraded
// retrieval provider was discovered by burning a complete durable run instead
// of by a cheap probe before generation started.
package images

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrNoHealthyImageProvider is returned when every retrieval provider is
// unhealthy. A run that needs internet images cannot succeed without at least
// one usable source, so this is a fail-closed preflight error rather than a
// soft warning.
var ErrNoHealthyImageProvider = errors.New("images: no healthy retrieval provider")

// ProbeImageProviderHealth verifies that at least one retrieval provider is
// healthy before the caller spends a durable run. It is idempotent, side-effect
// free and safe to invoke at PREFLIGHT time.
//
// It fails closed in two cases:
//   - the retrieval registry is not wired (no provider can ever answer), and
//   - every registered provider reports a health error.
//
// A registry with at least one healthy provider succeeds; the unhealthy ones
// are the caller's concern only through the returned error's absence (the
// per-provider detail is available from Diagnostics()/HealthReport()).
func (s *Service) ProbeImageProviderHealth(ctx context.Context) error {
	if s == nil {
		return fmt.Errorf("%w: image service is nil", ErrNoHealthyImageProvider)
	}
	registry := s.RetrievalRegistry()
	if registry == nil {
		return fmt.Errorf("%w: retrieval provider registry is not wired", ErrNoHealthyImageProvider)
	}
	report := registry.HealthReport(ctx)
	if report.AllHealthy() {
		return nil
	}
	reasons := make([]string, 0, len(report.Unhealthy))
	for provider, reason := range report.Unhealthy {
		reasons = append(reasons, fmt.Sprintf("%s: %s", provider, reason))
	}
	sort.Strings(reasons)
	return fmt.Errorf("%w: %s", ErrNoHealthyImageProvider, strings.Join(reasons, "; "))
}
