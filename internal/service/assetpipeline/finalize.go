package assetpipeline

import (
	"context"
	"time"

	"go.uber.org/zap"

	"velox/go-master/internal/service/assetindex"
	"velox/go-master/internal/service/mediaregistry"
)

type Finalizer struct {
	uploader   *Uploader
	finalizer  *mediaregistry.Finalizer
	assetIndex *assetindex.Service
	dedupe     *DedupeService
	lifecycle  *LifecycleService
	log        *zap.Logger
}

// NewFinalizer creates a new Finalizer with uploader, media finalizer, and asset index.
func NewFinalizer(uploader *Uploader, finalizer *mediaregistry.Finalizer, assetIndex *assetindex.Service, log *zap.Logger) *Finalizer {
	return &Finalizer{
		uploader:   uploader,
		finalizer:  finalizer,
		assetIndex: assetIndex,
		log:        log,
	}
}

// NewFinalizerWithLifecycle creates a new Finalizer with lifecycle policies.
func NewFinalizerWithLifecycle(
	uploader *Uploader,
	finalizer *mediaregistry.Finalizer,
	assetIndex *assetindex.Service,
	lifecycle *LifecycleService,
	log *zap.Logger,
) *Finalizer {
	return &Finalizer{
		uploader:   uploader,
		finalizer:  finalizer,
		assetIndex: assetIndex,
		lifecycle:  lifecycle,
		log:        log,
	}
}

func (f *Finalizer) Finalize(ctx context.Context, in *FinalizeInput) (*FinalizeResult, error) {
	out := &FinalizeResult{
		OK:        false,
		Status:    "failed",
		LocalPath: in.LocalPath,
	}

	// Use lifecycle service for duplicate checking if available
	if f.lifecycle != nil {
		lifecycleResult, err := f.lifecycle.ProcessAsset(ctx, in, "")
		if err != nil {
			return out, err
		}
		if lifecycleResult.OK && lifecycleResult.Status == "skipped_duplicate" {
			out.OK = true
			out.Status = lifecycleResult.Status
			out.DriveLink = lifecycleResult.DriveLink
			out.DriveFileID = lifecycleResult.DriveFileID
			out.DownloadLink = lifecycleResult.DownloadLink
			out.FileHash = lifecycleResult.FileHash
			return out, nil
		}
	}

	if in.RequireLocal && in.LocalPath == "" {
		out.Error = "missing local path"
		return out, nil
	}

	if in.LocalPath != "" && in.RequireHash {
		fileHash, err := HashFile(in.LocalPath)
		if err != nil {
			out.Error = "hash failed: " + err.Error()
			return out, err
		}
		out.FileHash = fileHash

		contentHash, err := ContentHashFile(in.LocalPath)
		if err != nil {
			if f.log != nil {
				f.log.Warn("content hash failed", zap.Error(err))
			}
		} else {
			out.ContentHash = contentHash
		}
	}

	// Use DriveFileID from input if available
	driveFileID := in.DriveFileID
	driveLink := in.DriveLink
	downloadLink := in.DownloadLink

	if in.RequireDrive && driveLink == "" {
		if f.uploader != nil {
			var err error
			driveLink, downloadLink, err = f.uploader.Upload(ctx, in.LocalPath, in.FolderID)
			if err != nil {
				out.Error = "upload failed: " + err.Error()
				return out, err
			}
			driveFileID = "" // Uploader doesn't return FileID yet
		} else {
			out.Error = "upload required but no uploader configured"
			return out, nil
		}
	}

	out.DriveLink = driveLink
	out.DriveFileID = driveFileID
	out.DownloadLink = downloadLink

	if f.finalizer != nil {
		rec := &mediaregistry.MediaRecord{
			ID:           in.ID,
			Name:         in.Name,
			Filename:     in.Filename,
			Source:       in.Source,
			MediaType:    string(in.Kind),
			FolderID:     in.FolderID,
			FolderPath:   in.FolderPath,
			Group:        in.Group,
			LocalPath:    in.LocalPath,
			DriveLink:    driveLink,
			DriveFileID:  driveFileID,
			DownloadLink: downloadLink,
			FileHash:     out.FileHash,
			ContentHash:  out.ContentHash,
			Metadata:     in.Metadata,
			Status:       "processed",
			SourceID:     in.SourceID,
			Subfolder:    in.Subfolder,
		}

		fin, err := f.finalizer.Finalize(ctx, rec, mediaregistry.FinalizeOptions{
			RequireLocal: in.RequireLocal,
			RequireHash:  in.RequireHash,
			RequireDrive: in.RequireDrive,
			VerifyDB:     in.VerifyDB,
		})
		if err != nil {
			return out, err
		}

		if !fin.OK {
			out.Error = fin.Error
			out.Status = fin.Status
			return out, nil
		}
	}

	if f.assetIndex != nil && out.ContentHash != "" {
		assetRec := &assetindex.AssetRecord{
			AssetID:      in.ID,
			AssetType:    string(in.Kind),
			Source:       in.Source,
			SourceID:     in.SourceID,
			GroupName:    in.Group,
			Subfolder:    in.Subfolder,
			LocalPath:    in.LocalPath,
			DriveLink:    driveLink,
			DownloadLink: downloadLink,
			FileHash:     out.FileHash,
			ContentHash:  out.ContentHash,
			Status:       "ready",
			Metadata:     in.Metadata,
			CreatedAt:    time.Now().UTC(),
			UpdatedAt:    time.Now().UTC(),
		}
		if err := f.assetIndex.Upsert(ctx, assetRec); err != nil {
			if f.log != nil {
				f.log.Warn("failed to write to asset_index", zap.String("id", in.ID), zap.Error(err))
			}
		}
	}

	out.OK = true
	out.Status = "processed"
	return out, nil
}
