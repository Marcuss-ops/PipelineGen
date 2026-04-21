package scriptclips

import (
	"context"
	"fmt"
	"os"
	"time"

	"velox/go-master/internal/audio/tts"
	"velox/go-master/internal/clipcache"
	"velox/go-master/internal/core/entities"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/stock"
	"velox/go-master/internal/translation"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// ScriptClipsService orchestrates the full pipeline:
// 1. Generate script from text
// 2. Extract entities from each segment
// 3. Find or download stock clips for each entity
// 4. Upload clips to Google Drive
// 5. Return complete mapping: script → segments → entities → clips
type ScriptClipsService struct {
	scriptGen          *ollama.Generator
	entityService      *entities.EntityService
	stockManager       *stock.StockManager
	driveClient        *drive.Client
	clipTranslator     *translation.ClipSearchTranslator
	clipCache          *clipcache.ClipCache
	downloadDir        string
	driveFolderID      string
	topic              string
	stockFallbackCount int
	ollamaClient       *ollama.Client
	validateLinks      bool
	edgeTTS            *tts.EdgeTTS
}

// NewScriptClipsService creates a new service
func NewScriptClipsService(
	scriptGen *ollama.Generator,
	entityService *entities.EntityService,
	stockManager *stock.StockManager,
	driveClient *drive.Client,
	downloadDir string,
	driveFolderID string,
	topic string,
	stockFallbackCount int,
	ollamaClient *ollama.Client,
	validateLinks bool,
	edgeTTS *tts.EdgeTTS,
) *ScriptClipsService {
	if downloadDir == "" {
		downloadDir = "/tmp/velox/downloads"
	}
	os.MkdirAll(downloadDir, 0755)

	if stockFallbackCount <= 0 {
		stockFallbackCount = 20
	}

	return &ScriptClipsService{
		scriptGen:          scriptGen,
		entityService:      entityService,
		stockManager:       stockManager,
		driveClient:        driveClient,
		clipTranslator:     translation.NewClipSearchTranslator(),
		downloadDir:        downloadDir,
		driveFolderID:      driveFolderID,
		topic:              topic,
		stockFallbackCount: stockFallbackCount,
		ollamaClient:       ollamaClient,
		validateLinks:      validateLinks,
		edgeTTS:            edgeTTS,
	}
}

// SetClipCache sets the clip cache for the service
func (s *ScriptClipsService) SetClipCache(cache *clipcache.ClipCache) {
	s.clipCache = cache
}

// GenerateScriptWithClips executes the full end-to-end pipeline
func (s *ScriptClipsService) GenerateScriptWithClips(ctx context.Context, req *ScriptClipsRequest) (*ScriptClipsResponse, error) {
	startTime := time.Now()
	logger.Info("Starting script generation with clips",
		zap.String("title", req.Title),
		zap.Int("duration", req.Duration),
		zap.String("language", req.Language),
	)

	// Helper to call progress callback safely
	reportProgress := func(step string, progress int, message string, entityName string, clipsDone int, clipsTotal int) {
		if req.ProgressCallback != nil {
			req.ProgressCallback(step, progress, message, entityName, clipsDone, clipsTotal)
		}
	}

	// Step 1: Generate script (0-15% progress)
	reportProgress("script_generation", 5, "Generating script via Ollama...", "", 0, 0)

	scriptResult, err := s.scriptGen.GenerateFromText(ctx, &ollama.TextGenerationRequest{
		SourceText: req.SourceText,
		Title:      req.Title,
		Language:   req.Language,
		Duration:   req.Duration,
		Tone:       req.Tone,
		Model:      req.Model,
	})
	if err != nil {
		return nil, fmt.Errorf("script generation failed: %w", err)
	}

	reportProgress("script_generation", 15, fmt.Sprintf("Script generated (%d words)", scriptResult.WordCount), "", 0, 0)
	logger.Info("Script generated, starting voiceover", zap.Int("word_count", scriptResult.WordCount))

	// Step 1b: Generate voiceover from the full script (optional, non-blocking)
	var voiceoverResult *tts.GenerationResult
	if s.edgeTTS != nil {
		reportProgress("voiceover_generation", 20, "Generating voiceover audio...", "", 0, 0)

		langCode := s.mapLanguageToCode(req.Language)
		voiceoverResult, err = s.edgeTTS.Generate(ctx, scriptResult.Script, langCode)
		if err != nil {
			logger.Warn("Voiceover generation failed (non-fatal, continuing)", zap.Error(err))
			reportProgress("voiceover_generation", 22, fmt.Sprintf("Voiceover failed: %v", err), "", 0, 0)
			voiceoverResult = nil
		} else {
			logger.Info("Voiceover generated",
				zap.String("file", voiceoverResult.FilePath),
				zap.Int("duration", voiceoverResult.Duration),
				zap.String("voice", voiceoverResult.VoiceUsed),
			)
			reportProgress("voiceover_generation", 25,
				fmt.Sprintf("Voiceover generated (%ds, voice: %s)", voiceoverResult.Duration, voiceoverResult.VoiceUsed),
				"", 0, 0)
		}
	} else {
		logger.Info("EdgeTTS not available, skipping voiceover")
	}

	// Step 2: Segment script and extract entities (30-40% progress)
	reportProgress("entity_extraction", 30, "Analyzing script and extracting entities...", "", 0, 0)

	segmentConfig := entities.SegmentConfig{
		TargetWordsPerSegment: 50,
		MinSegments:         1,
		MaxSegments:         20,
		OverlapWords:        5,
	}

	analysis, err := s.entityService.AnalyzeScript(ctx, scriptResult.Script, req.EntityCountPerSegment, segmentConfig)
	if err != nil {
		return nil, fmt.Errorf("entity analysis failed: %w", err)
	}

	reportProgress("entity_extraction", 40, fmt.Sprintf("Entity extraction completed (%d segments, %d entities)", analysis.TotalSegments, analysis.TotalEntities), "", 0, 0)

	// Step 3: Calculate timestamps
	reportProgress("timestamp_calculation", 45, "Calculating segment timestamps...", "", 0, 0)
	segments := s.calculateTimestamps(analysis, scriptResult.EstDuration)

	// Step 4: Find or download clips (45-100% progress)
	totalClipsFound, totalClipsMissing, err := s.processClipEntities(ctx, segments, reportProgress)
	if err != nil {
		return nil, fmt.Errorf("clip processing failed: %w", err)
	}

	processingTime := time.Since(startTime).Seconds()
	logger.Info("Script generation with clips completed",
		zap.Int("segments", len(segments)),
		zap.Int("clips_found", totalClipsFound),
		zap.Int("clips_missing", totalClipsMissing),
		zap.Float64("processing_time", processingTime),
	)

	resp := &ScriptClipsResponse{
		OK:                true,
		Script:            scriptResult.Script,
		WordCount:         scriptResult.WordCount,
		EstDuration:       scriptResult.EstDuration,
		Model:             scriptResult.Model,
		Segments:          segments,
		TotalClipsFound:   totalClipsFound,
		TotalClipsMissing: totalClipsMissing,
		ProcessingTime:    processingTime,
	}

	if voiceoverResult != nil {
		resp.VoiceoverFile = voiceoverResult.FilePath
		resp.VoiceoverDuration = voiceoverResult.Duration
		resp.VoiceoverVoice = voiceoverResult.VoiceUsed
	}

	return resp, nil
}
