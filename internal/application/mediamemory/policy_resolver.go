// Package mediamemory — policy_resolver.go owns the canonical
// defaulting of ResolvePolicy.
//
// godlike/06 SSOT: the API layer maps its wire DTO into
// OptionalResolvePolicy and defers every default to this
// application-layer resolver. No other file may define or apply
// canonical defaults for these knobs.
package mediamemory

import "github.com/Marcuss-ops/PipelineGen/internal/domain/media"

// Default policy constants. They are intentionally unexported so
// callers cannot bypass the resolver and invent their own defaults.
const (
	defaultPreferApprovedBindings = true
	defaultAllowExternalSearch    = false
	defaultMaxCandidatesPerSlot   = 10
	defaultAvoidRecentAssets      = false
	defaultCacheRead              = true
)

// ResolutionPolicyResolver applies canonical defaults to an
// OptionalResolvePolicy and returns a fully resolved ResolvePolicy.
// It is the single owner of the conservative dashboard-preview
// defaults (PreferApprovedBindings=true, AllowExternalSearch=false,
// AvoidRecentAssets=false, CacheRead=true, MaxCandidatesPerSlot=10,
// Mode=ANN).
type ResolutionPolicyResolver interface {
	// Resolve applies defaults to opts and returns a fully populated
	// ResolvePolicy. The language argument is the request language
	// and is stamped into SearchPolicy.Language for downstream
	// search backends.
	Resolve(language string, opts OptionalResolvePolicy) ResolvePolicy
}

// defaultPolicyResolver is the production implementation of
// ResolutionPolicyResolver.
type defaultPolicyResolver struct{}

// NewResolutionPolicyResolver returns the canonical production
// policy resolver.
func NewResolutionPolicyResolver() ResolutionPolicyResolver {
	return &defaultPolicyResolver{}
}

// Resolve applies canonical defaults to any missing optional fields
// and returns a fully populated ResolvePolicy.
func (r *defaultPolicyResolver) Resolve(language string, opts OptionalResolvePolicy) ResolvePolicy {
	policy := ResolvePolicy{
		PreferApprovedBindings: defaultPreferApprovedBindings,
		AllowExternalSearch:    defaultAllowExternalSearch,
		MaxCandidatesPerSlot:   defaultMaxCandidatesPerSlot,
		AvoidRecentAssets:      defaultAvoidRecentAssets,
		SearchPolicy: media.ResolutionSearchPolicy{
			Mode:          media.SearchModeANN,
			AllowExternal: defaultAllowExternalSearch,
			CacheRead:     defaultCacheRead,
			MaxCandidates: defaultMaxCandidatesPerSlot,
		},
	}

	if opts.PreferApprovedBindings != nil {
		policy.PreferApprovedBindings = *opts.PreferApprovedBindings
	}
	if opts.AllowExternalSearch != nil {
		policy.AllowExternalSearch = *opts.AllowExternalSearch
	}
	if opts.AvoidRecentAssets != nil {
		policy.AvoidRecentAssets = *opts.AvoidRecentAssets
	}
	if opts.CacheRead != nil {
		policy.SearchPolicy.CacheRead = *opts.CacheRead
	} else {
		policy.SearchPolicy.CacheRead = defaultCacheRead
	}
	if opts.MaxCandidatesPerSlot > 0 {
		policy.MaxCandidatesPerSlot = opts.MaxCandidatesPerSlot
	}

	mode := media.SearchModeANN
	if opts.Mode != "" {
		mode = media.SearchMode(opts.Mode)
	}
	policy.SearchPolicy.Mode = mode
	policy.SearchPolicy.AllowExternal = policy.AllowExternalSearch
	policy.SearchPolicy.PreferApproved = policy.PreferApprovedBindings
	policy.SearchPolicy.MaxCandidates = policy.MaxCandidatesPerSlot
	policy.SearchPolicy.AllowedProviders = append([]string(nil), opts.AllowedProviders...)
	policy.SearchPolicy.Language = language

	return policy
}
