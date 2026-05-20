package lifecycle

import (
	"context"

	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"

	"velox/go-master/internal/core/assetop"
	"velox/go-master/internal/media/assetindex"
	"velox/go-master/internal/media/assetregistry"
)

// Service orchestrates the full asset lifecycle:
// duplicate checking, upload, persistence, and reconciliation.
type Service struct {
	store         AssetRecordStore
	dedupe        *assetop.DedupeService
	reconcile     *assetop.ReconcileService
	uploader      *assetop.Uploader
	finalizer     *assetregistry.Finalizer
	uploadPolicy  assetop.UploadPolicy
	persistPolicy assetop.PersistPolicy
	registry      assetregistry.Registry
	assetIndex    *assetindex.Service
	log           *zap.Logger
}

// Config holds configuration for Service.
type Config struct {
	DuplicatePolicy assetop.DuplicatePolicy
	UploadPolicy    assetop.UploadPolicy
	PersistPolicy   assetop.PersistPolicy
	ReconcilePolicy assetop.ReconcilePolicy
}

// NewService creates a new lifecycle Service.
func NewService(
	store AssetRecordStore,
	driveSvc *gdrive.Service,
	registry assetregistry.Registry,
	assetIndex *assetindex.Service,
	finalizer *assetregistry.Finalizer,
	cfg Config,
	log *zap.Logger,
) *Service {
	dedupe := assetop.NewDedupeService(store, cfg.DuplicatePolicy, log)

	var reconcile *assetop.ReconcileService
	if cfg.ReconcilePolicy.Enabled {
		reconcile = assetop.NewReconcileService(store, driveSvc, cfg.ReconcilePolicy, log)
	}

	return &Service{
		store:         store,
		dedupe:        dedupe,
		reconcile:     reconcile,
		uploader:      assetop.NewUploader(driveSvc, log),
		finalizer:     finalizer,
		uploadPolicy:  cfg.UploadPolicy,
		persistPolicy: cfg.PersistPolicy,
		registry:      registry,
		assetIndex:    assetIndex,
		log:           log,
	}
}

// ProcessAsset processes an asset through the lifecycle:
// 1. Check for duplicates
// 2. Upload to Drive (if needed)
// 3. Persist to databases
func (s *Service) ProcessAsset(ctx context.Context, input *FinalizeInput, fileHash string) (*FinalizeResult, error) {
	out := &FinalizeResult{
		OK:        false,
		Status:    "failed",
		LocalPath: input.LocalPath,
	}

	// Step 1: Check for duplicates
	if s.dedupe != nil && s.dedupe.Policy().Enabled {
		query := assetop.ExistingAssetQuery{
			ID:       input.ID,
			FileHash: fileHash,
			Filename: input.Filename,
			Source:   input.Source,
		}

		existing, err := s.dedupe.CheckDuplicate(ctx, query)
		if err != nil {
			s.log.Warn("duplicate check failed", zap.Error(err))
		} else if existing != nil && s.dedupe.Policy().SkipIfExists {
			out.OK = true
			out.Status = "skipped_duplicate"
			out.DriveLink = existing.DriveLink
			out.DriveFileID = existing.DriveFileID
			out.DownloadLink = existing.DownloadLink
			out.FileHash = existing.FileHash
			s.log.Info("skipping duplicate asset",
				zap.String("id", input.ID),
				zap.String("existing_id", existing.ID))
			return out, nil
		}
	}

	// Step 2: Upload to Drive (if policy enabled and not already uploaded)
	driveLink := input.DriveLink
	driveFileID := input.DriveFileID
	downloadLink := input.DownloadLink

	if s.uploadPolicy.Enabled && driveLink == "" && input.FolderID != "" {
		if s.uploader != nil {
			link, dlink, fileID, err := s.uploader.Upload(ctx, input.LocalPath, input.FolderID)
			if err != nil {
				s.log.Warn("drive upload failed", zap.Error(err))
			} else {
				driveLink = link
				downloadLink = dlink
				driveFileID = fileID
				s.log.Info("asset uploaded to drive",
					zap.String("id", input.ID),
					zap.String("file_id", fileID))
			}
		}
	}

	// Step 3: Persist to databases (if policy enabled)
	if s.persistPolicy.SaveToAssetRegistry && s.finalizer != nil {
		rec := &assetregistry.MediaRecord{
			ID:           input.ID,
			Name:         input.Name,
			Filename:     input.Filename,
			Source:       input.Source,
			MediaType:    string(input.Kind),
			FolderID:     input.FolderID,
			FolderPath:   input.FolderPath,
			Group:        input.Group,
			LocalPath:    input.LocalPath,
			DriveLink:    driveLink,
			DriveFileID:  driveFileID,
			DownloadLink: downloadLink,
			FileHash:     fileHash,
			ContentHash:  fileHash,
			Metadata:     input.Metadata,
			Status:       "processed",
			Duration:     input.Duration,
			SourceID:     input.SourceID,
			Subfolder:    input.Subfolder,
		}

		finalizeOpts := assetregistry.FinalizeOptions{
			RequireLocal: false,
			RequireHash:  false,
			RequireDrive: driveLink != "",
			VerifyDB:     true,
		}

		finalResult, err := s.finalizer.Finalize(ctx, rec, finalizeOpts)
		if err != nil {
			return out, err
		}
		if !finalResult.OK {
			out.Error = finalResult.Error
			return out, nil
		}
	}

	out.OK = true
	out.Status = "processed"
	out.DriveLink = driveLink
	out.DriveFileID = driveFileID
	out.DownloadLink = downloadLink
	out.FileHash = fileHash
	return out, nil
}

// CheckDuplicate performs a read-only duplicate check for an asset.
func (s *Service) CheckDuplicate(ctx context.Context, input *FinalizeInput, fileHash string) (*FinalizeResult, error) {
	out := &FinalizeResult{
		OK:        false,
		Status:    "failed",
		LocalPath: input.LocalPath,
	}

	if s.dedupe == nil || !s.dedupe.Policy().Enabled {
		out.OK = true
		out.Status = "no_dedupe_policy"
		return out, nil
	}

	query := assetop.ExistingAssetQuery{
		ID:       input.ID,
		FileHash: fileHash,
		Filename: input.Filename,
		Source:   input.Source,
	}

	existing, err := s.dedupe.CheckDuplicate(ctx, query)
	if err != nil {
		return out, err
	}
	if existing != nil && s.dedupe.Policy().SkipIfExists {
		out.OK = true
		out.Status = "would_skip_duplicate"
		out.DriveLink = existing.DriveLink
		out.DriveFileID = existing.DriveFileID
		out.DownloadLink = existing.DownloadLink
		out.FileHash = existing.FileHash
		return out, nil
	}
	out.OK = true
	out.Status = "would_process"
	return out, nil
}

// Reconcile triggers reconciliation for a given source.
func (s *Service) Reconcile(ctx context.Context, source string) (int, error) {
	if s.reconcile == nil {
		return 0, nil
	}
	return s.reconcile.ReconcileDriveMissing(ctx, source)
}

// DefaultConfig returns the default lifecycle configuration.
func DefaultConfig() Config {
	return Config{
		DuplicatePolicy: assetop.DefaultDuplicatePolicy(),
		UploadPolicy:    assetop.DefaultUploadPolicy(),
		PersistPolicy:   assetop.DefaultPersistPolicy(),
		ReconcilePolicy: assetop.DefaultReconcilePolicy(),
	}
}
