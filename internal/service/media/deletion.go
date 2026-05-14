package media

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/repository/images"
	"velox/go-master/internal/repository/voiceovers"
	"velox/go-master/internal/service/assetindex"
	"velox/go-master/internal/service/assetregistry"
	"velox/go-master/internal/service/assettree"
	"velox/go-master/internal/upload/drive"
	driveutil "velox/go-master/pkg/drive"
	"velox/go-master/pkg/models"
)

// DeletionService handles synchronized deletion between database and cloud storage.
type DeletionService struct {
	artlistRepo   *clips.Repository
	clipsRepo     *clips.Repository
	stockRepo     *clips.Repository
	voiceoverRepo *voiceovers.Repository
	imagesRepo    *images.Repository
	driveUploader *drive.Uploader
	assetTreeSvc  *assettree.Service
	assetIndexSvc *assetindex.Service
	log           *zap.Logger
}

// NewDeletionService creates a new deletion service.
func NewDeletionService(
	artlistRepo, clipsRepo, stockRepo *clips.Repository,
	voiceoverRepo *voiceovers.Repository,
	imagesRepo *images.Repository,
	driveUploader *drive.Uploader,
	assetTreeSvc *assettree.Service,
	assetIndexSvc *assetindex.Service,
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
		log:           log,
	}
}

// DeleteClip deletes a clip by its ID and source.
func (s *DeletionService) DeleteClip(ctx context.Context, source string, clipID string, permanently bool) error {
	s.log.Info("deleting clip", zap.String("source", source), zap.String("clip_id", clipID), zap.Bool("permanently", permanently))

	// 1. Get Repo via centralized resolver
	canonical := assetregistry.CanonicalSource(source)
	if canonical == "" {
		return fmt.Errorf("invalid source: %s", source)
	}
	resolver := assetregistry.NewSourceResolver(s.artlistRepo, s.clipsRepo, s.stockRepo)
	repo := resolver.ResolveRepo(source)
	if repo == nil && canonical != "voiceover" && canonical != "images" {
		return fmt.Errorf("invalid source: %s", source)
	}

	// 2. Get Clip Data to find Drive file ID
	var clip *models.MediaAsset
	var err error

	if canonical == "voiceover" && s.voiceoverRepo != nil {
		rec, err := s.voiceoverRepo.GetByID(ctx, clipID)
		if err != nil {
			return fmt.Errorf("voiceover not found: %w", err)
		}
		clip = assetregistry.VoiceoverRecordToClip(rec)
	} else if canonical == "images" && s.imagesRepo != nil {
		img, err := s.imagesRepo.GetByID(ctx, clipID)
		if err != nil {
			return fmt.Errorf("image not found: %w", err)
		}
		clip = assetregistry.ImageAssetToClip(img)
	} else if repo != nil {
		clip, err = repo.GetClip(ctx, clipID)
		if err != nil {
			return fmt.Errorf("clip not found: %w", err)
		}
	} else {
		return fmt.Errorf("repository for %s not available", source)
	}

	// 3. Delete from Drive
	if s.driveUploader != nil {
		fileID := driveutil.FileIDFromLink(clip.DriveLink)
		if fileID == "" {
			fileID = driveutil.FileIDFromLink(clip.DownloadLink)
		}
		if fileID != "" {
			var driveErr error
			if permanently {
				driveErr = s.driveUploader.DeleteFile(ctx, fileID)
			} else {
				driveErr = s.driveUploader.TrashFile(ctx, fileID)
			}
			if driveErr != nil {
				s.log.Warn("failed to delete drive file", zap.String("file_id", fileID), zap.Error(driveErr))
			}
		}
	}

	// 4. Delete from DB
	if canonical == "voiceover" && s.voiceoverRepo != nil {
		err = s.voiceoverRepo.Delete(ctx, clipID)
	} else if canonical == "images" && s.imagesRepo != nil {
		err = s.imagesRepo.Delete(ctx, clipID)
	} else if repo != nil {
		err = repo.DeleteClip(ctx, clipID)
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

// FindClipByDriveFileID searches for a clip across repositories.
func (s *DeletionService) FindClipByDriveFileID(ctx context.Context, fileID string, sourceLimit string) (*models.MediaAsset, string, error) {
	repos := map[string]interface{}{
		"artlist":   s.artlistRepo,
		"clips":     s.clipsRepo,
		"stock":     s.stockRepo,
		"voiceover": s.voiceoverRepo,
		"images":    s.imagesRepo,
	}

	if sourceLimit != "" && sourceLimit != "all" {
		if repo, ok := repos[sourceLimit]; ok {
			repos = map[string]interface{}{sourceLimit: repo}
		} else {
			return nil, "", fmt.Errorf("invalid source limit: %s", sourceLimit)
		}
	}

	for source, repo := range repos {
		if repo == nil {
			continue
		}

		switch source {
		case "artlist", "clips", "stock":
			clipRepo := repo.(*clips.Repository)
			clip, err := clipRepo.GetClipByDriveFileID(ctx, fileID)
			if err == nil && clip != nil {
				return clip, source, nil
			}
		case "voiceover":
			voRepo := repo.(*voiceovers.Repository)
			rec, err := voRepo.GetByDriveFileID(ctx, fileID)
			if err == nil && rec != nil {
				return assetregistry.VoiceoverRecordToClip(rec), source, nil
			}
		case "images":
			imgRepo := repo.(*images.Repository)
			img, err := imgRepo.GetByDriveFileID(ctx, fileID)
			if err == nil && img != nil {
				return assetregistry.ImageAssetToClip(img), source, nil
			}
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
