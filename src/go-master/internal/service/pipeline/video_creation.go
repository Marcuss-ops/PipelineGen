// Package pipeline provides orchestration services for multi-step workflows.
package pipeline

import (
	"context"
	"fmt"
	"strings"
	"time"

	"velox/go-master/internal/core/entities"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/pkg/logger"
	"velox/go-master/pkg/security"
	"go.uber.org/zap"
)

// VideoCreationService orchestrates the full video creation pipeline
type VideoCreationService struct {
	scriptGen     ScriptGenerator
	entityService EntityService
	ttsGenerator  TTSGenerator
	videoProc     VideoProcessor
	outputDir     string
}

// NewVideoCreationService creates a new video creation service
func NewVideoCreationService(
	scriptGen ScriptGenerator,
	entityService EntityService,
	ttsGenerator TTSGenerator,
	videoProc VideoProcessor,
) *VideoCreationService {
	return &VideoCreationService{
		scriptGen:     scriptGen,
		entityService: entityService,
		ttsGenerator:  ttsGenerator,
		videoProc:     videoProc,
		outputDir:     "/tmp/velox/output",
	}
}

// NewVideoCreationServiceWithOutputDir creates a video creation service with a custom output directory
func NewVideoCreationServiceWithOutputDir(
	scriptGen ScriptGenerator,
	entityService EntityService,
	ttsGenerator TTSGenerator,
	videoProc VideoProcessor,
	outputDir string,
) *VideoCreationService {
	if outputDir == "" {
		outputDir = "/tmp/velox/output"
	}
	return &VideoCreationService{
		scriptGen:     scriptGen,
		entityService: entityService,
		ttsGenerator:  ttsGenerator,
		videoProc:     videoProc,
		outputDir:     outputDir,
	}
}

// CreateMaster executes the complete video creation pipeline
func (s *VideoCreationService) CreateMaster(ctx context.Context, req *VideoCreationRequest) (*VideoCreationResult, error) {
	// Validate request
	if err := s.validateRequest(req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Apply defaults
	s.applyDefaults(req)

	// Generate job ID
	jobID := "job_" + time.Now().Format("20060102150405")

	logger.Info("Starting video creation pipeline",
		zap.String("job_id", jobID),
		zap.String("video_name", req.VideoName),
		zap.String("language", req.Language),
		zap.Int("duration", req.Duration),
	)

	// Step 1: Generate script
	scriptResult, err := s.generateScript(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("script generation failed: %w", err)
	}

	// Step 2: Extract entities
	entityAnalysis, err := s.extractEntities(ctx, scriptResult.Script, req.EntityCount)
	if err != nil {
		logger.Warn("Entity extraction failed, continuing without entities",
			zap.Error(err),
		)
		entityAnalysis = &entities.ScriptEntityAnalysis{
			TotalSegments:   0,
			SegmentEntities: []entities.SegmentEntityResult{},
			TotalEntities:   0,
		}
	}

	// Step 3: Generate voiceover
	voiceoverResults, err := s.generateVoiceover(ctx, scriptResult.Script, req.Language, req.SkipGDocs)
	if err != nil {
		return nil, fmt.Errorf("voiceover generation failed: %w", err)
	}

	// Step 4: Create video processing job
	videoResult, err := s.createVideoJob(jobID, req, scriptResult.Script)
	if err != nil {
		logger.Warn("Video job creation failed, continuing anyway",
			zap.Error(err),
		)
		videoResult = &VideoGenerationResult{
			JobID:     jobID,
			VideoPath: "",
			Status:    "failed",
		}
	}

	// Build final result
	result := &VideoCreationResult{
		JobID:           jobID,
		VideoName:       req.VideoName,
		ProjectName:     req.ProjectName,
		Status:          "processing",
		ScriptGenerated: scriptResult.Generated,
		ScriptText:      scriptResult.Script,
		ScriptWordCount: scriptResult.WordCount,
		ScriptModel:     scriptResult.Model,
		EntityAnalysis:  entityAnalysis,
		VoiceoverResults: voiceoverResults,
		VideoCreated:    videoResult.Status == "created",
		VideoOutput:     videoResult.VideoPath,
	}

	logger.Info("Video creation pipeline completed",
		zap.String("job_id", jobID),
		zap.Bool("script_generated", result.ScriptGenerated),
		zap.Int("entities_extracted", entityAnalysis.TotalEntities),
		zap.Int("voiceovers_generated", len(voiceoverResults)),
		zap.Bool("video_created", result.VideoCreated),
	)

	return result, nil
}

// validateRequest validates the video creation request
func (s *VideoCreationService) validateRequest(req *VideoCreationRequest) error {
	if req.VideoName == "" {
		return fmt.Errorf("video_name is required")
	}
	if req.Source == "" && req.YouTubeURL == "" && req.ScriptText == "" {
		return fmt.Errorf("one of source, youtube_url, or script_text is required")
	}
	if len(req.Source) > 100000 {
		return fmt.Errorf("source text exceeds maximum length (100000 chars)")
	}
	if req.Duration < 10 {
		return fmt.Errorf("duration must be at least 10 seconds")
	}
	if req.Duration > 3600 {
		return fmt.Errorf("duration cannot exceed 60 minutes (3600 seconds)")
	}
	return nil
}

// applyDefaults applies default values to the request
func (s *VideoCreationService) applyDefaults(req *VideoCreationRequest) {
	if req.Language == "" {
		req.Language = "it"
	}
	if req.Duration == 0 {
		req.Duration = 25
	}
	if req.EntityCount <= 0 {
		req.EntityCount = 12
	}
}

// ScriptResult holds script generation output
type ScriptResult struct {
	Script    string
	WordCount int
	Model     string
	Generated bool
}

// generateScript handles script generation step
func (s *VideoCreationService) generateScript(ctx context.Context, req *VideoCreationRequest) (*ScriptResult, error) {
	// If script already provided, return it
	if req.ScriptText != "" {
		wordCount := 0
		if s.entityService != nil {
			wordCount = s.entityService.Segmenter().CountWords(req.ScriptText)
		}
		return &ScriptResult{
			Script:    req.ScriptText,
			WordCount: wordCount,
			Model:     "user_provided",
			Generated: false,
		}, nil
	}

	// YouTube URL not yet implemented
	if req.YouTubeURL != "" {
		return nil, fmt.Errorf("YouTube URL script generation is not implemented. Use /script/from-transcript with a pre-fetched transcript or provide source text directly")
	}

	// Generate from source text
	if req.Source == "" {
		return nil, fmt.Errorf("no source text provided")
	}

	logger.Info("Generating script from source",
		zap.String("video_name", req.VideoName),
		zap.Int("source_length", len(req.Source)),
	)

	ollamaLang := mapLanguageToOllama(req.Language)

	txtReq := &ollama.TextGenerationRequest{
		SourceText: req.Source,
		Title:      req.VideoName,
		Language:   ollamaLang,
		Duration:   req.Duration,
	}

	result, err := s.scriptGen.GenerateFromText(ctx, txtReq)
	if err != nil {
		return nil, fmt.Errorf("script generation failed: %w", err)
	}

	return &ScriptResult{
		Script:    result.Script,
		WordCount: result.WordCount,
		Model:     result.Model,
		Generated: true,
	}, nil
}

// extractEntities handles entity extraction step
func (s *VideoCreationService) extractEntities(ctx context.Context, script string, entityCount int) (*entities.ScriptEntityAnalysis, error) {
	if script == "" || s.entityService == nil {
		return &entities.ScriptEntityAnalysis{
			TotalSegments:   0,
			SegmentEntities: []entities.SegmentEntityResult{},
			TotalEntities:   0,
		}, nil
	}

	logger.Info("Starting entity extraction",
		zap.Int("entity_count_per_category", entityCount),
	)

	analysis, err := s.entityService.AnalyzeScript(
		ctx,
		script,
		entityCount,
		entities.SegmentConfig{
			TargetWordsPerSegment: 800,
		},
	)

	if err != nil {
		return nil, fmt.Errorf("entity extraction failed: %w", err)
	}

	logger.Info("Entity extraction completed",
		zap.Int("total_segments", analysis.TotalSegments),
		zap.Int("total_entities", analysis.TotalEntities),
	)

	return analysis, nil
}

// generateVoiceover handles voiceover generation step
func (s *VideoCreationService) generateVoiceover(ctx context.Context, script string, language string, skipGDocs bool) ([]VoiceoverResult, error) {
	if script == "" || skipGDocs || s.ttsGenerator == nil {
		return []VoiceoverResult{}, nil
	}

	logger.Info("Generating voiceover",
		zap.Int("script_length", len(script)),
		zap.String("language", language),
	)

	result, err := s.ttsGenerator.Generate(ctx, script, language)
	if err != nil {
		return nil, fmt.Errorf("voiceover generation failed: %w", err)
	}

	return []VoiceoverResult{
		{
			AudioFile: result.FilePath,
			Duration:  result.Duration,
			Language:  result.Language,
			Voice:     result.VoiceUsed,
		},
	}, nil
}

// createVideoJob handles video job creation step
func (s *VideoCreationService) createVideoJob(jobID string, req *VideoCreationRequest, script string) (*VideoGenerationResult, error) {
	if script == "" || s.videoProc == nil {
		return &VideoGenerationResult{
			JobID:     jobID,
			VideoPath: "",
			Status:    "skipped",
		}, nil
	}

	logger.Info("Creating video processing job",
		zap.String("job_id", jobID),
	)

	safeName := security.SanitizeFilename(req.VideoName)
	outputPath := s.outputDir + "/" + safeName + ".mp4"

	genReq := VideoGenerationRequest{
		JobID:         jobID,
		OutputPath:    outputPath,
		ProjectName:   req.ProjectName,
		VideoName:     req.VideoName,
		Language:      req.Language,
		Duration:      req.Duration,
		DriveFolderID: req.DriveFolder,
	}

	result, err := s.videoProc.GenerateVideo(context.Background(), genReq)
	if err != nil {
		return nil, fmt.Errorf("video job creation failed: %w", err)
	}

	return &VideoGenerationResult{
		JobID:     result.JobID,
		VideoPath: result.VideoPath,
		Status:    "created",
	}, nil
}

// mapLanguageToOllama converts language codes to full names
func mapLanguageToOllama(lang string) string {
	switch strings.ToLower(lang) {
	case "it", "ita", "italian":
		return "italian"
	case "en", "eng", "english":
		return "english"
	case "es", "esp", "spanish":
		return "spanish"
	case "fr", "fra", "french":
		return "french"
	case "de", "deu", "german":
		return "german"
	default:
		return "italian"
	}
}
