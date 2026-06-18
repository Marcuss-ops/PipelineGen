package images

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"go.uber.org/zap"
	domainasset "github.com/Marcuss-ops/PipelineGen/internal/core/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/media/storage"
)

func (s *Service) AnimateImage(ctx context.Context, imageHash string, duration int) (string, error) {
	// 1. Get image from repo
	asset, err := s.repo.GetImageByHash(ctx, imageHash)
	if err != nil {
		return "", fmt.Errorf("image not found: %w", err)
	}

	fullPath := filepath.Join(s.imagesDir, asset.PathRel)
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		return "", fmt.Errorf("local file not found: %s", fullPath)
	}

	// 2. Prepare output path
	outputName := fmt.Sprintf("animate_%s.mp4", imageHash)
	outputPath := filepath.Join(s.animationsDir, outputName)

	// 3. Run script
	scriptPath := filepath.Join(s.scriptsDir, "bridges", "animate_image.py")
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		// Fallback for development if scripts is in current dir
		scriptPath = "scripts/animate_image.py"
	}

	durStr := fmt.Sprintf("%d", duration)
	if duration <= 0 {
		durStr = "7"
	}

	cmd := exec.CommandContext(ctx, "python3", scriptPath, fullPath, "--output", outputPath, "--duration", durStr)
	output, err := cmd.CombinedOutput()
	if err != nil {
		s.log.Error("Animation script failed", zap.Error(err), zap.String("output", string(output)))
		return "", fmt.Errorf("animation failed: %w", err)
	}

	s.log.Info("Animation created", zap.String("path", outputPath))

	// 4. Upload to Drive
	var driveVideoID string
	var driveLink string
	if s.mediaStore != nil {
		fileID, wl, err := s.mediaStore.UploadToDrive(ctx, storage.AssetDestinationRequest{
			Source:    storage.SourceImage,
			MediaType: storage.MediaTypeImageVideo,
			Subject:   asset.SubjectID,
			Hash:      imageHash,
			Ext:       ".mp4",
			Style:     asset.SubjectID,
		}, outputPath)
		if err != nil {
			s.log.Warn("Drive video upload failed", zap.Error(err))
		} else {
			driveVideoID = fileID
			driveLink = wl
			s.log.Info("Drive video upload successful", zap.String("file_id", fileID))
		}
	}

	// 6. Salva nel DB stock (fallback)
	if s.stockRepo != nil {
		clip := &domainasset.MediaAsset{
			ID:             "ai_" + imageHash,
			Name:           "AI Animation: " + asset.SubjectID,
			MediaType:      "video",
			Source:         "nvidia-animation",
			CreatedAt:      time.Now(),
			DriveFileID:    driveVideoID,
			DriveLink:      driveLink,
			LifecycleState: domainasset.StateReady,
		}

		if err := s.stockRepo.Upsert(ctx, clip); err != nil {
			s.log.Warn("Failed to ingest animated clip into stock DB", zap.Error(err))
		} else {
			s.log.Info("Animated clip ingested into stock DB", zap.String("clip_id", clip.ID))
		}
	}

	return outputPath, nil
}
