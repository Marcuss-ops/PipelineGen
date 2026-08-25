package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/assetop"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assetindex"
)

var (
	ErrFinalizerUnavailable      = errors.New("asset lifecycle: finalizer unavailable")
	ErrAssetStoreUnavailable     = errors.New("asset lifecycle: asset store unavailable")
	ErrReconcilerUnavailable     = errors.New("asset lifecycle: reconciler unavailable")
	ErrDrivePublisherUnavailable = errors.New("asset lifecycle: Drive publisher unavailable")
	ErrDriveUploadFailed         = errors.New("asset lifecycle: Drive upload failed")
	ErrFinalizationFailed        = errors.New("asset lifecycle: finalization failed")
)

type Service struct {
	store         AssetRecordStore
	dedupe        *assetop.DedupeService
	reconcile     *assetop.ReconcileService
	publisher     delivery.Publisher
	driveReader   drive.Reader
	finalizer     Finalizer
	uploadPolicy  assetop.UploadPolicy
	persistPolicy assetop.PersistPolicy
	registry      artifacts.Registry
	assetIndex    *assetindex.Service
	log           *zap.Logger
}

type Config struct {
	DuplicatePolicy assetop.DuplicatePolicy
	UploadPolicy    assetop.UploadPolicy
	PersistPolicy   assetop.PersistPolicy
	ReconcilePolicy assetop.ReconcilePolicy
}

type ServiceDeps struct {
	Store       AssetRecordStore
	Publisher   delivery.Publisher
	DriveReader drive.Reader
	Registry    artifacts.Registry
	AssetIndex  *assetindex.Service
	Finalizer   Finalizer
	Log         *zap.Logger
}

func NewService(deps ServiceDeps, cfg Config) *Service {
	if deps.Log == nil {
		deps.Log = zap.NewNop()
	}
	dedupe := assetop.NewDedupeService(deps.Store, cfg.DuplicatePolicy, deps.Log)
	var reconcile *assetop.ReconcileService
	if cfg.ReconcilePolicy.Enabled && deps.DriveReader != nil {
		reconcile = assetop.NewReconcileService(deps.Store, deps.DriveReader, cfg.ReconcilePolicy, deps.Log)
	}
	return &Service{store: deps.Store, dedupe: dedupe, reconcile: reconcile, publisher: deps.Publisher, driveReader: deps.DriveReader, finalizer: deps.Finalizer, uploadPolicy: cfg.UploadPolicy, persistPolicy: cfg.PersistPolicy, registry: deps.Registry, assetIndex: deps.AssetIndex, log: deps.Log}
}

// ProcessAsset makes the canonical SQLite-owned record before any Drive side
// effect. The first commit records PUBLISH_PENDING; the second records either
// PUBLISHED or PUBLISH_FAILED so failed delivery remains recoverable.
func (s *Service) ProcessAsset(ctx context.Context, input *FinalizeInput, fileHash string) (*FinalizeResult, error) {
	if input == nil {
		return nil, fmt.Errorf("%w: input is required", ErrFinalizationFailed)
	}
	out := &FinalizeResult{LocalPath: input.LocalPath, DriveLink: input.DriveLink, DriveFileID: input.DriveFileID, DownloadLink: input.DownloadLink, LegacyFileMD5: fileHash}
	if input.RequireDrive && input.LocalPath == "" {
		return out, fmt.Errorf("%w: local path is required", ErrDriveUploadFailed)
	}
	needsDelivery := input.RequireDrive || (s.uploadPolicy.Enabled && input.LocalPath != "")
	if needsDelivery && !s.persistPolicy.SaveToAssetRegistry {
		return out, fmt.Errorf("%w: Drive delivery requires canonical persistence", ErrFinalizerUnavailable)
	}
	if s.persistPolicy.SaveToAssetRegistry && s.finalizer == nil {
		return nil, ErrFinalizerUnavailable
	}

	if s.dedupe != nil && s.dedupe.Policy().Enabled {
		if s.store == nil {
			return out, ErrAssetStoreUnavailable
		}
		existing, err := s.dedupe.CheckDuplicate(ctx, assetop.ExistingAssetQuery{ID: input.ID, LegacyFileMD5: fileHash, Filename: input.Filename, Source: input.Source})
		if err != nil {
			return out, fmt.Errorf("%w: duplicate check: %w", ErrFinalizationFailed, err)
		}
		if existing != nil && s.dedupe.Policy().SkipIfExists {
			out.OK, out.Status, out.DeliveryStatus = true, "skipped_duplicate", asset.AssetPublishPublished
			out.DriveLink, out.DriveFileID, out.DownloadLink, out.LegacyFileMD5 = existing.DriveLink, existing.DriveFileID, existing.DownloadLink, existing.LegacyFileMD5
			return out, nil
		}
	}

	driveLink, driveFileID, downloadLink := input.DriveLink, input.DriveFileID, input.DownloadLink
	publishStatus := asset.AssetPublishLocalOnly
	rec := &artifacts.MediaRecord{ID: input.ID, Name: input.Name, Filename: input.Filename, Source: input.Source, MediaType: string(input.Kind), FolderID: input.FolderID, FolderPath: input.FolderPath, Group: input.Group, LocalPath: input.LocalPath, DriveLink: driveLink, DriveFileID: driveFileID, DownloadLink: downloadLink, LegacyFileMD5: fileHash, ContentHash: fileHash, Metadata: input.Metadata, Status: "delivery_pending", PublishStatus: asset.AssetPublishPending, Duration: input.Duration, SourceID: input.SourceID, Subfolder: input.Subfolder}

	if needsDelivery {
		if _, err := s.commitRecord(ctx, rec, false); err != nil {
			return out, fmt.Errorf("%w: pending commit: %w", ErrFinalizationFailed, err)
		}
	}

	if needsDelivery {
		if s.publisher == nil {
			publishStatus = asset.AssetPublishFailed
			rec.PublishStatus, rec.Error = publishStatus, ErrDrivePublisherUnavailable.Error()
			if _, err := s.commitRecord(ctx, rec, false); err != nil {
				return out, fmt.Errorf("%w: recovery commit: %w", ErrFinalizationFailed, err)
			}
			if input.RequireDrive {
				return out, ErrDrivePublisherUnavailable
			}
		} else {
			filename := input.Filename
			if filename == "" {
				filename = filepath.Base(input.LocalPath)
			}
			pubRes, pubErr := s.publisher.Publish(ctx, delivery.PublishRequest{Destination: input.Destination, LocalPath: input.LocalPath, Filename: filename, AssetID: input.ID, Group: input.Group, Subject: input.Subject, ProjectID: input.ProjectID, Language: input.Language, Style: input.Style})
			if pubErr != nil || pubRes == nil {
				publishStatus = asset.AssetPublishFailed
				rec.PublishStatus = publishStatus
				if pubErr != nil {
					rec.Error = pubErr.Error()
				} else {
					rec.Error = "publisher returned nil result"
				}
				if _, err := s.commitRecord(ctx, rec, false); err != nil {
					return out, fmt.Errorf("%w: recovery commit: %w", ErrFinalizationFailed, err)
				}
				if input.RequireDrive {
					if pubErr != nil {
						return out, fmt.Errorf("%w: %w", ErrDriveUploadFailed, pubErr)
					}
					return out, fmt.Errorf("%w: publisher returned nil result", ErrDriveUploadFailed)
				}
			} else if pubRes.FileID == "" && pubRes.WebViewLink == "" {
				publishStatus = asset.AssetPublishFailed
				rec.PublishStatus = publishStatus
				rec.Error = "publisher returned no Drive file identity"
				if _, err := s.commitRecord(ctx, rec, false); err != nil {
					return out, fmt.Errorf("%w: recovery commit: %w", ErrFinalizationFailed, err)
				}
				if input.RequireDrive {
					return out, fmt.Errorf("%w: publisher returned no Drive file identity", ErrDriveUploadFailed)
				}
			} else {
				publishStatus = asset.AssetPublishPublished
				rec.PublishStatus = publishStatus
				driveLink, driveFileID = pubRes.WebViewLink, pubRes.FileID
				if driveLink == "" && driveFileID != "" {
					driveLink = "https://drive.google.com/file/d/" + driveFileID + "/view"
				}
				if pubRes.DownloadLink != "" {
					downloadLink = pubRes.DownloadLink
				} else if pubRes.FileID != "" {
					downloadLink = "https://drive.google.com/uc?id=" + pubRes.FileID
				}
			}
		}
	}

	if !needsDelivery {
		publishStatus = asset.AssetPublishLocalOnly
		rec.PublishStatus, rec.Status = publishStatus, "processed"
		if s.persistPolicy.SaveToAssetRegistry {
			if _, err := s.commitRecord(ctx, rec, false); err != nil {
				return out, fmt.Errorf("%w: commit: %w", ErrFinalizationFailed, err)
			}
		}
	} else if publishStatus == asset.AssetPublishPublished {
		rec.DriveLink, rec.DriveFileID, rec.DownloadLink, rec.Status = driveLink, driveFileID, downloadLink, "processed"
		if _, err := s.commitRecord(ctx, rec, true); err != nil {
			// Drive already owns the external side effect. Preserve its
			// identity and make one explicit recovery attempt so the
			// canonical record remains retryable instead of looking lost.
			terminalErr := err
			rec.PublishStatus = asset.AssetPublishFailed
			rec.Status = "delivery_pending"
			rec.Error = terminalErr.Error()
			if _, recoveryErr := s.commitRecord(ctx, rec, false); recoveryErr != nil {
				return out, fmt.Errorf("%w: terminal commit: %v; recovery commit: %w", ErrFinalizationFailed, terminalErr, recoveryErr)
			}
			return out, fmt.Errorf("%w: terminal commit: %w", ErrFinalizationFailed, terminalErr)
		}
	}

	out.OK, out.DeliveryStatus = true, publishStatus
	out.Status = "processed"
	if publishStatus == asset.AssetPublishFailed {
		out.Status = "delivery_pending"
	}
	out.DriveLink, out.DriveFileID, out.DownloadLink = driveLink, driveFileID, downloadLink
	return out, nil
}

func (s *Service) commitRecord(ctx context.Context, rec *artifacts.MediaRecord, requireDrive bool) (*artifacts.FinalizeResult, error) {
	if s.finalizer == nil {
		return nil, ErrFinalizerUnavailable
	}
	result, err := s.finalizer.Finalize(ctx, rec, artifacts.FinalizeOptions{RequireDrive: requireDrive, VerifyDB: true})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, errors.New("finalizer returned nil result")
	}
	if !result.OK {
		message := result.Error
		if message == "" {
			message = "finalizer returned an unsuccessful result"
		}
		return result, errors.New(message)
	}
	return result, nil
}

func (s *Service) CheckDuplicate(ctx context.Context, input *FinalizeInput, fileHash string) (*FinalizeResult, error) {
	if input == nil {
		return nil, fmt.Errorf("%w: input is required", ErrFinalizationFailed)
	}
	out := &FinalizeResult{Status: "failed", LocalPath: input.LocalPath}
	if s.dedupe != nil && s.dedupe.Policy().Enabled && s.store == nil {
		return out, ErrAssetStoreUnavailable
	}
	if s.dedupe == nil || !s.dedupe.Policy().Enabled {
		out.OK, out.Status = true, "no_dedupe_policy"
		return out, nil
	}
	existing, err := s.dedupe.CheckDuplicate(ctx, assetop.ExistingAssetQuery{ID: input.ID, LegacyFileMD5: fileHash, Filename: input.Filename, Source: input.Source})
	if err != nil {
		return out, fmt.Errorf("%w: duplicate check: %w", ErrFinalizationFailed, err)
	}
	if existing != nil && s.dedupe.Policy().SkipIfExists {
		out.OK, out.Status = true, "would_skip_duplicate"
		out.DriveLink, out.DriveFileID, out.DownloadLink, out.LegacyFileMD5 = existing.DriveLink, existing.DriveFileID, existing.DownloadLink, existing.LegacyFileMD5
		return out, nil
	}
	out.OK, out.Status = true, "would_process"
	return out, nil
}

func (s *Service) Reconcile(ctx context.Context, source string) (int, error) {
	if s.reconcile == nil {
		return 0, ErrReconcilerUnavailable
	}
	return s.reconcile.ReconcileDriveMissing(ctx, source)
}
func DefaultConfig() Config {
	return Config{DuplicatePolicy: assetop.DefaultDuplicatePolicy(), UploadPolicy: assetop.DefaultUploadPolicy(), PersistPolicy: assetop.DefaultPersistPolicy(), ReconcilePolicy: assetop.DefaultReconcilePolicy()}
}
