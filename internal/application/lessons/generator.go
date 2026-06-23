package lessons

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// GenerateChapters processes all chapter splits in parallel using concurrent.ParallelMap.
// Each chapter is generated via Ollama Chat API and optionally accompanied by an AI image.
//
// Concurrency is controlled by cfg.MaxParallelChapters (default: 5).
// If a chapter fails, the error is recorded but other chapters continue.
func (s *Service) GenerateChapters(
	ctx context.Context,
	chapters []ChapterSplit,
	req *LessonRequest,
	onProgress func(int, string),
) []ChapterResult {

	concurrency := s.cfg.MaxParallelChapters
	if concurrency < 1 {
		concurrency = 5
	}

	total := len(chapters)
	completed := 0
	var muCompleted sync.Mutex

	return concurrent.ParallelMap(chapters, concurrency, func(idx int, split ChapterSplit) ChapterResult {
		result := ChapterResult{
			Index: idx,
			Title: split.Title,
		}

		// 1. Generate chapter text via Ollama
		chapterCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()

		textReq := s.buildChapterGenerationRequest(split, req)
		genResult, err := s.generator.GenerateScript(chapterCtx, textReq)
		if err != nil {
			s.log.Error("chapter generation failed",
				zap.Int("chapter", idx),
				zap.String("title", split.Title),
				zap.Error(err),
			)
			result.Error = fmt.Errorf("chapter %d (%s) generation failed: %w", idx+1, split.Title, err).Error()
			return result
		}

		result.Content = strings.TrimSpace(genResult.Script)
		result.WordCount = genResult.WordCount

		s.log.Info("chapter generated",
			zap.Int("chapter", idx+1),
			zap.String("title", split.Title),
			zap.Int("words", result.WordCount),
		)

		// 2. Optionally generate image for this chapter
		if req.GenerateImages && s.imgService != nil {
			imgRef, imgErr := s.generateChapterImage(chapterCtx, split, req)
			if imgErr != nil {
				s.log.Warn("chapter image generation failed",
					zap.Int("chapter", idx),
					zap.String("title", split.Title),
					zap.Error(imgErr),
				)
				// Non-fatal: chapter is still valid without image
			} else {
				result.Image = imgRef
				s.log.Info("chapter image generated",
					zap.Int("chapter", idx+1),
					zap.String("hash", imgRef.Hash),
				)
			}
		}

		// Update progress
		muCompleted.Lock()
		completed++
		pct := 10 + (completed * 80 / total)
		if pct > 90 {
			pct = 90
		}
		msg := fmt.Sprintf("Chapter %d/%d completed: %s", completed, total, split.Title)
		muCompleted.Unlock()

		if onProgress != nil {
			onProgress(pct, msg)
		}

		return result
	})
}

// generateChapterImage generates an AI image for a single chapter.
// This is a Fase 2 placeholder — actual implementation will call images.Service.
func (s *Service) generateChapterImage(
	ctx context.Context,
	split ChapterSplit,
	req *LessonRequest,
) (*ImageRef, error) {
	if s.imgService == nil {
		return nil, fmt.Errorf("image service not available")
	}

	// Build a descriptive prompt for the image based on chapter title and content
	prompt := buildImagePrompt(split.Title, split.Text, req)

	// Use GenerateSmartImage: tries Google Vids first, falls back to NVIDIA Flux
	imageModel := req.ImageModel
	if imageModel == "" {
		imageModel = s.cfg.DefaultImageModel
	}

	width := req.ImageWidth
	if width <= 0 {
		width = 1024
	}
	height := req.ImageHeight
	if height <= 0 {
		height = 1024
	}

	asset, err := s.imgService.GenerateSmartImage(
		ctx,
		split.Title,                      // subject
		req.Title,                        // topic
		req.ImageStyle,                   // style
		[]string{prompt},                 // prompts
		[]string{req.Title, split.Title}, // tags
		width,
		height,
		imageModel,
		false, // skipDrive = false (always upload to Drive)
	)
	if err != nil {
		return nil, fmt.Errorf("smart image generation failed: %w", err)
	}
	if asset == nil {
		return nil, fmt.Errorf("smart image returned nil asset")
	}

	driveLink := ""
	driveFileID := ""
	if asset.DriveFileID != "" {
		driveFileID = asset.DriveFileID
		driveLink = drive.FileURLFromID(asset.DriveFileID)
	}

	return &ImageRef{
		Hash:        asset.Hash,
		PathRel:     asset.PathRel,
		URL:         "/assets/" + asset.PathRel,
		DriveLink:   driveLink,
		DriveFileID: driveFileID,
		Prompt:      prompt,
	}, nil
}

// buildImagePrompt creates a visual prompt for AI image generation from chapter content.
func buildImagePrompt(chapterTitle, chapterText string, req *LessonRequest) string {
	// Use the first ~300 chars of text for context, limited to a few sentences
	contextText := textutil.Truncate(chapterText, 300)

	prompt := fmt.Sprintf(
		"Educational illustration for a lesson chapter titled '%s'. "+
			"Style: %s. Topic: %s. Context: %s",
		chapterTitle,
		req.ImageStyle,
		req.Title,
		contextText,
	)
	return prompt
}
