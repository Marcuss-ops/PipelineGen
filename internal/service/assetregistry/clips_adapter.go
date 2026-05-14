package assetregistry

import (
	"context"
	"time"

	"velox/go-master/internal/repository/clips"
	"velox/go-master/pkg/models"
)

type ClipsRegistry struct {
	repo *clips.Repository
}

func NewClipsRegistry(repo *clips.Repository) *ClipsRegistry {
	return &ClipsRegistry{repo: repo}
}

func (r *ClipsRegistry) UpsertMedia(ctx context.Context, rec *MediaRecord) error {
	clip := &models.MediaAsset{
		ID:                  rec.ID,
		Name:                rec.Name,
		Filename:            rec.Filename,
		FolderID:            rec.FolderID,
		FolderPath:          rec.FolderPath,
		Group:               rec.Group,
		MediaType:           rec.MediaType,
		DriveLink:           rec.DriveLink,
		DriveFileID:         rec.DriveFileID,
		DownloadLink:        rec.DownloadLink,
		Tags:                rec.Tags,
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
		UpdatedAt:           time.Now(),
	}
	clip.SetMetadataJSON(rec.Metadata)
	return r.repo.UpsertClip(ctx, clip)
}

func (r *ClipsRegistry) GetMedia(ctx context.Context, id string) (*MediaRecord, error) {
	clip, err := r.repo.GetClip(ctx, id)
	if err != nil {
		return nil, err
	}
	if clip == nil {
		return nil, nil
	}
	return clipToMediaRecord(clip), nil
}

func (r *ClipsRegistry) DeleteMedia(ctx context.Context, id string) error {
	return r.repo.DeleteClip(ctx, id)
}

func (r *ClipsRegistry) GetAllWithDriveFileID(ctx context.Context) ([]*MediaRecord, error) {
	clips, err := r.repo.GetAllWithDriveFileID(ctx)
	if err != nil {
		return nil, err
	}
	records := make([]*MediaRecord, 0, len(clips))
	for _, clip := range clips {
		records = append(records, clipToMediaRecord(clip))
	}
	return records, nil
}

func (r *ClipsRegistry) FindByPHash(ctx context.Context, phash string) (string, error) {
	return r.repo.FindByPHash(ctx, phash)
}

func clipToMediaRecord(clip *models.MediaAsset) *MediaRecord {
	rec := &MediaRecord{
		ID:                  clip.ID,
		Name:                clip.Name,
		Filename:            clip.Filename,
		FolderID:            clip.FolderID,
		FolderPath:          clip.FolderPath,
		Group:               clip.Group,
		MediaType:           clip.MediaType,
		DriveLink:           clip.DriveLink,
		DriveFileID:         clip.DriveFileID,
		DownloadLink:        clip.DownloadLink,
		Tags:                clip.Tags,
		Source:              clip.Source,
		Category:            clip.Category,
		ExternalURL:         clip.ExternalURL,
		Duration:            clip.Duration,
		FileHash:            clip.FileHash,
		LocalPath:           clip.LocalPath,
		Status:              clip.Status,
		Error:               clip.Error,
		PHash:               clip.PHash,
		VisualEmbeddingJSON: clip.VisualEmbeddingJSON,
	}
	rec.Metadata = clip.MetadataJSON()
	return rec
}
