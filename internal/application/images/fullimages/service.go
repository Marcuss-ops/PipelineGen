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

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	ffmpeg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"go.uber.org/zap"
)

// Section describes a single text part for which a video should be generated.
type Section struct {
	Title  string `json:"title" binding:"required" example:"Castello Medievale"`
	Text   string `json:"text"  example:"Descrizione della scena..."`
	Style  string `json:"style" example:"medievale"`
	Engine string `json:"engine,omitempty"` // Deprecated: generation always uses Google Slides + Ken Burns.
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
	publisher  delivery.Publisher
	imagesDir  string
	log        *zap.Logger
}

// NewService creates a FullImages video-generation service.
func NewService(imgService *imgservice.Service, ffmpegProc *ffmpeg.Processor, publisher delivery.Publisher, imagesDir string, log *zap.Logger) *Service {
	return &Service{
		imgService: imgService,
		ffmpegProc: ffmpegProc,
		publisher:  publisher,
		imagesDir:  imagesDir,
		log:        log,
	}
}

const (
	videoGenTimeout = 5 * time.Minute
	imageGenWidth   = 1344
	imageGenHeight  = 768
	videoDuration   = 7
	videoMaxWorkers = 3

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
		i, sec := i, sec
		wg.Add(1)
		concurrent.SafeGoFunc("fullimages-section", struct {
			Idx   int
			Sec   Section
			Sem   chan struct{}
			Topic string
		}{Idx: i, Sec: sec, Sem: sem, Topic: topic}, func(arg struct {
			Idx   int
			Sec   Section
			Sem   chan struct{}
			Topic string
		}) {
			defer wg.Done()
			arg.Sem <- struct{}{}
			defer func() { <-arg.Sem }()

			results[arg.Idx] = s.generateOneVideo(ctx, arg.Sec, arg.Topic, arg.Idx)
		})
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

// generateOneVideo generates the source image exclusively through Google
// Slides, then converts it to a Ken Burns MP4.
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

	videoDir := filepath.Join(s.imagesDir, style, genID)
	videoName := genID + ".mp4"
	videoPath := filepath.Join(videoDir, videoName)

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

	s.log.Info("fullimages: generating source image with Google Slides",
		zap.Int("section", idx),
		zap.String("subject", subject),
		zap.String("style", style),
	)

	tags := []string{subject, "style:" + style}
	imageAsset, err := s.imgService.GenerateSmartImage(
		ctx,
		subject,
		topic,
		style,
		prompts,
		tags,
		imageGenWidth,
		imageGenHeight,
		"", // empty resolves to the sole canonical model: nano-banana-pro
		true,
	)
	if err != nil || imageAsset == nil {
		errMsg := "Google Slides image generation failed"
		if err != nil {
			errMsg = err.Error()
		}
		s.log.Error("fullimages: no image could be generated", zap.Int("section", idx), zap.Error(err))
		return SectionVideo{SectionIndex: idx, Title: sec.Title, Style: style, Error: errMsg}
	}

	s.log.Info("fullimages: image ready",
		zap.Int("section", idx),
		zap.String("hash", imageAsset.Hash),
		zap.String("path_rel", imageAsset.PathRel),
		zap.String("source", imageAsset.SourceURL),
	)

	imagePath := resolveImagePath(s.imagesDir, imageAsset.Hash)
	if imagePath == "" && imageAsset.PathRel != "" {
		imagePath = filepath.Join(s.imagesDir, imageAsset.PathRel)
	}
	if imagePath == "" {
		s.log.Error("fullimages: image file not found on disk", zap.Int("section", idx), zap.String("hash", imageAsset.Hash))
		return SectionVideo{
			SectionIndex: idx,
			Title:        sec.Title,
			Style:        style,
			Error:        "image file not found on disk after generation",
		}
	}

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

	return s.uploadAndFinish(ctx, sec, idx, videoPath, videoName, genID, style, prompt)
}

// processGeneratedVideo handles a video file already generated by an external
// compatibility caller. New generation does not use this path.
func (s *Service) processGeneratedVideo(ctx context.Context, sec Section, idx int, tempPath, genID, style, prompt string) SectionVideo {
	videoDir := filepath.Join(s.imagesDir, style, genID)
	videoName := genID + ".mp4"
	videoPath := filepath.Join(videoDir, videoName)

	if err := os.MkdirAll(videoDir, 0755); err != nil {
		return SectionVideo{SectionIndex: idx, Title: sec.Title, Error: fmt.Sprintf("failed to create dir: %v", err)}
	}

	if tempPath != videoPath {
		if err := os.Rename(tempPath, videoPath); err != nil {
			input, readErr := os.ReadFile(tempPath)
			if readErr != nil {
				return SectionVideo{SectionIndex: idx, Title: sec.Title, Error: fmt.Sprintf("failed to move video: %v", readErr)}
			}
			if writeErr := os.WriteFile(videoPath, input, 0644); writeErr != nil {
				return SectionVideo{SectionIndex: idx, Title: sec.Title, Error: fmt.Sprintf("failed to write video: %v", writeErr)}
			}
		}
	}

	return s.uploadAndFinish(ctx, sec, idx, videoPath, videoName, genID, style, prompt)
}

// uploadAndFinish handles the final Drive upload and result packaging.
func (s *Service) uploadAndFinish(ctx context.Context, sec Section, idx int, videoPath, videoName, genID, style, prompt string) SectionVideo {
	// P0-2 (July 2026): check publisher — the canonical path.
	// The legacy mediaStore fallback was retired per godlike/07.
	if s.publisher == nil {
		return SectionVideo{
			SectionIndex: idx,
			Title:        sec.Title,
			Style:        style,
			VideoPath:    videoPath,
		}
	}

	req := drive.AssetDestinationRequest{
		Source:    "fullimages",
		MediaType: drive.MediaTypeImageVideo,
		Style:     style,
		Subject:   genID,
		Hash:      genID,
		Ext:       filepath.Ext(videoName),
	}

	fileID, webLink, err := s.publishToDrive(ctx, req, videoPath)
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

	saveCacheSidecar(videoPath, webLink, fileID)

	if err := s.imgService.RegisterVideoAsset(ctx, videoPath, prompt, "ken-burns", style, videoDuration, fileID, webLink); err != nil {
		s.log.Warn("fullimages: failed to register video asset in DB", zap.Error(err))
	}

	return SectionVideo{
		SectionIndex: idx,
		Title:        sec.Title,
		Style:        style,
		DriveLink:    webLink,
		DriveFileID:  fileID,
		VideoPath:    videoPath,
	}
}

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

// publishToDrive is the P0-2 canonical bridge for fullimages video uploads.
// Routes through delivery.Publisher.Publish. The legacy mediaStore.UploadToDrive
// fallback was RETIRED per P0-2 godlike/07 closure (July 2026).
func (s *Service) publishToDrive(ctx context.Context, req drive.AssetDestinationRequest, filePath string) (string, string, error) {
	if s == nil {
		return "", "", fmt.Errorf("fullimages.Service.publishToDrive: nil receiver")
	}
	if s.publisher == nil {
		return "", "", fmt.Errorf("fullimages.Service.publishToDrive: publisher not configured (P0-2 godlike/07: nil publisher fail-closed)")
	}
	result, err := s.publisher.Publish(ctx, delivery.PublishRequest{
		Destination:    delivery.DestinationImage,
		LocalPath:      filePath,
		Filename:       filepath.Base(filePath),
		Style:          req.Style,
		Subject:        req.Subject,
		Group:          req.Subject,
		ConflictPolicy: delivery.ConflictSkip,
	})
	if err != nil {
		return "", "", fmt.Errorf("fullimages publishToDrive: %w", err)
	}
	return result.FileID, result.WebViewLink, nil
}
