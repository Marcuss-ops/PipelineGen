package youtube

import (
	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
)

// toAssetDomainSlice converts a slice of models.MediaAsset to asset.MediaAsset.
func toAssetDomainSlice(items []models.MediaAsset) []asset.MediaAsset {
	out := make([]asset.MediaAsset, 0, len(items))
	for i := range items {
		a := toAssetDomain(&items[i])
		if a != nil {
			out = append(out, *a)
		}
	}
	return out
}

// toAssetDomain converts a models.MediaAsset to the canonical asset.MediaAsset.
func toAssetDomain(m *models.MediaAsset) *asset.MediaAsset {
	if m == nil {
		return nil
	}
	a := &asset.MediaAsset{
		ID:             m.ID,
		Source:         m.Source,
		Name:           m.Name,
		Filename:       m.Filename,
		MediaType:      m.MediaType,
		Category:       m.Category,
		Group:          m.Group,
		SourceURL:      m.ExternalURL,
		ExternalURL:    m.ExternalURL,
		ClipPageURL:    m.ClipPageURL,
		ThumbnailURL:   m.ThumbURL,
		DurationMs:     int64(m.Duration),
		Tags:           m.Tags,
		SearchTerms:    m.SearchTerms,
		SearchText:     m.SearchText,
		LifecycleState: asset.LifecycleState(m.Status),
		CreatedAt:      m.CreatedAt,
		UpdatedAt:      m.UpdatedAt,
		DeletedAt:      m.DeletedAt,
	}
	if m.Metadata != nil {
		a.Metadata = make(map[string]any, len(m.Metadata))
		for k, v := range m.Metadata {
			a.Metadata[k] = v
		}
	}
	return a
}
