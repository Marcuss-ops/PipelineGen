package asset

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
)

// ProviderRegistryVersion is the canonical version of the provider
// descriptor registry. Bump it whenever the registry shape, provider
// set or matching rules change so downstream fingerprints can
// invalidate stale cached decisions.
const ProviderRegistryVersion = media.VersionProviderRegistry

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
	// DefaultAuthor is the canonical attribution to use when none is
	// provided by the source. It centralises per-provider attribution
	// (e.g. "Wikipedia Contributors") so callers do not hardcode it.
	DefaultAuthor string
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

// clone returns a shallow copy of the descriptor with a private copy of
// the Aliases slice, so callers cannot mutate internal registry state.
func (d ProviderDescriptor) clone() ProviderDescriptor {
	if len(d.Aliases) > 0 {
		aliases := make([]string, len(d.Aliases))
		copy(aliases, d.Aliases)
		d.Aliases = aliases
	}
	return d
}

// ProviderRegistry is the canonical registry of provider descriptors.
// A single registry instance centralises provider recognition and
// replaces ad-hoc string matching.
//
// The registry is thread-safe and can be sealed after bootstrap so that
// no further mutation is possible. Reads always return descriptors by
// value to prevent callers from mutating internal state.
type ProviderRegistry struct {
	mu          sync.RWMutex
	descriptors []ProviderDescriptor
	byID        map[ImageProvider]ProviderDescriptor
	sealed      bool
}

// NewProviderRegistry returns an empty, unsealed registry.
func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{
		descriptors: make([]ProviderDescriptor, 0),
		byID:        make(map[ImageProvider]ProviderDescriptor),
	}
}

// ErrProviderIDEmpty is returned when a descriptor with an empty ID is
// registered.
var ErrProviderIDEmpty = fmt.Errorf("provider descriptor ID is empty")

// ErrProviderDuplicate is returned when the same ID is registered twice.
var ErrProviderDuplicate = fmt.Errorf("provider descriptor ID already registered")

// ErrProviderRegistrySealed is returned when Register is called after
// the registry has been sealed.
var ErrProviderRegistrySealed = fmt.Errorf("provider registry is sealed")

// Register adds a descriptor to the registry. The same ID can be
// registered only once. Registration fails closed for empty IDs,
// duplicate IDs, or after the registry has been sealed.
func (r *ProviderRegistry) Register(d ProviderDescriptor) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.sealed {
		return ErrProviderRegistrySealed
	}
	if d.ID == "" {
		return ErrProviderIDEmpty
	}
	if _, ok := r.byID[d.ID]; ok {
		return ErrProviderDuplicate
	}
	cloned := d.clone()
	r.descriptors = append(r.descriptors, cloned)
	r.byID[d.ID] = cloned
	return nil
}

// ByID returns the descriptor with the given ID. The second result is
// true iff the descriptor exists.
func (r *ProviderRegistry) ByID(id ImageProvider) (ProviderDescriptor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	d, ok := r.byID[id]
	if !ok {
		return ProviderDescriptor{}, false
	}
	return d.clone(), true
}

// Match returns the first descriptor whose MatchSource function or
// aliases identify the supplied source string. Matching is exact or
// hostname-aware; broad substring matching is not used.
func (r *ProviderRegistry) Match(source string) (ProviderDescriptor, bool) {
	if source == "" {
		return ProviderDescriptor{}, false
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	lower := strings.ToLower(strings.TrimSpace(source))
	key, isHost := hostKey(lower)

	for _, d := range r.descriptors {
		if d.MatchSource != nil {
			if d.MatchSource(lower) {
				return d, true
			}
			continue
		}
		if matchesKey(key, isHost, string(d.ID), d.Aliases) {
			return d.clone(), true
		}
	}
	return ProviderDescriptor{}, false
}

// All returns a defensive copy of the registered descriptors.
func (r *ProviderRegistry) All() []ProviderDescriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]ProviderDescriptor, len(r.descriptors))
	for i, d := range r.descriptors {
		out[i] = d.clone()
	}
	return out
}

// Seal marks the registry as immutable. After Seal returns, any call
// to Register fails with ErrProviderRegistrySealed.
func (r *ProviderRegistry) Seal() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sealed = true
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
	mustRegister := func(d ProviderDescriptor) {
		if err := reg.Register(d); err != nil {
			panic(fmt.Sprintf("provider registry bootstrap: register %q: %v", d.ID, err))
		}
	}

	// Retrieved providers.
	mustRegister(ProviderDescriptor{
		ID:                  ProviderWikipedia,
		Aliases:             []string{"wikipedia.org", "wikipedia"},
		Origin:              ImageOriginRetrieved,
		DefaultRightsStatus: "CC-BY-SA-4.0",
		DefaultAuthor:       "Wikipedia Contributors",
		LicenseResolver:     providerLicenseResolver("CC-BY-SA-4.0"),
		Materializer:        providerMaterializer(ProviderWikipedia, ImageOriginRetrieved),
		MetadataMapper:      canonicalMetadataMapper(ProviderWikipedia, ImageOriginRetrieved),
	})
	mustRegister(ProviderDescriptor{
		ID:                  ProviderWikimediaCommons,
		Aliases:             []string{"commons.wikimedia.org", "wikimedia_commons"},
		Origin:              ImageOriginRetrieved,
		DefaultRightsStatus: "unknown",
		DefaultAuthor:       "Unknown",
		LicenseResolver:     providerLicenseResolver("unknown"),
		Materializer:        providerMaterializer(ProviderWikimediaCommons, ImageOriginRetrieved),
		MetadataMapper:      canonicalMetadataMapper(ProviderWikimediaCommons, ImageOriginRetrieved),
	})
	mustRegister(ProviderDescriptor{
		ID:                  ProviderDuckDuckGo,
		Aliases:             []string{"duckduckgo"},
		Origin:              ImageOriginRetrieved,
		DefaultRightsStatus: "unknown",
		DefaultAuthor:       "Unknown",
		LicenseResolver:     providerLicenseResolver("unknown"),
		Materializer:        providerMaterializer(ProviderDuckDuckGo, ImageOriginRetrieved),
		MetadataMapper:      canonicalMetadataMapper(ProviderDuckDuckGo, ImageOriginRetrieved),
	})
	mustRegister(ProviderDescriptor{
		ID:                  ProviderSearXNG,
		Aliases:             []string{"searxng"},
		Origin:              ImageOriginRetrieved,
		DefaultRightsStatus: "unknown",
		DefaultAuthor:       "Unknown",
		LicenseResolver:     providerLicenseResolver("unknown"),
		Materializer:        providerMaterializer(ProviderSearXNG, ImageOriginRetrieved),
		MetadataMapper:      canonicalMetadataMapper(ProviderSearXNG, ImageOriginRetrieved),
	})
	mustRegister(ProviderDescriptor{
		ID:                  ProviderDrive,
		MatchSource:         exactOrAliasMatcher(string(ProviderDrive), []string{"drive", "drive.google.com"}),
		Origin:              ImageOriginRetrieved,
		DefaultRightsStatus: "unknown",
		DefaultAuthor:       "Unknown",
		LicenseResolver:     providerLicenseResolver("unknown"),
		Materializer:        providerMaterializer(ProviderDrive, ImageOriginRetrieved),
		MetadataMapper:      canonicalMetadataMapper(ProviderDrive, ImageOriginRetrieved),
	})
	mustRegister(ProviderDescriptor{
		ID:                  ProviderUpload,
		MatchSource:         exactOrAliasMatcher(string(ProviderUpload), []string{"upload"}),
		Origin:              ImageOriginUploaded,
		DefaultRightsStatus: "unknown",
		DefaultAuthor:       "Unknown",
		LicenseResolver:     providerLicenseResolver("unknown"),
		Materializer:        providerMaterializer(ProviderUpload, ImageOriginUploaded),
		MetadataMapper:      canonicalMetadataMapper(ProviderUpload, ImageOriginUploaded),
	})

	// Generated providers.
	mustRegister(ProviderDescriptor{
		ID:                  ProviderGoogleSlides,
		Aliases:             []string{"google-slides", "google-flow", "google-vids", "google-vids-image", "google slides", "google vids"},
		Origin:              ImageOriginGenerated,
		DefaultRightsStatus: "proprietary",
		DefaultAuthor:       "PipelineGen",
		LicenseResolver:     providerLicenseResolver("proprietary"),
		Materializer:        providerMaterializer(ProviderGoogleSlides, ImageOriginGenerated),
		MetadataMapper:      canonicalMetadataMapper(ProviderGoogleSlides, ImageOriginGenerated),
	})
	mustRegister(ProviderDescriptor{
		ID:                  ProviderNVIDIA,
		Aliases:             []string{"nvidia", "nvidia-local", "local-nim"},
		Origin:              ImageOriginGenerated,
		DefaultRightsStatus: "proprietary",
		DefaultAuthor:       "PipelineGen",
		LicenseResolver:     providerLicenseResolver("proprietary"),
		Materializer:        providerMaterializer(ProviderNVIDIA, ImageOriginGenerated),
		MetadataMapper:      canonicalMetadataMapper(ProviderNVIDIA, ImageOriginGenerated),
	})
	mustRegister(ProviderDescriptor{
		ID:                  ProviderFlux,
		Aliases:             []string{"flux-1-dev", "flux-1-schnell", "flux.1-schnell", "flux1-schnell", "flux-2-klein", "flux.2-klein-4b", "flux-2-klein-4b"},
		Origin:              ImageOriginGenerated,
		DefaultRightsStatus: "proprietary",
		DefaultAuthor:       "PipelineGen",
		LicenseResolver:     providerLicenseResolver("proprietary"),
		Materializer:        providerMaterializer(ProviderFlux, ImageOriginGenerated),
		MetadataMapper:      canonicalMetadataMapper(ProviderFlux, ImageOriginGenerated),
	})

	reg.Seal()
	return reg
}

// providerLicenseResolver returns a LicenseResolver that honours an
// explicit license key in the raw metadata when present and falls back
// to the provider's canonical default license otherwise.
func providerLicenseResolver(defaultLicense string) func(context.Context, Metadata) (string, error) {
	return func(_ context.Context, raw Metadata) (string, error) {
		if raw != nil {
			if v, ok := raw["license"].(string); ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v), nil
			}
		}
		return defaultLicense, nil
	}
}

// providerMaterializer materialises an asset by keeping the original
// source URL and stamping canonical provider/origin metadata.
func providerMaterializer(id ImageProvider, origin ImageOrigin) func(context.Context, MaterializeRequest) (MaterializeResult, error) {
	return func(_ context.Context, req MaterializeRequest) (MaterializeResult, error) {
		meta := Metadata{
			"provider": string(id),
			"origin":   string(origin),
		}
		if req.Metadata != nil {
			for k, v := range req.Metadata {
				if k != "provider" && k != "origin" {
					meta[k] = v
				}
			}
		}
		return MaterializeResult{
			AssetID:      req.AssetID,
			CanonicalURL: req.SourceURL,
			Metadata:     meta,
		}, nil
	}
}

// canonicalMetadataMapper normalises a raw metadata map so that the
// canonical provider and origin keys always reflect the registry entry.
func canonicalMetadataMapper(id ImageProvider, origin ImageOrigin) func(raw Metadata) (Metadata, error) {
	return func(raw Metadata) (Metadata, error) {
		out := Metadata{}
		if raw != nil {
			for k, v := range raw {
				out[k] = v
			}
		}
		out["provider"] = string(id)
		out["origin"] = string(origin)
		return out, nil
	}
}

// exactOrAliasMatcher returns a matcher that accepts a source only when it
// exactly equals the canonical ID or one of the supplied aliases. This avoids
// false positives from substrings (e.g. "drive" inside an unrelated URL).
func exactOrAliasMatcher(id string, aliases []string) func(string) bool {
	return func(source string) bool {
		key, isHost := hostKey(source)
		return matchesKey(key, isHost, id, aliases)
	}
}

// hostKey returns the comparison key for a source string. If the source
// is a valid URL with a host, it returns the lowercased host (without
// port) and true. Otherwise it returns the original lowercased string
// and false.
func hostKey(source string) (string, bool) {
	if strings.Contains(source, "://") {
		if u, err := url.Parse(source); err == nil && u.Host != "" {
			host := strings.ToLower(u.Host)
			if idx := strings.IndexByte(host, ':'); idx >= 0 {
				host = host[:idx]
			}
			return host, true
		}
	}
	return source, false
}

// matchesKey reports whether key matches id or any of aliases. For
// hostname keys, it accepts exact matches and subdomain matches (so
// "en.wikipedia.org" matches alias "wikipedia.org"). For non-hostname
// keys, only exact matches are accepted.
func matchesKey(key string, isHost bool, id string, aliases []string) bool {
	idLower := strings.ToLower(id)
	if key == idLower {
		return true
	}
	for _, alias := range aliases {
		if alias == "" {
			continue
		}
		aliasLower := strings.ToLower(alias)
		if key == aliasLower {
			return true
		}
		if isHost {
			// "en.wikipedia.org" matches "wikipedia.org"
			if strings.HasSuffix(key, "."+aliasLower) {
				return true
			}
		}
	}
	return false
}
