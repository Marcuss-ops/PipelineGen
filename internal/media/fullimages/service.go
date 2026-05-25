package fullimages

import (
	"context"
	"fmt"
	"sync"

	"go.uber.org/zap"
	imgservice "velox/go-master/internal/media/images"
	"velox/go-master/internal/media/models"
)

// Section describes a single text part for which an image should be generated.
// Title is used as the image subject/prompt, while Text provides additional context.
type Section struct {
	Title string `json:"title" binding:"required" example:"Introduzione"`
	Text  string `json:"text"  example:"Il testo completo di questa sezione..."`
}

// SectionImage holds the result for one generated image.
type SectionImage struct {
	SectionIndex int    `json:"section_index"`
	Title        string `json:"title"`
	Hash         string `json:"hash,omitempty"`
	PathRel      string `json:"path_rel,omitempty"`
	SourceURL    string `json:"source_url,omitempty"`
	DisplayURL   string `json:"display_url,omitempty"`
	Error        string `json:"error,omitempty"`
}

// Result wraps all section images into a single response.
type Result struct {
	Images []SectionImage `json:"images"`
}

// Service generates one image per text section using a two-tier strategy:
//   1. GenerateSmartImage — Google Accounting AI (if configured) with NVIDIA fallback
//   2. GenerateAImage    — direct NVIDIA API (fallback if tier 1 produces nothing)
//
// No entity extraction or asset association is performed — each section receives
// a pure AI-generated image based on its title and text.
type Service struct {
	imgService *imgservice.Service
	log        *zap.Logger
}

// NewService creates a FullImages service.
func NewService(imgService *imgservice.Service, log *zap.Logger) *Service {
	return &Service{
		imgService: imgService,
		log:        log,
	}
}

// GenerateForSections produces one image per section in parallel (worker pool).
// topic is an optional context string appended to every prompt.
// language is passed to any image-search fallback (default "it").
func (s *Service) GenerateForSections(ctx context.Context, sections []Section, topic, language string) (*Result, error) {
	if len(sections) == 0 {
		return nil, fmt.Errorf("at least one section is required")
	}
	if language == "" {
		language = "it"
	}

	s.log.Info("fullimages: starting generation",
		zap.Int("section_count", len(sections)),
		zap.String("topic", topic),
		zap.String("language", language),
	)

	const maxWorkers = 4
	results := make([]SectionImage, len(sections))
	sem := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup

	for i, sec := range sections {
		wg.Add(1)
		go func(idx int, sec Section) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			img := s.generateOne(ctx, sec, topic, language, idx)
			results[idx] = img
		}(i, sec)
	}

	wg.Wait()

	okCount := 0
	for _, r := range results {
		if r.Error == "" {
			okCount++
		}
	}

	s.log.Info("fullimages: generation complete",
		zap.Int("total", len(sections)),
		zap.Int("success", okCount),
		zap.Int("failed", len(sections)-okCount),
	)

	return &Result{Images: results}, nil
}

// generateOne attempts to create an image for a single section.
func (s *Service) generateOne(ctx context.Context, sec Section, topic, language string, idx int) SectionImage {
	ctx, cancel := context.WithTimeout(ctx, imageGenerationTimeout)
	defer cancel()

	subject := sec.Title
	if subject == "" {
		subject = fmt.Sprintf("section_%d", idx)
	}

	prompts := buildSectionPrompts(sec, topic)
	tags := []string{subject, topic}

	// Tier 1: GenerateSmartImage (Google Accounting AI → NVIDIA fallback)
	asset, err := s.imgService.GenerateSmartImage(ctx, subject, topic, prompts, tags, imageWidth, imageHeight, "")
	if err == nil && asset != nil {
		s.log.Info("fullimages: smart image generated",
			zap.Int("section", idx),
			zap.String("subject", subject),
			zap.String("hash", asset.Hash),
		)
		return sectionImageFromAsset(idx, sec.Title, asset, "")
	}

	s.log.Warn("fullimages: smart image failed, trying NVIDIA direct",
		zap.Int("section", idx),
		zap.String("subject", subject),
		zap.Error(err),
	)

	// Tier 2: Fallback to NVIDIA direct generation
	directPrompt := pickBestPrompt(prompts, subject, topic)
	asset, err = s.imgService.GenerateAImage(ctx, directPrompt, "", imageWidth, imageHeight, tags)
	if err != nil {
		s.log.Error("fullimages: all generation methods failed",
			zap.Int("section", idx),
			zap.String("subject", subject),
			zap.Error(err),
		)
		return SectionImage{
			SectionIndex: idx,
			Title:        sec.Title,
			Error:        fmt.Sprintf("image generation failed: %v", err),
		}
	}

	s.log.Info("fullimages: nvidia direct image generated",
		zap.Int("section", idx),
		zap.String("subject", subject),
		zap.String("hash", asset.Hash),
	)

	return sectionImageFromAsset(idx, sec.Title, asset, "")
}

// sectionImageFromAsset converts a *models.ImageAsset into a SectionImage.
func sectionImageFromAsset(idx int, title string, asset *models.ImageAsset, errStr string) SectionImage {
	if errStr != "" {
		return SectionImage{SectionIndex: idx, Title: title, Error: errStr}
	}
	return SectionImage{
		SectionIndex: idx,
		Title:        title,
		Hash:         asset.Hash,
		PathRel:      asset.PathRel,
		SourceURL:    asset.SourceURL,
		DisplayURL:   resolveDisplayURL(asset),
	}
}
