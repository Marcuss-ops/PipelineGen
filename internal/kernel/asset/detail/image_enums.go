// Package asset — image enumeration extensions (FASE 1A of the
// image-territories action plan, July 2026).
//
// ImageOrigin and ImageProvider are declared in image_taxonomy.go
// (Step 1 of the same plan) and reused here as the canonical typed
// enums. This file adds three things on top:
//
//  1. The missing ImageSearchTerritory enum, used as the routing key
//     for ImageSearchResolver (FASE 6): callers pass a territory and
//     the resolver fans out to RetrievalProviderRegistry,
//     GenerationProviderRegistry, or both.
//
//  2. Explicit String() methods on all three types. The underlying
//     representation is already a string, so casting (e.g.
//     `string(o)`) works, but the explicit method (a) locks the
//     canonical serialization, (b) makes the types satisfy
//     fmt.Stringer at compile time, and (c) gives operators a stable
//     anchor for log-printable values.
//
//  3. Compile-time assertions: `var _ fmt.Stringer = ...` so any
//     future drift (drop the method, change the return type) is a
//     build error and not a runtime panic.
//
// Backward compatibility: this file does NOT redeclare ImageOrigin
// or ImageProvider — Go's single-package rule forbids duplicate
// type declarations. New methods on existing types are added in
// the same package without touching image_taxonomy.go.
package detail

// ════════════════════════════════════════════════════════════════════
// ImageSearchTerritory — drives the routing of ImageSearchResolver
// ════════════════════════════════════════════════════════════════════

// ImageSearchTerritory is the routing key for image search queries
// across the two image territories (retrieved vs generated). The
// ImageSearchResolver.Resolve(territory) method (FASE 6) maps each
// value to the canonical ImageSearcher for that territory:
//
//   - TerritoryRetrieved: only RetrievalProviderRegistry
//     (Wikipedia, DuckDuckGo, SearXNG, Drive).
//   - TerritoryGenerated: only GenerationProviderRegistry
//     (GoogleSlides, Flux, NVIDIA).
//   - TerritoryAll:       fan out to both and merge the result set.
//
// The string values are persisted in API responses and (eventually)
// in URL query parameters — they are stable across versions.
type ImageSearchTerritory string

const (
	// TerritoryRetrieved limits search to the retrieved territory
	// (Wikipedia/SearXNG/DuckDuckGo/Drive/manual uploads).
	TerritoryRetrieved ImageSearchTerritory = "retrieved"

	// TerritoryGenerated limits search to the generated territory
	// (GoogleSlides/Flux/NVIDIA). Does NOT auto-generate new images;
	// see internal/capabilities/images/workflow/generated/ (FASE 5/6).
	TerritoryGenerated ImageSearchTerritory = "generated"

	// TerritoryAll fans out to both territories; ImageSearchResolver
	// merges the results applying the territory-specific weight.
	TerritoryAll ImageSearchTerritory = "all"
)

// String returns the canonical, persisted string representation.
func (t ImageSearchTerritory) String() string {
	return string(t)
}

// IsValid reports whether the territory matches a known constant.
// Empty string is intentionally INVALID — a caller that wants to
// search across both territories MUST pass TerritoryAll explicitly.
func (t ImageSearchTerritory) IsValid() bool {
	switch t {
	case TerritoryRetrieved, TerritoryGenerated, TerritoryAll:
		return true
	}
	return false
}

// String methods for ImageOrigin and ImageProvider are defined by the
// canonical asset package. ImageSearchTerritory remains local to detail.
