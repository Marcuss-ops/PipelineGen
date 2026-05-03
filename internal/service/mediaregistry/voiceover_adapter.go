package mediaregistry

import (
	"context"
	"time"

	"velox/go-master/internal/repository/voiceovers"
)

type VoiceoverRegistry struct {
	repo *voiceovers.Repository
}

func NewVoiceoverRegistry(repo *voiceovers.Repository) *VoiceoverRegistry {
	return &VoiceoverRegistry{repo: repo}
}

func (r *VoiceoverRegistry) UpsertMedia(ctx context.Context, rec *MediaRecord) error {
	return r.repo.Upsert(ctx, &voiceovers.Record{
		ID:          rec.ID,
		Filename:    rec.Filename,
		LocalPath:   rec.LocalPath,
		FolderID:    rec.FolderID,
		FolderPath:  rec.FolderPath,
		DriveLink:   rec.DriveLink,
		DownloadLink: rec.DownloadLink,
		FileHash:    rec.FileHash,
		Status:      rec.Status,
		Error:       rec.Error,
		Metadata:    rec.Metadata,
		UpdatedAt:   time.Now(),
	})
}

func (r *VoiceoverRegistry) GetMedia(ctx context.Context, id string) (*MediaRecord, error) {
	rec, err := r.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, nil
	}
	return voiceoverToMediaRecord(rec), nil
}

func voiceoverToMediaRecord(rec *voiceovers.Record) *MediaRecord {
	return &MediaRecord{
		ID:           rec.ID,
		Name:         rec.Filename,
		Filename:     rec.Filename,
		FolderID:     rec.FolderID,
		FolderPath:   rec.FolderPath,
		LocalPath:    rec.LocalPath,
		DriveLink:    rec.DriveLink,
		DownloadLink: rec.DownloadLink,
		FileHash:     rec.FileHash,
		Status:       rec.Status,
		Error:        rec.Error,
		Metadata:     rec.Metadata,
		Duration:     int(rec.DurationSeconds),
	}
}
