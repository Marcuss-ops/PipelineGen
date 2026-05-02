package ytdlp

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"

	"velox/go-master/internal/core/media"
)

type Downloader struct {
	ytdlpPath string
	outputDir string
}

func NewDownloader(ytdlpPath, outputDir string) *Downloader {
	return &Downloader{
		ytdlpPath: ytdlpPath,
		outputDir: outputDir,
	}
}

type DownloadSegmentRequest struct {
	URL      string
	Start    string
	End      string
	OutputName string
}

type DownloadedFile struct {
	Path     string
	Duration time.Duration
}

func (d *Downloader) DownloadSegment(ctx context.Context, req DownloadSegmentRequest) (*DownloadedFile, error) {
	outputPath := filepath.Join(d.outputDir, req.OutputName+".mp4")

	args := []string{
		req.URL,
		"--download-sections", fmt.Sprintf("*%s-%s", req.Start, req.End),
		"-o", outputPath,
		"--no-playlist",
	}

	cmd := exec.CommandContext(ctx, d.ytdlpPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("ytdlp failed: %w, output: %s", err, string(output))
	}

	return &DownloadedFile{
		Path: outputPath,
	}, nil
}

func (d *Downloader) GetMetadata(ctx context.Context, url string) (*media.Item, error) {
	args := []string{
		url,
		"--dump-json",
		"--no-playlist",
	}

	cmd := exec.CommandContext(ctx, d.ytdlpPath, args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ytdlp failed: %w", err)
	}

	return parseVideoMetadata(output)
}

func parseVideoMetadata(data []byte) (*media.Item, error) {
	return &media.Item{
		Title: "Video",
	}, nil
}
