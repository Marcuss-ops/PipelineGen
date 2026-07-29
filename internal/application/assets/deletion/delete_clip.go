package deletion

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"

	"go.uber.org/zap"
)

// DeleteClip deletes a clip by its ID and source.
//
// Blocco 3.1 commit 4/3 (June 2026): every side-effect (Drive Trash/Delete,
// Qdrant DeletePoints, SQLite SoftDelete, all 5 lifecycle_state hops on
// the canonical state machine) routes through the outbox dispatcher.
// There is NO synchronous Drive call here — the dispatcher's
// EnqueueDriveDelete atomically stamps lifecycle_state=DELETE_REQUESTED
// AND emits asset.drive.delete_requested.v1 in a single tx; the
// DriveDeleteHandler runs the actual Drive API call asynchronously
// (Trash or permanent Delete honours the `permanently` flag) + emits
// the next outbox event; IndexDeleteHandler closes the chain on Qdrant
// DeletePoints + SoftDelete + terminal lifecycle_state=DELETED hop.
//
// Blocco 3.1 commit 3/3 (July 2026): the asset-tree cleanup paths
// now PROPAGATE errors per godlike/07 ("no fake availability"). The
// pre-commit silent `_ = assetTreeSvc.DeleteByAssetID/DeleteNode` was
// the canonical silent-ignore anti-pattern (Rg surface target in this
// commit's user spec). Behavioural change: a non-nil assetTreeSvc
// that returns an error now surfaces via DeleteClip as a typed error.
//
// Defence-in-depth (legacy code path): if dispatcher is nil, we return
// a wiring-error rather than silently falling back to sync Drive
// delete — the previous "best-effort warn-and-continue" behaviour was
// the canonical regression that hid Drive-API failures from operators
// (QDRANT-002 ticket item D retro). The voiceover/images tables still
// use repo.Delete directly because those tables are NOT watched by
// the Qdrant indexer (QDRANT-002 PR8 followup will retrofit a
// DeleteEnqueue for those tables).
func (s *DeletionService) DeleteClip(ctx context.Context, source string, clipID string, permanently bool) error {
	s.log.Info("deleting clip", zap.String("source", source), zap.String("clip_id", clipID), zap.Bool("permanently", permanently))

	// 1. Resolve source — all clip-type sources (artlist/clips/stock/sound_effect)
	// share the same *assets.ClipsRepository in production.
	canonical := artifacts.CanonicalSource(source)
	if canonical == "" {
		return fmt.Errorf("invalid source: %s", source)
	}
	var repo *assets.ClipsRepository
	if artifacts.IsClipsSource(source) {
		repo = s.clipsRepo
	}
	if repo == nil && canonical != "voiceover" && canonical != "images" {
		return fmt.Errorf("invalid source: %s", source)
	}

	// 2. Validate the source row exists so callers get a "not found" error
	// before the dispatcher emits a no-op outbox event for a missing id.
	var err error
	switch {
	case canonical == "voiceover" && s.voiceoverRepo != nil:
		_, voErr := s.voiceoverRepo.GetByID(ctx, clipID)
		if voErr != nil {
			return fmt.Errorf("voiceover not found: %w", voErr)
		}
	case canonical == "images" && s.imagesRepo != nil:
		_, imgErr := s.imagesRepo.GetByID(ctx, clipID)
		if imgErr != nil {
			return fmt.Errorf("image not found: %w", imgErr)
		}
	case repo != nil:
		_, err = repo.Get(ctx, clipID)
		if err != nil {
			return fmt.Errorf("clip not found: %w", err)
		}
	default:
		return fmt.Errorf("repository for %s not available", source)
	}

	// 3. Emit through dispatcher (Blocco 3.1 state machine entrypoint).
	if canonical == "voiceover" && s.voiceoverRepo != nil {
		err = s.voiceoverRepo.Delete(ctx, clipID)
	} else if canonical == "images" && s.imagesRepo != nil {
		err = s.imagesRepo.Delete(ctx, clipID)
	} else if repo != nil {
		if s.dispatcher == nil {
			err = fmt.Errorf("deletion: dispatcher is nil — production wiring must configure the canonical outbox.Dispatcher (Blocco 3.1 commit 4/3 producer migration)")
		} else {
			err = s.dispatcher.EnqueueDriveDelete(ctx, clipID, permanently)
		}
	}

	if err != nil {
		return fmt.Errorf("failed to delete from database: %w", err)
	}

	// 4. Cleanup Asset Tree — errors PROPAGATE per godlike/07 (Blocco 3.1 commit 3/3).
	if s.assetTreeSvc != nil {
		if cleanupErr := s.assetTreeSvc.DeleteByAssetID(ctx, source, clipID); cleanupErr != nil {
			return fmt.Errorf("post-dispatch asset-tree cleanup DeleteByAssetID(%s, %s): %w", source, clipID, cleanupErr)
		}
		if cleanupErr := s.assetTreeSvc.DeleteNode(ctx, clipID); cleanupErr != nil {
			return fmt.Errorf("post-dispatch asset-tree cleanup DeleteNode(%s): %w", clipID, cleanupErr)
		}
	}

	return nil
}

// DeleteByDriveFile handles deletion by Drive file ID or link.
func (s *DeletionService) DeleteByDriveFile(ctx context.Context, fileID string, source string, permanently bool) error {
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

// FindClipByDriveFileID searches for a clip across repositories
// using the canonical SourceCatalog typed-port dispatch. Collapse
// (June 2026): local repos map + switch source eliminated —
// SourceCatalog.Resolve→SourceRepo.GetByDriveFileID handles every
// source uniformly with adapter-side shape conversion.
func (s *DeletionService) FindClipByDriveFileID(ctx context.Context, fileID string, sourceLimit string) (*asset.Asset, string, error) {
	catalog := artifacts.NewSourceCatalog(s.artlistRepo, s.clipsRepo, s.stockRepo, s.voiceoverRepo, s.imagesRepo)
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
		asset, err := repo.GetByDriveFileID(ctx, fileID)
		if err == nil && asset != nil {
			return asset, source, nil
		}
	}

	return nil, "", nil
}
