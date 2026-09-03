// Package clips (conversions) — typed conversions between the canonical
// domain shape (internal/kernel/asset.Asset + internal/kernel/asset.AssetNode)
// and the infra-shaped sqlite/assettree node (internal/platform/
// database/sqlite/assets.AssetNode).
//
// Wave 14 — PR2 slice 1/8 (June 2026): previously inlined in
// internal/capabilities/assets/clips/helpers.go as `ClipToAssetNode` (CamelCase
// exported) and `treeNodeToAssetNode` (lowercase — only call sites are
// inside the same api/ package). Both helpers were grandfathered on
// the retired API/infrastructure boundary because they reached into a
// platform layer from transport code, violating the four-root architecture.
//
// After PR2 slice 1: the two functions are lifted to this file. The
// application layer is the canonical seam above the infra, so it is
// the right place for cross-boundary type adapters. The api/ handlers
// now call appclips.ClipToAssetNode(...) / appclips.TreeNodeToAssetNode(...)
// without themselves importing infra. internal/capabilities/assets/clips/helpers.go
// is REMOVED and its line dropped from the allowlist.
//
// Why CamelCase (was lowercase `treeNodeToAssetNode`): a same-package
// private fn was fine when only api/clips used it. Now that 4 api files
// + the application layer need the symbol, it MUST be exported.
//
// Why both functions still return/post the infra `*assets.AssetNode`
// (rather than collapsing to a single domain-only helper): the caller
// pattern is `node := ClipToAssetNode(clip); assetTreeSvc.UpsertNode(ctx,
// node)` — UpsertNode's signature in
// internal/capabilities/assets/assettree.Service accepts *assets.AssetNode
// from the sqlite/assettree domain. A deeper unification (collapse
// *assets.AssetNode + *asset.AssetNode in Wave 16) is out of scope for
// this slice — the goal is to remove ONE api/ file from the allowlist
// without touching the assettree package or any caller of UpsertNode.
package clips

import (
	"path/filepath"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	assets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
)

// ClipToAssetNode converts a canonical asset.Asset to the shared
// sqlite/assettree AssetNode shape. Exported so api-layer handlers
// (internal/capabilities/assets/clips/*) can build asset-tree nodes from a
// domain clip without themselves importing infrastructure types.
func ClipToAssetNode(clip *asset.Asset) *assets.AssetNode {
	if clip == nil {
		return nil
	}
	nodeType := "file"
	if clip.IsFolder() {
		nodeType = "folder"
	} else if clip.MediaType != "" {
		nodeType = string(clip.MediaType)
	}

	return &assets.AssetNode{
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

// TreeNodeToAssetNode converts a sqlite/assettree node to the canonical
// domain asset.AssetNode shape. Used by GET folder/tree endpoints to
// reshape infra-shaped tree rows for the api response (folder_tree.go,
// GetFolderChildren, GetTree fallbacks).
//
// Pre-PR2: lowercase `treeNodeToAssetNode` in internal/capabilities/assets/clips/helpers.go.
func TreeNodeToAssetNode(tn *assets.AssetNode) *asset.AssetNode {
	if tn == nil {
		return nil
	}
	return &asset.AssetNode{
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

// ImageAssetToAsset converts an image domain DTO to the canonical asset shape
// used by clips/API callers.
func ImageAssetToAsset(item *detail.ImageAsset) *asset.Asset {
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
	clip.SetLegacyFileMD5(item.Hash)
	clip.SetLocalPath(item.PathRel)
	return clip
}
