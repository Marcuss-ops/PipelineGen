package ingest

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assetop"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
	vorepo "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
)

type voiceoverStoreAdapter struct {
	repo *vorepo.VoiceoversRepository
}

func NewVoiceoverStoreAdapter(repo *vorepo.VoiceoversRepository) lifecycle.AssetRecordStore {
	return &voiceoverStoreAdapter{repo: repo}
}

func (a *voiceoverStoreAdapter) Upsert(ctx context.Context, rec *artifacts.MediaRecord) error {
	return a.repo.Upsert(ctx, mediaRecordToVoiceover(rec))
}

func (a *voiceoverStoreAdapter) Get(ctx context.Context, id string) (*artifacts.MediaRecord, error) {
	vRec, err := a.repo.GetByID(ctx, id)
	if err != nil || vRec == nil {
		return nil, err
	}
	return voiceoverToMediaRecord(vRec), nil
}

func (a *voiceoverStoreAdapter) FindExisting(ctx context.Context, query assetop.ExistingAssetQuery) (*assetop.AssetRecord, error) {
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
		vRec, err := a.repo.GetByDriveFileID(ctx, query.DriveFileID)
		if err != nil {
			return nil, err
		}
		if vRec != nil {
			return mediaRecordToAssetRecord(voiceoverToMediaRecord(vRec)), nil
		}
	}

	if query.LegacyFileMD5 != "" {
		recs, err := a.repo.ListAll(ctx)
		if err != nil {
			return nil, err
		}
		for _, rec := range recs {
			if strings.EqualFold(strings.TrimSpace(rec.LegacyFileMD5), strings.TrimSpace(query.LegacyFileMD5)) {
				return mediaRecordToAssetRecord(voiceoverToMediaRecord(rec)), nil
			}
		}
	}

	if query.Filename != "" {
		recs, err := a.repo.ListAll(ctx)
		if err != nil {
			return nil, err
		}
		for _, rec := range recs {
			if strings.EqualFold(strings.TrimSpace(rec.Filename), strings.TrimSpace(query.Filename)) {
				return mediaRecordToAssetRecord(voiceoverToMediaRecord(rec)), nil
			}
		}
	}

	return nil, nil
}

func (a *voiceoverStoreAdapter) ListWithDriveFileID(ctx context.Context, source string) ([]*assetop.AssetRecord, error) {
	recs, err := a.repo.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	var out []*assetop.AssetRecord
	for _, rec := range recs {
		if strings.TrimSpace(rec.DriveFileID) == "" {
			continue
		}
		out = append(out, mediaRecordToAssetRecord(voiceoverToMediaRecord(rec)))
	}
	return out, nil
}

func (a *voiceoverStoreAdapter) MarkDriveMissing(ctx context.Context, id string) error {
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

func (a *voiceoverStoreAdapter) DeleteAssetRecord(ctx context.Context, id string) error {
	return a.repo.Delete(ctx, id)
}

func voiceoverToMediaRecord(rec *vorepo.Record) *artifacts.MediaRecord {
	if rec == nil {
		return nil
	}
	return &artifacts.MediaRecord{
		ID:           rec.ID,
		Name:         rec.TextPreview,
		Filename:     rec.Filename,
		Source:       "voiceover",
		MediaType:    "audio",
		FolderID:     rec.FolderID,
		FolderPath:   rec.FolderPath,
		LocalPath:    rec.LocalPath,
		DriveFileID:  rec.DriveFileID,
		DriveLink:    rec.DriveLink,
		DownloadLink: rec.DownloadLink,
		LegacyFileMD5:     rec.LegacyFileMD5,
		Status:       rec.Status,
		Error:        rec.Error,
		Metadata:     rec.Metadata,
		SourceID:     rec.RequestID,
	}
}

func mediaRecordToVoiceover(rec *artifacts.MediaRecord) *vorepo.Record {
	meta := map[string]any{}
	if strings.TrimSpace(rec.Metadata) != "" && rec.Metadata != "{}" {
		_ = json.Unmarshal([]byte(rec.Metadata), &meta)
	}

	requestID := getString(meta, "request_id")
	textHash := getString(meta, "text_hash")
	textPreview := getString(meta, "text_preview")
	language := getString(meta, "language")
	voice := getString(meta, "voice")
	cleanedPath := getString(meta, "cleaned_path")
	strategy := getString(meta, "strategy")

	now := time.Now().UTC()
	return &vorepo.Record{
		ID:           rec.ID,
		RequestID:    requestID,
		TextHash:     textHash,
		TextPreview:  textPreview,
		Language:     language,
		Voice:        voice,
		Filename:     rec.Filename,
		LocalPath:    rec.LocalPath,
		CleanedPath:  cleanedPath,
		FolderID:     rec.FolderID,
		FolderPath:   rec.FolderPath,
		DriveFileID:  rec.DriveFileID,
		DriveLink:    rec.DriveLink,
		DownloadLink: rec.DownloadLink,
		LegacyFileMD5:     rec.LegacyFileMD5,
		Status:       rec.Status,
		Error:        rec.Error,
		Strategy:     strategy,
		Metadata:     rec.Metadata,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}
