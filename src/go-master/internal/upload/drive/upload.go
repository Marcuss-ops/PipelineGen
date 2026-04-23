package drive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"google.golang.org/api/drive/v3"

	"go.uber.org/zap"
	"velox/go-master/pkg/logger"
)

// UploadVideo carica un video su Drive
func (c *Client) UploadVideo(ctx context.Context, videoPath, folderID, filename string) (string, error) {
	// Validate folderID
	if folderID == "" {
		return "", fmt.Errorf("folderID cannot be empty")
	}

	file, err := os.Open(videoPath)
	if err != nil {
		return "", fmt.Errorf("failed to open video: %w", err)
	}
	defer file.Close()

	// Validate file exists and is readable
	stat, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("failed to stat video: %w", err)
	}
	if stat.Size() == 0 {
		return "", fmt.Errorf("video file is empty")
	}

	if filename == "" {
		filename = filepath.Base(videoPath)
	}

	driveFile := &drive.File{
		Name:    filename,
		Parents: []string{folderID},
	}

	result, err := c.service.Files.Create(driveFile).
		Media(file).
		Context(ctx).
		Do()
	if err != nil {
		return "", fmt.Errorf("upload failed: %w", err)
	}

	logger.Info("Uploaded to Drive", zap.String("filename", filename), zap.String("id", result.Id))
	return result.Id, nil
}

// UploadFile carica un file generico su Drive
func (c *Client) UploadFile(ctx context.Context, filePath, folderID, filename string) (string, error) {
	// Validate folderID
	if folderID == "" {
		return "", fmt.Errorf("folderID cannot be empty")
	}

	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Validate file exists and is readable
	stat, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("failed to stat file: %w", err)
	}
	if stat.Size() == 0 {
		return "", fmt.Errorf("file is empty")
	}

	if filename == "" {
		filename = filepath.Base(filePath)
	}

	driveFile := &drive.File{
		Name:    filename,
		Parents: []string{folderID},
	}

	result, err := c.service.Files.Create(driveFile).
		Media(file).
		Context(ctx).
		Do()
	if err != nil {
		return "", fmt.Errorf("upload failed: %w", err)
	}

	logger.Info("Uploaded file to Drive", zap.String("filename", filename), zap.String("id", result.Id))
	return result.Id, nil
}
