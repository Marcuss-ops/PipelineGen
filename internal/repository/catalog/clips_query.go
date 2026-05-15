package catalog

import (
	"context"
	"strings"
)

// SearchClips queries the clips database for matching media.
func (r *Repository) SearchClips(q string) ([]CatalogRecord, error) {
	if r.clipsRepo == nil {
		return nil, nil
	}

	clips, err := r.clipsRepo.SearchClipsByKeywords(context.Background(), "", strings.Fields(q), 100)
	if err != nil {
		return nil, err
	}

	var results []CatalogRecord
	for _, clip := range clips {
		rec := CatalogRecord{
			ID:        clip.ID,
			Name:      clip.Name,
			Path:      clip.FolderPath,
			Link:      clip.DriveLink,
			Source:    "clip_drive",
			DriveID:   clip.ID,
			MediaType: clip.MediaType,
			Tags:      clip.Tags,
			Duration:  clip.Duration,
		}
		results = append(results, rec)
	}
	return results, nil
}
