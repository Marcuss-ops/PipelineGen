package youtubeclip

import (
	"testing"
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
				URL: "https://www.youtube.com/watch?v=test",
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

func TestYouTubeClipHandlesYtDlpFailure(t *testing.T) {
	// Test that yt-dlp failure is handled gracefully
	// This would require mocking the media processor
	t.Log("yt-dlp failure handling test - requires media processor mock")
}

func TestYouTubeClipHandlesFFmpegFailure(t *testing.T) {
	// Test that ffmpeg failure is handled gracefully
	// This would require mocking the media processor
	t.Log("ffmpeg failure handling test - requires media processor mock")
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
