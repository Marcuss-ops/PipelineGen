package clips

import (
	"path/filepath"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ImageAssetToAsset converts an image domain DTO to the canonical asset shape
// used by clips/API callers.
func ImageAssetToAsset(item *asset.ImageAsset) *asset.Asset {
	if item == nil {
		return nil
	}
	name := item.Description
	if name == "" {
		name = filepath.Base(item.PathRel)
	}
	id := item.SlugID
	if id == "" {
		id = item.Hash
	}
	clip := &asset.Asset{
		ID:          id,
		Name:        name,
		Filename:    filepath.Base(item.PathRel),
		Source:      "images",
		MediaType:   "image",
		Tags:        item.Tags,
		SearchTerms: []string{item.Description},
		CreatedAt:   item.CreatedAt,
		UpdatedAt:   item.CreatedAt,
	}
	clip.SetDriveLink(item.SourceURL)
	clip.SetDriveFileID(item.DriveFileID)
	clip.SetFileHash(item.Hash)
	clip.SetLocalPath(item.PathRel)
	return clip
}
