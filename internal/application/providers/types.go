// Package providers defines the canonical Provider contract and the
// IO types used at the boundary between the assets application layer
// and the various source integrations (artlist, youtube, stock, ...).
//
// File map:
//
//   - provider.go         Provider interface + sentinel errors
//   - types.go            IO types (Request/Response/Candidate)         [this file]
//   - registry.go         one-shot Registry with Register/Freeze/ByCapability
//   - registry_test.go    unit tests for the registry semantics
//   - adapters/<source>/  Provider implementations per source
//
// Spec: Agent 3 / PRs 3A-3D (see docs/adr/). The provider abstraction
// replaces the switch-on-source pattern mandated by AGENTS.md Pattern 7.
package providers

import (
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/assets"
)

// Capability is a tag describing what a Provider can do. Kept as a
// plain string enum (not a struct) so ByCapability reduces to a
// memcmp per iteration.
type Capability string

const (
	CapabilitySearch Capability = "search"
	CapabilityFetch  Capability = "fetch"
	CapabilityVideo  Capability = "video"
	CapabilityImage  Capability = "image"
	CapabilityMusic  Capability = "music"
	CapabilityVoice  Capability = "voice"
)

// SearchRequest is the canonical, provider-agnostic query payload.
// Concrete Providers translate this into their native request type
// at the adapter boundary. Fields that a particular Provider does
// not support are documented on the adapter's Search method.
type SearchRequest struct {
	// Query is the free-form term to search for.
	Query string
	// Limit caps the number of Candidate items returned.
	// 0 means "use provider default".
	Limit int
	// PageToken is an opaque cursor returned in a prior response.
	// Empty on first call.
	PageToken string
	// TopicOnly is a hint: when true, providers with a dedicated
	// topic path (e.g. YouTube SearchByTopic) prefer it.
	TopicOnly bool
	// Filters carries provider-specific predicates as a shallow
	// key->value bag. Adapters validate the keys; unknown keys are
	// silently ignored when the adapter cannot satisfy them.
	Filters map[string]any
}

// Candidate is a single search hit normalized at the boundary.
// Provider-specific fields go into ProviderMetadata so downstream
// application code stays decoupled from concrete sources.
type Candidate struct {
	// AssetID is the canonical asset identifier in our system.
	// Empty when the candidate comes from an out-of-system source
	// and has not yet been ingested.
	AssetID string
	// Title is a human-readable label.
	Title string
	// SourceName is the Provider.Name() of the originating source.
	SourceName string
	// ProvisionalAsset is populated when the adapter already knows
	// how to mint a canonical *assets.Asset — used by fetch flows
	// that want to skip an extra re-resolve.
	ProvisionalAsset *assets.Asset
	// ProviderMetadata is opaque blob preserved verbatim for
	// downstream consumers (e.g. native preview URL, source label).
	ProviderMetadata map[string]any
}

// FetchRequest drives the binary download for a known candidate.
// The Provider MUST NOT decide the destination itself: the caller
// resolves a destination via internal/core/destination.Resolver.
type FetchRequest struct {
	// AssetID is the canonical identifier of the asset to fetch.
	// Empty means the caller relies on SourceRef alone.
	AssetID string
	// SourceRef is a provider-specific stable reference
	// (e.g. YouTube video ID, Artlist clip slug).
	SourceRef string
	// DestinationID identifies where the fetched bytes should land.
	// The provider must NOT make this decision itself; consumers
	// resolve it via internal/core/destination.Resolver.
	DestinationID string
}

// FetchedAsset carries the result of a successful Fetch.
type FetchedAsset struct {
	// Asset is the canonical representation of the staged asset.
	Asset *assets.Asset
	// LocalPath is the on-disk location where the bytes were staged.
	// The caller is responsible for any subsequent upload.
	LocalPath string
	// FetchedAt timestamps when the binary became available.
	FetchedAt time.Time
	// Bytes is the size of the staged payload.
	Bytes int64
}
