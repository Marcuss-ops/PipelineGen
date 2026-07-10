package fullimages

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"go.uber.org/zap"
)

// Section describes a single text part for which an image should be generated.
type Section struct {
	Title string `json:"title" binding:"required" example:"Castello Medievale"`
	Text  string `json:"text"  example:"Descrizione della scena..."`
	Style string `json:"style" example:"medievale"`
}

// SectionImage holds the result for one generated image.
//
// PR-IMAGES-FULLIMAGES-IMAGE-ONLY (2026-07-10, CUTOVER phase, IMAGES-LEGACY-CLEANUP
// wave): the pre-CUTOVER `SectionVideo` type is RENAMED. The `VideoPath`
// field becomes `ImagePath` (the path on disk is to a generated IMAGE,
// not a Ken Burns MP4). Wire-shape breaking change per Option B — see
// the commit message for the consumer awareness note.
type SectionImage struct {
	SectionIndex int    `json:"section_index"`
	Title        string `json:"title"`
	Style        string `json:"style,omitempty"`
	DriveLink    string `json:"drive_link,omitempty"`
	DriveFileID  string `json:"drive_file_id,omitempty"`
	ImagePath    string `json:"image_path,omitempty"`
	Error        string `json:"error,omitempty"`
}

// Result wraps all section images into a single response.
//
// PR-IMAGES-FULLIMAGES-IMAGE-ONLY (2026-07-10, CUTOVER phase): the
// pre-CUTOVER `Videos` field (json: "videos") is RENAMED to `Images`
// (json: "images"). Wire-shape breaking change per Option B.
type Result struct {
	Images []SectionImage `json:"images"`
}

// Service generates one image per text section.
//
// PR-IMAGES-FULLIMAGES-IMAGE-ONLY (2026-07-10, CUTOVER phase): the
// pre-CUTOVER 5-field struct (imgService + ffmpegProc + publisher +
// imagesDir + log) is REDUCED to 3 fields (imgService + imagesDir +
// log). The `ffmpegProc` + `publisher` fields were ONLY consumed by
// the now-retired Ken Burns video pipeline (processGeneratedVideo /
// uploadAndFinish / publishToDrive / cacheMeta / cachePath /
// saveCacheSidecar / loadCacheSidecar) which generated MP4s. The
// image-only path consumes ONLY `s.imgService.GenerateSmartImage`
// (which performs the Drive upload internally via the skipDrive=false
// contract) — no FFmpeg or external Publisher is needed at this layer.
type Service struct {
	imgService *imgservice.Service
	imagesDir  string
	log        *zap.Logger
}

// NewService creates a FullImages image-generation service.
//
// PR-IMAGES-FULLIMAGES-IMAGE-ONLY (2026-07-10, CUTOVER phase): the
// pre-CUTOVER 5-arg signature
// (imgService, ffmpegProc, publisher, imagesDir, log) is REPLACED
// with this 3-arg signature. The `ffmpegProc` + `publisher` args
// were ONLY consumed by the now-retired Ken Burns video pipeline.
func NewService(imgService *imgservice.Service, imagesDir string, log *zap.Logger) *Service {
	return &Service{
		imgService: imgService,
		imagesDir:  imagesDir,
		log:        log,
	}
}

const (
	imageGenWidth   = 1344
	imageGenHeight  = 768
	imageMaxWorkers = 3
)

// GenerateForSections produces one image per section in parallel.
//
// PR-IMAGES-FULLIMAGES-IMAGE-ONLY (2026-07-10, CUTOVER phase): the
// pre-CUTOVER `generateOneVideo` per-section call is RENAMED to
// `generateOneImage`. The pre-CUTOVER cache-pre-check
// (`os.Stat(videoPath)` + `loadCacheSidecar`) is RETIRED — the
// imgservice layer performs its own per-asset caching keyed on the
// content hash, and the canonical image-asset writer preserves
// idempotency on re-generation.
func (s *Service) GenerateForSections(ctx context.Context, sections []Section, topic, language string) (*Result, error) {
	if len(sections) == 0 {
		return nil, fmt.Errorf("at least one section is required")
	}

	s.log.Info("fullimages: starting image generation",
		zap.Int("section_count", len(sections)),
		zap.String("topic", topic),
	)

	results := make([]SectionImage, len(sections))
	sem := make(chan struct{}, imageMaxWorkers)
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

			results[arg.Idx] = s.generateOneImage(ctx, arg.Sec, arg.Topic, arg.Idx)
		})
	}

	wg.Wait()

	okCount := 0
	for _, r := range results {
		if r.Error == "" {
			okCount++
		}
	}

	s.log.Info("fullimages: image generation complete",
		zap.Int("total", len(sections)),
		zap.Int("success", okCount),
		zap.Int("failed", len(sections)-okCount),
	)

	return &Result{Images: results}, nil
}

// generateOneImage generates the source image through the canonical
// imgservice.GenerateSmartImage path (which performs the Drive upload
// via the skipDrive=false contract).
//
// Per-section safety timeout: 5 minutes. PR-IMAGES-FULLIMAGES-IMAGE-ONLY
// (2026-07-10, CUTOVER phase) inlines this constant at the callsite per
// godlike/07 minimum-blast-radius — the pre-CUTOVER `videoGenTimeout`
// const was retired with the Ken Burns video pipeline; no
// `imageGenTimeout` const is invented (honor the "delete 4 video-only
// constants" user spec literal).
func (s *Service) generateOneImage(ctx context.Context, sec Section, topic string, idx int) SectionImage {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	subject := sec.Title
	if subject == "" {
		subject = fmt.Sprintf("section_%d", idx)
	}
	style := strings.TrimSpace(sec.Style)
	prompts := buildSectionPrompts(sec, topic)

	s.log.Info("fullimages: generating source image",
		zap.Int("section", idx),
		zap.String("subject", subject),
		zap.String("style", style),
		zap.Int("prompt_count", len(prompts)),
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
		"",    // empty resolves to the sole canonical model: nano-banana-pro
		false, // skipDrive = false, we WANT to upload the image to Drive!
	)
	if err != nil || imageAsset == nil {
		errMsg := "image generation failed"
		if err != nil {
			errMsg = err.Error()
		}
		s.log.Error("fullimages: no image could be generated", zap.Int("section", idx), zap.Error(err))
		return SectionImage{SectionIndex: idx, Title: sec.Title, Style: style, Error: errMsg}
	}

	s.log.Info("fullimages: image ready",
		zap.Int("section", idx),
		zap.String("hash", imageAsset.Hash),
		zap.String("path_rel", imageAsset.PathRel),
		zap.String("source", imageAsset.SourceURL),
	)

	imagePath := resolveImagePath(s.imagesDir, imageAsset.Hash)
	if imagePath == "" && imageAsset.PathRel != "" {
		imagePath = filepath.Join(filepath.Dir(s.imagesDir), imageAsset.PathRel)
	}
	if imagePath == "" {
		s.log.Error("fullimages: image file not found on disk",
			zap.String("imagesDir", s.imagesDir),
			zap.String("mediaDir", filepath.Dir(s.imagesDir)),
			zap.String("pathRel", imageAsset.PathRel),
		)
		return SectionImage{
			SectionIndex: idx,
			Title:        sec.Title,
			Style:        style,
			Error:        "image file not found on disk after generation",
		}
	}

	return SectionImage{
		SectionIndex: idx,
		Title:        sec.Title,
		Style:        style,
		DriveLink:    s.imgService.FormatDriveLink(imageAsset.DriveFileID),
		DriveFileID:  imageAsset.DriveFileID,
		ImagePath:    imagePath,
	}
}
