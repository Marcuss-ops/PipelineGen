package assetop

import "context"

// DriveFileState is the capability-owned port the reconciler uses to ask
// whether a Drive file still exists in a usable (non-trashed) state.
//
// The reconciler does not need a Drive client: it needs one question answered.
// Depending on the full *platform/drive.Reader made a storage provider part of
// the capability's compile graph and forced every test to build a Drive fake.
// The concrete adapter satisfies this interface structurally, so the
// composition root still passes drive.Reader unchanged; a unit test passes a
// two-line fake.
type DriveFileState interface {
	FileIsNotTrashed(ctx context.Context, fileID string) (bool, error)
}
