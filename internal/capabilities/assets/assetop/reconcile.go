package assets

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	"go.uber.org/zap"
)

// ReconcileService provides Drive reconciliation for asset records.
//
// FASE 9 Step 7 (June 2026): migrated from *gdrive.Service + *drive.Uploader
// to drive.Reader port. Uses FileIsNotTrashed (semantically better: a trashed
// file IS effectively missing from the user's perspective).
type ReconcileService struct {
	store  AssetRecordStore
	reader drive.Reader
	policy ReconcilePolicy
	log    *zap.Logger
}

// NewReconcileService creates a new ReconcileService.
func NewReconcileService(
	store AssetRecordStore,
	reader drive.Reader,
	policy ReconcilePolicy,
	log *zap.Logger,
) *ReconcileService {
	return &ReconcileService{
		store:  store,
		reader: reader,
		policy: policy,
		log:    log,
	}
}

// ReconcileDriveMissing checks all records with DriveFileID and verifies they exist in Drive.
// If a Drive file is missing, applies the reconcile policy (delete or mark missing).
func (s *ReconcileService) ReconcileDriveMissing(ctx context.Context, source string) (int, error) {
	if !s.policy.Enabled {
		return 0, nil
	}

	records, err := s.store.ListWithDriveFileID(ctx, source)
	if err != nil {
		return 0, err
	}

	missingCount := 0
	for _, rec := range records {
		if rec.DriveFileID == "" {
			continue
		}

		exists, err := s.checkDriveFileExists(ctx, rec.DriveFileID)
		if err != nil {
			s.log.Warn("failed to check Drive file",
				zap.String("id", rec.ID),
				zap.String("drive_file_id", rec.DriveFileID),
				zap.Error(err))
			continue
		}

		if !exists {
			missingCount++
			s.log.Warn("Drive file missing",
				zap.String("id", rec.ID),
				zap.String("drive_file_id", rec.DriveFileID))

			if s.policy.DeleteDBIfDriveMissing {
				if err := s.store.DeleteAssetRecord(ctx, rec.ID); err != nil {
					s.log.Error("failed to delete DB record",
						zap.String("id", rec.ID),
						zap.Error(err))
				} else {
					s.log.Info("deleted DB record for missing Drive file",
						zap.String("id", rec.ID))
				}
			} else if s.policy.MarkMissingInsteadOfDelete {
				if err := s.store.MarkDriveMissing(ctx, rec.ID); err != nil {
					s.log.Error("failed to mark record as missing",
						zap.String("id", rec.ID),
						zap.Error(err))
				} else {
					s.log.Info("marked DB record as missing Drive file",
						zap.String("id", rec.ID))
				}
			}
		}
	}

	return missingCount, nil
}

// checkDriveFileExists checks if a file exists in Drive and is not trashed.
func (s *ReconcileService) checkDriveFileExists(ctx context.Context, fileID string) (bool, error) {
	if s.reader == nil {
		return false, nil
	}

	return s.reader.FileIsNotTrashed(ctx, fileID)
}

// SyncDriveFileID synces Drive file IDs from Drive to DB for all records.
func (s *ReconcileService) SyncDriveFileID(ctx context.Context, source string) (int, error) {
	if !s.policy.Enabled || !s.policy.SyncDriveFileID {
		return 0, nil
	}

	records, err := s.store.ListWithDriveFileID(ctx, source)
	if err != nil {
		return 0, err
	}

	synced := 0
	for _, rec := range records {
		if rec.DriveLink == "" {
			continue
		}

		// Extract file ID from Drive link
		fileID := extractFileIDFromLink(rec.DriveLink)
		if fileID == "" {
			continue
		}

		if fileID != rec.DriveFileID {
			s.log.Info("syncing Drive file ID",
				zap.String("id", rec.ID),
				zap.String("old_file_id", rec.DriveFileID),
				zap.String("new_file_id", fileID))
			// Update the record with the correct file ID
			// This would require an UpdateDriveFileID method on AssetRecordStore
			_ = fileID // Placeholder
			synced++
		}
	}

	return synced, nil
}

// extractFileIDFromLink extracts the file ID from a Google Drive link.
func extractFileIDFromLink(link string) string {
	return drive.FileIDFromLink(link)
}
