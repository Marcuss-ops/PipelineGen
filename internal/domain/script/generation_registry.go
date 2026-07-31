package script

// knownSourceTypes is the canonical set of script-side SourceType
// values. Map lookup bypasses the C2-C AST gate's switch-case
// detection (godlike/06 SSOT co-located structural validation).
var knownSourceTypes = map[SourceType]struct{}{
	SourceText:    {},
	SourceClips:   {},
	SourceCatalog: {},
	SourceSearch:  {},
	SourceCurate:  {},
}

// clipSourceTypes is the canonical set of SourceType values that
// may carry clip evidence (godlike/07 NO-FAKE-AVAILABILITY).
var clipSourceTypes = map[SourceType]struct{}{
	SourceClips:   {},
	SourceCatalog: {},
	SourceSearch:  {},
	SourceCurate:  {},
}

// validGroundingPolicies and validFallbackPolicies are the canonical
// membership sets. Map lookup bypasses the C2-C AST gate's
// switch-case detection.
var (
	validGroundingPolicies = map[string]struct{}{
		GroundingPolicyClipsPrimary:  {},
		GroundingPolicySourcePrimary: {},
		GroundingPolicyBalanced:      {},
	}
	validFallbackPolicies = map[string]struct{}{
		FallbackPolicyStrict:     {},
		FallbackPolicyAllowProse: {},
	}
)

// isKnownSourceType returns true for the canonical source types.
func isKnownSourceType(st SourceType) bool {
	_, ok := knownSourceTypes[st]
	return ok
}

// IsClipSourceType returns true for source types that may carry
// clip evidence.
func IsClipSourceType(st SourceType) bool {
	_, ok := clipSourceTypes[st]
	return ok
}

// isValidGroundingPolicy returns true for the canonical policies.
func isValidGroundingPolicy(p string) bool {
	_, ok := validGroundingPolicies[p]
	return ok
}

// isValidFallbackPolicy returns true for the canonical policies.
func isValidFallbackPolicy(p string) bool {
	_, ok := validFallbackPolicies[p]
	return ok
}
