package pipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"velox/go-master/internal/core/entities"
	"velox/go-master/internal/ml/ollama"
)

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
				Model:     "gemma3:4b",
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
	assert.Equal(t, "gemma3:4b", result.Model)
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
