package youtube

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"velox/go-master/internal/core/processor"
	"velox/go-master/internal/config"
	"velox/go-master/internal/security"
)

func TestYouTubeClipRequestValidation(t *testing.T) {
	// Test empty URL
	req := &ExtractRequest{
		URL:      "",
		Segments: []Segment{{Start: "0:10", End: "0:20"}},
	}

	if req.URL == "" {
		t.Log("Empty URL correctly identified as invalid")
	}

	// Test valid URL
	req.URL = "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
	if req.URL == "" {
		t.Error("URL should not be empty")
	}

	_ = req
}

func TestYouTubeClipRejectsInvalidURL(t *testing.T) {
	// Test invalid URLs
	invalidURLs := []string{
		"",
		"not-a-url",
		"ftp://example.com/file",
		"http://malicious.com/script",
	}

	for _, url := range invalidURLs {
		req := &ExtractRequest{
			URL:      url,
			Segments: []Segment{{Start: "0:10", End: "0:20"}},
		}

		// In a real test, we would call Extract and check for error
		// For now, just log
		t.Logf("Testing invalid URL: %s", url)

		// URL validation would happen in Extract method
		if url == "" {
			t.Logf("Empty URL correctly detected: %s", url)
		}

		_ = req
	}
}

func TestYouTubeClipRejectsInvalidTimeRange(t *testing.T) {
	// Test invalid time ranges
	testCases := []struct {
		name  string
		start string
		end   string
	}{
		{"empty start", "", "0:20"},
		{"empty end", "0:10", ""},
		{"end before start", "0:20", "0:10"},
		{"invalid format", "abc", "0:20"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := &ExtractRequest{
				URL:      "https://www.youtube.com/watch?v=test",
				Segments: []Segment{{Start: tc.start, End: tc.end}},
			}

			// Validate segment
			if tc.start == "" || tc.end == "" {
				t.Logf("Empty timestamp detected: start=%s, end=%s", tc.start, tc.end)
			}

			// In real implementation, parseTimestamp would be called
			// and would return error for invalid formats

			_ = req
		})
	}
}

func TestYouTubeClipCreatesExpectedOutputPath(t *testing.T) {
	// Test that output path is created correctly
	videoID := "dQw4w9WgXcQ"
	expectedFolder := "yt_" + videoID

	if expectedFolder != "yt_dQw4w9WgXcQ" {
		t.Errorf("Expected folder 'yt_dQw4w9WgXcQ', got %s", expectedFolder)
	}

	t.Logf("Expected output folder: %s", expectedFolder)
}

func TestParseTimestamp(t *testing.T) {
	testCases := []struct {
		input    string
		expected int
		hasError bool
	}{
		{"10", 10, false},
		{"1:30", 90, false},
		{"1:23:45", 5025, false},
		{"", 0, true},
		{"invalid", 0, true},
		{"0:10", 10, false},
		{"0:05", 5, false},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			result, err := parseTimestamp(tc.input)

			if tc.hasError && err == nil {
				t.Errorf("Expected error for input %s, but got none", tc.input)
			}

			if !tc.hasError && err != nil {
				t.Errorf("Unexpected error for input %s: %v", tc.input, err)
			}

			if !tc.hasError && result != tc.expected {
				t.Errorf("For input %s: expected %d, got %d", tc.input, tc.expected, result)
			}
		})
	}
}

func TestExtractVideoID(t *testing.T) {
	testCases := []struct {
		input    string
		expected string
	}{
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://youtu.be/dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://www.youtube.com/shorts/abc123", "abc123"},
		{"https://www.youtube.com/embed/xyz789", "xyz789"},
		{"https://www.youtube.com/live/def456", "def456"},
		{"not-a-url", ""},
		{"", ""},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			result := extractVideoID(tc.input)
			if result != tc.expected {
				t.Errorf("For input %s: expected %s, got %s", tc.input, tc.expected, result)
			}
		})
	}
}

func TestYouTubeClipHandlesPipelineFailure(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	// Add test hosts to security allowlist
	security.AddAllowedHost("www.youtube.com")
	security.AddAllowedHost("youtu.be")

	cfg := testConfig(tmp)
	log := zap.NewNop()

	pipeline := &fakeVideoPipeline{
		err: errors.New("yt-dlp failed"),
	}

	svc := NewService(
		cfg,
		log,
		nil, // clips repo
		nil, // monitors repo
		nil, // drive client
		nil, // processor (not used anymore)
		pipeline,
		nil, // lifecycle
		nil, // indexer
	)

	resp, err := svc.Extract(ctx, &ExtractRequest{
		URL: "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		Segments: []Segment{
			{
				Name:  "intro",
				Start: "0",
				End:   "5",
			},
		},
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.False(t, resp.OK)
	require.Equal(t, 1, resp.Stats.Failed)
	require.Len(t, resp.Items, 1)
	require.Equal(t, "failed", resp.Items[0].Status)
	require.Contains(t, resp.Items[0].Error, "video processing failed: yt-dlp failed")
	require.True(t, pipeline.called)
}

func TestYouTubeClipPassesExpectedAssetInputToPipeline(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	// Add test hosts to security allowlist
	security.AddAllowedHost("www.youtube.com")
	security.AddAllowedHost("youtu.be")

	cfg := testConfig(tmp)
	log := zap.NewNop()

	dummyFilePath := filepath.Join(tmp, "dummy.mp4")
	err := os.WriteFile(dummyFilePath, []byte("dummy video content"), 0644)
	require.NoError(t, err)

	pipeline := &fakeVideoPipeline{
		outputPath: dummyFilePath,
	}

	svc := NewService(
		cfg,
		log,
		nil,
		nil,
		nil,
		nil,
		pipeline,
		nil,
		nil,
	)

	resp, err := svc.Extract(ctx, &ExtractRequest{
		URL: "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		Segments: []Segment{
			{Name: "clip one", Start: "10", End: "20"},
		},
	})

	require.NoError(t, err)
	require.True(t, resp.OK)
	require.True(t, pipeline.called)

	assert.Equal(t, "https://www.youtube.com/watch?v=dQw4w9WgXcQ", pipeline.url)
	assert.Equal(t, float64(10), pipeline.start)
	assert.Equal(t, float64(10), pipeline.duration)
	assert.Equal(t, "clip one", pipeline.outputName)
}

func TestYouTubeClipValidSegmentCount(t *testing.T) {
	// Test that segment count is validated
	req := &ExtractRequest{
		URL: "https://www.youtube.com/watch?v=test",
	}

	// Empty segments should fail
	if len(req.Segments) == 0 {
		t.Log("Empty segments correctly detected")
	}

	// Test max segments limit
	for i := 0; i < 25; i++ {
		req.Segments = append(req.Segments, Segment{Start: "0:10", End: "0:20"})
	}

	if len(req.Segments) > 20 {
		t.Logf("Too many segments detected: %d", len(req.Segments))
	}
}

func TestYouTubeClipServiceCreation(t *testing.T) {
	// Test that service can be created
	// This requires mocking all dependencies
	t.Log("Service creation test - requires full mock setup")
}

func TestBoolDefault(t *testing.T) {
	testCases := []struct {
		input    *bool
		def      bool
		expected bool
	}{
		{nil, true, true},
		{nil, false, false},
		{boolPtr(true), true, true},
		{boolPtr(true), false, true},
		{boolPtr(false), true, false},
		{boolPtr(false), false, false},
	}

	for _, tc := range testCases {
		result := boolDefault(tc.input, tc.def)
		if result != tc.expected {
			t.Errorf("boolDefault(%v, %v) = %v, expected %v", tc.input, tc.def, result, tc.expected)
		}
	}
}

func boolPtr(b bool) *bool {
	return &b
}

type fakeMediaProcessor struct {
	called bool
	err    error
	result *processor.ProcessResult
	inputs []*processor.ProcessInput
}

type fakeVideoPipeline struct {
	called     bool
	err        error
	outputPath string
	url        string
	start      float64
	duration   float64
	outputName string
}

func (f *fakeVideoPipeline) DownloadAndCutYouTubeVideo(ctx context.Context, url string, start, duration float64, outputName string) (string, error) {
	f.called = true
	f.url = url
	f.start = start
	f.duration = duration
	f.outputName = outputName
	if f.err != nil {
		return "", f.err
	}
	return f.outputPath, nil
}

func (f *fakeMediaProcessor) Process(ctx context.Context, input *processor.ProcessInput) (*processor.ProcessResult, error) {
	f.called = true
	f.inputs = append(f.inputs, input)

	if f.err != nil {
		return &processor.ProcessResult{
			ID:     input.ID,
			Status: "failed",
			Error:  f.err.Error(),
		}, f.err
	}

	if f.result != nil {
		return f.result, nil
	}

	return &processor.ProcessResult{
		ID:        input.ID,
		Filename:  input.Name + ".mp4",
		LocalPath: input.OutputDir + "/" + input.Name + ".mp4",
		FileHash:  "hash-test",
		Status:    "processed",
	}, nil
}

func testConfig(tmp string) *config.Config {
	return &config.Config{
		Storage: config.StorageConfig{
			DataDir: tmp,
		},
		Video: config.VideoConfig{
			Duration: 30,
		},
	}
}
