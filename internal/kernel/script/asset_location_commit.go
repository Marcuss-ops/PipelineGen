package script

import (
)

import "context"

// AssetLocationChange is the durable state change produced by Drive
// reconciliation for a media asset. DriveLink is empty when the location
// is unusable; DriveFileID is retained when known so operators can diagnose
// or republish the asset without losing the original Drive identity.
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
