package youtube

import (
	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
)

func toAssetDomainSlice(items []models.MediaAsset) []asset.MediaAsset {
	out := make([]asset.MediaAsset, 0, len(items))
	for i := range items {
		converted := toAssetDomain(&items[i])
		if converted != nil {
			out = append(out, *converted)
		}
	}
	return out
}

func toAssetDomain(m *models.MediaAsset) *asset.MediaAsset {
	if m == nil {
		return nil
	}
	state := asset.LifecycleState(m.LifecycleState)
	if !state.Valid() {
		state = asset.StateReady
	}
	a := &asset.MediaAsset{
		ID: m.ID,
		Source: m.Source,
		Name: m.Name,
		Filename: m.Filename,
		MediaType: m.MediaType,
		Category: m.Category,
		Group: m.Group,
		SourceURL: m.ExternalURL,
		ExternalURL: m.ExternalURL,
		ClipPageURL: m.ClipPageURL,
		ThumbnailURL: m.ThumbURL,
		DurationMs: int64(m.Duration),
		Tags: append([]string(nil), m.Tags...),
		SearchTerms: append([]string(nil), m.SearchTerms...),
		SearchText: m.SearchText,
		LifecycleState: state,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
		DeletedAt: m.DeletedAt,
		FolderID: m.FolderID,
		ParentFolderID: m.ParentFolderID,
		FolderPath: m.FolderPath,
		Depth: m.Depth,
		IsFolder: m.IsFolder,
		SceneType: m.SceneType,
		QualityScore: m.QualityScore,
		ReuseCount: m.ReuseCount,
		LastUsedAt: m.LastUsedAt,
		UsableFor: append([]string(nil), m.UsableFor...),
		AvoidFor: append([]string(nil), m.AvoidFor...),
		PHash: m.PHash,
		ChildCount: m.ChildCount,
	}
	if m.Metadata != nil {
		a.Metadata = make(map[string]any, len(m.Metadata))
		for key, value := range m.Metadata {
			a.Metadata[key] = value
		}
	}
	return a
}
