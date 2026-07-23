package asset

import (
	"context"
	"strings"
	"sync"
)

// ProviderRegistryVersion is the canonical version of the provider
// descriptor registry. Bump it whenever the registry shape, provider
// set or matching rules change so downstream fingerprints can
// invalidate stale cached decisions.
const ProviderRegistryVersion = "provider-registry-v1"

// ProviderDescriptor is the canonical, data-driven description of a
// media source/provider. It replaces scattered switch/Contains
// classification across the application.
type ProviderDescriptor struct {
	// ID is the canonical provider identifier.
	ID ImageProvider
	// Aliases are strings that can identify the provider in URLs,
	// source labels, metadata JSON, etc.
	Aliases []string
	// MatchSource is an optional custom matcher. When nil, the
	// registry matches by checking whether the lowercased source
	// equals the ID or any alias, or contains any alias.
	MatchSource func(source string) bool
	// Origin classifies assets coming from this provider.
	Origin ImageOrigin
	// DefaultRightsStatus is the canonical rights status when none
	// is provided by the source (e.g. "unknown", "cc-by",
	// "proprietary").
	DefaultRightsStatus string
	// LicenseResolver resolves a license string from raw metadata.
	LicenseResolver func(ctx context.Context, rawMetadata Metadata) (string, error)
	// Materializer materialises an asset into its canonical form.
	Materializer func(ctx context.Context, req MaterializeRequest) (MaterializeResult, error)
	// MetadataMapper maps raw provider metadata into the canonical
	// metadata shape.
	MetadataMapper func(raw Metadata) (Metadata, error)
}

// MaterializeRequest is the input to a ProviderDescriptor.Materializer.
type MaterializeRequest struct {
	AssetID   string
	SourceURL string
	Metadata  Metadata
}

// MaterializeResult is the output of a ProviderDescriptor.Materializer.
type MaterializeResult struct {
	AssetID      string
	CanonicalURL string
	Metadata     Metadata
}

// ProviderRegistry is the canonical registry of provider descriptors.
// A single registry instance centralises provider recognition and
// replaces ad-hoc string matching.
type ProviderRegistry struct {
	descriptors []*ProviderDescriptor
	byID        map[ImageProvider]*ProviderDescriptor
}

// NewProviderRegistry returns an empty registry.
func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{
		descriptors: make([]*ProviderDescriptor, 0),
		byID:        make(map[ImageProvider]*ProviderDescriptor),
	}
}

// Register adds a descriptor to the registry. The same ID can be
// registered only once.
func (r *ProviderRegistry) Register(d ProviderDescriptor) bool {
	if _, ok := r.byID[d.ID]; ok {
		return false
	}
	ptr := &d
	r.descriptors = append(r.descriptors, ptr)
	r.byID[d.ID] = ptr
	return true
}

// ByID returns the descriptor with the given ID, or nil.
func (r *ProviderRegistry) ByID(id ImageProvider) *ProviderDescriptor {
	return r.byID[id]
}

// Match returns the first descriptor whose MatchSource function or
// aliases identify the supplied source string. It returns nil when no
// descriptor matches.
func (r *ProviderRegistry) Match(source string) *ProviderDescriptor {
	if source == "" {
		return nil
	}
	lower := strings.ToLower(strings.TrimSpace(source))
	for _, d := range r.descriptors {
		if d.MatchSource != nil {
			if d.MatchSource(lower) {
				return d
			}
			continue
		}
		if strings.Contains(lower, string(d.ID)) {
			return d
		}
		for _, alias := range d.Aliases {
			if alias != "" && strings.Contains(lower, strings.ToLower(alias)) {
				return d
			}
		}
	}
	return nil
}

// All returns a defensive copy of the registered descriptors.
func (r *ProviderRegistry) All() []*ProviderDescriptor {
	out := make([]*ProviderDescriptor, len(r.descriptors))
	copy(out, r.descriptors)
	return out
}

// DefaultProviderRegistry returns the canonical registry pre-populated
// with known image providers. It is safe for concurrent reads.
var (
	defaultProviderRegistry     *ProviderRegistry
	defaultProviderRegistryOnce sync.Once
)

// DefaultProviderRegistry returns the canonical provider registry.
func DefaultProviderRegistry() *ProviderRegistry {
	defaultProviderRegistryOnce.Do(func() {
		defaultProviderRegistry = buildDefaultProviderRegistry()
	})
	return defaultProviderRegistry
}

func buildDefaultProviderRegistry() *ProviderRegistry {
	reg := NewProviderRegistry()

	// Retrieved providers.
	reg.Register(ProviderDescriptor{
		ID:                  ProviderWikipedia,
		Aliases:             []string{"wikipedia.org", "wikipedia"},
		Origin:              ImageOriginRetrieved,
		DefaultRightsStatus: "cc-by",
	})
	reg.Register(ProviderDescriptor{
		ID:                  ProviderDuckDuckGo,
		Aliases:             []string{"duckduckgo"},
		Origin:              ImageOriginRetrieved,
		DefaultRightsStatus: "unknown",
	})
	reg.Register(ProviderDescriptor{
		ID:                  ProviderSearXNG,
		Aliases:             []string{"searxng"},
		Origin:              ImageOriginRetrieved,
		DefaultRightsStatus: "unknown",
	})
	reg.Register(ProviderDescriptor{
		ID:                  ProviderDrive,
		MatchSource:         exactOrAliasMatcher(string(ProviderDrive), []string{"drive", "drive.google.com"}),
		Origin:              ImageOriginRetrieved,
		DefaultRightsStatus: "unknown",
	})
	reg.Register(ProviderDescriptor{
		ID:                  ProviderUpload,
		MatchSource:         exactOrAliasMatcher(string(ProviderUpload), []string{"upload"}),
		Origin:              ImageOriginUploaded,
		DefaultRightsStatus: "unknown",
	})

	// Generated providers.
	reg.Register(ProviderDescriptor{
		ID:                  ProviderGoogleSlides,
		Aliases:             []string{"google-slides", "google-flow", "google-vids", "google-vids-image", "google slides", "google vids"},
		Origin:              ImageOriginGenerated,
		DefaultRightsStatus: "proprietary",
	})
	reg.Register(ProviderDescriptor{
		ID:                  ProviderNVIDIA,
		Aliases:             []string{"nvidia", "nvidia-local", "local-nim"},
		Origin:              ImageOriginGenerated,
		DefaultRightsStatus: "proprietary",
	})
	reg.Register(ProviderDescriptor{
		ID:                  ProviderFlux,
		Aliases:             []string{"flux-1-dev", "flux-1-schnell", "flux.1-schnell", "flux1-schnell", "flux-2-klein", "flux.2-klein-4b", "flux-2-klein-4b"},
		Origin:              ImageOriginGenerated,
		DefaultRightsStatus: "proprietary",
	})

	return reg
}

// exactOrAliasMatcher returns a matcher that accepts a source only when it
// exactly equals the canonical ID or one of the supplied aliases. This avoids
// false positives from substrings (e.g. "drive" inside an unrelated URL).
func exactOrAliasMatcher(id string, aliases []string) func(string) bool {
	return func(source string) bool {
		if source == id {
			return true
		}
		for _, a := range aliases {
			if source == a {
				return true
			}
		}
		return false
	}
}
