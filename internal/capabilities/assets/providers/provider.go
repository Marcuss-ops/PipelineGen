package assets

import (
	"context"
)

// A Provider is split along capability boundaries (Interface
// Segregation Principle):
//
//   - Provider: the minimal base — Name + Capabilities. Every
//     source integration satisfies this.
//   - SearchProvider: adds Search(). Search-only sources satisfy
//     this AND nothing else.
//   - FetchProvider: adds Fetch(). Fetch-only sources satisfy
//     this AND nothing else.
//
// A source that does both implements both SearchProvider and
// FetchProvider (Go satisfies both because each embeds Provider).
//
// No source currently implements FetchProvider — the contract is
// reserved for the post-Stock wave so that the channel-monitor
// YouTube fetch path and Stock's binary delivery have a stable
// target. Adapters that do NOT download (artlist: no public
// fetch binary path; youtube: download lives in channel-monitor)
// satisfy only SearchProvider and omit CapabilityFetch from
// Capabilities().

// Provider is the canonical minimal contract every source
// integration must implement: identify itself + advertise its
// capabilities.
//
// A Provider alone is not queryable — it just declares identity
// and intent. To run a search, register as a SearchProvider. To
// download, register as a FetchProvider. Composable sources
// implement both.
//
// A Provider MUST NOT:
//
//   - write into Qdrant;
//   - decide asset lifecycle transitions;
//   - create Google Docs or other cross-domain side-effects;
//   - import any HTTP API handler package;
//   - execute business workflow that crosses its bounded context.
type Provider interface {
	// Name returns the human-readable provider identifier
	// (e.g. "artlist", "youtube", "stock"). Must be unique within a
	// Registry. Must be stable across calls. Must NOT be empty
	// (Registry.Register returns ErrEmptyName otherwise).
	Name() string

	// Capabilities advertises what this provider can do.
	// Used by Registry.ByCapability() and by callers that want to
	// filter providers by what they support.
	Capabilities() []Capability
}

// SearchProvider is a Provider that can return Candidates for a
// query. Adapters that only fetch (no live search) must NOT
// implement this interface.
//
// Artlist, YouTube and Stock implement SearchProvider (Stock was
// added August 2026 so it appears in the unified search discovery
// universe).
type SearchProvider interface {
	Provider

	// Search returns up to req.Limit candidates matching req.Query.
	// NextPageToken in the result is non-empty iff the underlying
	// source paginates; providers without a cursor (artlist,
	// youtube) always return NextPageToken == "".
	//
	// The returned slice is owned by the implementation and must
	// never be mutated by the caller.
	Search(ctx context.Context, req SearchRequest) (SearchResult, error)
}

// FetchProvider is a Provider that can download a known asset
// into a staging location.
//
// No adapter currently implements this interface — see the file
// preamble for context. The contract is intentionally retained so
// the upcoming channel-monitor YouTube fetch path and Stock's
// binary delivery have a stable target.
type FetchProvider interface {
	Provider

	// Fetch downloads the asset identified by req into a staging
	// location. Returns FetchedAsset.LocalPath on success. The
	// caller is responsible for any subsequent upload.
	Fetch(ctx context.Context, req FetchRequest) (*FetchedAsset, error)
}
