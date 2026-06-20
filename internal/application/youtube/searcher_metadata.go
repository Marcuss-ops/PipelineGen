package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/security"
	urlutil "github.com/Marcuss-ops/PipelineGen/pkg/urlutil"

	"go.uber.org/zap"
)

// GetVideoInfo retrieves full metadata for a YouTube video without downloading it
func (s *Service) GetVideoInfo(ctx context.Context, videoURL string) (*VideoMetadata, error) {
	if videoURL == "" {
		return nil, fmt.Errorf("url is required")
	}

	if err := security.ValidateDownloadURL(videoURL); err != nil {
		return nil, err
	}

	videoID, _ := urlutil.ExtractVideoID(videoURL)

	// 1. Check L1 Cache
	if videoID != "" {
		if val, ok := s.metadataL1.Load(videoID); ok {
			if entry, ok := val.(metadataL1Entry); ok {
				if time.Since(entry.AddedAt) < 7*24*time.Hour {
					s.log.Info("Serving YouTube video metadata from L1 cache", zap.String("videoID", videoID))
					return entry.Metadata, nil
				}
			}
		}
	}

	// 2. Check L2 Cache
	if videoID != "" {
		if cached, ok := s.getCachedVideoMetadata(ctx, videoID); ok {
			s.log.Info("Serving YouTube video metadata from L2 SQLite cache", zap.String("videoID", videoID))
			// Populate L1 cache
			s.metadataL1.Store(videoID, metadataL1Entry{
				Metadata: cached,
				AddedAt:  time.Now(),
			})
			return cached, nil
		}
	}

	s.log.Info("Retrieving YouTube video info", zap.String("url", videoURL))

	ytdlpPath := s.cfg.External.ResolvedYtdlpPath()

	args := []string{
		videoURL,
		"--dump-json",
		"--no-playlist",
		"--no-warnings",
	}

	// JS runtime for signature solving (YouTube's n-challenge / SABR enforcement)
	if s.cfg.External.YouTubeJSRuntimePath != "" {
		args = append(args, "--js-runtime", s.cfg.External.YouTubeJSRuntimePath)
	}

	// Use android+web player clients for faster extraction.
	// Do NOT pass cookies by default — they disable the android client,
	// falling back to web-only extraction that may fail n-challenge solving.
	args = append(args, "--extractor-args", "youtube:player_client=android,web")

	cmd := exec.CommandContext(ctx, ytdlpPath, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		s.log.Error("yt-dlp info failed", zap.Error(err), zap.String("stderr", stderr.String()))
		return nil, fmt.Errorf("failed to get video info: %w", err)
	}

	var raw struct {
		ID          string  `json:"id"`
		Title       string  `json:"title"`
		Description string  `json:"description"`
		Duration    float64 `json:"duration"`
		Uploader    string  `json:"uploader"`
		UploadDate  string  `json:"upload_date"`
		ViewCount   int64   `json:"view_count"`
		Thumbnails  []struct {
			URL    string `json:"url"`
			Width  int    `json:"width"`
			Height int    `json:"height"`
		} `json:"thumbnails"`
		Chapters []struct {
			Title     string  `json:"title"`
			StartTime float64 `json:"start_time"`
			EndTime   float64 `json:"end_time"`
		} `json:"chapters"`
		Categories []string `json:"categories"`
		Tags       []string `json:"tags"`
	}

	if err := json.Unmarshal([]byte(stdout.String()), &raw); err != nil {
		return nil, fmt.Errorf("failed to parse video info: %w", err)
	}

	metadata := &VideoMetadata{
		ID:          raw.ID,
		URL:         videoURL,
		Title:       raw.Title,
		Description: raw.Description,
		Duration:    raw.Duration,
		Uploader:    raw.Uploader,
		UploadDate:  raw.UploadDate,
		ViewCount:   raw.ViewCount,
		Categories:  raw.Categories,
		Tags:        raw.Tags,
	}

	// Process thumbnails
	if len(raw.Thumbnails) > 0 {
		metadata.ThumbnailURL = raw.Thumbnails[len(raw.Thumbnails)-1].URL
		for _, t := range raw.Thumbnails {
			metadata.Thumbnails = append(metadata.Thumbnails, VideoThumbnail{
				URL:    t.URL,
				Width:  t.Width,
				Height: t.Height,
			})
		}
	}

	// Process chapters
	for _, c := range raw.Chapters {
		metadata.Chapters = append(metadata.Chapters, VideoChapter{
			Title:     c.Title,
			StartTime: c.StartTime,
			EndTime:   c.EndTime,
		})
	}

	// Cache the video metadata in L1 and L2
	if raw.ID != "" {
		s.setCachedVideoMetadata(ctx, raw.ID, metadata)
		s.metadataL1.Store(raw.ID, metadataL1Entry{
			Metadata: metadata,
			AddedAt:  time.Now(),
		})
	}

	return metadata, nil
}
