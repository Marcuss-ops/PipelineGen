package stockorchestrator

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

func (s *StockOrchestratorService) uploadToDrive(ctx context.Context, videos []DownloadedVideo, topic string, createFolders bool, folderStructure string) ([]UploadedClip, []error) {
	uploaded := make([]UploadedClip, 0, len(videos))
	var errs []error

	// Determine folder path
	folderPath := s.buildFolderPath(topic, folderStructure)

	// Create folder structure on Drive
	var folderID string
	var err error

	if createFolders {
		// Create nested folders: Stock Videos/{topic}/{entities}
		parts := strings.Split(folderPath, "/")
		currentParentID := "root"

		for _, part := range parts {
			if part == "" {
				continue
			}
			folderID, err = s.driveClient.GetOrCreateFolder(ctx, part, currentParentID)
			if err != nil {
				return nil, []error{fmt.Errorf("failed to create folder '%s': %w", part, err)}
			}
			currentParentID = folderID
		}
	} else {
		// Upload to root
		folderID = "root"
	}

	// Upload each video
	for _, video := range videos {
		filename := fmt.Sprintf("%s_%s.mp4", sanitizeFilename(video.Title), video.VideoID)

		fileID, err := s.driveClient.UploadFile(ctx, video.LocalPath, folderID, filename)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to upload %s: %w", video.Title, err))
			logger.Warn("Failed to upload video to Drive",
				zap.String("title", video.Title),
				zap.Error(err),
			)
			continue
		}

		uploaded = append(uploaded, UploadedClip{
			FileName:      filename,
			FileID:        fileID,
			DriveURL:      drive.GetDriveLink(fileID),
			FolderPath:    folderPath,
			OriginalTitle: video.Title,
		})

		logger.Info("Video uploaded to Drive",
			zap.String("filename", filename),
			zap.String("folder", folderPath),
			zap.String("drive_url", drive.GetDriveLink(fileID)),
		)

		// Clean up local file after upload
		os.Remove(video.LocalPath)
	}

	return uploaded, errs
}

// buildFolderPath creates the folder path for Drive organization
func (s *StockOrchestratorService) buildFolderPath(topic string, customStructure string) string {
	if customStructure != "" {
		// Replace placeholders
		path := strings.ReplaceAll(customStructure, "{topic}", topic)
		path = strings.ReplaceAll(path, "{date}", time.Now().Format("2006-01-02"))
		return path
	}

	// Default structure: Stock Videos/{topic}
	return fmt.Sprintf("Stock Videos/%s", topic)
}

// Helper functions

