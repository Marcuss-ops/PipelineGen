package providers

import (
	"context"
)

// Per PR 3E no sentinel error is provided for "fetch unsupported".
// Adapters that do not declare CapabilityFetch MUST not advertise a
// recoverable fallback: callers route through
// Registry.ByCapability(CapabilityFetch) instead, and a direct
// interface call on such an adapter returns a plain unrecoverable
// error.

// Provider is the canonical contract every source integration must
// implement. Minimal: name + capabilities + search + fetch.
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

	// Search returns up to req.Limit candidates matching req.Query.
	// The returned slice is owned by the implementation and must
	// never be mutated by the caller.
	Search(ctx context.Context, req SearchRequest) ([]Candidate, error)

	// Fetch downloads the asset identified by req into a staging
	// location. Returns providers.ErrFetchNotImplemented if the
	// provider did not declare CapabilityFetch.
	Fetch(ctx context.Context, req FetchRequest) (*FetchedAsset, error)
}
