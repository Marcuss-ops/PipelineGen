package assets

import "context"

// Repository is the canonical CRUD contract for asset persistence.
// Consumer-owned: each caller declares what it needs.
type Repository interface {
	Upsert(ctx context.Context, asset *Asset) error
	Get(ctx context.Context, id string) (*Asset, error)
	List(ctx context.Context, filter Filter) ([]*Asset, error)
	Count(ctx context.Context, filter Filter) (int64, error)
	SoftDelete(ctx context.Context, id string) error
	Restore(ctx context.Context, id string) error
	HardDelete(ctx context.Context, id string) error
}

// LocationRepository is the contract for asset_locations persistence.
type LocationRepository interface {
	Upsert(ctx context.Context, loc *Location) error
	GetPrimary(ctx context.Context, assetID string) (*Location, error)
	ListByAsset(ctx context.Context, assetID string) ([]*Location, error)
	SetPrimary(ctx context.Context, assetID string, kind LocationKind) error
	Delete(ctx context.Context, assetID string, kind LocationKind) error
	DeleteAll(ctx context.Context, assetID string) error
}

// ProcessingRepository is the contract for asset_processing persistence.
type ProcessingRepository interface {
	Start(ctx context.Context, assetID, step string) error
	Complete(ctx context.Context, assetID, step string) error
	Fail(ctx context.Context, assetID, step, errMsg string) error
	Transition(ctx context.Context, assetID, step string, from, to ProcessingStatus) error
	Get(ctx context.Context, assetID, step string) (*ProcessingRecord, error)
	GetByAssetID(ctx context.Context, assetID string) ([]ProcessingRecord, error)
	GetFailed(ctx context.Context) ([]ProcessingRecord, error)
	Delete(ctx context.Context, assetID, step string) error
	DeleteAll(ctx context.Context, assetID string) error
}

// VersionRepository is the contract for asset_versions persistence.
type VersionRepository interface {
	GetCurrent(ctx context.Context, assetID string) (*Version, error)
	List(ctx context.Context, assetID string) ([]Version, error)
	Append(ctx context.Context, v *Version) error
}

// Store is the high-level unified CRUD repository for assets (with nested entities).
type Store interface {
	Get(ctx context.Context, id string) (*Details, error)
	List(ctx context.Context, filter Filter) ([]*Summary, error)
	Save(ctx context.Context, details *Details) error
	Delete(ctx context.Context, id string) error
}

// ArtifactStore manages metadata for stored artifacts.
type ArtifactStore interface {
	Create(ctx context.Context, a *Artifact) error
	Get(ctx context.Context, id string) (*Artifact, error)
	GetBySHA256(ctx context.Context, sha256 string) (*Artifact, error)
	UpdateStatus(ctx context.Context, id string, status ArtifactStatus, sha256 string, sizeBytes int64) error
	ListByJob(ctx context.Context, jobID string) ([]*Artifact, error)
}

// DeliveryStore manages delivery records.
type DeliveryStore interface {
	Create(ctx context.Context, d *Delivery) error
	Get(ctx context.Context, id string) (*Delivery, error)
	Update(ctx context.Context, d *Delivery) error
	ListPending(ctx context.Context) ([]*Delivery, error)
}
