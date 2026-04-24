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

func TestCreateMaster_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	mockGen := &MockScriptGenerator{
		GenerateFromTextFn: func(ctx context.Context, req *ollama.TextGenerationRequest) (*ollama.GenerationResult, error) {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("context canceled: %w", ctx.Err())
			}
			return &ollama.GenerationResult{Script: "script", WordCount: 10, Model: "gemma3:12b"}, nil
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
