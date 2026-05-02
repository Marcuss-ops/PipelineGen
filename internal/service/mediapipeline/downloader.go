package mediapipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"velox/go-master/pkg/media/downloader"
	"velox/go-master/pkg/pathutil"
)

func (s *Service) downloadOrCut(ctx context.Context, item *WorkItem, req *PipelineRequest) error {
	if s.ytdlpDownloader == nil {
		return fmt.Errorf("downloader not configured")
	}

	if item.SegmentSpec != nil {
		return s.downloadSegment(ctx, item)
	}

	return s.downloadFull(ctx, item)
}

func (s *Service) downloadSegment(ctx context.Context, item *WorkItem) error {
	seg := item.SegmentSpec

	outputDir := s.downloadOutputDir
	os.MkdirAll(outputDir, 0755)

	outputTemplate := filepath.Join(outputDir, pathutil.Slug(seg.Name))

	// Build download sections in format expected by yt-dlp: "*start-end"
	section := fmt.Sprintf("*%s-%s", seg.Start, seg.End)

	req := &downloader.DownloadRequest{
		URL:              seg.SourceURL,
		OutputPath:       outputTemplate,
		Format:          "bv*[height<=1080][ext=mp4]+ba[ext=m4a]/b[height<=1080][ext=mp4]/best[height<=1080]",
		MergeFormat:     "mp4",
		NoPlaylist:      true,
		DownloadSections: []string{section},
		ForceKeyframes:  false,
		Timeout:         10 * time.Minute,
	}

	result, err := s.ytdlpDownloader.DownloadSections(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to download segment: %w", err)
	}

	if len(result) > 0 {
		item.LocalPath = result[0].Path
		if result[0].Name != "" {
			item.Name = result[0].Name
		}
	}

	return nil
}

func (s *Service) downloadFull(ctx context.Context, item *WorkItem) error {
	outputDir := s.downloadOutputDir
	os.MkdirAll(outputDir, 0755)

	outputPath := filepath.Join(outputDir, pathutil.Slug(item.Name))

	req := &downloader.DownloadRequest{
		URL:        item.SourceURL,
		OutputPath: outputPath,
		Format:     "bestvideo+bestaudio/best",
		Timeout:    10 * time.Minute,
	}

	err := s.ytdlpDownloader.Download(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to download: %w", err)
	}

	item.LocalPath = req.OutputPath + ".mp4"
	return nil
}
