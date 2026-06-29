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
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

// DeletionService handles synchronized deletion between database and cloud drive.
type DeletionService struct {
	artlistRepo   *assets.ClipsRepository
	clipsRepo     *assets.ClipsRepository
	stockRepo     *assets.ClipsRepository
	voiceoverRepo *assets.VoiceoversRepository
	imagesRepo    *assets.ImagesRepository
	driveUploader *drive.Uploader
	assetTreeSvc  *assettree.Service
	assetIndexSvc *assetindex.Service
	// dispatcher is the canonical outbox.Dispatcher used by DeleteClip
	// to atomically (1) stamp media_assets.index_state=DELETE_PENDING
	// and (2) emit an asset.index.delete_requested.v1 event in a single
	// tx — the IndexDeleteHandler completes the picture with Qdrant
	// delete + SoftDelete + DELETED state flip. QDRANT-002 PR7 close-out
	// for the producer migration ticket item D (every direct
	// repo.SoftDelete path routes through Dispatcher.EnqueueAndDelete).
	dispatcher *outbox.Dispatcher
	log        *zap.Logger
}

// NewDeletionService creates a new deletion service.
//
// QDRANT-002 PR7: dispatcher is the canonical outbox.Dispatcher.
// Production wiring always supplies it; nil is allowed only in test
// fixtures that exercise the legacy direct SoftDelete path (the
// caller MUST opt-in via the dispatcherNilAllowed=true flag when
// wiring test fixtures so a regression that touches production
// wiring shows up at build time, not at runtime).
func NewDeletionService(
	artlistRepo, clipsRepo, stockRepo *assets.ClipsRepository,
	voiceoverRepo *assets.VoiceoversRepository,
	imagesRepo *assets.ImagesRepository,
	driveUploader *drive.Uploader,
	assetTreeSvc *assettree.Service,
	assetIndexSvc *assetindex.Service,
	dispatcher *outbox.Dispatcher,
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

	// 2. Get Clip Data to find Drive file ID
	var driveFileID string
	var err error

	if canonical == "voiceover" && s.voiceoverRepo != nil {
		rec, voErr := s.voiceoverRepo.GetByID(ctx, clipID)
		if voErr != nil {
			return fmt.Errorf("voiceover not found: %w", voErr)
		}
		clip := artifacts.VoiceoverRecordToClip(rec)
		driveFileID = clip.DriveFileID()
		if driveFileID == "" {
			driveFileID = driveutil.FileIDFromLink(clip.DriveLink())
		}
		if driveFileID == "" {
			driveFileID = driveutil.FileIDFromLink(clip.DownloadLink())
		}
	} else if canonical == "images" && s.imagesRepo != nil {
		img, imgErr := s.imagesRepo.GetByID(ctx, clipID)
		if imgErr != nil {
			return fmt.Errorf("image not found: %w", imgErr)
		}
		clip := artifacts.ImageAssetToClip(img)
		driveFileID = clip.DriveFileID()
		if driveFileID == "" {
			driveFileID = driveutil.FileIDFromLink(clip.DriveLink())
		}
		if driveFileID == "" {
			driveFileID = driveutil.FileIDFromLink(clip.DownloadLink())
		}
	} else if repo != nil {
		var clip *asset.Asset
		clip, err = repo.Get(ctx, clipID)
		if err != nil {
			return fmt.Errorf("clip not found: %w", err)
		}
		driveFileID = driveutil.FileIDFromLink(clip.DriveLink())
		if driveFileID == "" {
			driveFileID = driveutil.FileIDFromLink(clip.DownloadLink())
		}
	} else {
		return fmt.Errorf("repository for %s not available", source)
	}

	// 3. Delete from Drive
	if s.driveUploader != nil && driveFileID != "" {
		var driveErr error
		if permanently {
			driveErr = s.driveUploader.DeleteFile(ctx, driveFileID)
		} else {
			driveErr = s.driveUploader.TrashFile(ctx, driveFileID)
		}
		if driveErr != nil {
			s.log.Warn("failed to delete drive file", zap.String("file_id", driveFileID), zap.Error(driveErr))
		}
	}

	// 4. Delete from DB
	//
	// QDRANT-002 PR7: route through Dispatcher.EnqueueAndDelete for
	// the media_asset source. The Dispatcher's tx atomically stamps
	// index_state=DELETE_PENDING AND emits outbox_events asset.index.
	// delete_requested.v1 — IndexDeleteHandler.Handle completes the
	// delete (Qdrant DeletePoints → SoftDelete in SQLite → final
	// state flip to DELETED). For voiceover and images sources the
	// tables are NOT watched by the Qdrant indexer, so the direct
	// repo.Delete path is still correct (QDRANT-002 PR8 followup
	// will retrofit a DeleteEnqueue for those tables).
	if canonical == "voiceover" && s.voiceoverRepo != nil {
		err = s.voiceoverRepo.Delete(ctx, clipID)
	} else if canonical == "images" && s.imagesRepo != nil {
		err = s.imagesRepo.Delete(ctx, clipID)
	} else if repo != nil {
		if s.dispatcher == nil {
			// Defense-in-depth: production wiring must supply a
			// non-nil dispatcher. The error here shows up at the
			// first delete attempt in a misconfigured deployment,
			// not at boot time — that's intentional. Test fixtures
			// that exercise the legacy SoftDelete path already opt
			// out of Dispatcher routing through a sentinel value, so
			// this branch is reachable only via a wiring mistake.
			err = fmt.Errorf("deletion: dispatcher is nil — production wiring must configure the canonical outbox.Dispatcher (QDRANT-002 PR7 producer migration)")
		} else {
			err = s.dispatcher.EnqueueAndDelete(ctx, clipID)
		}
	}

	if err != nil {
		return fmt.Errorf("failed to delete from database: %w", err)
	}

	// 5. Cleanup Asset Tree
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
