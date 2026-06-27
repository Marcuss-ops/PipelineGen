package clips

import (
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// VoiceoverDTOToClip converts the application-layer port DTO
// (*ClipVoiceoverRecordDTO, declared in ports.go) to *asset.Asset.
//
// W14-PR2 slice 4 (June 2026): enables clip_ops.go + clip_read.go to
// stay port-only when projecting voiceover hits into the Asset slice
// that the response is marshalled from. Placed here (next to the
// DTO definition) rather than in internal/application/assets/artifacts
// because that package already imports this package — keeping the
// converter here breaks the artifacts→clips→artifacts cycle while
// avoiding duplicating the 12-field Asset projection at the four
// call sites.
//
// RFC3339 strings parse back to time.Time (zero-value on parse
// failure) so the resulting *asset.Asset satisfies the same
// downstream consumers as VoiceoverRecordToClip.
func VoiceoverDTOToClip(dto *ClipVoiceoverRecordDTO) *asset.Asset {
	if dto == nil {
		return nil
	}
	name := dto.Filename
	if name == "" {
		name = dto.TextPreview
		if len(name) > 50 {
			name = name[:50]
		}
	}
	createdAt, _ := time.Parse(time.RFC3339, dto.CreatedAtRFC)
	updatedAt, _ := time.Parse(time.RFC3339, dto.UpdatedAtRFC)
	clip := &asset.Asset{
		ID:          dto.ID,
		Name:        name,
		Filename:    dto.Filename,
		Source:      "voiceover",
		MediaType:   "audio",
		SearchTerms: []string{dto.TextPreview},
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
	}
	clip.SetFolderID(dto.FolderID)
	clip.SetFolderPath(dto.FolderPath)
	clip.SetDriveLink(dto.DriveLink)
	clip.SetDriveFileID(dto.DriveFileID)
	clip.SetDownloadLink(dto.DownloadLink)
	clip.SetFileHash(dto.FileHash)
	clip.SetLocalPath(dto.LocalPath)
	clip.SetMetadataJSON(dto.Metadata)
	return clip
}
