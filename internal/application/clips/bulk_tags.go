package clips

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
)

// BulkTagsUseCase adds or removes tags from multiple clips atomically.
type BulkTagsUseCase struct {
	clipsRepo    *assets.ClipsRepository
	assetTreeSvc *assettree.Service
}

// NewBulkTagsUseCase constructs the use case.
func NewBulkTagsUseCase(clipsRepo *assets.ClipsRepository, treeSvc *assettree.Service) *BulkTagsUseCase {
	return &BulkTagsUseCase{clipsRepo: clipsRepo, assetTreeSvc: treeSvc}
}

// BulkTagsRequest contains the input for bulk tag operations.
type BulkTagsRequest struct {
	Source string   `json:"source"`
	IDs    []string `json:"ids"`
	Tags   []string `json:"tags"`
}

// BulkTagsResult contains the output after the operation.
type BulkTagsResult struct {
	Source  string `json:"source"`
	Count   int    `json:"count"`
	Message string `json:"message"`
}

func (uc *BulkTagsUseCase) repoForSource(source string) *assets.ClipsRepository {
	if uc.clipsRepo == nil {
		return nil
	}
	return uc.clipsRepo
}

// AddTags adds tags to multiple clips.
func (uc *BulkTagsUseCase) AddTags(ctx context.Context, req BulkTagsRequest) (*BulkTagsResult, error) {
	if len(req.IDs) == 0 || len(req.Tags) == 0 {
		return &BulkTagsResult{Source: req.Source, Count: 0, Message: "no items or tags provided"}, nil
	}

	repo := uc.repoForSource(req.Source)
	if repo == nil {
		return nil, fmt.Errorf("invalid source: %s", req.Source)
	}

	if err := repo.BulkAddTags(ctx, req.IDs, req.Tags); err != nil {
		return nil, fmt.Errorf("bulk add tags failed: %w", err)
	}

	// Update Asset Tree if available
	if uc.assetTreeSvc != nil {
		for _, id := range req.IDs {
			clip, err := repo.GetClip(ctx, id)
			if err == nil && clip != nil {
				node := clipToAssetNode(clip)
				uc.assetTreeSvc.UpsertNode(ctx, node)
			}
		}
	}

	return &BulkTagsResult{
		Source:  req.Source,
		Count:   len(req.IDs),
		Message: fmt.Sprintf("added tags to %d items", len(req.IDs)),
	}, nil
}

// RemoveTags removes tags from multiple clips.
func (uc *BulkTagsUseCase) RemoveTags(ctx context.Context, req BulkTagsRequest) (*BulkTagsResult, error) {
	if len(req.IDs) == 0 || len(req.Tags) == 0 {
		return &BulkTagsResult{Source: req.Source, Count: 0, Message: "no items or tags provided"}, nil
	}

	repo := uc.repoForSource(req.Source)
	if repo == nil {
		return nil, fmt.Errorf("invalid source: %s", req.Source)
	}

	if err := repo.BulkRemoveTags(ctx, req.IDs, req.Tags); err != nil {
		return nil, fmt.Errorf("bulk remove tags failed: %w", err)
	}

	// Update Asset Tree if available
	if uc.assetTreeSvc != nil {
		for _, id := range req.IDs {
			clip, err := repo.GetClip(ctx, id)
			if err == nil && clip != nil {
				node := clipToAssetNode(clip)
				uc.assetTreeSvc.UpsertNode(ctx, node)
			}
		}
	}

	return &BulkTagsResult{
		Source:  req.Source,
		Count:   len(req.IDs),
		Message: fmt.Sprintf("removed tags from %d items", len(req.IDs)),
	}, nil
}

// clipToAssetNode converts a canonical asset.Asset to an assets.AssetNode.
func clipToAssetNode(clip *asset.Asset) *assets.AssetNode {
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
