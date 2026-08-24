package voiceover

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// NewVoiceoverRegistryAdapter returns an artifacts.Registry backed by a
// VoiceoversRepository. The returned *artifacts.SimpleRegistry delegates
// every Registry method to a repo-specific callback.
func NewVoiceoverRegistryAdapter(repo *assets.VoiceoversRepository) artifacts.Registry {
	return &artifacts.SimpleRegistry{
		UpsertFn: func(ctx context.Context, rec *artifacts.MediaRecord) error {
			converted, err := mediaRecordToVoiceover(rec)
			if err != nil {
				return err
			}
			return repo.Upsert(ctx, converted)
		},
		GetFn: func(ctx context.Context, id string) (*artifacts.MediaRecord, error) {
			vRec, err := repo.GetByID(ctx, id)
			if err != nil || vRec == nil {
				return nil, err
			}
			converted, err := voiceoverToMediaRecord(vRec)
			if err != nil {
				return nil, err
			}
			return converted, nil
		},
		DeleteFn: func(ctx context.Context, id string) error {
			return repo.Delete(ctx, id)
		},
		ListFn: func(ctx context.Context) ([]*artifacts.MediaRecord, error) {
			records, err := repo.ListAll(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]*artifacts.MediaRecord, 0, len(records))
			for _, record := range records {
				if record == nil || record.DriveFileID == "" {
					continue
				}
				converted, err := voiceoverToMediaRecord(record)
				if err != nil {
					return nil, err
				}
				out = append(out, converted)
			}
			return out, nil
		},
		PHashFn: artifacts.NoopFindByPHash,
	}
}

// VoiceoverMetadata is the typed persistence envelope stored in a voiceover
// media record's metadata column. Keeping the wire shape here avoids passing
// anonymous structs or map[string]any through the registry adapter.
type VoiceoverMetadata struct {
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

// Decode parses persisted metadata. Empty metadata is treated as the legacy
// zero envelope; malformed non-empty JSON is returned to the registry caller.
func (m *VoiceoverMetadata) Decode(raw string) error {
	if m == nil {
		return fmt.Errorf("voiceover metadata: nil receiver")
	}
	if raw == "" || raw == "{}" {
		return nil
	}
	if err := json.Unmarshal([]byte(raw), m); err != nil {
		return fmt.Errorf("voiceover metadata decode: %w", err)
	}
	return nil
}

// Encode serializes the typed metadata envelope for persistence.
func (m VoiceoverMetadata) Encode() ([]byte, error) {
	encoded, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("voiceover metadata encode: %w", err)
	}
	return encoded, nil
}

// ── Type conversions ───────────────────────────────────────────────────

func mediaRecordToVoiceover(mediaRec *artifacts.MediaRecord) (*assets.Record, error) {
	if mediaRec == nil {
		return nil, fmt.Errorf("voiceover metadata: nil media record")
	}
	var meta VoiceoverMetadata
	if err := meta.Decode(mediaRec.Metadata); err != nil {
		return nil, err
	}

	rec := &assets.Record{
		ID:            mediaRec.ID,
		RequestID:     meta.RequestID,
		TextHash:      meta.TextHash,
		TextPreview:   meta.TextPreview,
		Language:      meta.Language,
		Voice:         meta.Voice,
		Filename:      mediaRec.Filename,
		LocalPath:     mediaRec.LocalPath,
		CleanedPath:   meta.CleanedPath,
		FolderID:      mediaRec.FolderID,
		FolderPath:    mediaRec.FolderPath,
		DriveFileID:   mediaRec.DriveFileID,
		DriveLink:     mediaRec.DriveLink,
		DownloadLink:  mediaRec.DownloadLink,
		LegacyFileMD5: mediaRec.LegacyFileMD5,
		Status:        mediaRec.Status,
		Error:         mediaRec.Error,
		Strategy:      meta.Strategy,
		Metadata:      mediaRec.Metadata,
	}
	if meta.CreatedAt != "" {
		rec.CreatedAt = timeutil.ParseRFC3339(meta.CreatedAt)
	}
	if meta.UpdatedAt != "" {
		rec.UpdatedAt = timeutil.ParseRFC3339(meta.UpdatedAt)
	}
	return rec, nil
}

func voiceoverToMediaRecord(rec *assets.Record) (*artifacts.MediaRecord, error) {
	if rec == nil {
		return nil, fmt.Errorf("voiceover metadata: nil record")
	}
	metaJSON, err := (VoiceoverMetadata{
		TextHash: rec.TextHash, TextPreview: rec.TextPreview, Language: rec.Language,
		Voice: rec.Voice, CleanedPath: rec.CleanedPath, Strategy: rec.Strategy,
		RequestID: rec.RequestID, CreatedAt: timeutil.FormatRFC3339(rec.CreatedAt),
		UpdatedAt: timeutil.FormatRFC3339(rec.UpdatedAt),
	}).Encode()
	if err != nil {
		return nil, err
	}

	return &artifacts.MediaRecord{
		ID: rec.ID, Source: "voiceover", Name: rec.TextPreview, Filename: rec.Filename,
		FolderID: rec.FolderID, FolderPath: rec.FolderPath, MediaType: "audio",
		DriveFileID: rec.DriveFileID, DriveLink: rec.DriveLink, DownloadLink: rec.DownloadLink,
		LegacyFileMD5: rec.LegacyFileMD5, LocalPath: rec.LocalPath, Status: rec.Status,
		Error: rec.Error, Metadata: string(metaJSON),
	}, nil
}
