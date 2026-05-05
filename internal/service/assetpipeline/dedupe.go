package assetpipeline

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// ExistingAssetQuery defines the query parameters for finding existing assets.
type ExistingAssetQuery struct {
	// ID is the asset ID
	ID string
	// FileHash is the MD5/SHA hash of the file
	FileHash string
	// DriveFileID is the Google Drive file ID
	DriveFileID string
	// Filename is the asset filename
	Filename string
	// Source is the asset source (youtube, artlist, voiceover, etc.)
	Source string
}

// AssetRecord represents a common asset record for lifecycle management.
// This is used to abstract domain-specific records (clips, voiceovers, etc.).
type AssetRecord struct {
	ID           string
	Name         string
	Filename     string
	Source       string
	MediaType    string
	DriveFileID  string
	DriveLink    string
	DownloadLink string
	FileHash     string
	LocalPath    string
	Status       string
	Error        string
	Metadata     string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// AssetRecordStore defines the interface for storing and querying asset records.
// This abstracts domain-specific repositories (clips, voiceovers, etc.).
type AssetRecordStore interface {
	// FindExisting finds an existing asset record by the given query.
	FindExisting(ctx context.Context, query ExistingAssetQuery) (*AssetRecord, error)
	// ListWithDriveFileID lists all records that have a non-empty Drive file ID.
	ListWithDriveFileID(ctx context.Context, source string) ([]*AssetRecord, error)
	// MarkDriveMissing marks a record as missing Drive file.
	MarkDriveMissing(ctx context.Context, id string) error
	// DeleteAssetRecord deletes an asset record by ID.
	DeleteAssetRecord(ctx context.Context, id string) error
}

// DedupeService provides duplicate checking for assets.
type DedupeService struct {
	store  AssetRecordStore
	policy  DuplicatePolicy
	log     *zap.Logger
}

// NewDedupeService creates a new DedupeService.
func NewDedupeService(store AssetRecordStore, policy DuplicatePolicy, log *zap.Logger) *DedupeService {
	return &DedupeService{
		store:  store,
		policy:  policy,
		log:     log,
	}
}

// CheckDuplicate checks if an asset already exists based on the duplicate policy.
// Returns the existing record if found, or nil if no duplicate exists.
func (s *DedupeService) CheckDuplicate(ctx context.Context, query ExistingAssetQuery) (*AssetRecord, error) {
	if !s.policy.Enabled {
		return nil, nil
	}

	if s.policy.CheckByDriveFileID && query.DriveFileID != "" {
		rec, err := s.store.FindExisting(ctx, ExistingAssetQuery{DriveFileID: query.DriveFileID})
		if err != nil {
			s.log.Warn("failed to check duplicate by DriveFileID", zap.Error(err))
		} else if rec != nil {
			s.log.Info("duplicate found by DriveFileID",
				zap.String("id", rec.ID),
				zap.String("drive_file_id", query.DriveFileID))
			return rec, nil
		}
	}

	if s.policy.CheckByHash && query.FileHash != "" {
		rec, err := s.store.FindExisting(ctx, ExistingAssetQuery{FileHash: query.FileHash})
		if err != nil {
			s.log.Warn("failed to check duplicate by hash", zap.Error(err))
		} else if rec != nil {
			s.log.Info("duplicate found by hash",
				zap.String("id", rec.ID),
				zap.String("hash", query.FileHash))
			return rec, nil
		}
	}

	if s.policy.CheckByFilename && query.Filename != "" {
		rec, err := s.store.FindExisting(ctx, ExistingAssetQuery{Filename: query.Filename, Source: query.Source})
		if err != nil {
			s.log.Warn("failed to check duplicate by filename", zap.Error(err))
		} else if rec != nil {
			s.log.Info("duplicate found by filename",
				zap.String("id", rec.ID),
				zap.String("filename", query.Filename))
			return rec, nil
		}
	}

	return nil, nil
}
