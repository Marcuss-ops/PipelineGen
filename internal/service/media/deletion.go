package media

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"go.uber.org/zap"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/repository/images"
	"velox/go-master/internal/repository/voiceovers"
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
	log           *zap.Logger
}

// NewDeletionService creates a new deletion service.
func NewDeletionService(
	artlistRepo, clipsRepo, stockRepo *clips.Repository,
	voiceoverRepo *voiceovers.Repository,
	imagesRepo *images.Repository,
	driveUploader *drive.Uploader,
	assetTreeSvc *assettree.Service,
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
		log:           log,
	}
}

// DeleteClip deletes a clip by its ID and source.
func (s *DeletionService) DeleteClip(ctx context.Context, source string, clipID string, permanently bool) error {
	s.log.Info("deleting clip", zap.String("source", source), zap.String("clip_id", clipID), zap.Bool("permanently", permanently))

	// 1. Get Repo
	repo := s.resolveRepo(source)
	if repo == nil && strings.ToLower(source) != "voiceover" && strings.ToLower(source) != "images" {
		return fmt.Errorf("invalid source: %s", source)
	}

	// 2. Get Clip Data to find Drive file ID
	var clip *models.Clip
	var err error

	if strings.ToLower(source) == "voiceover" && s.voiceoverRepo != nil {
		rec, err := s.voiceoverRepo.GetByID(ctx, clipID)
		if err != nil {
			return fmt.Errorf("voiceover not found: %w", err)
		}
		clip = voiceoverRecordToClip(rec)
	} else if strings.ToLower(source) == "images" && s.imagesRepo != nil {
		id, _ := strconv.ParseInt(clipID, 10, 64)
		img, err := s.imagesRepo.GetByID(ctx, id)
		if err != nil {
			return fmt.Errorf("image not found: %w", err)
		}
		clip = imageAssetToClip(img)
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
	if strings.ToLower(source) == "voiceover" && s.voiceoverRepo != nil {
		err = s.voiceoverRepo.Delete(ctx, clipID)
	} else if strings.ToLower(source) == "images" && s.imagesRepo != nil {
		id, _ := strconv.ParseInt(clipID, 10, 64)
		err = s.imagesRepo.Delete(ctx, id)
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
func (s *DeletionService) FindClipByDriveFileID(ctx context.Context, fileID string, sourceLimit string) (*models.Clip, string, error) {
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
				return voiceoverRecordToClip(rec), source, nil
			}
		case "images":
			imgRepo := repo.(*images.Repository)
			img, err := imgRepo.GetByDriveFileID(ctx, fileID)
			if err == nil && img != nil {
				return imageAssetToClip(img), source, nil
			}
		}
	}

	return nil, "", nil
}

func (s *DeletionService) resolveRepo(source string) *clips.Repository {
	switch strings.ToLower(source) {
	case "artlist":
		return s.artlistRepo
	case "youtube", "clips", "boxe", "wwe", "discovery", "music":
		return s.clipsRepo
	case "stock":
		return s.stockRepo
	default:
		return nil
	}
}

// Helper converters (copied from handlers/media/converters.go for now)
func voiceoverRecordToClip(rec *voiceovers.Record) *models.Clip {
	name := rec.Filename
	if name == "" {
		name = rec.TextPreview
		if len(name) > 50 {
			name = name[:50]
		}
	}
	return &models.Clip{
		ID:           rec.ID,
		Name:         name,
		Filename:     rec.Filename,
		FolderID:     rec.FolderID,
		FolderPath:   rec.FolderPath,
		DriveLink:    rec.DriveLink,
		DownloadLink: rec.DownloadLink,
		FileHash:     rec.FileHash,
		LocalPath:    rec.LocalPath,
		Source:       "voiceover",
		Metadata:     rec.Metadata,
		CreatedAt:    rec.CreatedAt,
		UpdatedAt:    rec.UpdatedAt,
	}
}

func imageAssetToClip(img *models.ImageAsset) *models.Clip {
	name := img.Description
	if name == "" {
		name = filepath.Base(img.PathRel)
	}
	return &models.Clip{
		ID:        strconv.FormatInt(img.ID, 10),
		Name:      name,
		Filename:  filepath.Base(img.PathRel),
		DriveLink: img.SourceURL,
		FileHash:  img.Hash,
		LocalPath: img.PathRel,
		Source:    "images",
		CreatedAt: img.CreatedAt,
	}
}
