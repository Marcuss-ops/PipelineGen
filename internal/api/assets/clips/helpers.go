package clips

import (
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	repo "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
)

// ClipToAssetNode converts a canonical asset.Asset to a domain
// *asset.AssetNode for unified tree handling. Exported so the
// sibling sources package (handler_sources_register_from_youtube.go)
// can build asset-tree nodes without depending on clips package
// internals.
//
// PG-005 (June 2026): the return type is the domain *asset.AssetNode
// (instead of the previous concrete *assets.AssetNode that lived in
// internal/infrastructure/database/sqlite/assets). This drops the
// sole infrastructure import from this file. Callers down-stream
// consume the domain node via *assettree.Service adapters; the
// adapter projects the domain fields onto whatever storage type the
// asset-tree expects (a Wave 11/12 follow-up aligns that contract).
func ClipToAssetNode(clip *asset.Asset) *asset.AssetNode {
	if clip == nil {
		return nil
	}
	nodeType := "file"
	if clip.IsFolder() {
		nodeType = "folder"
	} else if clip.MediaType != "" {
		nodeType = string(clip.MediaType)
	}

	return &asset.AssetNode{
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
// PG-005 (June 2026): both input and output are now domain
// *asset.AssetNode (was concrete *assets.AssetNode on both sides).
// Concrete brand removed; the function only projects the same domain
// fields verbatim today and is retained as a stable seam for the
// Wave 11/12 follow-up that splits domain vs tree-representation types.
func treeNodeToAssetNode(tn any) *asset.AssetNode {
	switch n := tn.(type) {
	case nil:
		return nil
	case *asset.AssetNode:
		return &asset.AssetNode{
			ID:          n.ID,
			Source:      n.Source,
			AssetID:     n.AssetID,
			Name:        n.Name,
			Type:        n.Type,
			ParentID:    n.ParentID,
			RootID:      n.RootID,
			Path:        n.Path,
			Depth:       n.Depth,
			IsFolder:    n.IsFolder,
			DriveFileID: n.DriveFileID,
			DriveLink:   n.DriveLink,
			Metadata:    n.Metadata,
			CreatedAt:   n.CreatedAt,
			UpdatedAt:   n.UpdatedAt,
			ChildCount:  n.ChildCount,
		}
	case *repo.AssetNode:
		return &asset.AssetNode{
			ID:          n.ID,
			Source:      n.Source,
			AssetID:     n.AssetID,
			Name:        n.Name,
			Type:        n.Type,
			ParentID:    n.ParentID,
			RootID:      n.RootID,
			Path:        n.Path,
			Depth:       n.Depth,
			IsFolder:    n.IsFolder,
			DriveFileID: n.DriveFileID,
			DriveLink:   n.DriveLink,
			Metadata:    n.Metadata,
			CreatedAt:   n.CreatedAt,
			UpdatedAt:   n.UpdatedAt,
			ChildCount:  n.ChildCount,
		}
	default:
		return nil
	}
}
