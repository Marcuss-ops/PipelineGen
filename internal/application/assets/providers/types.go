// Package providers defines the canonical Provider contract and the
// IO types used at the boundary between the assets application layer
// and the various source integrations (artlist, youtube, stock, ...).
//
// Spec: Agent 3 / PR 3E (consolidation + legacy elimination).
// Replaces PR 3C/3D foundation (commit a05e6580) at the new home
// internal/application/assets/providers/. Distinct from the prior
// foundation commit:
//
//   - typed SearchFilters (no map[string]any in domain contract);
//   - structured Candidate (no ProvisionalAsset, no ProviderMetadata);
//   - canonical home moved from internal/application/providers/.
//
// File map:
//
//   - types.go            IO types (Request / Response / Candidate)  [this file]
//   - provider.go         Provider interface + sentinel errors
//   - registry.go         one-shot Registry (deterministic All/ByCapability)
//   - registry_test.go    unit tests covering nil/empty/duplicate/concurrent/freeze
//   - adapters/<source>/  Provider implementations per source
package providers

import (
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// Capability is a tag describing what a Provider can do.
// Plain string enum keeps ByCapability cost at O(N) memcmp.
type Capability string

const (
	CapabilitySearch Capability = "search"
	CapabilityFetch  Capability = "fetch"
	CapabilityVideo  Capability = "video"
	CapabilityImage  Capability = "image"
	CapabilityMusic  Capability = "music"
	CapabilityVoice  Capability = "voice"
)

// SortMode declares the desired ordering for a Search.
// Concrete Providers translate this into their native request.
// Empty SortMode == "no preference" — adapter falls back to native default.
type SortMode string

const (
	SortByRelevance SortMode = "relevance"
	SortByNewest    SortMode = "newest"
	SortByOldest    SortMode = "oldest"
	SortByLongest   SortMode = "longest"
	SortByShortest  SortMode = "shortest"
)

// SearchFilters carries provider-specific predicates as a typed
// struct. Each Provider documents which fields it honours; unknown
// fields are silently ignored when the adapter cannot satisfy them.
//
// map[string]any was forbidden because it transformed the registry
// into another untyped container at the application layer
// (Agent 3 PR 3E constraints).
type SearchFilters struct {
	// PublishedAfter excludes results with PublishedAt < PublishedAfter.
	// Honours providers with a publication date in their metadata.
	PublishedAfter *time.Time
	// Sort declares the requested ordering.
	Sort SortMode
	// MediaTypes filters by media type. Empty list means "any".
	MediaTypes []asset.MediaType
	// MinDuration clamps out content shorter than this.
	MinDuration time.Duration
	// MaxDuration clamps out content longer than this.
	MaxDuration time.Duration
}

// SearchRequest is the canonical, provider-agnostic query payload.
// Concrete Providers translate this into their native request type
// at the adapter boundary.
type SearchRequest struct {
	// Query is the free-form term to search for.
	Query string
	// Limit caps the number of Candidate items returned.
	// 0 means "use provider default".
	Limit int
	// TopicOnly is a hint: when true, providers with a dedicated
	// topic path (e.g. YouTube SearchByTopic) prefer it.
	TopicOnly bool
	// Filters carries typed provider-specific predicates. Empty
	// SearchFilters == "no preference".
	Filters SearchFilters
}

// SearchResult is the canonical reply to a Search.
//
// NextPageToken is the empty string when the underlying source has
// no more results or lacks cursor support (artlist, youtube).
// Providers that paginate set NextPageToken to an opaque string
// echoed back via SearchRequest on the next call. The contract
// is intentionally symmetric: callers treat "" as "last page".
type SearchResult struct {
	Candidates    []Candidate
	NextPageToken string
}

// Candidate is a single search hit normalized at the boundary.
// The provider returns the literal representation of what it found;
// asset canonicalization happens downstream in the ingest use case.
type Candidate struct {
	SourceName   string
	SourceRef    string
	Title        string
	PreviewURL   string
	ThumbnailURL string
	MediaType    asset.MediaType
	Duration     time.Duration
	PublishedAt  *time.Time
	Score        float64
}

// FetchRequest drives the binary download for a known candidate.
// The Provider MUST NOT decide the destination itself: the caller
// resolves a destination via internal/core/asset.Resolver.
//
// SegmentStart and SegmentEnd are optional bounds for partial
// downloads (e.g. YouTube clip extraction). Both default to 0
// meaning "full asset". Providers that don't support segments
// ignore these fields silently.
type FetchRequest struct {
	AssetID       string
	SourceRef     string
	DestinationID string
	// SegmentStart is the optional start offset for partial downloads.
	// 0 means "from the beginning".
	SegmentStart time.Duration
	// SegmentEnd is the optional end offset. 0 means "to the end of the asset".
	SegmentEnd time.Duration
}

// FetchedAsset carries the result of a successful Fetch.
type FetchedAsset struct {
	// Asset is the canonical representation of the staged asset.
	Asset *asset.Asset
	// LocalPath is the on-disk location where the bytes were staged.
	// The caller is responsible for any subsequent upload.
	LocalPath string
	// FetchedAt timestamps when the binary became available.
	FetchedAt time.Time
	// Bytes is the size of the staged payload.
	Bytes int64
}
