package lessons

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	imgservice "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// Service provides web lesson generation from source text.
// Orchestrates splitting, parallel chapter generation, and optional image generation.
type Service struct {
	cfg        *LessonsConfig
	generator  *ollama.Generator
	imgService *imgservice.Service
	docClient  delivery.DocPublisher
	log        *zap.Logger
}

// NewService creates a new lessons service.
// generator is required; imgService and docClient are optional.
func NewService(
	cfg *LessonsConfig,
	generator *ollama.Generator,
	imgService *imgservice.Service,
	docClient delivery.DocPublisher,
	log *zap.Logger,
) *Service {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	return &Service{
		cfg:        cfg,
		generator:  generator,
		imgService: imgService,
		docClient:  docClient,
		log:        log,
	}
}

// IsEnabled returns whether the lessons service is enabled.
func (s *Service) IsEnabled() bool {
	return s.cfg != nil && s.cfg.Enabled
}

// GenerateLesson runs the full lesson generation pipeline synchronously.
// Splits source text, generates chapters in parallel, optionally generates images,
// and produces Markdown + PDF output.
func (s *Service) GenerateLesson(ctx context.Context, req *LessonRequest) (*LessonResult, error) {
	return s.GenerateLessonWithProgress(ctx, req, nil)
}

// GenerateLessonWithProgress runs the lesson pipeline with progress callbacks.
// onProgress receives percentage (0-100) and a status message.
func (s *Service) GenerateLessonWithProgress(
	ctx context.Context,
	req *LessonRequest,
	onProgress func(int, string),
) (*LessonResult, error) {
	if !s.IsEnabled() {
		return nil, fmt.Errorf("lessons service is disabled")
	}
	if s.generator == nil {
		return nil, fmt.Errorf("ollama generator not initialized")
	}
	if strings.TrimSpace(req.SourceText) == "" {
		return nil, fmt.Errorf("source_text is required")
	}

	// Apply defaults
	lang := req.Language
	if lang == "" {
		lang = s.cfg.DefaultLanguage
	}
	tone := req.Tone
	if tone == "" {
		tone = s.cfg.DefaultTone
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = extractTitle(req.SourceText)
	}

	// Step 1: Split into chapters
	if onProgress != nil {
		onProgress(5, "Splitting source text into chapters")
	}
	chapters := s.SplitIntoChapters(req.SourceText, req.MaxChapters)
	s.log.Info("source text split into chapters",
		zap.Int("chapter_count", len(chapters)),
		zap.String("title", title),
	)

	if len(chapters) == 0 {
		return nil, fmt.Errorf("no chapters could be extracted from source text")
	}

	// Step 2: Generate chapters in parallel
	if onProgress != nil {
		onProgress(10, "Generating chapters in parallel")
	}

	// Apply defaults to request before passing to generator
	req.Title = title
	req.Language = lang
	req.Tone = tone

	results := s.GenerateChapters(ctx, chapters, req, onProgress)

	// Step 3: Assemble result struct
	if onProgress != nil {
		onProgress(90, "Assembling lesson result")
	}

	lesson := s.assembleResult(title, lang, results)

	if !lesson.Success {
		return lesson, nil
	}

	// Step 4: Save Markdown file
	if onProgress != nil {
		onProgress(92, "Saving Markdown file")
	}
	outputDir := filepath.Join("data", "lessons", textutil.Slugify(title))
	mdPath, err := s.SaveLessonMarkdown(lesson, outputDir)
	if err != nil {
		s.log.Warn("failed to save lesson markdown", zap.Error(err))
	} else {
		lesson.MarkdownPath = mdPath
	}

	// Step 5: Generate PDF (if requested)
	if req.GeneratePDF {
		if onProgress != nil {
			onProgress(95, "Generating PDF")
		}
		pdfPath, err := s.GenerateLessonPDF(lesson, outputDir)
		if err != nil {
			s.log.Warn("failed to generate lesson PDF", zap.Error(err))
		} else {
			lesson.PDFPath = pdfPath
		}
	}

	if onProgress != nil {
		onProgress(100, "Lesson generation completed")
	}

	return lesson, nil
}

// assembleResult builds the final LessonResult from chapter results.
func (s *Service) assembleResult(title, language string, chapters []ChapterResult) *LessonResult {
	totalWords := 0
	successCount := 0
	for _, ch := range chapters {
		totalWords += ch.WordCount
		if ch.Error == "" {
			successCount++
		}
	}

	result := &LessonResult{
		Success:     successCount > 0,
		Title:       title,
		Language:    language,
		Chapters:    chapters,
		TotalWords:  totalWords,
		GeneratedAt: timeutil.FormatRFC3339(time.Now()),
	}

	if successCount == 0 {
		result.Error = "all chapters failed to generate"
	}

	return result
}

// estimateChapterDuration estimates a shorter duration for a single chapter.
func estimateChapterDuration(chapterText string) int {
	words := len(strings.Fields(chapterText))
	if words == 0 {
		return 120
	}
	est := (words * 60) / 140
	if est < 60 {
		return 60
	}
	if est > 600 {
		return 600
	}
	return est
}

// buildChapterGenerationRequest creates a types.TextGenerationRequest for Ollama chapter generation.
func (s *Service) buildChapterGenerationRequest(chapter ChapterSplit, req *LessonRequest) types.TextGenerationRequest {
	duration := estimateChapterDuration(chapter.Text)
	return types.TextGenerationRequest{
		Language:   req.Language,
		Duration:   duration,
		Tone:       req.Tone,
		Model:      s.cfg.DefaultModel,
		Prompt:     fmt.Sprintf("Write an educational lesson chapter titled '%s' based on the provided reference material.", chapter.Title),
		SourceText: fmt.Sprintf("CHAPTER TITLE: %s\n\nREFERENCE MATERIAL:\n%s", chapter.Title, chapter.Text),
		Title:      chapter.Title,
	}
}
