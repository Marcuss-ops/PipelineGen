package providers

import (
	"context"
	"time"
)

// HealthProbe is the canonical health-check signature for a
// Provider. A nil HealthProbe on a ProviderEntry signals that the
// provider does not advertise health; HealthCheck() then omits the
// provider from its result map (no entry → no probe configured).
type HealthProbe func(ctx context.Context) error

// ProviderCapabilityDetail is the per-capability detail block.
//
// S3a (June 2026): this type is an open extension point. Today it
// carries tagged identity (so callers can detect "this capability
// has a non-nil detail pointer"). Future S3b-S3d waves will add
// the per-capability fields (e.g. PerResultLimitPerSource on Search,
// PerPageByteBudget on Fetch). To remain forward-compatible, all
// existing call sites MUST:
//   - read via `if det := entry.Capabilities.Search; det != nil`;
//   - write via direct field assignment on a *ProviderEntry (preferred)
//     OR by setting the per-capability detail pointer from a helper.
type ProviderCapabilityDetail struct {
	// Reserved for future per-capability metadata. The marker struct
	// is intentional: typed access (Option A) keeps
	// ProviderCapabilitySet ergonomics at O(1) and avoids map
	// allocations on the hot path (All/ByCapability/HealthCheck).
}

// ProviderCapabilitySet is the typed, per-capability map attached to
// a ProviderEntry. Migrated from the prior `[]Capability` shape via
// the EXPAND phase of godlike/07 (zero-legacy policy): existing
// adapters continue to expose `Capabilities() []Capability` on the
// Provider interface; the registry normalises those into the typed
// ProviderCapabilitySet once at Register time.
//
// Each pointer is nil when the provider does NOT advertise that
// capability, OR when the caller has not enriched the entry with a
// capability-specific detail pointer (zero-default path).
// CapabilitySet.Has(cap) reports truthiness for the specified cap.
//
// Rationale for Option A (typed pointers) vs Option B (map):
//   - O(1) typed-field reads on the hot path (HealthCheck /
//     ByCapability);
//   - compile-time enforcement when future capabilities are added
//     (new capability → add field → adapters compile-fail until
//     refreshed, vs map silently returning zero value);
//   - canonical in-tree clarity (no `for k, v := range caps` hot loops);
//   - forward-extension is a struct-field-add (one line, not a map
//     re-key). Trade-off: a new tag added at Provider-level requires
//     a ProviderCapabilitySet field add; documented in the package
//     doc so future maintainers understand the design choice.
type ProviderCapabilitySet struct {
	Search *ProviderCapabilityDetail
	Fetch  *ProviderCapabilityDetail
	Video  *ProviderCapabilityDetail
	Image  *ProviderCapabilityDetail
	Music  *ProviderCapabilityDetail
	Voice  *ProviderCapabilityDetail
	Script *ProviderCapabilityDetail
}

// Detail returns the per-capability detail pointer (or nil when the
// cap is unknown / not advertised by the set). Pointer return lets
// callers stamp in-place via `*caps.Detail(CapabilitySearch) = &Detail{}`.
func (s *ProviderCapabilitySet) Detail(cap Capability) *ProviderCapabilityDetail {
	switch cap {
	case CapabilitySearch:
		return s.Search
	case CapabilityFetch:
		return s.Fetch
	case CapabilityVideo:
		return s.Video
	case CapabilityImage:
		return s.Image
	case CapabilityMusic:
		return s.Music
	case CapabilityVoice:
		return s.Voice
	case CapabilityScript:
		return s.Script
	default:
		return nil
	}
}

// Has reports whether the set carries a non-nil detail pointer for
// the given capability. Used by callers that want a quick "does this
// provider support X with enriched metadata?" check without iterating.
func (s *ProviderCapabilitySet) Has(cap Capability) bool {
	return s.Detail(cap) != nil
}

// ProviderEntry is the full per-provider record held by the
// registry. S3a (June 2026) replaces the previous
// `map[string]Provider` storage with `map[string]*ProviderEntry` to
// carry the typed capability set + provider-level defaults (timeout,
// limits, health probe) without forcing every call site to thread an
// additional map[Capability]Detail alongside the provider lookups.
//
// Field semantics:
//   - Name         : canonical human-readable identifier, matches
//     Provider.Name() (the registry uses Name as the
//     dedup key).
//   - Provider     : the underlying source integration. Required.
//     Lookups via All/ByCapability/Get strip this and
//     return Provider for back-compat.
//   - Capabilities : typed set declared at Register time. Pointer
//     fields are populated when the provider advertises
//     the capability AND the caller has enriched the
//     entry with a per-capability detail. Zero value
//     (all nil) is acceptable and means "no per-
//     capability enrichment declared" — this is the
//     migration-friendly path from the previous
//     `[]Capability` shape.
//   - HealthProbe  : optional liveness probe for this provider. When
//     nil, Registry.HealthCheck omits the provider from
//     the result map (no entry = no probe configured).
//   - Timeout      : per-provider timeout applied to the HealthProbe
//     call. 0 falls back to DefaultHealthTimeout (5s).
//     Per S3a spec wording.
//   - MaxResults   : per-provider cap on candidate counts. 0 means
//     "use adapter default". Forwarded to consumers
//     in a future wave that pipes registry-level
//     limits into SearchRequest.Limit.
//   - MaxPages     : per-provider cap on pagination depth. 0 means
//     "no pagination" / adapter default. Same forward-
//     shape as MaxResults.
type ProviderEntry struct {
	Name         string
	Provider     Provider
	Capabilities ProviderCapabilitySet
	HealthProbe  HealthProbe
	Timeout      time.Duration
	MaxResults   int
	MaxPages     int
}

// HealthTimeout returns the effective probe timeout for this entry:
// the entry's Timeout unless zero, in which case DefaultHealthTimeout.
// Kept on the entry (not on Registry) so per-entry overrides win
// without registry-level state mutation.
func (e *ProviderEntry) HealthTimeout() time.Duration {
	if e == nil || e.Timeout <= 0 {
		return DefaultHealthTimeout
	}
	return e.Timeout
}
