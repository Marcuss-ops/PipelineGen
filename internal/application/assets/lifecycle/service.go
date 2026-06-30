package lifecycle

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assetop"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

// Service orchestrates the full asset lifecycle:
// duplicate checking, upload, persistence, and reconciliation.
//
// FASE 9 Step 7 (June 2026): migrated from *gdrive.Service + *drive.Uploader
// to drive.Admin (UploadFile) + drive.Reader (reconcile/verifier) ports.
type Service struct {
	store         AssetRecordStore
	dedupe        *assetop.DedupeService
	reconcile     *assetop.ReconcileService
	driveAdmin    drive.Admin
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

// NewService creates a new lifecycle Service.
//
// FASE 9 Step 7: driveAdmin is used for UploadFile; driveReader is used
// for FileIsNotTrashed in the reconcile service. Both are satisfied by
// the same *drive.Uploader concrete in production wiring.
func NewService(
	store AssetRecordStore,
	driveAdmin drive.Admin,
	driveReader drive.Reader,
	registry artifacts.Registry,
	assetIndex *assetindex.Service,
	finalizer *artifacts.Finalizer,
	cfg Config,
	log *zap.Logger,
) *Service {
	dedupe := assetop.NewDedupeService(store, cfg.DuplicatePolicy, log)

	var reconcile *assetop.ReconcileService
	if cfg.ReconcilePolicy.Enabled && driveReader != nil {
		reconcile = assetop.NewReconcileService(store, driveReader, cfg.ReconcilePolicy, log)
	}

	return &Service{
		store:         store,
		dedupe:        dedupe,
		reconcile:     reconcile,
		driveAdmin:    driveAdmin,
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
		if s.driveAdmin != nil {
			result, err := s.driveAdmin.UploadFile(ctx, input.LocalPath, input.FolderID, filepath.Base(input.LocalPath))
			if err != nil {
				s.log.Warn("drive upload failed", zap.Error(err))
			} else {
				driveLink = result.WebViewLink
				downloadLink = "https://drive.google.com/uc?id=" + result.FileID
				driveFileID = result.FileID
				s.log.Info("asset uploaded to drive",
					zap.String("id", input.ID),
					zap.String("file_id", result.FileID))
			}
		}
	}

	// Step 3: Persist to databases (if policy enabled)
	if s.persistPolicy.SaveToAssetRegistry && s.finalizer != nil {
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
		finalizeOpts := artifacts.FinalizeOptions{
			RequireLocal: false,
			RequireHash:  false,
			RequireDrive: input.RequireDrive || driveLink != "",
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

// UploadOnly uploads a local file to Drive WITHOUT any database
// write. This is Phase 1 of the new canonical VOICEOVER 2-PHASE
// SPLIT (P0.7 Wave 21, June 2026 — Step 9/12 finalizer unification).
//
// Difference vs. ProcessAsset: ProcessAsset uploads AND persists
// to media_assets (via finalizer.Finalize + registry.UpsertMedia);
// UploadOnly stops at the Drive surface and returns the canonical
// upload URLs. The caller (voiceover.Service.destinationStage) is
// responsible for marking BatchItem Status=StatusUploaded on
// success and for routing Drive upload failures through
// FailureUpload at the Stage-2 fail() contract.
//
// Atomicity note: this method writes NOTHING to SQLite. Persistence
// happens separately via finalizeStage's tx-scoped write chain
// (voiceover.persistence.Repository.* + outbox + the new
// UpsertVoiceoverProjectionTx). The pre-fix bug was: ProcessAsset
// wrote media_assets at this point; finalizeStage then ALSO wrote
// voiceovers in a SECOND tx. A Drive upload success followed by an
// InsertTx failure would leave media_assets orphan (audio uploaded
// but listed-as-failed). Removing the ProcessAsset call from
// destinationStage eliminates this partial-save bug because
// NOTHING is persisted until finalizeStage's tx commits — a tx
// failure aborts the entire atomic-write, and the upload becomes
// a fire-and-detect orphan (handled by the replace-mode cleanup
// goroutine downstream).
//
// Layering note: this method does NOT touch the dedupe gate —
// the gate (PR-VO-B3 CountByDriveFileIDTx) runs in finalizeStage
// INSIDE the actual finalize tx, not here. A Drive upload of a
// duplicate is permitted at this step (upload == idempotent —
// the actual finalization gate is the visibility boundary).
//
// Returns nil error + empty DriveLink when upload is disabled
// (s.uploadPolicy.Enabled == false) so callers can still
// proceed through finalizeStage with the local-only path.
func (s *Service) UploadOnly(ctx context.Context, input *FinalizeInput) (*UploadOnlyResult, error) {
	if input == nil {
		return nil, fmt.Errorf("lifecycle.UploadOnly: FinalizeInput is required (nil input)")
	}
	if s.driveAdmin == nil {
		return nil, fmt.Errorf("lifecycle.UploadOnly: driveAdmin not wired (composition root)")
	}

	driveLink := input.DriveLink
	driveFileID := input.DriveFileID
	downloadLink := input.DownloadLink

	if s.uploadPolicy.Enabled && driveLink == "" && input.FolderID != "" && input.LocalPath != "" {
		// Drive.Admin.UploadFile is the PR-VO-B1 hardened entry —
		// failures do NOT propagate false-success (the previous
		// log-warn best-effort surface for upload errors). The
		// caller surfaces a FailureUpload on err.
		result, err := s.driveAdmin.UploadFile(ctx, input.LocalPath, input.FolderID, filepath.Base(input.LocalPath))
		if err != nil {
			if s.log != nil {
				s.log.Warn("lifecycle.UploadOnly: Drive upload failed (caller surfaces FailureUpload)",
					zap.String("id", input.ID),
					zap.Error(err))
			}
			return nil, fmt.Errorf("lifecycle.UploadOnly: driveAdmin.UploadFile: %w", err)
		}
		driveLink = result.WebViewLink
		downloadLink = "https://drive.google.com/uc?id=" + result.FileID
		driveFileID = result.FileID

		if s.log != nil {
			s.log.Info("lifecycle.UploadOnly: Drive upload OK (Phase 1 of new 2-phase split)",
				zap.String("id", input.ID),
				zap.String("file_id", result.FileID))
		}
	}

	return &UploadOnlyResult{
		DriveLink:    driveLink,
		DriveFileID:  driveFileID,
		DownloadLink: downloadLink,
	}, nil
}

// UpsertVoiceoverProjectionTx writes the canonical media_assets
// projection row for a voiceover asset INSIDE the caller-owned tx.
// This is Phase 2 of the new canonical VOICEOVER 2-PHASE SPLIT
// (P0.7 Wave 21, June 2026 — Step 9/12 finalizer unification).
//
// Atomicity guarantee: the caller (voiceover.finalizeStage) holds
// the *sql.Tx from BeginTx → Commit. Inside that tx, three writes
// happen atomically:
//
//	1. voiceovers table UPSERT     → voiceover.persistence.Repository.InsertTx (existing)
//	2. media_assets projection UPSERT (this method)
//	3. asset.index.requested outbox → outboxEnqueuer.EnqueueIndexEvent (existing)
//
// All three commit together via tx.Commit() — partial-save is
// impossible. Pre-fix the same canonical content was written TWICE
// (ProcessAsset + finalizeStage) across TWO transactions; a
// failure between the two left an orphan row in media_assets.
//
// UPSERT semantics: ON CONFLICT (id) DO UPDATE SET. Idempotent
// on retry (a re-run of finalizeStage updates the projection
// columns in place, doesn't double-insert).
// `source` is forced to `voiceover` so the row is discoverable
// by the voiceover→media_assets SQL verification query:
// `SELECT 1 FROM media_assets WHERE id = ? AND source = 'voiceover'`.
//
// Layering note: this method EXPECTS the caller to have already
// verified that the BatchItem has a Drive link from Phase 1
// (else we persist a projection without a Drive URL — fail-closed
// at the voiceover.persistence.Repository layer per its own
// fail-fast policies; this method does not re-validate).
func (s *Service) UpsertVoiceoverProjectionTx(ctx context.Context, tx *sql.Tx, in *VoiceoverProjectionInput) error {
	if in == nil {
		return fmt.Errorf("lifecycle.UpsertVoiceoverProjectionTx: VoiceoverProjectionInput is required (nil input)")
	}
	if tx == nil {
		return fmt.Errorf("lifecycle.UpsertVoiceoverProjectionTx: *sql.Tx is required (caller-owned tx; caller forgot to pass it)")
	}
	if in.ID == "" {
		return fmt.Errorf("lifecycle.UpsertVoiceoverProjectionTx: ID is required (primary key on media_assets)")
	}
	if in.Source == "" {
		// Forcing the canonical discriminator — never trust the
		// caller to set it correctly; this method is the SOLE
		// writer of media_assets rows in the voiceover pipeline.
		in.Source = "voiceover"
	}

	// On-conflict UPSERT: drives the canonical id-keyed SQL
	// identity. Mirrors the legacy voiceover.lifecycle route
	// (registry.UpsertMedia → clips_repository.UpsertClipTx in
	// production) without introducing a new dependency on
	// the existing Registry tx-less API.
	const upsertSQL = `
		INSERT INTO media_assets (
			id,
			source,
			name,
			filename,
			folder_id,
			folder_path,
			media_type,
			local_path,
			drive_file_id,
			drive_link,
			download_link,
			file_hash,
			language,
			status,
			metadata_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			source         = excluded.source,
			name           = excluded.name,
			filename       = excluded.filename,
			folder_id      = excluded.folder_id,
			folder_path    = excluded.folder_path,
			media_type     = excluded.media_type,
			local_path     = excluded.local_path,
			drive_file_id  = excluded.drive_file_id,
			drive_link     = excluded.drive_link,
			download_link  = excluded.download_link,
			file_hash      = excluded.file_hash,
			language       = excluded.language,
			status         = excluded.status,
			metadata_json  = excluded.metadata_json,
			updated_at     = datetime('now')
	`
	if _, err := tx.ExecContext(ctx, upsertSQL,
		in.ID,
		in.Source,
		in.Name,
		in.Filename,
		in.FolderID,
		in.FolderPath,
		in.MediaType,
		in.LocalPath,
		in.DriveFileID,
		in.DriveLink,
		in.DownloadLink,
		in.FileHash,
		in.Language,
		in.Status,
		in.Metadata,
	); err != nil {
		if s.log != nil {
			s.log.Error("lifecycle.UpsertVoiceoverProjectionTx: media_assets UPSERT failed (tx will rollback)",
				zap.String("id", in.ID),
				zap.Error(err))
		}
		return fmt.Errorf("lifecycle.UpsertVoiceoverProjectionTx: media_assets UPSERT: %w", err)
	}

	if s.log != nil {
		s.log.Info("lifecycle.UpsertVoiceoverProjectionTx: media_assets projection written (Phase 2 of new 2-phase split)",
			zap.String("id", in.ID),
			zap.String("source", in.Source))
	}
	return nil
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
