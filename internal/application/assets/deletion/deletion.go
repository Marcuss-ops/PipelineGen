package deletion

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

// DispatcherPort is the application-layer port for DeletionService's
// outbox emit (Pattern 0 — declared at the consumer side, satisfied
// by *outbox.Dispatcher in production. Structural satisfaction means
// callers don't need to import outbox — they pass the production
// concrete directly).
//
// Blocco 3.1 commit 4/3 (June 2026): the port is intentionally
// NARROW (single method). The wider outbox.Dispatcher surface is
// available elsewhere in the composition root — DeletionService
// itself only ever needs the drive-delete emission path. Consumers
// that need EnqueueIndexDelete / EnqueueAndRestore etc wire those
// directly via their own ports.
type DispatcherPort interface {
	EnqueueDriveDelete(ctx context.Context, assetID string, permanently bool) error
}

// DeletionService handles deletion routing.
// Blocco 3.1 commit 4/3 (June 2026): the service no longer accepts
// synchronous Drive side-effects — every deletion route (Drive Trash
// or permanent Delete, Qdrant DeletePoints, SQLite SoftDelete, all
// lifecycle_state hops on the canonical state machine) is delegated
// to the outbox dispatcher. See the dispatcher's EnqueueDriveDelete
// docstring (internal/infrastructure/database/sqlite/outbox/
// dispatcher_delete.go) for the full state-machine sequence.
type DeletionService struct {
	artlistRepo   *assets.ClipsRepository
	clipsRepo     *assets.ClipsRepository
	stockRepo     *assets.ClipsRepository
	voiceoverRepo *assets.VoiceoversRepository
	imagesRepo    *assets.ImagesRepository
	// driveUploader is RETIRED at Blocco 3.1 commit 4/3 (June 2026).
	// The field + ctor parameter are retained for back-compat with
	// 3 production callers (internal/app/build_bundles_domain.go,
	// internal/app/module_media.go, internal/application/assets/
	// maintenance/service_test.go) but ignored by DeleteClip. Future
	// commit retires the field; tracked under the Blocco 3.1 commit
	// 4/3 forward-pointer in architecture/current.yaml.
	driveUploader *drive.Uploader
	assetTreeSvc  *assettree.Service
	assetIndexSvc *assetindex.Service
	dispatcher    DispatcherPort
	log           *zap.Logger
}

// NewDeletionService creates a new deletion service.
//
// QDRANT-002 PR7: dispatcher is the canonical outbox.Dispatcher.
// Blocco 3.1 commit 4/3 (June 2026): dispatcher's type is the
// port interface DispatcherPort so test fixtures can substitute a
// recording mock without spinning up an in-memory SQLite + txmgr
// fixture. Production wiring passes *outbox.Dispatcher which
// satisfies the port structurally.
func NewDeletionService(
	artlistRepo, clipsRepo, stockRepo *assets.ClipsRepository,
	voiceoverRepo *assets.VoiceoversRepository,
	imagesRepo *assets.ImagesRepository,
	driveUploader *drive.Uploader,
	assetTreeSvc *assettree.Service,
	assetIndexSvc *assetindex.Service,
	dispatcher DispatcherPort,
	log *zap.Logger,
) *DeletionService {
	return &DeletionService{
		artlistRepo:   artlistRepo,
		clipsRepo:     clipsRepo,
		stockRepo:     stockRepo,
		voiceoverRepo: voiceoverRepo,
		imagesRepo:    imagesRepo,
		driveUploader: driveUploader,
		assetTreeSvc:  assetTreeSvc,
		assetIndexSvc: assetIndexSvc,
		dispatcher:    dispatcher,
		log:           log,
	}
}

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
	// Drive file IDs are NOT needed here — the dispatcher-side
	// DriveDeleteHandler reads them from the SQLite row when the event
	// is processed.
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
	// For voiceover/images the direct repo.Delete is correct because those
	// tables have no Qdrant index (QDRANT-002 PR8 follow-up will retrofit
	// a DeleteEnqueue for them); for media_asset the dispatcher is the
	// canonical path.
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

	// 4. Cleanup Asset Tree
	if s.assetTreeSvc != nil {
		_ = s.assetTreeSvc.DeleteByAssetID(ctx, source, clipID)
		_ = s.assetTreeSvc.DeleteNode(ctx, clipID)
	}

	return nil
}

// DeleteByDriveFile handles deletion by Drive file ID or link.
func (s *DeletionService) DeleteByDriveFile(ctx context.Context, fileID string, source string, permanently bool) error {
	// Logic from processDriveFileDelete
	if fileID == "" {
		return fmt.Errorf("file_id is required")
	}

	// If source is "all" or empty, search everywhere
	// For now, simplify and just find the clip
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

	// Filter to a single source if requested
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

func (s *DeletionService) CleanupOrphanFiles(ctx context.Context, assetsDir string, dryRun bool) (int, error) {
	s.log.Info("starting deep orphan file cleanup", zap.String("dir", assetsDir), zap.Bool("dry_run", dryRun))

	// 1. Get all assets from database
	dbAssets, err := s.assetIndexSvc.ListAll(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to list assets from DB: %w", err)
	}

	// Build map of absolute local paths for fast lookup
	referencedPaths := make(map[string]bool)
	for _, asset := range dbAssets {
		if asset.LocalPath != "" {
			absPath, _ := filepath.Abs(asset.LocalPath)
			referencedPaths[absPath] = true
		}
	}

	// 2. Scan directory
	var deletedCount int
	err = filepath.Walk(assetsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		absPath, _ := filepath.Abs(path)
		if !referencedPaths[absPath] {
			s.log.Info("found orphan file", zap.String("path", path))
			if !dryRun {
				if err := os.Remove(path); err != nil {
					s.log.Error("failed to delete orphan file", zap.String("path", path), zap.Error(err))
				} else {
					deletedCount++
				}
			} else {
				deletedCount++
			}
		}
		return nil
	})

	return deletedCount, err
}
