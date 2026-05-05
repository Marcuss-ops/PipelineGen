package assetpipeline

import (
	"context"

	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"

	"velox/go-master/internal/service/assetindex"
	"velox/go-master/internal/service/mediaregistry"
)

// LifecycleService orchestrates the full asset lifecycle:
// duplicate checking, upload, persistence, and reconciliation.
type LifecycleService struct {
	store        AssetRecordStore
	dedupe       *DedupeService
	reconcile    *ReconcileService
	uploadPolicy UploadPolicy
	persistPolicy PersistPolicy
	registry     *mediaregistry.Registry
	assetIndex   *assetindex.Service
	log          *zap.Logger
}

// LifecycleConfig holds configuration for LifecycleService.
type LifecycleConfig struct {
	DuplicatePolicy DuplicatePolicy
	UploadPolicy   UploadPolicy
	PersistPolicy  PersistPolicy
	ReconcilePolicy ReconcilePolicy
}

// NewLifecycleService creates a new LifecycleService.
func NewLifecycleService(
	store AssetRecordStore,
	driveSvc *gdrive.Service,
	registry *mediaregistry.Registry,
	assetIndex *assetindex.Service,
	cfg LifecycleConfig,
	log *zap.Logger,
) *LifecycleService {
	dedupe := NewDedupeService(store, cfg.DuplicatePolicy, log)

	var reconcile *ReconcileService
	if cfg.ReconcilePolicy.Enabled {
		reconcile = NewReconcileService(store, driveSvc, cfg.ReconcilePolicy, log)
	}

	return &LifecycleService{
		store:        store,
		dedupe:       dedupe,
		reconcile:    reconcile,
		uploadPolicy: cfg.UploadPolicy,
		persistPolicy: cfg.PersistPolicy,
		registry:     registry,
		assetIndex:   assetIndex,
		log:          log,
	}
}

// ProcessAsset processes an asset through the lifecycle:
// 1. Check for duplicates
// 2. Upload to Drive (if needed)
// 3. Persist to databases
func (s *LifecycleService) ProcessAsset(ctx context.Context, input *FinalizeInput, fileHash string) (*FinalizeResult, error) {
	out := &FinalizeResult{
		OK:        false,
		Status:    "failed",
		LocalPath: input.LocalPath,
	}

	// Step 1: Check for duplicates
	if s.dedupe != nil && s.dedupe.policy.Enabled {
		query := ExistingAssetQuery{
			ID:          input.ID,
			FileHash:    fileHash,
			Filename:    input.Filename,
			Source:      input.Source,
		}

		existing, err := s.dedupe.CheckDuplicate(ctx, query)
		if err != nil {
			s.log.Warn("duplicate check failed", zap.Error(err))
		} else if existing != nil && s.dedupe.policy.SkipIfExists {
			// Duplicate found, skip processing
			out.OK = true
			out.Status = "skipped_duplicate"
			out.DriveLink = existing.DriveLink
			out.DownloadLink = existing.DownloadLink
			out.FileHash = existing.FileHash
			s.log.Info("skipping duplicate asset",
				zap.String("id", input.ID),
				zap.String("existing_id", existing.ID))
			return out, nil
		}
	}

	// Step 2: Upload to Drive (handled by Finalizer)
	// This is delegated to the existing Finalizer

	// Step 3: Persist to databases (handled by Finalizer)
	// This is delegated to the existing Finalizer

	out.OK = true
	out.Status = "processed"
	return out, nil
}

// Reconcile triggers reconciliation for a given source.
func (s *LifecycleService) Reconcile(ctx context.Context, source string) (int, error) {
	if s.reconcile == nil {
		return 0, nil
	}

	return s.reconcile.ReconcileDriveMissing(ctx, source)
}

// DefaultLifecycleConfig returns the default lifecycle configuration.
func DefaultLifecycleConfig() LifecycleConfig {
	return LifecycleConfig{
		DuplicatePolicy: DefaultDuplicatePolicy(),
		UploadPolicy:   DefaultUploadPolicy(),
		PersistPolicy:  DefaultPersistPolicy(),
		ReconcilePolicy: DefaultReconcilePolicy(),
	}
}
