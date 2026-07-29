// Package clips — voiceover_dto_to_clip.go.
//
// Commit E (July 2026): the api/clips handler subtree migrated from
// *assets.VoiceoversRepository to the typed appclips.VoiceoverRepositoryPort,
// which returns *ClipVoiceoverRecordDTO values (the application-layer
// DTO declared in ports.go, distinct from the infra *assets.Record).
// The api handler needs to marshal those DTO values into the
// *asset.Asset shape that the HTTP response envelope expects;
// artifacts.VoiceoverRecordToClip operates on the CONCRETE infra
// record, so it cannot be reused at the typed-port seam.
//
// VoiceoverDTOToClip is the canonical DTO → asset.Asset converter at
// the application-layer (ports.go sibling). artifacts.VoiceoverRecordToClip
// is preserved verbatim for backward-compatibility with the existing
// artifacts.SourceCatalog + source_resolver.go callers that legitimately
// hold concrete *assets.Record pointers (kept outside Commit E's
// 7-file scope).
//
// Location rationale: a converter at this seam lives in the appclips
// package because the DTO is owned by appclips (declared in ports.go).
// Defining the converter here avoids an artifacts → appclips import
// (which would cycle: appclips/imports already pulls in artifacts).
//
// RFC-string parsing: the DTO carries CreatedAtRFC + UpdatedAtRFC as
// RFC3339Nano strings (kept portable, no time.Time coupling at the
// adapter boundary). Empty strings yield zero time.Time — which the
// existing asset.InsertPaths treat as "let DB default-fill".
package clips

import (
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// VoiceoverDTOToClip converts an appclips.ClipVoiceoverRecordDTO (the
// typed-port shape returned by VoiceoverRepositoryPort) to an
// *asset.Asset for api/clips handler responses.
//
// Parallel to artifacts.VoiceoverRecordToClip (which keeps the
// concrete-*assets.Record contract for source_resolver.go callers).
//
// Commit E (July 2026): canonical DTO → asset.Asset entry-point at
// the api/clips ↔ application/clips seam.
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
	clip := &asset.Asset{
		ID:          dto.ID,
		Name:        name,
		Filename:    dto.Filename,
		Source:      "voiceover",
		MediaType:   "audio",
		SearchTerms: []string{dto.TextPreview},
	}
	// RFC-string parsing: parse if non-empty (DTO convention). Empty
	// RFCs are tolerated — zero time.Time signals "let DB default-fill"
	// to the existing InsertPaths.
	if dto.CreatedAtRFC != "" {
		if t, err := time.Parse(time.RFC3339Nano, dto.CreatedAtRFC); err == nil {
			clip.CreatedAt = t
		}
	}
	if dto.UpdatedAtRFC != "" {
		if t, err := time.Parse(time.RFC3339Nano, dto.UpdatedAtRFC); err == nil {
			clip.UpdatedAt = t
		}
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
