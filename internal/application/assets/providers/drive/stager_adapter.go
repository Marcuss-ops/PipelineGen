// Package drive — stager_adapter.go (ART-002 P4.2, July 2026).
//
// DriveStager is a SKELETON adapter that implements assets.SourceStager
// for the SourceKind=SourceKindDrive slot in assets.SourceStagerRegistry.
//
// godlike/07 no-fake-availability disclosure:
// The Drive provider package has not yet been built (the
// internal/application/assets/providers/drive/ directory was empty
// before P4.2). The canonical Drive asset-fetcher will live in this
// package once the drive provider lands; in the meantime, this Stager
// exists only so the SourceStagerRegistry can resolve the Drive slot
// without returning ErrSourceKindUnknown to higher-level orchestrators.
//
// Note for the real implementation: Drive assets are already on Drive,
// so the "stage" semantic is a no-op (the file does not need to be
// downloaded). The real DriveStager will translate ref.URL into a
// drive.DriveFileRef and return a StagedAsset whose LocalPath points
// at the cached local copy (if any) or the Drive file ID directly.
// StageSource returns ErrDriveStagerNotImplemented (typed sentinel,
// reachable via errors.Is) so callers can branch on the failure mode
// rather than silently defaulting to a different kind.
//
// Cleanup is a no-op (Drive files are not removed by the stager).
//
// Forward-pointer: when the Drive provider package lands (tracked in
// architecture/current.yaml under the SourceStager wave), this
// skeleton will be replaced with a real adapter wrapping the
// canonical drive.Reader / drive.FileLifecycle ports.
package drive

import (
	"context"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
)

// Compile-time assertion: *DriveStager satisfies assets.SourceStager.
var _ assets.SourceStager = (*DriveStager)(nil)

// ErrDriveStagerNotImplemented is returned by DriveStager.StageSource
// for every call. Reachable via errors.Is from any caller seam.
//
// Per godlike/07: this sentinel is the typed "no fake availability"
// signal that the Drive provider package has not landed yet.
// Production callers MUST treat ErrDriveStagerNotImplemented as a
// hard failure (not a fallback trigger) and surface the error to
// the operator.
var ErrDriveStagerNotImplemented = errors.New("drive stager: not implemented (provider package not yet built; SKELETON per godlike/07)")

// DriveStager is the skeleton SourceStager adapter for the Drive kind.
// Holds no state (the real adapter will hold a drive.Reader reference
// when the provider package lands).
type DriveStager struct{}

// NewDriveStager returns a new DriveStager. No constructor arguments
// (skeleton: no dependencies to wire).
func NewDriveStager() *DriveStager {
	return &DriveStager{}
}

// StageSource returns ErrDriveStagerNotImplemented for every call.
// The real implementation will look up the Drive file via drive.Reader
// and return a StagedAsset whose LocalPath is the cached copy or the
// Drive file ID (TBD when the drive provider lands).
func (s *DriveStager) StageSource(ctx context.Context, ref assets.SourceRef) (*assets.StagedAsset, error) {
	_ = ctx
	return nil, fmt.Errorf("%w: url=%q", ErrDriveStagerNotImplemented, ref.URL)
}

// Cleanup is a no-op for the Drive skeleton (Drive files are not
// removed by the stager). The real implementation will also be a
// no-op (Drive lifecycle is owned by drive.FileLifecycle, not the
// stager).
func (s *DriveStager) Cleanup(ctx context.Context, staged *assets.StagedAsset) error {
	_ = ctx
	_ = staged
	return nil
}
