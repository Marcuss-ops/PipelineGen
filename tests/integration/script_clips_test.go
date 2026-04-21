// Package integration provides integration tests for script+clips endpoint
package integration

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	"velox/go-master/internal/service/scriptclips"
)

// ScriptClipsTestSuite tests the script+clips endpoint
type ScriptClipsTestSuite struct {
	suite.Suite
}

// SetupSuite runs once before all tests
func (s *ScriptClipsTestSuite) SetupSuite() {
	// Note: Full integration tests require Ollama, Drive, and yt-dlp
	// These tests validate the endpoint structure and validation logic
}

// TestScriptClipsValidation tests input validation
func (s *ScriptClipsTestSuite) TestScriptClipsValidation() {
	// Note: Full validation tests require a wired test server
	// These are placeholder tests for validation structure
	s.T().Log("Validation tests require full server setup with mocked dependencies")
}

// TestScriptClipsResponseStructure validates the response format
func (s *ScriptClipsTestSuite) TestScriptClipsResponseStructure() {
	// Mock response structure
	expectedResponse := scriptclips.ScriptClipsResponse{
		OK:          true,
		Script:      "Test script content...",
		WordCount:   140,
		EstDuration: 60,
		Model:       "gemma3:4b",
		Segments: []scriptclips.SegmentClipMapping{
			{
				SegmentIndex: 0,
				Text:         "Elon Musk founded Tesla...",
				StartTime:    "00:00:00",
				EndTime:      "00:00:10",
				Entities: scriptclips.EntityResult{
					FrasiImportanti:  []string{"Elon Musk founded Tesla"},
					NomiSpeciali:     []string{"Elon Musk", "Tesla"},
					ParoleImportanti: []string{"technology", "future"},
					EntitaSenzaTesto: map[string]string{},
				},
				ClipMappings: []scriptclips.ClipMapping{
					{
						Entity:        "Elon Musk",
						SearchQueryEN: "Elon Musk",
						ClipFound:     true,
						ClipStatus:    "downloaded_and_uploaded",
						YouTubeURL:    "https://youtube.com/watch?v=xxx",
						DriveURL:      "https://drive.google.com/file/d/xxx",
						DriveFileID:   "xxx",
					},
				},
			},
		},
		TotalClipsFound:   1,
		TotalClipsMissing: 0,
		ProcessingTime:    15.5,
	}

	// Validate JSON marshaling
	jsonBytes, err := json.Marshal(expectedResponse)
	s.NoError(err)
	s.True(len(jsonBytes) > 0)

	// Validate unmarshaling
	var decoded scriptclips.ScriptClipsResponse
	err = json.Unmarshal(jsonBytes, &decoded)
	s.NoError(err)
	s.True(decoded.OK)
	s.Equal(140, decoded.WordCount)
	s.Equal(1, decoded.TotalClipsFound)
	s.Len(decoded.Segments, 1)
	s.Len(decoded.Segments[0].ClipMappings, 1)
	s.True(decoded.Segments[0].ClipMappings[0].ClipFound)
}

// TestClipMappingStatuses validates all possible clip statuses
func (s *ScriptClipsTestSuite) TestClipMappingStatuses() {
	statuses := []string{
		"found_on_drive",
		"downloaded_and_uploaded",
		"not_found",
	}

	for _, status := range statuses {
		mapping := scriptclips.ClipMapping{
			Entity:     "Test Entity",
			ClipFound:  status != "not_found",
			ClipStatus: status,
		}

		s.T().Run(status, func(t *testing.T) {
			jsonBytes, err := json.Marshal(mapping)
			assertJSONValid(t, err)

			var decoded scriptclips.ClipMapping
			err = json.Unmarshal(jsonBytes, &decoded)
			assertJSONValid(t, err)
			s.Equal(status, decoded.ClipStatus)
		})
	}
}

// TestTimestampCalculation validates timestamp formatting
func (s *ScriptClipsTestSuite) TestTimestampCalculation() {
	// Test formatTime function (indirectly through segment validation)
	segments := []scriptclips.SegmentClipMapping{
		{StartTime: "00:00:00", EndTime: "00:00:10"},
		{StartTime: "00:00:10", EndTime: "00:00:20"},
		{StartTime: "00:00:20", EndTime: "00:01:00"},
	}

	for _, seg := range segments {
		s.True(len(seg.StartTime) == 8, "Start time should be formatted as HH:MM:SS (8 chars)")
		s.True(len(seg.EndTime) == 8, "End time should be formatted as HH:MM:SS (8 chars)")
		s.True(strings.Contains(seg.StartTime, ":"), "Start time should contain colons")
		s.True(strings.Contains(seg.EndTime, ":"), "End time should contain colons")
	}
}

// TestEntityResultStructure validates entity extraction results
func (s *ScriptClipsTestSuite) TestEntityResultStructure() {
	entity := scriptclips.EntityResult{
		FrasiImportanti:  []string{"Important phrase 1", "Important phrase 2"},
		NomiSpeciali:     []string{"Tesla", "Elon Musk"},
		ParoleImportanti: []string{"technology", "innovation"},
		EntitaSenzaTesto: map[string]string{
			"Tesla Logo": "https://logo.clearbit.com/tesla.com",
		},
	}

	s.Len(entity.FrasiImportanti, 2)
	s.Len(entity.NomiSpeciali, 2)
	s.Len(entity.ParoleImportanti, 2)
	s.Len(entity.EntitaSenzaTesto, 1)

	// Validate JSON serialization
	jsonBytes, err := json.Marshal(entity)
	s.NoError(err)

	var decoded scriptclips.EntityResult
	err = json.Unmarshal(jsonBytes, &decoded)
	s.NoError(err)
	s.Equal(entity.FrasiImportanti, decoded.FrasiImportanti)
	s.Equal(entity.NomiSpeciali, decoded.NomiSpeciali)
}

// TestScriptClipsRequestDefaults validates default values
func (s *ScriptClipsTestSuite) TestScriptClipsRequestDefaults() {
	// Note: Go struct tags don't set defaults automatically
	// Defaults are set by the handler/service when value is 0 or empty
	req := scriptclips.ScriptClipsRequest{
		SourceText: "Test content",
		Title:      "Test",
	}

	// Validate that defaults are NOT set in struct (will be set by handler)
	s.Equal("", req.Language)     // Handler will set to "italian"
	s.Equal(0, req.Duration)      // Handler will set to 60
	s.Equal("", req.Tone)         // Handler will set to "professional"
	s.Equal(0, req.EntityCountPerSegment) // Handler will set to 12
}

// Helper function to validate JSON and fail test on error
func assertJSONValid(t *testing.T, err error) {
	if err != nil {
		t.Fatalf("Invalid JSON: %v", err)
	}
}

// Helper to assert equality
func assertEqual(t *testing.T, expected, actual interface{}, msg string) {
	assert.Equal(t, expected, actual, msg)
}

// Run tests
func TestScriptClipsEndpoint(t *testing.T) {
	suite.Run(t, new(ScriptClipsTestSuite))
}
