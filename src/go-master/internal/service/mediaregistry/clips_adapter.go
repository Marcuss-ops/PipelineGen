package mediaregistry

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
	clip := &models.Clip{
		ID:           rec.ID,
		Name:         rec.Name,
		Filename:     rec.Filename,
		FolderID:     rec.FolderID,
		FolderPath:   rec.FolderPath,
		Group:        rec.Group,
		MediaType:    rec.MediaType,
		DriveLink:    rec.DriveLink,
		DownloadLink: rec.DownloadLink,
		Tags:         rec.Tags,
		Source:       rec.Source,
		Category:     rec.Category,
		ExternalURL:  rec.ExternalURL,
		Duration:     rec.Duration,
		Metadata:     rec.Metadata,
		FileHash:     rec.FileHash,
		LocalPath:    rec.LocalPath,
		UpdatedAt:    time.Now(),
	}
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

func clipToMediaRecord(clip *models.Clip) *MediaRecord {
	return &MediaRecord{
		ID:           clip.ID,
		Name:         clip.Name,
		Filename:     clip.Filename,
		FolderID:     clip.FolderID,
		FolderPath:   clip.FolderPath,
		Group:        clip.Group,
		MediaType:    clip.MediaType,
		DriveLink:    clip.DriveLink,
		DownloadLink: clip.DownloadLink,
		Tags:         clip.Tags,
		Source:       clip.Source,
		Category:     clip.Category,
		ExternalURL:  clip.ExternalURL,
		Duration:     clip.Duration,
		Metadata:     clip.Metadata,
		FileHash:     clip.FileHash,
		LocalPath:    clip.LocalPath,
		Status:       "",
	}
}
