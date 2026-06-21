package youtube

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// GetVideoInfo retrieves full metadata for a YouTube video without downloading it.
// Uses SearchRunnerPort when wired; falls back to legacy os/exec when not.
func (s *Service) GetVideoInfo(ctx context.Context, videoURL string) (*VideoMetadata, error) {
	if videoURL == "" {
		return nil, fmt.Errorf("url is required")
	}

	videoID := ""
	if id, err := extractVideoIDFromURL(videoURL); err == nil {
		videoID = id
	}

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
			s.metadataL1.Store(videoID, metadataL1Entry{Metadata: cached, AddedAt: time.Now()})
			return cached, nil
		}
	}

	s.log.Info("Retrieving YouTube video info", zap.String("url", videoURL))

	// Delegate to the port (infrastructure layer)
	if s.searchRunner == nil {
		return nil, fmt.Errorf("youtube: search runner not wired")
	}

	info, err := s.searchRunner.GetVideoInfo(ctx, videoURL)
	if err != nil {
		return nil, err
	}

	// Convert port DTO to application-layer type
	metadata := &VideoMetadata{
		ID:          info.ID,
		URL:         videoURL,
		Title:       info.Title,
		Description: info.Description,
		Duration:    info.Duration,
		Uploader:    info.Uploader,
		UploadDate:  info.UploadDate,
		ViewCount:   info.ViewCount,
		Categories:  info.Categories,
		Tags:        info.Tags,
	}

	for _, t := range info.Thumbnails {
		metadata.Thumbnails = append(metadata.Thumbnails, VideoThumbnail{
			URL: t.URL, Width: t.Width, Height: t.Height,
		})
	}
	if len(info.Thumbnails) > 0 {
		metadata.ThumbnailURL = info.Thumbnails[len(info.Thumbnails)-1].URL
	}
	for _, c := range info.Chapters {
		metadata.Chapters = append(metadata.Chapters, VideoChapter{
			Title: c.Title, StartTime: c.StartTime, EndTime: c.EndTime,
		})
	}

	// Cache the video metadata
	if info.ID != "" {
		s.setCachedVideoMetadata(ctx, info.ID, metadata)
		s.metadataL1.Store(info.ID, metadataL1Entry{Metadata: metadata, AddedAt: time.Now()})
	}

	return metadata, nil
}

// extractVideoIDFromURL extracts the YouTube video ID from a URL.
func extractVideoIDFromURL(url string) (string, error) {
	if url == "" {
		return "", fmt.Errorf("empty URL")
	}
	// Simple extraction for common formats
	for _, prefix := range []string{"https://www.youtube.com/watch?v=", "http://www.youtube.com/watch?v=", "https://youtube.com/watch?v="} {
		if len(url) > len(prefix) && url[:len(prefix)] == prefix {
			id := url[len(prefix):]
			if idx := indexOf(id, '&'); idx >= 0 {
				id = id[:idx]
			}
			if id != "" {
				return id, nil
			}
		}
	}
	return "", fmt.Errorf("could not extract video ID from URL")
}

func indexOf(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
