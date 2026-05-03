package mediaregistry

import (
	"context"
	"os"
	"time"

	"go.uber.org/zap"
)

type Finalizer struct {
	registry      Registry
	driveVerifier DriveVerifier
	log           *zap.Logger
}

func NewFinalizer(registry Registry, driveVerifier DriveVerifier, log *zap.Logger) *Finalizer {
	return &Finalizer{
		registry:      registry,
		driveVerifier: driveVerifier,
		log:           log,
	}
}

func (f *Finalizer) Finalize(ctx context.Context, rec *MediaRecord, opts FinalizeOptions) (*FinalizeResult, error) {
	result := &FinalizeResult{
		Record: rec,
		Status: rec.Status,
	}

	if rec.LocalPath == "" && opts.RequireLocal {
		result.OK = false
		result.Status = "failed"
		result.Error = "missing local path"
		f.log.Warn("finalize failed: missing local path", zap.String("id", rec.ID))
		return result, nil
	}

	if rec.LocalPath != "" {
		if _, err := os.Stat(rec.LocalPath); os.IsNotExist(err) {
			result.OK = false
			result.Status = "failed"
			result.Error = "local file does not exist"
			result.LocalExists = false
			f.log.Warn("finalize failed: local file missing", zap.String("id", rec.ID), zap.String("path", rec.LocalPath))
			return result, nil
		}
		result.LocalExists = true
	}

	if rec.FileHash == "" && opts.RequireHash {
		result.OK = false
		result.Status = "failed"
		result.Error = "missing file hash"
		f.log.Warn("finalize failed: missing file hash", zap.String("id", rec.ID))
		return result, nil
	}

	if opts.RequireDrive && rec.DriveLink == "" {
		result.OK = false
		result.Status = "failed"
		result.Error = "missing drive link after upload"
		f.log.Warn("finalize failed: missing drive link", zap.String("id", rec.ID))
		return result, nil
	}

	if rec.DriveLink != "" && f.driveVerifier != nil {
		exists, err := f.driveVerifier.VerifyDriveLink(ctx, rec.DriveLink)
		if err != nil {
			f.log.Warn("drive verification error", zap.String("id", rec.ID), zap.Error(err))
		}
		result.DriveUploaded = exists
	}

	if err := f.registry.UpsertMedia(ctx, rec); err != nil {
		result.OK = false
		result.Status = "failed"
		result.Error = "db update failed: " + err.Error()
		f.log.Error("finalize failed: db update", zap.String("id", rec.ID), zap.Error(err))
		return result, nil
	}
	result.DBSaved = true

	if opts.VerifyDB {
		time.Sleep(100 * time.Millisecond)
		saved, err := f.registry.GetMedia(ctx, rec.ID)
		if err != nil {
			result.OK = false
			result.Status = "failed"
			result.Error = "db verify failed: " + err.Error()
			f.log.Error("finalize failed: db verify", zap.String("id", rec.ID), zap.Error(err))
			return result, nil
		}
		if saved == nil {
			result.OK = false
			result.Status = "failed"
			result.Error = "db verify failed: record not found after save"
			f.log.Error("finalize failed: record not found", zap.String("id", rec.ID))
			return result, nil
		}
		if opts.RequireDrive && saved.DriveLink == "" {
			result.OK = false
			result.Status = "partial"
			result.Error = "db saved without drive link"
			f.log.Warn("finalize partial: db saved without drive link", zap.String("id", rec.ID))
			return result, nil
		}
	}

	result.OK = true
	if result.Status == "" {
		result.Status = "processed"
	}

	f.log.Info("finalize complete",
		zap.String("id", rec.ID),
		zap.String("status", result.Status),
		zap.Bool("db_saved", result.DBSaved),
		zap.Bool("local_exists", result.LocalExists),
		zap.Bool("drive_uploaded", result.DriveUploaded))

	return result, nil
}
