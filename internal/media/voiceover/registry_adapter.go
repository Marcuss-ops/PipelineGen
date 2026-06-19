package voiceover

import (
	"context"
	"encoding/json"

	"github.com/Marcuss-ops/PipelineGen/internal/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/voiceovers"
	timeutil "github.com/Marcuss-ops/PipelineGen/internal/platform"
)

type voiceoverRegistryAdapter struct {
	repo *voiceovers.Repository
}

func NewVoiceoverRegistryAdapter(repo *voiceovers.Repository) artifacts.Registry {
	return &voiceoverRegistryAdapter{repo: repo}
}

func (a *voiceoverRegistryAdapter) UpsertMedia(ctx context.Context, rec *artifacts.MediaRecord) error {
	vRec := mediaRecordToVoiceover(rec)
	return a.repo.Upsert(ctx, vRec)
}

func (a *voiceoverRegistryAdapter) GetMedia(ctx context.Context, id string) (*artifacts.MediaRecord, error) {
	vRec, err := a.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if vRec == nil {
		return nil, nil
	}
	return voiceoverToMediaRecord(vRec), nil
}

func (a *voiceoverRegistryAdapter) DeleteMedia(ctx context.Context, id string) error {
	return a.repo.Delete(ctx, id)
}

func (a *voiceoverRegistryAdapter) GetAllWithDriveFileID(ctx context.Context) ([]*artifacts.MediaRecord, error) {
	records, err := a.repo.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	var result []*artifacts.MediaRecord
	for _, rec := range records {
		if rec.DriveFileID != "" {
			result = append(result, voiceoverToMediaRecord(rec))
		}
	}
	return result, nil
}

func (a *voiceoverRegistryAdapter) FindByPHash(ctx context.Context, phash string) (string, error) {
	// Voiceovers are audio — pHash is not applicable.
	return "", nil
}

func mediaRecordToVoiceover(mediaRec *artifacts.MediaRecord) *voiceovers.Record {
	var meta struct {
		TextHash    string `json:"text_hash"`
		TextPreview string `json:"text_preview"`
		Language    string `json:"language"`
		Voice       string `json:"voice"`
		CleanedPath string `json:"cleaned_path"`
		Strategy    string `json:"strategy"`
		RequestID   string `json:"request_id"`
		CreatedAt   string `json:"created_at"`
		UpdatedAt   string `json:"updated_at"`
	}
	_ = json.Unmarshal([]byte(mediaRec.Metadata), &meta)

	rec := &voiceovers.Record{
		ID:           mediaRec.ID,
		RequestID:    meta.RequestID,
		TextHash:     meta.TextHash,
		TextPreview:  meta.TextPreview,
		Language:     meta.Language,
		Voice:        meta.Voice,
		Filename:     mediaRec.Filename,
		LocalPath:    mediaRec.LocalPath,
		CleanedPath:  meta.CleanedPath,
		FolderID:     mediaRec.FolderID,
		FolderPath:   mediaRec.FolderPath,
		DriveFileID:  mediaRec.DriveFileID,
		DriveLink:    mediaRec.DriveLink,
		DownloadLink: mediaRec.DownloadLink,
		FileHash:     mediaRec.FileHash,
		Status:       mediaRec.Status,
		Error:        mediaRec.Error,
		Strategy:     meta.Strategy,
		Metadata:     mediaRec.Metadata,
	}

	if meta.CreatedAt != "" {
		rec.CreatedAt = timeutil.ParseRFC3339(meta.CreatedAt)
	}
	if meta.UpdatedAt != "" {
		rec.UpdatedAt = timeutil.ParseRFC3339(meta.UpdatedAt)
	}

	return rec
}

func voiceoverToMediaRecord(rec *voiceovers.Record) *artifacts.MediaRecord {
	meta := map[string]any{
		"text_hash":    rec.TextHash,
		"text_preview": rec.TextPreview,
		"language":     rec.Language,
		"voice":        rec.Voice,
		"cleaned_path": rec.CleanedPath,
		"strategy":     rec.Strategy,
		"request_id":   rec.RequestID,
		"created_at":   timeutil.FormatRFC3339(rec.CreatedAt),
		"updated_at":   timeutil.FormatRFC3339(rec.UpdatedAt),
	}
	metaJSON, _ := json.Marshal(meta)

	return &artifacts.MediaRecord{
		ID:           rec.ID,
		Source:       "voiceover",
		Name:         rec.TextPreview,
		Filename:     rec.Filename,
		FolderID:     rec.FolderID,
		FolderPath:   rec.FolderPath,
		MediaType:    "audio",
		DriveFileID:  rec.DriveFileID,
		DriveLink:    rec.DriveLink,
		DownloadLink: rec.DownloadLink,
		FileHash:     rec.FileHash,
		LocalPath:    rec.LocalPath,
		Status:       rec.Status,
		Error:        rec.Error,
		Metadata:     string(metaJSON),
	}
}
