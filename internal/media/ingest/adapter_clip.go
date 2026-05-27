package ingest

import (
	"context"
	"strings"
	"time"

	"velox/go-master/internal/core/assetop"
	"velox/go-master/internal/core/lifecycle"
	"velox/go-master/internal/media/assetregistry"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/repository/clips"
)

type clipStoreAdapter struct {
	repo *clips.Repository
}

func NewClipStoreAdapter(repo *clips.Repository) lifecycle.AssetRecordStore {
	return &clipStoreAdapter{repo: repo}
}

func (a *clipStoreAdapter) Upsert(ctx context.Context, rec *assetregistry.MediaRecord) error {
	clip := &models.MediaAsset{
		ID:                  rec.ID,
		Name:                rec.Name,
		Filename:            rec.Filename,
		FolderID:            rec.FolderID,
		FolderPath:          rec.FolderPath,
		Group:               rec.Group,
		MediaType:           rec.MediaType,
		DriveLink:           rec.DriveLink,
		DownloadLink:        rec.DownloadLink,
		DriveFileID:         rec.DriveFileID,
		Tags:                append([]string(nil), rec.Tags...),
		Source:              rec.Source,
		Category:            rec.Category,
		ExternalURL:         rec.ExternalURL,
		Duration:            rec.Duration,
		FileHash:            rec.FileHash,
		LocalPath:           rec.LocalPath,
		Status:              rec.Status,
		Error:               rec.Error,
		PHash:               rec.PHash,
		VisualEmbeddingJSON: rec.VisualEmbeddingJSON,
		UpdatedAt:           time.Now().UTC(),
	}
	clip.SetMetadataJSON(rec.Metadata)
	return a.repo.UpsertClip(ctx, clip)
}

func (a *clipStoreAdapter) Get(ctx context.Context, id string) (*assetregistry.MediaRecord, error) {
	clip, err := a.repo.GetClip(ctx, id)
	if err != nil || clip == nil {
		return nil, err
	}
	return clipAssetToMediaRecord(clip), nil
}

func (a *clipStoreAdapter) FindExisting(ctx context.Context, query assetop.ExistingAssetQuery) (*assetop.AssetRecord, error) {
	if query.ID != "" {
		rec, err := a.Get(ctx, query.ID)
		if err != nil {
			return nil, err
		}
		if rec != nil {
			return mediaRecordToAssetRecord(rec), nil
		}
	}

	if query.DriveFileID != "" {
		clip, err := a.repo.GetClipByDriveFileID(ctx, query.DriveFileID)
		if err != nil {
			return nil, err
		}
		if clip != nil {
			return mediaRecordToAssetRecord(clipAssetToMediaRecord(clip)), nil
		}
	}

	if query.FileHash != "" {
		clipsList, err := a.repo.FindClipsByHash(ctx, query.FileHash)
		if err != nil {
			return nil, err
		}
		for _, clip := range clipsList {
			if clip != nil {
				return mediaRecordToAssetRecord(clipAssetToMediaRecord(clip)), nil
			}
		}
	}

	return nil, nil
}

func (a *clipStoreAdapter) ListWithDriveFileID(ctx context.Context, source string) ([]*assetop.AssetRecord, error) {
	clipsList, err := a.repo.GetAllWithDriveFileID(ctx)
	if err != nil {
		return nil, err
	}
	var out []*assetop.AssetRecord
	for _, clip := range clipsList {
		if source != "" && !strings.EqualFold(strings.TrimSpace(clip.Source), strings.TrimSpace(source)) {
			continue
		}
		out = append(out, mediaRecordToAssetRecord(clipAssetToMediaRecord(clip)))
	}
	return out, nil
}

func (a *clipStoreAdapter) MarkDriveMissing(ctx context.Context, id string) error {
	rec, err := a.Get(ctx, id)
	if err != nil {
		return err
	}
	if rec == nil {
		return nil
	}
	rec.Status = "drive_missing"
	return a.Upsert(ctx, rec)
}

func (a *clipStoreAdapter) DeleteAssetRecord(ctx context.Context, id string) error {
	return a.repo.DeleteClip(ctx, id)
}

func clipAssetToMediaRecord(clip *models.MediaAsset) *assetregistry.MediaRecord {
	if clip == nil {
		return nil
	}
	return &assetregistry.MediaRecord{
		ID:                  clip.ID,
		Name:                clip.Name,
		Filename:            clip.Filename,
		Source:              clip.Source,
		Category:            clip.Category,
		MediaType:           clip.MediaType,
		ExternalURL:         clip.ExternalURL,
		FolderID:            clip.FolderID,
		FolderPath:          clip.FolderPath,
		Group:               clip.Group,
		LocalPath:           clip.LocalPath,
		DriveLink:           clip.DriveLink,
		DriveFileID:         clip.DriveFileID,
		DownloadLink:        clip.DownloadLink,
		Tags:                append([]string(nil), clip.Tags...),
		Duration:            clip.Duration,
		FileHash:            clip.FileHash,
		Status:              clip.Status,
		Error:               clip.Error,
		PHash:               clip.PHash,
		VisualEmbeddingJSON: clip.VisualEmbeddingJSON,
		Metadata:            clip.MetadataJSON(),
		SourceID:            firstNonEmpty(clip.ExternalURL, clip.Filename, clip.ID),
	}
}
