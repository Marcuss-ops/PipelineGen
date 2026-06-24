package clips

import (
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// ClipToAssetNode converts a canonical asset.Asset to a domain
// *asset.AssetNode for unified tree handling. Exported so the
// sibling sources package (handler_sources_register_from_youtube.go)
// can build asset-tree nodes without depending on clips package
// internals.
//
// PG-005 (June 2026): this file has zero internal/infrastructure
// imports. The previous *assets.AssetNode switch case is gone because
// the typed-clip route no longer reaches through
// sqlite/assets.AssetNode (the bridge moved into the asset-tree
// adapter at internal/app/clips_adapters.go). The wave-11/12 follow-up
// will replace *asset.AssetNode with an explicit clips-domain tree
// seam; for now this helper stays as a pure-domain projector.
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
		ChildCount:  clip.ChildCount(),
	}
}

// treeNodeToAssetNode was previously a switch over (domain
// *asset.AssetNode, infra *repo.AssetNode, nil) used by some legacy
// response shapes. PG-005 (June 2026): the *repo.AssetNode branch is
// gone (infra type removed from this file's import set). The
// function is preserved for callers expecting the same signature;
// input == output today. If a third type-arms-out case appears in
// the future, land it as a Wave 11/12 follow-up rather than
// re-introducing the infra import.
func treeNodeToAssetNode(tn any) *asset.AssetNode {
	switch n := tn.(type) {
	case nil:
		return nil
	case *asset.AssetNode:
		return n
	default:
		return nil
	}
}
