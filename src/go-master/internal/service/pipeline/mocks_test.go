package pipeline

import (
	"context"

	"velox/go-master/internal/core/entities"
	"velox/go-master/internal/ml/ollama"
)

// ---------------------------------------------------------------------------
// Mock implementations
// ---------------------------------------------------------------------------

// MockScriptGenerator implements ScriptGenerator
type MockScriptGenerator struct {
	GenerateFromTextFn func(ctx context.Context, req *ollama.TextGenerationRequest) (*ollama.GenerationResult, error)
}

func (m *MockScriptGenerator) GenerateFromText(ctx context.Context, req *ollama.TextGenerationRequest) (*ollama.GenerationResult, error) {
	if m.GenerateFromTextFn != nil {
		return m.GenerateFromTextFn(ctx, req)
	}
	return &ollama.GenerationResult{
		Script:      "Mock generated script",
		WordCount:   100,
		EstDuration: 30,
		Model:       "gemma3:4b",
	}, nil
}

// MockEntityService implements EntityService
type MockEntityService struct {
	AnalyzeScriptFn func(ctx context.Context, script string, entityCount int, segmentConfig entities.SegmentConfig) (*entities.ScriptEntityAnalysis, error)
	SegmenterFn     func() entities.Segmenter
}

func (m *MockEntityService) AnalyzeScript(ctx context.Context, script string, entityCount int, segmentConfig entities.SegmentConfig) (*entities.ScriptEntityAnalysis, error) {
	if m.AnalyzeScriptFn != nil {
		return m.AnalyzeScriptFn(ctx, script, entityCount, segmentConfig)
	}
	return &entities.ScriptEntityAnalysis{
		TotalSegments:   2,
		SegmentEntities: []entities.SegmentEntityResult{},
		TotalEntities:   5,
	}, nil
}

func (m *MockEntityService) Segmenter() entities.Segmenter {
	if m.SegmenterFn != nil {
		return m.SegmenterFn()
	}
	return &MockSegmenter{}
}

// MockSegmenter implements entities.Segmenter
type MockSegmenter struct {
	CountWordsFn func(text string) int
}

func (m *MockSegmenter) Split(text string, config entities.SegmentConfig) []string {
	return []string{text}
}

func (m *MockSegmenter) CountWords(text string) int {
	if m.CountWordsFn != nil {
		return m.CountWordsFn(text)
	}
	return len(text) / 5 // rough estimate
}

func (m *MockSegmenter) EstimateSegments(text string, wordsPerSegment int) int {
	return 1
}

// MockTTSGenerator implements TTSGenerator
type MockTTSGenerator struct {
	GenerateFn func(ctx context.Context, text string, language string) (*TTSResult, error)
}

func (m *MockTTSGenerator) Generate(ctx context.Context, text string, language string) (*TTSResult, error) {
	if m.GenerateFn != nil {
		return m.GenerateFn(ctx, text, language)
	}
	return &TTSResult{
		FilePath:  "/tmp/velox/audio/test.mp3",
		Duration:  30.0,
		Language:  language,
		VoiceUsed: "default-voice",
	}, nil
}

// MockVideoProcessor implements VideoProcessor
type MockVideoProcessor struct {
	GenerateVideoFn func(ctx context.Context, req VideoGenerationRequest) (*VideoGenerationResult, error)
}

func (m *MockVideoProcessor) GenerateVideo(ctx context.Context, req VideoGenerationRequest) (*VideoGenerationResult, error) {
	if m.GenerateVideoFn != nil {
		return m.GenerateVideoFn(ctx, req)
	}
	return &VideoGenerationResult{
		JobID:     req.JobID,
		VideoPath: req.OutputPath,
		Status:    "created",
	}, nil
}

// ---------------------------------------------------------------------------
// Helper: newTestService
// ---------------------------------------------------------------------------

func newTestService(opts ...func(*VideoCreationService)) *VideoCreationService {
	svc := &VideoCreationService{
		scriptGen:     &MockScriptGenerator{},
		entityService: &MockEntityService{},
		ttsGenerator:  &MockTTSGenerator{},
		videoProc:     &MockVideoProcessor{},
	}
	for _, opt := range opts {
		opt(svc)
	}
	return svc
}

func withScriptGen(g ScriptGenerator) func(*VideoCreationService) {
	return func(s *VideoCreationService) { s.scriptGen = g }
}

func withEntityService(e EntityService) func(*VideoCreationService) {
	return func(s *VideoCreationService) { s.entityService = e }
}

func withTTSGenerator(t TTSGenerator) func(*VideoCreationService) {
	return func(s *VideoCreationService) { s.ttsGenerator = t }
}

func withVideoProcessor(v VideoProcessor) func(*VideoCreationService) {
	return func(s *VideoCreationService) { s.videoProc = v }
}
