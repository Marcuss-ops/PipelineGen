package channelmonitor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/youtube"
	"velox/go-master/pkg/logger"
)

func (m *V3Monitor) downloadPreciseClip(ctx context.Context, videoID string, start, duration int, outFile string) error {
	url := fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)

	outDir := filepath.Dir(outFile)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	dlCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	logger.Debug("Downloading clip with yt-dlp",
		zap.String("video_id", videoID),
		zap.String("output", outFile))

	return youtube.DownloadSection(dlCtx, youtube.SectionDownloadOptions{
		YtDlpPath:   m.ytdlpPath,
		URL:         url,
		OutputFile:  outFile,
		StartSec:    start,
		Duration:    duration,
		MaxFilesize: "1G",
	})
}

func (m *V3Monitor) resolveTargetFolder(ctx context.Context, category, title string) (string, string, error) {
	if category == "" {
		category = "General"
	}

	folderName := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' || r == ' ' {
			return r
		}
		return -1
	}, category)

	folderName = strings.TrimSpace(folderName)
	if folderName == "" {
		folderName = "Unknown"
	}

	folderPath := folderName + "/" + sanitizeFolderName(title)

	logger.Info("Folder resolved",
		zap.String("path", folderPath),
		zap.String("category", category))

	return "TEMP_FOLDER_ID_" + folderName, folderPath, nil
}

func (m *V3Monitor) uploadToDrive(ctx context.Context, file, folderID, folderPath string) (string, error) {
	info, err := os.Stat(file)
	if err != nil {
		return "", fmt.Errorf("file not found: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("path is a directory, not a file")
	}

	filename := filepath.Base(file)
	logger.Info("Would upload to Drive",
		zap.String("file", filename),
		zap.String("folder_id", folderID),
		zap.String("folder_path", folderPath),
		zap.Int64("size_bytes", info.Size()))

	return "FILE_ID_" + strings.TrimSuffix(filename, filepath.Ext(filename)), nil
}
