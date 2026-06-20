package providers

import (
	"context"
	"errors"
)

// ErrFetchNotImplemented is returned by a Provider that does not
// implement the Fetch operation. Callers can use errors.Is to detect
// the gap and fall back to the legacy download pipeline.
var ErrFetchNotImplemented = errors.New("providers: fetch not implemented")

// Provider is the canonical contract every source integration must
// implement. The interface is held deliberately minimal (search +
// fetch). Capabilities() advertises what the implementation supports
// so callers can dispatch by capability instead of switch-on-source
// (the pattern AGENTS.md §Pattern 7 outlaws).
//
// A Provider MUST NOT:
//   - write into Qdrant;
//   - decide asset lifecycle transitions;
//   - create Google Docs or other cross-domain side-effects;
//   - import any HTTP API handler package;
//   - execute business workflow that crosses its bounded context.
type Provider interface {
	// Name returns the human-readable provider identifier
	// (e.g. "artlist", "youtube", "stock"). Must be unique within a
	// Registry. Must be stable across calls.
	Name() string

	// Capabilities advertises what this provider can do.
	// Used by Registry.ByCapability() and by callers that want to
	// filter providers by what they support.
	Capabilities() []Capability

	// Search returns up to req.Limit candidates matching req.Query.
	// Implementations translate req.TopicOnly and req.Filters into
	// their native request. The returned slice is owned by the
	// implementation and must never be mutated by the caller.
	Search(ctx context.Context, req SearchRequest) ([]Candidate, error)

	// Fetch downloads the asset identified by req into a staging
	// location. Returns providers.ErrFetchNotImplemented if the
	// provider does not support the operation (callers fall back).
	Fetch(ctx context.Context, req FetchRequest) (*FetchedAsset, error)
}
