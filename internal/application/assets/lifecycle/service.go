package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assetop"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

var (
	// ErrFinalizerUnavailable is returned when persistence is required but
	// the canonical asset finalizer was not wired.
	ErrFinalizerUnavailable = errors.New("asset lifecycle: finalizer unavailable")
	// ErrReconcilerUnavailable is returned when reconciliation was requested
	// but the lifecycle service has no reconciliation capability.
	ErrReconcilerUnavailable = errors.New("asset lifecycle: reconciler unavailable")
)

// Service orchestrates the full asset lifecycle:
// duplicate checking, upload, persistence, and reconciliation.
//
// FASE 9 Step 7 (June 2026): drive.Admin (UploadFile) + drive.Reader.
//
// F2.7 (June 2026): driveAdmin REMOVED. The Drive write path now goes
// through delivery.Publisher (canonical Pattern 0 port) — every upload
// flows through DestinationRegistry + RequireSubpath + ConflictPolicy
// instead of bypassing them via the raw drive.Admin.UploadFile port.
//
// driveReader (drive.Reader) is retained for the read-only reconcile
// path (DriveIsNotTrashed); it is the only Drive-side touch left on
// service-side callers.
type Service struct {
	store         AssetRecordStore
	dedupe        *assetop.DedupeService
	reconcile     *assetop.ReconcileService
	publisher     delivery.Publisher
	driveReader   drive.Reader
	finalizer     *artifacts.Finalizer
	uploadPolicy  assetop.UploadPolicy
	persistPolicy assetop.PersistPolicy
	registry      artifacts.Registry
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

// ServiceDeps carries the dependencies for NewService. Grouping them
// keeps the constructor under the archcheck 8-parameter cap while
// preserving the canonical lifecycle service surface.
type ServiceDeps struct {
	Store       AssetRecordStore
	Publisher   delivery.Publisher
	DriveReader drive.Reader
	Registry    artifacts.Registry
	AssetIndex  *assetindex.Service
	Finalizer   *artifacts.Finalizer
	Log         *zap.Logger
}

// NewService creates a new lifecycle Service.
//
// FASE 9 Step 7: driveAdmin is used for UploadFile; driveReader is used
// for FileIsNotTrashed in the reconcile service. Both are satisfied by
// the same *drive.Uploader concrete in production wiring.
//
// F2.7 (June 2026): driveAdmin (drive.Admin) replaced by publisher
// (delivery.Publisher). driveReader stays — reconcile touches the read-
// only drive surface (DriveIsNotTrashed). NewLifecycleFromDeps at the
// composition root is the only place drive.Admin lives; the lifecycle
// service itself NEVER holds a drive.Admin handle (P0 #7 invariant: no
// raw SDK / legacy port access from application code).
func NewService(deps ServiceDeps, cfg Config) *Service {
	dedupe := assetop.NewDedupeService(deps.Store, cfg.DuplicatePolicy, deps.Log)

	var reconcile *assetop.ReconcileService
	if cfg.ReconcilePolicy.Enabled && deps.DriveReader != nil {
		reconcile = assetop.NewReconcileService(deps.Store, deps.DriveReader, cfg.ReconcilePolicy, deps.Log)
	}

	return &Service{
		store:         deps.Store,
		dedupe:        dedupe,
		reconcile:     reconcile,
		publisher:     deps.Publisher,
		driveReader:   deps.DriveReader,
		finalizer:     deps.Finalizer,
		uploadPolicy:  cfg.UploadPolicy,
		persistPolicy: cfg.PersistPolicy,
		registry:      deps.Registry,
		assetIndex:    deps.AssetIndex,
		log:           deps.Log,
	}
}

// ProcessAsset processes an asset through the lifecycle:
// 1. Check for duplicates
// 2. Upload to Drive (if needed)
// 3. Persist to databases
func (s *Service) ProcessAsset(ctx context.Context, input *FinalizeInput, fileHash string) (*FinalizeResult, error) {
	if s.persistPolicy.SaveToAssetRegistry && s.finalizer == nil {
		return nil, ErrFinalizerUnavailable
	}

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

	if s.uploadPolicy.Enabled && driveLink == "" && input.LocalPath != "" {
		if s.publisher == nil {
			// F2.7: publisher unwired means the composition root fell
			// through without constructing delivery.Publisher. This is
			// a code defect — surface the gap loudly so operators see
			// it at first invocation. With RequireDrive=true, the
			// caller demands a Drive URL; bail out as UPLOAD_FAILED
			// instead of silently producing a local-only record.
			if input.RequireDrive {
				out.Status = "UPLOAD_FAILED"
				out.Error = fmt.Sprintf("lifecycle.ProcessAsset: publisher not wired (composition root); RequireDrive=true cannot proceed")
				s.log.Error("lifecycle.ProcessAsset: publisher not wired + RequireDrive=true — abort without persistence",
					zap.String("id", input.ID))
				return out, nil
			}
			s.log.Warn("lifecycle.ProcessAsset: publisher not wired (composition root) — RequireDrive=false, proceeding without Drive upload",
				zap.String("id", input.ID))
		} else {
			// F2.7: build the canonical delivery.PublishRequest. The
			// Publisher routes through DestinationRegistry +
			// RequireSubpath + ConflictPolicy — the legacy
			// drive.Admin.UploadFile bypass is closed. FolderID passes
			// through RootFolderOverride (back-compat escape hatch for
			// callers with an explicit folder target).
			filename := input.Filename
			if filename == "" {
				filename = filepath.Base(input.LocalPath)
			}
			// PR-P12-LIFECYCLE-SEMANTIC (July 2026): RootFolderOverride
			// REMOVED. Destination + Group + Subject + ProjectID + Language
			// provide canonical semantic routing via DestinationRegistry +
			// PathBuilder. The explicit folder override is the
			// infrastructure-layer escape hatch (delivery.Publisher
			// internal); application-layer code uses typed semantic fields.
			// Forward-pointer: PR-P12-LIFECYCLE-CALLER-VERIFY (deadline
			// 2026-08-15) — audit all callers of ProcessAsset that set
			// input.FolderID to verify semantic fields resolve to the
			// same folder.
			pubReq := delivery.PublishRequest{
				Destination: input.Destination,
				LocalPath:   input.LocalPath,
				Filename:    filename,
				AssetID:     input.ID,
				Group:       input.Group,
				Subject:     input.Subject,
				ProjectID:   input.ProjectID,
				Language:    input.Language,
				Style:       input.Style,
			}
			pubRes, pubErr := s.publisher.Publish(ctx, pubReq)
			if pubErr != nil {
				if input.RequireDrive {
					out.Status = "UPLOAD_FAILED"
					out.Error = fmt.Sprintf("lifecycle.ProcessAsset: publisher.Publish failed and RequireDrive=true: %v", pubErr)
					s.log.Error("lifecycle.ProcessAsset: publisher.Publish failed + RequireDrive=true — abort without persistence",
						zap.String("id", input.ID),
						zap.String("destination", string(input.Destination)),
						zap.Error(pubErr))
					return out, nil
				}
				s.log.Warn("lifecycle.ProcessAsset: publisher.Publish failed (RequireDrive=false — best-effort, proceeding without Drive URL)",
					zap.String("id", input.ID),
					zap.Error(pubErr))
			} else {
				driveLink = pubRes.WebViewLink
				if pubRes.DownloadLink != "" {
					downloadLink = pubRes.DownloadLink
				} else if pubRes.FileID != "" {
					downloadLink = "https://drive.google.com/uc?id=" + pubRes.FileID
				}
				driveFileID = pubRes.FileID
				s.log.Info("lifecycle.ProcessAsset: asset uploaded to drive (via Publisher, F2.7)",
					zap.String("id", input.ID),
					zap.String("file_id", pubRes.FileID),
					zap.String("destination", string(input.Destination)),
					zap.String("action", string(pubRes.Action)))
			}
		}
	}

	// Step 3: Persist to databases (if policy enabled)
	if s.persistPolicy.SaveToAssetRegistry {
		rec := &artifacts.MediaRecord{
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

		// fix/voiceover-require-drive-on-intent: honour the caller's
		// RequireDrive intent (set at the request boundary, e.g. the
		// voiceover process passes true whenever dest.FolderID or the
		// cfg-level voiceover folder is set). The legacy
		// `driveLink != ""` fallback is preserved as an OR so callers
		// that haven't yet started setting input.RequireDrive keep
		// their existing behaviour. Once every entrypoint sets the
		// field explicitly, the OR can drop.
		requireDrive := input.RequireDrive || driveLink != "" || input.Destination == delivery.DestinationImage
		finalizeOpts := artifacts.FinalizeOptions{
			RequireLocal: false,
			RequireHash:  false,
			RequireDrive: requireDrive,
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

// UploadOnly + UpsertVoiceoverProjectionTx — extracted to
// service_voiceover.go (PR-LIFECYCLE-SPLIT, July 2026).
// See that file for the canonical voiceover-specific lifecycle methods.

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
		return 0, ErrReconcilerUnavailable
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
