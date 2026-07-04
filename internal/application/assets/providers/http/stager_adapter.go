// Package httpstager — stager_adapter.go (ART-002 P4.2, July 2026).
//
// HTTPStager is a SKELETON adapter that implements assets.SourceStager
// for the SourceKind=SourceKindHTTP slot in assets.SourceStagerRegistry.
//
// Package-name note (godlike/06 forward-pointer): the package is named
// `httpstager` (not `http`) to avoid shadowing the stdlib `net/http`
// when the real implementation lands and needs `import "net/http"`.
// The directory path stays `internal/application/assets/providers/http/`
// for routing; only the Go package declaration differs.
//
// godlike/07 no-fake-availability disclosure:
// The HTTP provider package has not yet been built (the
// internal/application/assets/providers/http/ directory was empty
// before P4.2). The canonical HTTP asset-fetcher will live in this
// package once the http provider lands; in the meantime, this Stager
// exists only so the SourceStagerRegistry can resolve the HTTP slot
// without returning ErrSourceKindUnknown to higher-level orchestrators
// that may already dispatch against all 5 canonical kinds.
//
// StageSource returns ErrHTTPStagerNotImplemented (typed sentinel,
// reachable via errors.Is) so callers can branch on the failure mode
// rather than silently defaulting to a different kind. Cleanup is a
// no-op (the underlying HTTP fetch never staged a file).
//
// Forward-pointer: when the HTTP provider package lands (tracked in
// architecture/current.yaml under the SourceStager wave), this
// skeleton will be replaced with a real adapter wrapping the
// canonical HTTP Downloader port.
package httpstager

import (
	"context"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
)

// Compile-time assertion: *HTTPStager satisfies assets.SourceStager.
// Matches the canonical ArtlistStager + YouTubeStager precedent.
var _ assets.SourceStager = (*HTTPStager)(nil)

// ErrHTTPStagerNotImplemented is returned by HTTPStager.StageSource
// for every call. Reachable via errors.Is from any caller seam; the
// message carries the failing ref so log-scanners can correlate the
// rejection with the SourceRef that triggered it.
//
// Per godlike/07: this sentinel is the typed "no fake availability"
// signal that the HTTP provider package has not landed yet. Production
// callers MUST treat ErrHTTPStagerNotImplemented as a hard failure
// (not a fallback trigger) and surface the error to the operator.
var ErrHTTPStagerNotImplemented = errors.New("http stager: not implemented (provider package not yet built; SKELETON per godlike/07)")

// HTTPStager is the skeleton SourceStager adapter for the HTTP kind.
// Holds no state (the real adapter will hold an *http.Client or a
// Downloader port when the provider package lands).
type HTTPStager struct{}

// NewHTTPStager returns a new HTTPStager. No constructor arguments
// (skeleton: no dependencies to wire).
func NewHTTPStager() *HTTPStager {
	return &HTTPStager{}
}

// StageSource returns ErrHTTPStagerNotImplemented for every call.
// The real implementation will download the asset at ref.URL via the
// canonical HTTP Downloader port (TBD when the http provider lands).
func (s *HTTPStager) StageSource(ctx context.Context, ref assets.SourceRef) (*assets.StagedAsset, error) {
	_ = ctx // ctx is reserved for the real implementation; skeleton takes no action
	return nil, fmt.Errorf("%w: url=%q", ErrHTTPStagerNotImplemented, ref.URL)
}

// Cleanup is a no-op for the HTTP skeleton (StageSource never produces
// a file to clean up). The real implementation will mirror the
// ArtlistStager Cleanup surface (remove the staged file or its
// parent temp dir, no-op on nil/empty).
func (s *HTTPStager) Cleanup(ctx context.Context, staged *assets.StagedAsset) error {
	_ = ctx
	_ = staged
	return nil
}
