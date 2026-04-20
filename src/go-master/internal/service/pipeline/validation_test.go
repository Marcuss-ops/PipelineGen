package pipeline

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
		Source:    string(make([]byte, 100001)),
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
		Duration:  4000,
	}
	err := svc.validateRequest(req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot exceed")
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
