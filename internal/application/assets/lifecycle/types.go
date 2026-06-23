package lifecycle

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assetop"
)

type AssetKind string

const (
	AssetKindVideo    AssetKind = "video"
	AssetKindAudio    AssetKind = "audio"
	AssetKindImage    AssetKind = "image"
	AssetKindDocument AssetKind = "document"
)

type FinalizeInput struct {
	ID        string
	Name      string
	Filename  string
	Kind      AssetKind
	Source    string
	SourceID  string
	Group     string
	Subfolder string

	LocalPath  string
	FolderID   string
	FolderPath string

	DriveLink    string
	DriveFileID  string
	DownloadLink string
	FileHash     string
	Metadata     string

	Duration     int
	RequireLocal bool
	RequireHash  bool
	RequireDrive bool
	VerifyDB     bool
}

type FinalizeResult struct {
	OK           bool
	Status       string
	FileHash     string
	ContentHash  string
	DriveLink    string
	DriveFileID  string
	DownloadLink string
	LocalPath    string
	Error        string
}

// AssetRecordStore defines the interface for asset record persistence.
// Method signatures use assetop.X types directly (no re-export aliases).
// The three impact adapters in internal/application/assets/ingest/
// (adapter_voiceover.go, adapter_clip.go, adapter_image.go) already
// reference `assetop.ExistingAssetQuery` and `assetop.AssetRecord` so
// dropping the prior `type ... = assetop.X` aliases from this file
// required NO consumer-side changes. See W16-PR4 PR description.
type AssetRecordStore interface {
	Upsert(ctx context.Context, rec *artifacts.MediaRecord) error
	Get(ctx context.Context, id string) (*artifacts.MediaRecord, error)
	FindExisting(ctx context.Context, query assetop.ExistingAssetQuery) (*assetop.AssetRecord, error)
	ListWithDriveFileID(ctx context.Context, source string) ([]*assetop.AssetRecord, error)
	MarkDriveMissing(ctx context.Context, id string) error
	DeleteAssetRecord(ctx context.Context, id string) error
}
