package clips

import (
	"context"
	"fmt"
)

// BulkTagsUseCase adds or removes tags from multiple clips atomically.
//
// PG-005 (June 2026): treeBuilder replaces the previous concrete
// *assettree.Service dependency. The infra-to-domain bridges (the
// clipToAssetNode converter and the UpsertNode call) live in the
// composition-root adapter at
// internal/app/clips_adapters.go::clipsAssetTreeAdapter, so this use
// case has zero internal/infrastructure imports.
type BulkTagsUseCase struct {
	sourceResolver SourceResolverPort
	treeBuilder    ClipTreeBuilderPort
}

// NewBulkTagsUseCase constructs the use case.
// PG-005 (June 2026): the resolver parameter is the typed
// SourceResolverPort (defined in this package's ports.go) and the
// tree parameter is the typed ClipTreeBuilderPort. Both return
// ports (NOT the concrete *assets.ClipsRepository or *assets.AssetNode)
// so the use case stays domain-only.
func NewBulkTagsUseCase(resolver SourceResolverPort, tree ClipTreeBuilderPort) *BulkTagsUseCase {
	return &BulkTagsUseCase{sourceResolver: resolver, treeBuilder: tree}
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

func (uc *BulkTagsUseCase) repoForSource(source string) ClipRepositoryPort {
	if uc.sourceResolver == nil {
		return nil
	}
	return uc.sourceResolver.ResolveRepo(source)
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

	// Update Asset Tree via the typed port. The adapter handles the
	// domain-to-infra shape conversion.
	if uc.treeBuilder != nil {
		for _, id := range req.IDs {
			clip, err := repo.GetClip(ctx, id)
			if err == nil && clip != nil {
				uc.treeBuilder.UpsertFromAsset(ctx, clip)
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

	// Update Asset Tree via the typed port. Adapter handles the
	// domain-to-infra shape conversion.
	if uc.treeBuilder != nil {
		for _, id := range req.IDs {
			clip, err := repo.GetClip(ctx, id)
			if err == nil && clip != nil {
				uc.treeBuilder.UpsertFromAsset(ctx, clip)
			}
		}
	}

	return &BulkTagsResult{
		Source:  req.Source,
		Count:   len(req.IDs),
		Message: fmt.Sprintf("removed tags from %d items", len(req.IDs)),
	}, nil
}
