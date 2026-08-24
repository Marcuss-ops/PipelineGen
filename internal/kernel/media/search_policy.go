package media

// SearchMode selects the search strategy. It mirrors the values in
// internal/capabilities/assets/search but is defined at the domain level so
// capability types can depend on it without pulling in the search
// application package.
type SearchMode string

const (
	SearchModeANN    SearchMode = "ann"
	SearchModeHybrid SearchMode = "hybrid"
)

// ResolutionSearchPolicy is the canonical set of knobs that control
// how a resolution pass searches the media catalog and external
// providers. It is shared by the Brain and MediaMemory capabilities.
type ResolutionSearchPolicy struct {
	// Mode selects the search strategy. Empty defaults to SearchModeANN.
	Mode SearchMode `json:"mode,omitempty"`

	// AllowExternal controls whether external providers may be
	// consulted at all. When false, the search is limited to local /
	// semantic backends.
	AllowExternal bool `json:"allow_external,omitempty"`

	// AllowedProviders restricts the fan-out to a specific set of
	// provider names. Empty means "all eligible backends".
	AllowedProviders []string `json:"allowed_providers,omitempty"`

	// CacheRead controls whether local/cache backends are consulted.
	// When false, only backends that are not pure local caches are
	// used. Backends classify themselves via a capability tag in
	// their metadata.
	CacheRead bool `json:"cache_read,omitempty"`

	// PreferApproved is advisory for backends that track approval
	// state; it is forwarded as a filter hint where supported.
	PreferApproved bool `json:"prefer_approved,omitempty"`

	// MaxCandidates is the upper bound on candidates returned.
	// Zero lets the underlying search default apply.
	MaxCandidates int `json:"max_candidates,omitempty"`

	// Language is the BCP-47 language code for the query.
	Language string `json:"language,omitempty"`

	// MediaTypes restricts results to the given media types
	// (e.g. "video", "image"). Empty means "all".
	MediaTypes []string `json:"media_types,omitempty"`
}

// DefaultResolutionSearchPolicy returns the conservative default used
// by the dashboard preview and other sandboxed callers.
func DefaultResolutionSearchPolicy() ResolutionSearchPolicy {
	return ResolutionSearchPolicy{
		Mode:          SearchModeANN,
		AllowExternal: false,
		CacheRead:     true,
		MaxCandidates: 10,
	}
}

// SearchModeToSearch bridges the domain-level SearchMode to the
// application-level search.SearchMode. It is intentionally thin so
// the domain type stays independent from the search package.
func SearchModeToSearch(m SearchMode) string {
	return string(m)
}
