package deletion

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"go.uber.org/zap"
)

// DeleteClip validates the source row, emits the canonical deletion request and
// cleans the asset-tree projection through the mutation capability.
func (s *DeletionService) DeleteClip(ctx context.Context, source, clipID string, permanently bool) error {
	s.log.Info("deleting clip", zap.String("source", source), zap.String("clip_id", clipID), zap.Bool("permanently", permanently))

	canonical := artifacts.CanonicalSource(source)
	if canonical == "" {
		return fmt.Errorf("invalid source: %s", source)
	}
	var repo *assets.ClipsRepository
	if artifacts.IsClipsSource(source) {
		repo = s.repositories.Clips
	}
	if repo == nil && canonical != "voiceover" && canonical != "images" {
		return fmt.Errorf("invalid source: %s", source)
	}

	var err error
	switch {
	case canonical == "voiceover" && s.repositories.Voiceover != nil:
		_, err = s.repositories.Voiceover.GetByID(ctx, clipID)
		if err != nil {
			return fmt.Errorf("voiceover not found: %w", err)
		}
	case canonical == "images" && s.repositories.Images != nil:
		_, err = s.repositories.Images.GetByID(ctx, clipID)
		if err != nil {
			return fmt.Errorf("image not found: %w", err)
		}
	case repo != nil:
		_, err = repo.Get(ctx, clipID)
		if err != nil {
			return fmt.Errorf("clip not found: %w", err)
		}
	default:
		return fmt.Errorf("repository for %s not available", source)
	}

	switch {
	case canonical == "voiceover" && s.repositories.Voiceover != nil:
		err = s.repositories.Voiceover.Delete(ctx, clipID)
	case canonical == "images" && s.repositories.Images != nil:
		err = s.repositories.Images.Delete(ctx, clipID)
	case repo != nil:
		if s.mutation.Dispatcher == nil {
			err = fmt.Errorf("deletion: dispatcher is nil — production wiring must configure the canonical outbox dispatcher")
		} else {
			err = s.mutation.Dispatcher.EnqueueDriveDelete(ctx, clipID, permanently)
		}
	}
	if err != nil {
		return fmt.Errorf("failed to delete from database: %w", err)
	}

	if s.mutation.AssetTree != nil {
		if err := s.mutation.AssetTree.DeleteByAssetID(ctx, source, clipID); err != nil {
			return fmt.Errorf("post-dispatch asset-tree cleanup DeleteByAssetID(%s, %s): %w", source, clipID, err)
		}
		if err := s.mutation.AssetTree.DeleteNode(ctx, clipID); err != nil {
			return fmt.Errorf("post-dispatch asset-tree cleanup DeleteNode(%s): %w", clipID, err)
		}
	}
	return nil
}

func (s *DeletionService) DeleteByDriveFile(ctx context.Context, fileID, source string, permanently bool) error {
	if fileID == "" {
		return fmt.Errorf("file_id is required")
	}
	clip, foundSource, err := s.FindClipByDriveFileID(ctx, fileID, source)
	if err != nil {
		return err
	}
	if clip == nil {
		return fmt.Errorf("clip not found in database for file %s", fileID)
	}
	return s.DeleteClip(ctx, foundSource, clip.ID, permanently)
}

// FindClipByDriveFileID searches through the canonical SourceCatalog.
func (s *DeletionService) FindClipByDriveFileID(ctx context.Context, fileID, sourceLimit string) (*asset.Asset, string, error) {
	catalog := artifacts.NewSourceCatalog(
		s.repositories.Artlist,
		s.repositories.Clips,
		s.repositories.Stock,
		s.repositories.Voiceover,
		s.repositories.Images,
	)
	sources := catalog.Names()
	if sourceLimit != "" && sourceLimit != "all" {
		canonical := artifacts.CanonicalSource(sourceLimit)
		if canonical == "" {
			return nil, "", fmt.Errorf("invalid source limit: %s", sourceLimit)
		}
		sources = []string{canonical}
	}
	for _, source := range sources {
		repo, ok := catalog.Resolve(source)
		if !ok || repo == nil {
			continue
		}
		found, err := repo.GetByDriveFileID(ctx, fileID)
		if err == nil && found != nil {
			return found, source, nil
		}
	}
	return nil, "", nil
}
