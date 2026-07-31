package script

import "context"

// AssetLocationChange is the durable state change produced by Drive
// reconciliation for a media asset. DriveFileID and DriveLink are both
// empty when reconciliation has invalidated the location.
type AssetLocationChange struct {
	AssetID     string
	DriveFileID string
	DriveLink   string
}

// AssetLocationCommitter is the narrow application port used after Drive
// verification. Infrastructure implementations must persist the SQLite
// location change and its Qdrant indexing outbox event atomically.
type AssetLocationCommitter interface {
	CommitAssetLocations(ctx context.Context, changes []AssetLocationChange) error
}
