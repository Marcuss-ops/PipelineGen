package clips

import (
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	sqlite "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite"
)

// ClipToAssetNode converts a canonical asset.Asset to sqlite.AssetNode
// for unified tree handling. Exported so the sibling sources package
// (handler_sources_register_from_youtube.go) can build asset-tree nodes
// without depending on clips package internals.
func ClipToAssetNode(clip *asset.Asset) *sqlite.AssetNode {
	if clip == nil {
		return nil
	}
	nodeType := "file"
	if clip.IsFolder() {
		nodeType = "folder"
	} else if clip.MediaType != "" {
		nodeType = string(clip.MediaType)
	}

	return &sqlite.AssetNode{
		ID:          clip.ID,
		Source:      string(clip.Source),
		AssetID:     clip.ID,
		Name:        clip.Name,
		Type:        nodeType,
		ParentID:    clip.ParentFolderID(),
		Path:        clip.FolderPath(),
		Depth:       clip.Depth(),
		IsFolder:    clip.IsFolder(),
		DriveFileID: clip.DriveFileID(),
		DriveLink:   clip.DriveLink(),
		Metadata:    clip.MetadataJSON(),
		CreatedAt:   clip.CreatedAt,
		UpdatedAt:   clip.UpdatedAt,
		ChildCount:  clip.ChildCount(),
	}
}

// treeNodeToAssetNode converts a sqlite/assettree node to the older
// media/models type used in some response shapes.
func treeNodeToAssetNode(tn *sqlite.AssetNode) *media.AssetNode {
	if tn == nil {
		return nil
	}
	return &media.AssetNode{
		ID:          tn.ID,
		Source:      tn.Source,
		AssetID:     tn.AssetID,
		Name:        tn.Name,
		Type:        tn.Type,
		ParentID:    tn.ParentID,
		RootID:      tn.RootID,
		Path:        tn.Path,
		Depth:       tn.Depth,
		IsFolder:    tn.IsFolder,
		DriveFileID: tn.DriveFileID,
		DriveLink:   tn.DriveLink,
		Metadata:    tn.Metadata,
		ChildCount:  tn.ChildCount,
	}
}
