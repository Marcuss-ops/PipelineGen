package images

// Capability enumerates the image-domain features that the API layer
// advertises. Each capability has an explicit status used by the HTTP
// layer to decide between 200 / 501 / 503 (per the fix(images):
// expose truthful capability availability contract).
//
// Invariants:
//   - StatusAvailable         — feature is wired AND its configurable
//     dependencies are present.
//   - StatusNotImplemented    — feature is wired as a stub. The 501 path
//     is the honest outcome.
//   - StatusMissingDependency — feature is wired but a required dep is
//     absent (e.g. NVIDIA_API_KEY not set).
//     The 503 path is the honest outcome.
//
// Today's mapping (June 2026):
//   - CapImageGenNvidia    — NVIDIA Flux image gen (depends on
//     nvidiaAPIKey + cfg.Concurrency.MaxConcurrentNvidiaGenerations)
//   - CapRemoteImageGen    — Remote Google Flow image gen (depends on
//     remoteImageEndpointURL)
//   - CapImageGenChrome    — Chrome/Playwright image gen (depends on
//     imageGen ImageGenerator being wired — FASE 2, June 2026)
type Capability string

const (
	CapImageGenNvidia Capability = "image_gen_nvidia"
	CapRemoteImageGen Capability = "remote_image_gen"
	// CapImageGenChrome mirrors the RequiredCapabilities entry in
	// internal/application/jobs/registry.go::Compose() TypeImageGenerateGoogle.
	// Keep the string value in sync across both declaration sites.
	CapImageGenChrome Capability = "image_gen_chrome"
)

// CapabilityStatus is the explicit availability flag for a capability.
// HTTP routes consult this to decide between 200 (Available),
// 501 NotImplemented, or 503 ServiceUnavailable.
type CapabilityStatus string

const (
	StatusAvailable         CapabilityStatus = "available"
	StatusNotImplemented    CapabilityStatus = "not_implemented"
	StatusMissingDependency CapabilityStatus = "missing_dependency"
)

// nvidiaAPIKeyPlaceholder is the well-known "not set" sentinel used by
// example configs and onboarding scripts. CapabilityResolution treats
// it as equivalent to the empty string so dev laptops without a real
// key don't accidentally report Available.
const nvidiaAPIKeyPlaceholder = "PASTE_YOUR_NVIDIA_API_KEY_HERE"

// CapabilityResolution returns CapabilityStatus for the given Capability.
// The mapping is computed at the call site from the receiver's wiring
// so the API layer always sees fresh results after composition roots
// swap dependencies. Caching here would risk stale diagnostic reports.
//
// Single source of truth — the route handlers and the Diagnostics
// method both consult this rather than reading env vars or feature
// booleans in multiple places (Rule: do not duplicate feature flag in
// multiple places).
func (s *Service) CapabilityResolution(cap Capability) CapabilityStatus {
	if s == nil {
		// Nil-safe: a nil receiver returns NotImplemented for every
		// capability so handlers cannot panic on a mis-wired service.
		return StatusNotImplemented
	}
	switch cap {
	case CapImageGenNvidia:
		// Disabled/Removed: Chrome/Playwright (Google Slides) is the only provider
		return StatusNotImplemented
	case CapRemoteImageGen:
		// Disabled/Removed: Chrome/Playwright (Google Slides) is the only provider
		return StatusNotImplemented
	case CapImageGenChrome:
		// Available when an ImageGenerator (ChromeImageProvider) is wired.
		// No env-var dependency — the provider is injected at composition time.
		if s.imageGen == nil {
			return StatusMissingDependency
		}
		return StatusAvailable
	default:
		return StatusNotImplemented
	}
}

// AllCapabilities returns the resolved status for every known capability.
// Used by the diagnostic endpoint to surface truthful availability
// without leaking placeholder / env-var state to the caller — every
// value here is a single CapabilityStatus result derived from
// CapabilityResolution.
func (s *Service) AllCapabilities() map[Capability]CapabilityStatus {
	return map[Capability]CapabilityStatus{
		CapImageGenNvidia: s.CapabilityResolution(CapImageGenNvidia),
		CapRemoteImageGen: s.CapabilityResolution(CapRemoteImageGen),
		CapImageGenChrome: s.CapabilityResolution(CapImageGenChrome),
	}
}
