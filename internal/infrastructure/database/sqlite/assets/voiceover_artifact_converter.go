package assets

import "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"

// VoiceoverRecordToAsset converts a concrete SQLite record at the
// infrastructure/application boundary. Application use cases consume the
// resulting domain asset rather than the repository record type.
func VoiceoverRecordToAsset(rec *Record) *asset.Asset {
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
	clip := &asset.Asset{
		ID:          rec.ID,
		Name:        name,
		Filename:    rec.Filename,
		Source:      "voiceover",
		MediaType:   "audio",
		SearchTerms: []string{rec.TextPreview},
		CreatedAt:   rec.CreatedAt,
		UpdatedAt:   rec.UpdatedAt,
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
