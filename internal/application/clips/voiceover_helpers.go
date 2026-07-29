package clips

import (
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// VoiceoverDTOToAsset converts a ClipVoiceoverRecordDTO into the
// canonical *asset.Asset shape used by api/clips callers (clip_ops
// Cleanup loop, clip_enrich / VerifyClip voiceover branch, clip_read
// GetClip + ListClips voiceover paths).
//
// PG-005 (June 2026): mirrors artifacts.VoiceoverRecordToClip but
// consumes the application-layer DTO so the api layer never imports
// internal/infrastructure/database/sqlite/assets. Field set is the
// same projection logic — name falls back to a 50-char TextPreview
// slice when filename is empty; the canonical asset.Source value for
// voiceovers is "voiceover". The DTO carries RFC3339-string
// timestamps; we parse them back to time.Time so downstream consumers
// (deletion service, diagnostics, ORDER BY updated_at queries) keep
// the same sort semantics they had against the previous concrete
// *assets.Record path.
func VoiceoverDTOToAsset(rec *ClipVoiceoverRecordDTO) *asset.Asset {
	if rec == nil {
		return nil
	}
	name := rec.Filename
	if name == "" {
		name = rec.TextPreview
		if len(name) > 50 {
			name = name[:50]
		}
	}
	var createdAt, updatedAt time.Time
	if rec.CreatedAtRFC != "" {
		if t, err := time.Parse(time.RFC3339Nano, rec.CreatedAtRFC); err == nil {
			createdAt = t
		}
	}
	if rec.UpdatedAtRFC != "" {
		if t, err := time.Parse(time.RFC3339Nano, rec.UpdatedAtRFC); err == nil {
			updatedAt = t
		}
	}
	clip := &asset.Asset{
		ID:          rec.ID,
		Name:        name,
		Filename:    rec.Filename,
		Source:      "voiceover",
		MediaType:   "audio",
		SearchTerms: []string{rec.TextPreview},
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
	}
	clip.SetFolderID(rec.FolderID)
	clip.SetFolderPath(rec.FolderPath)
	clip.SetDriveLink(rec.DriveLink)
	clip.SetDriveFileID(rec.DriveFileID)
	clip.SetDownloadLink(rec.DownloadLink)
	clip.SetFileHash(rec.FileHash)
	clip.SetLocalPath(rec.LocalPath)
	clip.SetMetadataJSON(rec.Metadata)
	return clip
}
