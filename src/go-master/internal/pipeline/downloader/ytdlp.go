package downloader

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

type YtDlpDownloader struct {
	binPath   string
	outputDir string
}

func NewYtDlpDownloader(binPath, outputDir string) *YtDlpDownloader {
	if binPath == "" {
		binPath = "yt-dlp"
	}
	_ = os.MkdirAll(outputDir, 0755)
	return &YtDlpDownloader{binPath: binPath, outputDir: outputDir}
}

func (d *YtDlpDownloader) DownloadClip(ctx context.Context, videoID string, start, duration int) (string, error) {
	url := "https://www.youtube.com/watch?v=" + videoID
	outFile := filepath.Join(d.outputDir, fmt.Sprintf("clip_%s_%d.mp4", videoID, start))

	// Format timestamps: MM:SS format
	startMin := start / 60
	startSec := start % 60
	endTime := start + duration
	endMin := endTime / 60
	endSec := endTime % 60
	sectionArg := fmt.Sprintf("*%d:%02d-%d:%02d", startMin, startSec, endMin, endSec)

	args := []string{
		"--download-section", sectionArg,
		"-f", "best[ext=mp4]/best",
		"-o", outFile,
		"--no-playlist",
		url,
	}

	cmd := exec.CommandContext(ctx, d.binPath, args...)
	if err := cmd.Run(); err != nil {
		return "", err
	}

	return outFile, nil
}
