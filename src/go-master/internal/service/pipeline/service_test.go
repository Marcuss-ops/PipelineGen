package pipeline

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
		Model:       "llama2",
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

// ---------------------------------------------------------------------------
// Tests: validateRequest
// ---------------------------------------------------------------------------

func TestValidateRequest_MissingVideoName(t *testing.T) {
	svc := newTestService()
	req := &VideoCreationRequest{
		VideoName: "",
		Source:    "some source",
		Duration:  30,
	}
	err := svc.validateRequest(req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "video_name is required")
}

func TestValidateRequest_MissingSource(t *testing.T) {
	svc := newTestService()
	req := &VideoCreationRequest{
		VideoName: "test",
		Duration:  30,
	}
	err := svc.validateRequest(req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "one of source, youtube_url, or script_text is required")
}

func TestValidateRequest_SourceTooLong(t *testing.T) {
	svc := newTestService()
	req := &VideoCreationRequest{
		VideoName: "test",
		Source:    string(make([]byte, 50001)),
		Duration:  30,
	}
	err := svc.validateRequest(req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum length")
}

func TestValidateRequest_DurationTooShort(t *testing.T) {
	svc := newTestService()
	req := &VideoCreationRequest{
		VideoName: "test",
		Source:    "source",
		Duration:  5,
	}
	err := svc.validateRequest(req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be at least 10 seconds")
}

func TestValidateRequest_DurationTooLong(t *testing.T) {
	svc := newTestService()
	req := &VideoCreationRequest{
		VideoName: "test",
		Source:    "source",
		Duration:  2000,
	}
	err := svc.validateRequest(req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot exceed 30 minutes")
}

func TestValidateRequest_ValidRequest(t *testing.T) {
	svc := newTestService()
	req := &VideoCreationRequest{
		VideoName:  "test-video",
		Source:     "some source text",
		Duration:   30,
		Language:   "en",
		ScriptText: "provided script",
	}
	err := svc.validateRequest(req)
	assert.NoError(t, err)
}

func TestValidateRequest_ValidWithYouTubeURL(t *testing.T) {
	svc := newTestService()
	req := &VideoCreationRequest{
		VideoName:  "test-video",
		YouTubeURL: "https://youtube.com/watch?v=abc",
		Duration:   30,
	}
	err := svc.validateRequest(req)
	assert.NoError(t, err)
}

func TestValidateRequest_ValidWithScriptTextOnly(t *testing.T) {
	svc := newTestService()
	req := &VideoCreationRequest{
		VideoName:  "test-video",
		ScriptText: "provided script",
		Duration:   30,
	}
	err := svc.validateRequest(req)
	assert.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Tests: applyDefaults
// ---------------------------------------------------------------------------

func TestApplyDefaults_Language(t *testing.T) {
	svc := newTestService()
	req := &VideoCreationRequest{
		VideoName: "test",
		Source:    "source",
		Duration:  30,
		Language:  "",
	}
	svc.applyDefaults(req)
	assert.Equal(t, "it", req.Language)
}

func TestApplyDefaults_Duration(t *testing.T) {
	svc := newTestService()
	req := &VideoCreationRequest{
		VideoName: "test",
		Source:    "source",
		Duration:  0,
	}
	svc.applyDefaults(req)
	assert.Equal(t, 25, req.Duration)
}

func TestApplyDefaults_EntityCount(t *testing.T) {
	svc := newTestService()
	req := &VideoCreationRequest{
		VideoName:   "test",
		Source:      "source",
		Duration:    30,
		EntityCount: 0,
	}
	svc.applyDefaults(req)
	assert.Equal(t, 12, req.EntityCount)
}

func TestApplyDefaults_NegativeEntityCount(t *testing.T) {
	svc := newTestService()
	req := &VideoCreationRequest{
		VideoName:   "test",
		Source:      "source",
		Duration:    30,
		EntityCount: -1,
	}
	svc.applyDefaults(req)
	assert.Equal(t, 12, req.EntityCount)
}

func TestApplyDefaults_DoesNotOverrideExistingValues(t *testing.T) {
	svc := newTestService()
	req := &VideoCreationRequest{
		VideoName:   "test",
		Source:      "source",
		Language:    "en",
		Duration:    60,
		EntityCount: 20,
	}
	svc.applyDefaults(req)
	assert.Equal(t, "en", req.Language)
	assert.Equal(t, 60, req.Duration)
	assert.Equal(t, 20, req.EntityCount)
}

// ---------------------------------------------------------------------------
// Tests: mapLanguageToOllama
// ---------------------------------------------------------------------------

func TestMapLanguageToOllama(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"Italian code", "it", "italian"},
		{"Italian full", "italian", "italian"},
		{"Italian alt", "ita", "italian"},
		{"English code", "en", "english"},
		{"English full", "english", "english"},
		{"English alt", "eng", "english"},
		{"Spanish code", "es", "spanish"},
		{"Spanish full", "spanish", "spanish"},
		{"Spanish alt", "esp", "spanish"},
		{"French code", "fr", "french"},
		{"French full", "french", "french"},
		{"French alt", "fra", "french"},
		{"German code", "de", "german"},
		{"German full", "german", "german"},
		{"German alt", "deu", "german"},
		{"Uppercase", "IT", "italian"},
		{"Mixed case", "En", "english"},
		{"Unknown defaults to italian", "ja", "italian"},
		{"Empty defaults to italian", "", "italian"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapLanguageToOllama(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// ---------------------------------------------------------------------------
// Tests: generateScript
// ---------------------------------------------------------------------------

func TestGenerateScript_UserProvided(t *testing.T) {
	svc := newTestService()
	req := &VideoCreationRequest{
		ScriptText: "User provided script",
	}
	result, err := svc.generateScript(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "User provided script", result.Script)
	assert.False(t, result.Generated)
	assert.Equal(t, "user_provided", result.Model)
}

func TestGenerateScript_YouTubeURLNotImplemented(t *testing.T) {
	svc := newTestService()
	req := &VideoCreationRequest{
		YouTubeURL: "https://youtube.com/watch?v=abc",
	}
	_, err := svc.generateScript(context.Background(), req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not implemented")
}

func TestGenerateScript_NoSourceText(t *testing.T) {
	svc := newTestService()
	req := &VideoCreationRequest{
		VideoName: "test",
	}
	_, err := svc.generateScript(context.Background(), req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no source text provided")
}

func TestGenerateScript_FromSourceText(t *testing.T) {
	mockGen := &MockScriptGenerator{
		GenerateFromTextFn: func(ctx context.Context, req *ollama.TextGenerationRequest) (*ollama.GenerationResult, error) {
			assert.Equal(t, "Test Video", req.Title)
			assert.Equal(t, "italian", req.Language)
			return &ollama.GenerationResult{
				Script:    "Generated script from source",
				WordCount: 50,
				Model:     "llama2",
			}, nil
		},
	}
	svc := newTestService(withScriptGen(mockGen))
	req := &VideoCreationRequest{
		VideoName: "Test Video",
		Source:    "Original source text",
		Language:  "it",
	}
	result, err := svc.generateScript(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "Generated script from source", result.Script)
	assert.True(t, result.Generated)
	assert.Equal(t, 50, result.WordCount)
	assert.Equal(t, "llama2", result.Model)
}

func TestGenerateScript_SourceGenerationError(t *testing.T) {
	expectedErr := errors.New("ollama connection failed")
	mockGen := &MockScriptGenerator{
		GenerateFromTextFn: func(ctx context.Context, req *ollama.TextGenerationRequest) (*ollama.GenerationResult, error) {
			return nil, expectedErr
		},
	}
	svc := newTestService(withScriptGen(mockGen))
	req := &VideoCreationRequest{
		VideoName: "Test Video",
		Source:    "source text",
	}
	_, err := svc.generateScript(context.Background(), req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "script generation failed")
	assert.Contains(t, err.Error(), "ollama connection failed")
}

func TestGenerateScript_UserProvidedWithWordCount(t *testing.T) {
	mockSegmenter := &MockSegmenter{
		CountWordsFn: func(text string) int { return 42 },
	}
	mockEntitySvc := &MockEntityService{
		SegmenterFn: func() entities.Segmenter { return mockSegmenter },
	}
	svc := newTestService(withEntityService(mockEntitySvc))
	req := &VideoCreationRequest{
		ScriptText: "Test script with words",
	}
	result, err := svc.generateScript(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, 42, result.WordCount)
}

// ---------------------------------------------------------------------------
// Tests: extractEntities
// ---------------------------------------------------------------------------

func TestExtractEntities_EmptyScript(t *testing.T) {
	svc := newTestService()
	result, err := svc.extractEntities(context.Background(), "", 10)
	require.NoError(t, err)
	assert.Equal(t, 0, result.TotalSegments)
	assert.Equal(t, 0, result.TotalEntities)
}

func TestExtractEntities_NilEntityService(t *testing.T) {
	svc := newTestService(withEntityService(nil))
	result, err := svc.extractEntities(context.Background(), "some script", 10)
	require.NoError(t, err)
	assert.Equal(t, 0, result.TotalSegments)
	assert.Equal(t, 0, result.TotalEntities)
}

func TestExtractEntities_Success(t *testing.T) {
	expectedAnalysis := &entities.ScriptEntityAnalysis{
		TotalSegments:   3,
		SegmentEntities: []entities.SegmentEntityResult{},
		TotalEntities:   15,
	}
	mockEntitySvc := &MockEntityService{
		AnalyzeScriptFn: func(ctx context.Context, script string, entityCount int, segmentConfig entities.SegmentConfig) (*entities.ScriptEntityAnalysis, error) {
			return expectedAnalysis, nil
		},
	}
	svc := newTestService(withEntityService(mockEntitySvc))
	result, err := svc.extractEntities(context.Background(), "test script", 12)
	require.NoError(t, err)
	assert.Equal(t, 3, result.TotalSegments)
	assert.Equal(t, 15, result.TotalEntities)
}

func TestExtractEntities_Error(t *testing.T) {
	expectedErr := errors.New("analysis failed")
	mockEntitySvc := &MockEntityService{
		AnalyzeScriptFn: func(ctx context.Context, script string, entityCount int, segmentConfig entities.SegmentConfig) (*entities.ScriptEntityAnalysis, error) {
			return nil, expectedErr
		},
	}
	svc := newTestService(withEntityService(mockEntitySvc))
	_, err := svc.extractEntities(context.Background(), "test script", 12)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "entity extraction failed")
}

// ---------------------------------------------------------------------------
// Tests: generateVoiceover
// ---------------------------------------------------------------------------

func TestGenerateVoiceover_EmptyScript(t *testing.T) {
	svc := newTestService()
	results, err := svc.generateVoiceover(context.Background(), "", "en", false)
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestGenerateVoiceover_SkipGDocs(t *testing.T) {
	svc := newTestService()
	results, err := svc.generateVoiceover(context.Background(), "script", "en", true)
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestGenerateVoiceover_NilTTSGenerator(t *testing.T) {
	svc := newTestService(withTTSGenerator(nil))
	results, err := svc.generateVoiceover(context.Background(), "script", "en", false)
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestGenerateVoiceover_Success(t *testing.T) {
	expectedResult := &TTSResult{
		FilePath:  "/tmp/velox/audio/output.mp3",
		Duration:  45.5,
		Language:  "en",
		VoiceUsed: "en-US-GuyNeural",
	}
	mockTTS := &MockTTSGenerator{
		GenerateFn: func(ctx context.Context, text string, language string) (*TTSResult, error) {
			return expectedResult, nil
		},
	}
	svc := newTestService(withTTSGenerator(mockTTS))
	results, err := svc.generateVoiceover(context.Background(), "Test script", "en", false)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "/tmp/velox/audio/output.mp3", results[0].AudioFile)
	assert.Equal(t, 45.5, results[0].Duration)
	assert.Equal(t, "en", results[0].Language)
	assert.Equal(t, "en-US-GuyNeural", results[0].Voice)
}

func TestGenerateVoiceover_Error(t *testing.T) {
	expectedErr := errors.New("TTS service unavailable")
	mockTTS := &MockTTSGenerator{
		GenerateFn: func(ctx context.Context, text string, language string) (*TTSResult, error) {
			return nil, expectedErr
		},
	}
	svc := newTestService(withTTSGenerator(mockTTS))
	_, err := svc.generateVoiceover(context.Background(), "script", "en", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "voiceover generation failed")
}

// ---------------------------------------------------------------------------
// Tests: createVideoJob
// ---------------------------------------------------------------------------

func TestCreateVideoJob_EmptyScript(t *testing.T) {
	svc := newTestService()
	result, err := svc.createVideoJob("job_1", &VideoCreationRequest{VideoName: "test"}, "")
	require.NoError(t, err)
	assert.Equal(t, "skipped", result.Status)
	assert.Empty(t, result.VideoPath)
}

func TestCreateVideoJob_NilVideoProcessor(t *testing.T) {
	svc := newTestService(withVideoProcessor(nil))
	result, err := svc.createVideoJob("job_1", &VideoCreationRequest{VideoName: "test"}, "script")
	require.NoError(t, err)
	assert.Equal(t, "skipped", result.Status)
}

func TestCreateVideoJob_Success(t *testing.T) {
	mockVideo := &MockVideoProcessor{
		GenerateVideoFn: func(ctx context.Context, req VideoGenerationRequest) (*VideoGenerationResult, error) {
			assert.Equal(t, "job_1", req.JobID)
			return &VideoGenerationResult{
				JobID:     req.JobID,
				VideoPath: "/tmp/velox/output/test.mp4",
				Status:    "created",
			}, nil
		},
	}
	svc := newTestService(withVideoProcessor(mockVideo))
	req := &VideoCreationRequest{
		VideoName:   "test",
		ProjectName: "myproject",
		Language:    "en",
		Duration:    30,
		DriveFolder: "folder-123",
	}
	result, err := svc.createVideoJob("job_1", req, "script")
	require.NoError(t, err)
	assert.Equal(t, "created", result.Status)
	assert.Equal(t, "job_1", result.JobID)
	assert.Contains(t, result.VideoPath, "test.mp4")
}

func TestCreateVideoJob_Error(t *testing.T) {
	expectedErr := errors.New("video processing failed")
	mockVideo := &MockVideoProcessor{
		GenerateVideoFn: func(ctx context.Context, req VideoGenerationRequest) (*VideoGenerationResult, error) {
			return nil, expectedErr
		},
	}
	svc := newTestService(withVideoProcessor(mockVideo))
	req := &VideoCreationRequest{VideoName: "test"}
	_, err := svc.createVideoJob("job_1", req, "script")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "video job creation failed")
}

// ---------------------------------------------------------------------------
// Tests: CreateMaster (full pipeline)
// ---------------------------------------------------------------------------

func TestCreateMaster_InvalidRequest(t *testing.T) {
	svc := newTestService()
	req := &VideoCreationRequest{
		VideoName: "",
		Duration:  30,
	}
	_, err := svc.CreateMaster(context.Background(), req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid request")
}

func TestCreateMaster_FullPipelineSuccess(t *testing.T) {
	svc := newTestService()
	req := &VideoCreationRequest{
		VideoName:   "test-video",
		Source:      "source text",
		Language:    "en",
		Duration:    30,
		EntityCount: 5,
	}
	result, err := svc.CreateMaster(context.Background(), req)
	require.NoError(t, err)
	assert.NotEmpty(t, result.JobID)
	assert.Equal(t, "test-video", result.VideoName)
	assert.Equal(t, "processing", result.Status)
	assert.True(t, result.ScriptGenerated)
	assert.True(t, result.VideoCreated)
	assert.Len(t, result.VoiceoverResults, 1)
}

func TestCreateMaster_ScriptGenerationFailure(t *testing.T) {
	mockGen := &MockScriptGenerator{
		GenerateFromTextFn: func(ctx context.Context, req *ollama.TextGenerationRequest) (*ollama.GenerationResult, error) {
			return nil, errors.New("generation failed")
		},
	}
	svc := newTestService(withScriptGen(mockGen))
	req := &VideoCreationRequest{
		VideoName: "test",
		Source:    "source",
		Duration:  30,
	}
	_, err := svc.CreateMaster(context.Background(), req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "script generation failed")
}

func TestCreateMaster_EntityExtractionFailureIsNonFatal(t *testing.T) {
	mockEntitySvc := &MockEntityService{
		AnalyzeScriptFn: func(ctx context.Context, script string, entityCount int, segmentConfig entities.SegmentConfig) (*entities.ScriptEntityAnalysis, error) {
			return nil, errors.New("extraction failed")
		},
	}
	svc := newTestService(withEntityService(mockEntitySvc))
	req := &VideoCreationRequest{
		VideoName:   "test",
		ScriptText:  "provided script",
		Language:    "en",
		Duration:    30,
		SkipGDocs:   true,
		EntityCount: 5,
	}
	result, err := svc.CreateMaster(context.Background(), req)
	require.NoError(t, err)
	// Pipeline continues even if entity extraction fails
	assert.Equal(t, 0, result.EntityAnalysis.TotalEntities)
}

func TestCreateMaster_VoiceoverFailureStopsPipeline(t *testing.T) {
	expectedErr := errors.New("TTS failure")
	mockTTS := &MockTTSGenerator{
		GenerateFn: func(ctx context.Context, text string, language string) (*TTSResult, error) {
			return nil, expectedErr
		},
	}
	svc := newTestService(withTTSGenerator(mockTTS))
	req := &VideoCreationRequest{
		VideoName:   "test",
		ScriptText:  "script",
		Language:    "en",
		Duration:    30,
		EntityCount: 5,
	}
	_, err := svc.CreateMaster(context.Background(), req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "voiceover generation failed")
}

func TestCreateMaster_VideoJobFailureIsNonFatal(t *testing.T) {
	mockVideo := &MockVideoProcessor{
		GenerateVideoFn: func(ctx context.Context, req VideoGenerationRequest) (*VideoGenerationResult, error) {
			return nil, errors.New("video failed")
		},
	}
	svc := newTestService(withVideoProcessor(mockVideo))
	req := &VideoCreationRequest{
		VideoName:   "test",
		ScriptText:  "script",
		Language:    "en",
		Duration:    30,
		SkipGDocs:   true,
		EntityCount: 5,
	}
	result, err := svc.CreateMaster(context.Background(), req)
	require.NoError(t, err)
	// Pipeline continues, video_created should be false
	assert.False(t, result.VideoCreated)
	assert.Equal(t, "processing", result.Status)
}

func TestCreateMaster_WithProvidedScriptSkipsGeneration(t *testing.T) {
	scriptGenCalled := false
	mockGen := &MockScriptGenerator{
		GenerateFromTextFn: func(ctx context.Context, req *ollama.TextGenerationRequest) (*ollama.GenerationResult, error) {
			scriptGenCalled = true
			return nil, errors.New("should not be called")
		},
	}
	svc := newTestService(
		withScriptGen(mockGen),
		withTTSGenerator(nil),
		withVideoProcessor(nil),
	)
	req := &VideoCreationRequest{
		VideoName:   "test",
		ScriptText:  "pre-written script",
		Duration:    30,
		SkipGDocs:   true,
		EntityCount: 5,
	}
	result, err := svc.CreateMaster(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, scriptGenCalled, "Script generator should not be called when script is provided")
	assert.False(t, result.ScriptGenerated, "ScriptGenerated should be false for user-provided scripts")
	assert.Equal(t, "pre-written script", result.ScriptText)
}

func TestCreateMaster_JobIDFormat(t *testing.T) {
	svc := newTestService()
	req := &VideoCreationRequest{
		VideoName:  "test",
		Source:     "source",
		Duration:   30,
		SkipGDocs:  true,
	}
	result, err := svc.CreateMaster(context.Background(), req)
	require.NoError(t, err)
	// Job ID should start with "job_"
	assert.True(t, len(result.JobID) > 4, "JobID should have a timestamp suffix")
	assert.Equal(t, "job_", result.JobID[:4])
}

func TestCreateMaster_AppliesDefaults(t *testing.T) {
	svc := newTestService()
	req := &VideoCreationRequest{
		VideoName:  "test",
		Source:     "source",
		Duration:   10, // minimum valid; defaults tested separately below
		SkipGDocs:  true,
	}
	result, err := svc.CreateMaster(context.Background(), req)
	require.NoError(t, err)
	assert.NotEmpty(t, result.JobID)
	assert.Equal(t, "processing", result.Status)
}

func TestApplyDefaults_DurationZeroGetsDefault(t *testing.T) {
	svc := newTestService()
	req := &VideoCreationRequest{
		VideoName: "test",
		Source:    "source",
		Duration:  0,
	}
	svc.applyDefaults(req)
	assert.Equal(t, 25, req.Duration, "Duration 0 should default to 25")
}

func TestCreateMaster_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	mockGen := &MockScriptGenerator{
		GenerateFromTextFn: func(ctx context.Context, req *ollama.TextGenerationRequest) (*ollama.GenerationResult, error) {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("context canceled: %w", ctx.Err())
			}
			return &ollama.GenerationResult{Script: "script", WordCount: 10, Model: "llama2"}, nil
		},
	}
	svc := newTestService(withScriptGen(mockGen))
	req := &VideoCreationRequest{
		VideoName: "test",
		Source:    "source",
		Duration:  30,
	}
	_, err := svc.CreateMaster(ctx, req)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// Tests: NewVideoCreationService
// ---------------------------------------------------------------------------

func TestNewVideoCreationService(t *testing.T) {
	mockGen := &MockScriptGenerator{}
	mockEntity := &MockEntityService{}
	mockTTS := &MockTTSGenerator{}
	mockVideo := &MockVideoProcessor{}

	svc := NewVideoCreationService(mockGen, mockEntity, mockTTS, mockVideo)

	require.NotNil(t, svc)
	assert.Equal(t, mockGen, svc.scriptGen)
	assert.Equal(t, mockEntity, svc.entityService)
	assert.Equal(t, mockTTS, svc.ttsGenerator)
	assert.Equal(t, mockVideo, svc.videoProc)
}
