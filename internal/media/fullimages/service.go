package fullimages

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	imgservice "velox/go-master/internal/media/images"
	"velox/go-master/internal/pkg/hashutil"
	"velox/go-master/internal/pkg/media/ffmpeg"
	driveup "velox/go-master/internal/upload/drive"
)

// Section describes a single text part for which a video should be generated.
type Section struct {
	Title  string `json:"title" binding:"required" example:"Castello Medievale"`
	Text   string `json:"text"  example:"Descrizione della scena..."`
	Style  string `json:"style" example:"medievale"`
	Engine string `json:"engine" example:"google-vids"` // "ken-burns" or "google-vids"
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

// Service generates one video per text section.
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
	videoGenTimeout = 5 * time.Minute
	imageGenWidth   = 1344
	imageGenHeight  = 768
	videoDuration   = 7
	videoMaxWorkers = 3

	// Output resolution for the final MP4 video (1920x1080)
	videoOutWidth  = 1920
	videoOutHeight = 1080
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
	prompts := buildSectionPrompts(sec, topic)
	prompt := pickBestPrompt(prompts, subject, topic)
	genID := hashutil.MD5String(prompt)[:12]

	// Precompute paths
	videoDir := filepath.Join(s.imagesDir, style, genID)
	videoName := genID + ".mp4"
	videoPath := filepath.Join(videoDir, videoName)

	// === Step 0: Cache check — return existing video if found ===
	if _, err := os.Stat(videoPath); err == nil {
		s.log.Info("fullimages: video already exists, returning cached",
			zap.Int("section", idx),
			zap.String("video_path", videoPath),
		)
		driveLink, driveFileID := loadCacheSidecar(videoPath)
		return SectionVideo{
			SectionIndex: idx,
			Title:        sec.Title,
			Style:        style,
			DriveLink:    driveLink,
			DriveFileID:  driveFileID,
			VideoPath:    videoPath,
		}
	}

	// === Step 1: Generate video/image via selected Engine ===
	engine := strings.ToLower(strings.TrimSpace(sec.Engine))
	if engine == "" {
		engine = "ken-burns" // Default
	}

	if engine == "google-vids" {
		s.log.Info("fullimages: generating full AI video via Google Vids", zap.Int("section", idx), zap.String("prompt", prompt))
		videoPath, err := s.imgService.GenerateVideoAI(ctx, prompt, style)
		if err == nil && videoPath != "" {
			// Video generated directly!
			return s.processGeneratedVideo(ctx, sec, idx, videoPath, genID, style, prompt)
		}
		s.log.Warn("fullimages: google-vids failed, falling back to ken-burns", zap.Error(err))
	}

	s.log.Info("fullimages: generating smart image for ken-burns", zap.Int("section", idx), zap.String("subject", subject), zap.String("style", style))

	tags := []string{subject, "style:" + style}

	// Use GenerateSmartImage which handles styles and fallback
	asset, err := s.imgService.GenerateSmartImage(ctx, subject, topic, style, prompts, tags, imageGenWidth, imageGenHeight, "flux-1-dev", true)

	if err != nil || asset == nil {
		errMsg := "all NVIDIA models failed"
		if err != nil {
			errMsg = err.Error()
		}
		s.log.Error("fullimages: no image could be generated", zap.Int("section", idx), zap.Error(err))
		return SectionVideo{SectionIndex: idx, Title: sec.Title, Style: style, Error: errMsg}
	}

	s.log.Info("fullimages: image ready",
		zap.Int("section", idx),
		zap.String("hash", asset.Hash),
		zap.String("path_rel", asset.PathRel),
		zap.String("source", asset.SourceURL),
	)

	// === Step 2: Locate image on disk ===
	imagePath := resolveImagePath(s.imagesDir, asset.Hash)
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

	// === Step 3: Convert image to 1920x1080 MP4 video with Ken Burns zoom ===
	s.log.Info("fullimages: converting image to 1920x1080 video",
		zap.Int("section", idx),
		zap.String("image", imagePath),
		zap.String("video", videoPath),
	)
	if err := os.MkdirAll(videoDir, 0755); err != nil {
		s.log.Error("fullimages: failed to create video dir", zap.String("dir", videoDir), zap.Error(err))
		return SectionVideo{
			SectionIndex: idx,
			Title:        sec.Title,
			Style:        style,
			Error:        fmt.Sprintf("failed to create video directory: %v", err),
		}
	}
	if err := s.ffmpegProc.ImageToVideo(ctx, imagePath, videoPath, ffmpeg.ImageToVideoOptions{
		Duration: videoDuration,
		Width:    videoOutWidth,
		Height:   videoOutHeight,
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

	s.log.Info("fullimages: video created",
		zap.Int("section", idx),
		zap.String("video_path", videoPath),
		zap.Int("width", videoOutWidth),
		zap.Int("height", videoOutHeight),
	)

	// === Step 4: Upload video to Drive ===
	return s.uploadAndFinish(ctx, sec, idx, videoPath, videoName, genID, style, prompt)
}

// processGeneratedVideo handles a video file already generated (e.g. via Google Vids)
func (s *Service) processGeneratedVideo(ctx context.Context, sec Section, idx int, tempPath, genID, style, prompt string) SectionVideo {
	// Precompute paths
	videoDir := filepath.Join(s.imagesDir, style, genID)
	videoName := genID + ".mp4"
	videoPath := filepath.Join(videoDir, videoName)

	if err := os.MkdirAll(videoDir, 0755); err != nil {
		return SectionVideo{SectionIndex: idx, Title: sec.Title, Error: fmt.Sprintf("failed to create dir: %v", err)}
	}

	// Move if different
	if tempPath != videoPath {
		if err := os.Rename(tempPath, videoPath); err != nil {
			// Try copy if rename fails (e.g. cross-device)
			input, err := os.ReadFile(tempPath)
			if err != nil {
				return SectionVideo{SectionIndex: idx, Title: sec.Title, Error: fmt.Sprintf("failed to move video: %v", err)}
			}
			if err := os.WriteFile(videoPath, input, 0644); err != nil {
				return SectionVideo{SectionIndex: idx, Title: sec.Title, Error: fmt.Sprintf("failed to write video: %v", err)}
			}
		}
	}

	// Upload to Drive (re-using Step 4 logic)
	return s.uploadAndFinish(ctx, sec, idx, videoPath, videoName, genID, style, prompt)
}

// uploadAndFinish handles the final Drive upload and result packaging.
func (s *Service) uploadAndFinish(ctx context.Context, sec Section, idx int, videoPath, videoName, genID, style, prompt string) SectionVideo {
	if s.driveUp == nil || s.driveRoot == "" {
		return SectionVideo{
			SectionIndex: idx,
			Title:        sec.Title,
			Style:        style,
			VideoPath:    videoPath,
		}
	}

	// Hierarchy: Root -> Style -> genID -> File
	folderID := s.driveRoot
	if style != "" {
		fid, err := s.driveUp.GetOrCreateFolder(ctx, style, s.driveRoot)
		if err == nil {
			folderID = fid
		}
	}

	genFolderID, err := s.driveUp.GetOrCreateFolder(ctx, genID, folderID)
	if err == nil {
		folderID = genFolderID
	}

	upResult, err := s.driveUp.UploadFile(ctx, videoPath, folderID, videoName)
	if err != nil {
		s.log.Error("fullimages: Drive upload failed", zap.Int("section", idx), zap.Error(err))
		saveCacheSidecar(videoPath, "", "")
		return SectionVideo{
			SectionIndex: idx,
			Title:        sec.Title,
			Style:        style,
			VideoPath:    videoPath,
			Error:        fmt.Sprintf("Drive upload failed: %v", err),
		}
	}

	saveCacheSidecar(videoPath, upResult.WebViewLink, upResult.FileID)
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
func safeFolderName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "untitled"
	}
	cleaned := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == ' ' {
			return r
		}
		return -1
	}, s)
	parts := strings.Fields(cleaned)
	return strings.Join(parts, " ")
}

// --- Cache sidecar helpers ---
// A tiny JSON file next to the video remembers the Drive link for fast cache hits.

type cacheMeta struct {
	DriveLink   string `json:"drive_link,omitempty"`
	DriveFileID string `json:"drive_file_id,omitempty"`
}

func cachePath(videoPath string) string {
	return videoPath + ".cache.json"
}

func saveCacheSidecar(videoPath, driveLink, driveFileID string) {
	p := cachePath(videoPath)
	data, err := json.Marshal(cacheMeta{DriveLink: driveLink, DriveFileID: driveFileID})
	if err != nil {
		return
	}
	_ = os.WriteFile(p, data, 0644)
}

func loadCacheSidecar(videoPath string) (string, string) {
	p := cachePath(videoPath)
	data, err := os.ReadFile(p)
	if err != nil {
		return "", ""
	}
	var m cacheMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return "", ""
	}
	return m.DriveLink, m.DriveFileID
}
