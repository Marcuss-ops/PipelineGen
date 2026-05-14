package lifecycle

import (
	"context"

	"velox/go-master/internal/core/assetop"
	"velox/go-master/internal/service/assetregistry"
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

// Use assetop types for compatibility
type ExistingAssetQuery = assetop.ExistingAssetQuery
type AssetRecord = assetop.AssetRecord

// AssetRecordStore defines the interface for asset record persistence
type AssetRecordStore interface {
	Upsert(ctx context.Context, rec *assetregistry.MediaRecord) error
	Get(ctx context.Context, id string) (*assetregistry.MediaRecord, error)
	FindExisting(ctx context.Context, query ExistingAssetQuery) (*AssetRecord, error)
	ListWithDriveFileID(ctx context.Context, source string) ([]*AssetRecord, error)
	MarkDriveMissing(ctx context.Context, id string) error
	DeleteAssetRecord(ctx context.Context, id string) error
}
