package fullimages

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	imgservice "velox/go-master/internal/media/images"
	"velox/go-master/internal/pkg/media/ffmpeg"
	driveup "velox/go-master/internal/upload/drive"
)

// Section describes a single text part for which a video should be generated.
type Section struct {
	Title string `json:"title" binding:"required" example:"Castello Medievale"`
	Text  string `json:"text"  example:"Descrizione della scena..."`
	Style string `json:"style" example:"medievale"`
}

// SectionVideo holds the result for one generated video.
type SectionVideo struct {
	SectionIndex int    `json:"section_index"`
	Title        string `json:"title"`
	Style        string `json:"style,omitempty"`
	DriveLink    string `json:"drive_link,omitempty"`
	DriveFileID  string `json:"drive_file_id,omitempty"`
	VideoPath    string `json:"video_path,omitempty"`
	Error        string `json:"error,omitempty"`
}

// Result wraps all section videos into a single response.
type Result struct {
	Videos []SectionVideo `json:"videos"`
}

// Service generates one video per text section:
//  1. Generate 1920×1080 image via NVIDIA AI
//  2. Convert image to MP4 video with Ken Burns zoom (ffmpeg)
//  3. Upload video to Drive → {ImagesRoot}/{style}/{title-slug}.mp4
//
// No entity extraction or asset association. Each style gets its own
// Drive subfolder for clean organization.
type Service struct {
	imgService *imgservice.Service
	ffmpegProc *ffmpeg.Processor
	driveUp    *driveup.Uploader
	imagesDir  string
	driveRoot  string
	log        *zap.Logger
}

// NewService creates a FullImages video-generation service.
func NewService(imgService *imgservice.Service, ffmpegProc *ffmpeg.Processor, driveUp *driveup.Uploader, imagesDir, driveRoot string, log *zap.Logger) *Service {
	return &Service{
		imgService: imgService,
		ffmpegProc: ffmpegProc,
		driveUp:    driveUp,
		imagesDir:  imagesDir,
		driveRoot:  driveRoot,
		log:        log,
	}
}

const (
	videoGenTimeout   = 5 * time.Minute
	imageGenWidth     = 1024
	imageGenHeight    = 1024
	videoDuration     = 7
	videoMaxWorkers   = 3
)

// GenerateForSections produces one video per section in parallel.
func (s *Service) GenerateForSections(ctx context.Context, sections []Section, topic, language string) (*Result, error) {
	if len(sections) == 0 {
		return nil, fmt.Errorf("at least one section is required")
	}

	s.log.Info("fullimages: starting video generation",
		zap.Int("section_count", len(sections)),
		zap.String("topic", topic),
	)

	results := make([]SectionVideo, len(sections))
	sem := make(chan struct{}, videoMaxWorkers)
	var wg sync.WaitGroup

	for i, sec := range sections {
		wg.Add(1)
		go func(idx int, sec Section) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			results[idx] = s.generateOneVideo(ctx, sec, topic, idx)
		}(i, sec)
	}

	wg.Wait()

	okCount := 0
	for _, r := range results {
		if r.Error == "" {
			okCount++
		}
	}

	s.log.Info("fullimages: video generation complete",
		zap.Int("total", len(sections)),
		zap.Int("success", okCount),
		zap.Int("failed", len(sections)-okCount),
	)

	return &Result{Videos: results}, nil
}

// generateOneVideo handles the full pipeline for one section.
func (s *Service) generateOneVideo(ctx context.Context, sec Section, topic string, idx int) SectionVideo {
	ctx, cancel := context.WithTimeout(ctx, videoGenTimeout)
	defer cancel()

	subject := sec.Title
	if subject == "" {
		subject = fmt.Sprintf("section_%d", idx)
	}
	style := strings.TrimSpace(sec.Style)
	slug := safeFolderName(subject)
	prompts := buildSectionPrompts(sec, topic)
	prompt := pickBestPrompt(prompts, subject, topic)

	// === Step 1: Generate 1920×1080 image ===
	s.log.Info("fullimages: generating image",
		zap.Int("section", idx),
		zap.String("prompt", prompt),
		zap.String("style", style),
	)

	// Use style/title as slug so image lands in right folder.
	imgSlug := slug
	if style != "" {
		imgSlug = style + "/" + slug
	}
	tags := []string{subject, "style:" + style}

	asset, err := s.imgService.GenerateStyledImage(ctx, imgSlug, prompt, "", imageGenWidth, imageGenHeight, tags)
	if err != nil || asset == nil {
		errMsg := "image generation failed"
		if err != nil {
			errMsg = err.Error()
		}
		s.log.Error("fullimages: image gen failed", zap.Int("section", idx), zap.Error(err))
		return SectionVideo{SectionIndex: idx, Title: sec.Title, Style: style, Error: errMsg}
	}

	s.log.Info("fullimages: image ready",
		zap.Int("section", idx),
		zap.String("hash", asset.Hash),
		zap.String("path_rel", asset.PathRel),
	)

	// === Step 2: Locate image on disk ===
	// The image is stored in {imagesDir}/{slug}/{hash}.png. Since the ingest
	// pipeline may not populate PathRel reliably, resolve the file by searching
	// for the hash in the image storage area.
	imagePath := resolveImagePath(s.imagesDir, asset.Hash)

	// Fallback: try PathRel directly from the asset.
	if imagePath == "" && asset.PathRel != "" {
		imagePath = filepath.Join(s.imagesDir, asset.PathRel)
	}

	if imagePath == "" {
		s.log.Error("fullimages: image file not found on disk", zap.Int("section", idx), zap.String("hash", asset.Hash))
		return SectionVideo{
			SectionIndex: idx,
			Title:        sec.Title,
			Style:        style,
			Error:        "image file not found on disk after generation",
		}
	}

	// === Step 3: Convert image to MP4 video ===
	videoName := slug + ".mp4"
	videoDir := filepath.Join(s.imagesDir, style, slug)
	if err := os.MkdirAll(videoDir, 0755); err != nil {
		s.log.Error("fullimages: failed to create video dir", zap.String("dir", videoDir), zap.Error(err))
		return SectionVideo{
			SectionIndex: idx,
			Title:        sec.Title,
			Style:        style,
			Error:        fmt.Sprintf("failed to create video directory: %v", err),
		}
	}
	videoPath := filepath.Join(videoDir, videoName)
	if err := s.ffmpegProc.ImageToVideo(ctx, imagePath, videoPath, ffmpeg.ImageToVideoOptions{
		Duration: videoDuration,
		Width:    imageGenWidth,
		Height:   imageGenHeight,
		Zoom:     true,
	}); err != nil {
		s.log.Error("fullimages: video conversion failed", zap.Int("section", idx), zap.Error(err))
		return SectionVideo{
			SectionIndex: idx,
			Title:        sec.Title,
			Style:        style,
			Error:        fmt.Sprintf("video conversion failed: %v", err),
		}
	}

	s.log.Info("fullimages: video created", zap.Int("section", idx), zap.String("video_path", videoPath))

	// === Step 4: Upload video to Drive ===
	if s.driveUp == nil || s.driveRoot == "" {
		return SectionVideo{
			SectionIndex: idx,
			Title:        sec.Title,
			Style:        style,
			VideoPath:    videoPath,
			Error:        "Drive not configured",
		}
	}

	folderID := s.driveRoot
	if style != "" {
		fid, err := s.driveUp.GetOrCreateFolder(ctx, style, s.driveRoot)
		if err != nil {
			s.log.Warn("fullimages: failed to create Drive style folder", zap.String("style", style), zap.Error(err))
		} else {
			folderID = fid
		}
	}

	// Create a subfolder for the specific prompt inside the style folder.
	slugFolderID, err := s.driveUp.GetOrCreateFolder(ctx, slug, folderID)
	if err != nil {
		s.log.Warn("fullimages: failed to create Drive slug folder", zap.String("slug", slug), zap.Error(err))
	} else {
		folderID = slugFolderID
	}

	upResult, err := s.driveUp.UploadFile(ctx, videoPath, folderID, videoName)
	if err != nil {
		s.log.Error("fullimages: Drive upload failed", zap.Int("section", idx), zap.Error(err))
		return SectionVideo{
			SectionIndex: idx,
			Title:        sec.Title,
			Style:        style,
			VideoPath:    videoPath,
			Error:        fmt.Sprintf("Drive upload failed: %v", err),
		}
	}

	s.log.Info("fullimages: video uploaded to Drive",
		zap.Int("section", idx),
		zap.String("drive_link", upResult.WebViewLink),
		zap.String("file_id", upResult.FileID),
	)

	return SectionVideo{
		SectionIndex: idx,
		Title:        sec.Title,
		Style:        style,
		DriveLink:    upResult.WebViewLink,
		DriveFileID:  upResult.FileID,
		VideoPath:    videoPath,
	}
}

// safeFolderName creates a clean folder name without hyphens or underscores.
// It strips non-alphanumeric characters and collapses whitespace to spaces.
func safeFolderName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "untitled"
	}
	// Replace runs of non-alphanumeric (except spaces) with nothing
	cleaned := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == ' ' {
			return r
		}
		return -1 // drop
	}, s)
	// Collapse multiple spaces
	parts := strings.Fields(cleaned)
	return strings.Join(parts, " ")
}
