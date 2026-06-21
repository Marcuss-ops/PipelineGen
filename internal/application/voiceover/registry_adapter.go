package voiceover

import (
	"context"
	"encoding/json"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// NewVoiceoverRegistryAdapter returns an artifacts.Registry backed by a
// VoiceoversRepository. The returned *artifacts.SimpleRegistry delegates
// every Registry method to a repo-specific callback.
func NewVoiceoverRegistryAdapter(repo *assets.VoiceoversRepository) artifacts.Registry {
	return &artifacts.SimpleRegistry{
		UpsertFn: func(ctx context.Context, rec *artifacts.MediaRecord) error {
			return repo.Upsert(ctx, mediaRecordToVoiceover(rec))
		},
		GetFn: func(ctx context.Context, id string) (*artifacts.MediaRecord, error) {
			vRec, err := repo.GetByID(ctx, id)
			if err != nil || vRec == nil {
				return nil, err
			}
			return voiceoverToMediaRecord(vRec), nil
		},
		DeleteFn: func(ctx context.Context, id string) error {
			return repo.Delete(ctx, id)
		},
		ListFn: func(ctx context.Context) ([]*artifacts.MediaRecord, error) {
			return artifacts.GetAllWithDriveFileID(ctx, repo.ListAll,
				func(r *assets.Record) (*artifacts.MediaRecord, bool) {
					if r.DriveFileID == "" {
						return nil, false
					}
					return voiceoverToMediaRecord(r), true
				})
		},
		PHashFn: artifacts.NoopFindByPHash,
	}
}

// ── Type conversions ───────────────────────────────────────────────────

func mediaRecordToVoiceover(mediaRec *artifacts.MediaRecord) *assets.Record {
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

	rec := &assets.Record{
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

func voiceoverToMediaRecord(rec *assets.Record) *artifacts.MediaRecord {
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
