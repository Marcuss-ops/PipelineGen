package spec

// KnownSourceTypes is the canonical set of script-side SourceType
// values. Map lookup keeps the C2-C AST check deterministic.
// detection (godlike/06 SSOT co-located structural validation).
var KnownSourceTypes = map[SourceType]struct{}{
	SourceText:     {},
	SourceClips:    {},
	SourceCatalog:  {},
	SourceSearch:   {},
	SourceCurate:   {},
	SourceResearch: {},
}

// ClipSourceTypes is the canonical set of SourceType values that
// may carry clip evidence (godlike/07 NO-FAKE-AVAILABILITY).
var ClipSourceTypes = map[SourceType]struct{}{
	SourceClips:   {},
	SourceCatalog: {},
	SourceSearch:  {},
	SourceCurate:  {},
}

// ValidGroundingPolicies and ValidFallbackPolicies are the canonical
// membership sets. Map lookup keeps the C2-C AST check deterministic.
// switch-case detection.
var (
	ValidGroundingPolicies = map[string]struct{}{
		GroundingPolicyClipsPrimary:  {},
		GroundingPolicySourcePrimary: {},
		GroundingPolicyBalanced:      {},
	}
	ValidFallbackPolicies = map[string]struct{}{
		FallbackPolicyStrict:     {},
		FallbackPolicyAllowProse: {},
	}
)

// IsKnownSourceType returns true for the canonical source types.
func IsKnownSourceType(st SourceType) bool {
	_, ok := KnownSourceTypes[st]
	return ok
}

// IsClipSourceType returns true for source types that may carry
// clip evidence.
func IsClipSourceType(st SourceType) bool {
	_, ok := ClipSourceTypes[st]
	return ok
}

// ModeForSource returns the engine mode for a canonical source type.
// Clip-bearing source types share the clip-to-script path; all other
// source types use the text path. Keeping this policy beside the
// canonical SourceType registry prevents composition and plan builders
// from maintaining separate switches or maps.
func ModeForSource(st SourceType) string {
	if IsClipSourceType(st) {
		return "clip_to_script"
	}
	return "text"
}

// IsValidGroundingPolicy returns true for the canonical policies.
func IsValidGroundingPolicy(p string) bool {
	_, ok := ValidGroundingPolicies[p]
	return ok
}

// IsValidFallbackPolicy returns true for the canonical policies.
func IsValidFallbackPolicy(p string) bool {
	_, ok := ValidFallbackPolicies[p]
	return ok
}
