package youtube

import (
	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
)

// ToDomain converts a models.MediaAsset to the canonical asset.MediaAsset.
func ToDomain(m *models.MediaAsset) *asset.MediaAsset {
	if m == nil {
		return nil
	}
	return &asset.MediaAsset{
		ID:                  m.ID,
		Source:              m.Source,
		Name:                m.Name,
		Filename:            m.Filename,
		MediaType:           m.MediaType,
		Category:            m.Category,
		Group:               m.Group,
		SourceURL:           m.ExternalURL,
		ExternalURL:         m.ExternalURL,
		ClipPageURL:         m.ClipPageURL,
		ThumbnailURL:        m.ThumbURL,
		DurationMs:          int64(m.Duration),
		Tags:                m.Tags,
		SearchTerms:         m.SearchTerms,
		SearchText:          m.SearchText,
		LifecycleState:      asset.LifecycleState(m.Status),
		CreatedAt:           m.CreatedAt,
		UpdatedAt:           m.UpdatedAt,
		DeletedAt:           m.DeletedAt,
		EmbeddingJSON:       m.EmbeddingJSON,
		VisualEmbedding:     m.VisualEmbedding,
		TranscriptEmbedding: m.TranscriptEmbedding,
		VisualEmbeddingJSON: m.VisualEmbeddingJSON,
		FolderID:            m.FolderID,
		ParentFolderID:      m.ParentFolderID,
		FolderPath:          m.FolderPath,
		Depth:               m.Depth,
		IsFolder:            m.IsFolder,
		SceneType:           m.SceneType,
		QualityScore:        m.QualityScore,
		ReuseCount:          m.ReuseCount,
		LastUsedAt:          m.LastUsedAt,
		UsableFor:           m.UsableFor,
		AvoidFor:            m.AvoidFor,
		PHash:               m.PHash,
		ChildCount:          m.ChildCount,
		Status:              m.Status,
		Error:               m.Error,
		DriveFileID:         m.DriveFileID,
		DriveLink:           m.DriveLink,
		DownloadLink:        m.DownloadLink,
		LocalPath:           m.LocalPath,
		FileHash:            m.FileHash,
		Metadata:            m.Metadata,
	}
}

// ToLegacy converts a canonical asset.MediaAsset back to models.MediaAsset.
func ToLegacy(a *asset.MediaAsset) *models.MediaAsset {
	if a == nil {
		return nil
	}
	return &models.MediaAsset{
		ID:                  a.ID,
		Source:              a.Source,
		Name:                a.Name,
		Filename:            a.Filename,
		MediaType:           a.MediaType,
		Category:            a.Category,
		Group:               a.Group,
		ExternalURL:         a.ExternalURL,
		ClipPageURL:         a.ClipPageURL,
		ThumbURL:            a.ThumbnailURL,
		Duration:            int(a.DurationMs),
		Tags:                a.Tags,
		SearchTerms:         a.SearchTerms,
		SearchText:          a.SearchText,
		Status:              a.Status,
		CreatedAt:           a.CreatedAt,
		UpdatedAt:           a.UpdatedAt,
		DeletedAt:           a.DeletedAt,
		EmbeddingJSON:       a.EmbeddingJSON,
		VisualEmbedding:     a.VisualEmbedding,
		TranscriptEmbedding: a.TranscriptEmbedding,
		VisualEmbeddingJSON: a.VisualEmbeddingJSON,
		FolderID:            a.FolderID,
		ParentFolderID:      a.ParentFolderID,
		FolderPath:          a.FolderPath,
		Depth:               a.Depth,
		IsFolder:            a.IsFolder,
		SceneType:           a.SceneType,
		QualityScore:        a.QualityScore,
		ReuseCount:          a.ReuseCount,
		LastUsedAt:          a.LastUsedAt,
		UsableFor:           a.UsableFor,
		AvoidFor:            a.AvoidFor,
		PHash:               a.PHash,
		ChildCount:          a.ChildCount,
		Error:               a.Error,
		DriveFileID:         a.DriveFileID,
		DriveLink:           a.DriveLink,
		DownloadLink:        a.DownloadLink,
		LocalPath:           a.LocalPath,
		FileHash:            a.FileHash,
		Metadata:            a.Metadata,
	}
}

// ToDomainSlice converts a slice of legacy models to canonical.
func ToDomainSlice(ms []models.MediaAsset) []asset.MediaAsset {
	out := make([]asset.MediaAsset, 0, len(ms))
	for i := range ms {
		if a := ToDomain(&ms[i]); a != nil {
			out = append(out, *a)
		}
	}
	return out
}
