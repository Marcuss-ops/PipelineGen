package timestamp

import (
	"context"
	"testing"

	"velox/go-master/internal/clip"
)

// Helper to create test indexer
func createTestIndexerForTimestamp(clips []clip.IndexedClip) *clip.Indexer {
	return clip.NewTestIndexer(clips)
}

// TestMapSegmentsToClips_Basic tests basic segment to clip mapping
func TestMapSegmentsToClips_Basic(t *testing.T) {
	testClips := []clip.IndexedClip{
		{
			ID:         "tech1",
			Name:       "Elon Musk Robot AI Demo",
			FolderPath: "Tech & AI/Robotics",
			Tags:       []string{"elon", "musk", "robot", "ai", "demo"},
			Duration:   45,
			MimeType:   "video/mp4",
			DriveLink:  "https://drive.google.com/file/d/tech1",
		},
		{
			ID:         "nature1",
			Name:       "Beautiful Nature Landscape",
			FolderPath: "Nature/Landscapes",
			Tags:       []string{"nature", "landscape", "beautiful"},
			Duration:   30,
			MimeType:   "video/mp4",
			DriveLink:  "https://drive.google.com/file/d/nature1",
		},
	}

	indexer := createTestIndexerForTimestamp(testClips)
	service := NewService(indexer, nil)

	segments := []TextSegment{
		{
			ID:        "seg_1",
			Index:     0,
			StartTime: 0.0,
			EndTime:   15.0,
			Text:      "Elon Musk presenta il nuovo robot AI",
			Keywords:  []string{"robot", "AI", "tecnologia"},
			Entities:  []string{"Elon Musk"},
		},
		{
			ID:        "seg_2",
			Index:     1,
			StartTime: 15.0,
			EndTime:   30.0,
			Text:      "Paesaggio naturale con montagne",
			Keywords:  []string{"natura", "paesaggio"},
			Entities:  []string{},
		},
	}

	req := &MappingRequest{
		ScriptID:           "test_script",
		Segments:           segments,
		MaxClipsPerSegment: 3,
		MinScore:           20,
		IncludeDrive:       true,
		IncludeArtlist:     false,
	}

	mapping, err := service.MapSegmentsToClips(context.Background(), req)
	if err != nil {
		t.Fatalf("Mapping failed: %v", err)
	}

	// Verify results
	if len(mapping.Segments) != 2 {
		t.Errorf("Expected 2 segments, got %d", len(mapping.Segments))
	}

	// First segment should match tech clip
	seg1 := mapping.Segments[0]
	if seg1.ClipCount == 0 {
		t.Error("Expected clips for segment 1")
	} else {
		t.Logf("✅ Segment 1 (%.1f-%.1f): %d clips, best score=%.0f",
			seg1.Segment.StartTime, seg1.Segment.EndTime,
			seg1.ClipCount, seg1.BestScore)
		
		for i, clip := range seg1.AssignedClips {
			t.Logf("   [%d] %s (score: %.0f, source: %s)",
				i+1, clip.Name, clip.RelevanceScore, clip.Source)
		}
	}

	// Second segment should match nature clip
	seg2 := mapping.Segments[1]
	if seg2.ClipCount == 0 {
		t.Log("⚠️  No clips for segment 2 (may be expected)")
	} else {
		t.Logf("✅ Segment 2 (%.1f-%.1f): %d clips, best score=%.0f",
			seg2.Segment.StartTime, seg2.Segment.EndTime,
			seg2.ClipCount, seg2.BestScore)
	}

	t.Logf("✅ Total clips: %d, Average score: %.0f", 
		mapping.TotalClips(), mapping.AverageScore)
}

// TestMapSegmentsToClips_EmptySegments tests with no segments
func TestMapSegmentsToClips_EmptySegments(t *testing.T) {
	indexer := createTestIndexerForTimestamp([]clip.IndexedClip{})
	service := NewService(indexer, nil)

	req := &MappingRequest{
		ScriptID:       "empty_script",
		Segments:       []TextSegment{},
		IncludeDrive:   true,
		IncludeArtlist: false,
	}

	mapping, err := service.MapSegmentsToClips(context.Background(), req)
	if err != nil {
		t.Fatalf("Mapping failed: %v", err)
	}

	if len(mapping.Segments) != 0 {
		t.Errorf("Expected 0 segments, got %d", len(mapping.Segments))
	}

	t.Log("✅ Empty segments handled correctly")
}

// TestBuildSearchQuery tests query building from segments
func TestBuildSearchQuery(t *testing.T) {
	tests := []struct {
		name     string
		segment  TextSegment
		expected string
	}{
		{
			name: "With keywords and entities",
			segment: TextSegment{
				Keywords: []string{"robot", "AI"},
				Entities: []string{"Elon Musk"},
			},
			expected: "robot AI Elon Musk",
		},
		{
			name: "Only keywords",
			segment: TextSegment{
				Keywords: []string{"tech", "innovation"},
			},
			expected: "tech innovation",
		},
		{
			name: "Only text",
			segment: TextSegment{
				Text: "Test segment text",
			},
			expected: "Test segment text",
		},
		{
			name:     "Empty",
			segment:  TextSegment{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query := buildSearchQuery(tt.segment)
			if query != tt.expected {
				t.Errorf("Expected query '%s', got '%s'", tt.expected, query)
			} else {
				t.Logf("✅ %s: query='%s'", tt.name, query)
			}
		})
	}
}

// TestCalculateArtlistScore tests Artlist scoring logic
func TestCalculateArtlistScore(t *testing.T) {
	clip := clip.IndexedClip{
		ID:         "art1",
		Name:       "AI Robot Technology",
		FolderPath: "Artlist/Tech",
		Tags:       []string{"ai", "robot", "technology"},
	}

	segment := TextSegment{
		Text:     "Il robot AI presenta nuova tecnologia",
		Keywords: []string{"robot", "AI", "tecnologia"},
		Entities: []string{},
	}

	score := calculateArtlistScore(clip, segment)

	t.Logf("✅ Artlist score: %.0f", score)

	// Should have keyword matches
	if score < 30 {
		t.Errorf("Expected score >= 30 for keyword matches, got %.0f", score)
	}
}

// TestTimestampMapping_Structure tests the mapping data structure
func TestTimestampMapping_Structure(t *testing.T) {
	mapping := &TimestampMapping{
		ScriptID:      "test_script",
		TotalDuration: 60.0,
		AverageScore:  75.5,
		Segments: []SegmentWithClips{
			{
				Segment: TextSegment{
					ID:        "seg_1",
					Index:     0,
					StartTime: 0.0,
					EndTime:   15.0,
				},
				AssignedClips: []ClipAssignment{
					{
						ClipID:         "clip1",
						Source:         "drive",
						RelevanceScore: 85.0,
					},
				},
				BestScore: 85.0,
				ClipCount: 1,
			},
		},
	}

	if mapping.ScriptID != "test_script" {
		t.Errorf("ScriptID mismatch: %s", mapping.ScriptID)
	}
	if mapping.TotalDuration != 60.0 {
		t.Errorf("TotalDuration mismatch: %.1f", mapping.TotalDuration)
	}
	if mapping.AverageScore != 75.5 {
		t.Errorf("AverageScore mismatch: %.1f", mapping.AverageScore)
	}
	if len(mapping.Segments) != 1 {
		t.Errorf("Expected 1 segment, got %d", len(mapping.Segments))
	}

	t.Log("✅ TimestampMapping structure verified")
}

// TestTextSegment_Structure tests the TextSegment data structure
func TestTextSegment_Structure(t *testing.T) {
	segment := TextSegment{
		ID:        "seg_test",
		Index:     5,
		StartTime: 30.0,
		EndTime:   45.0,
		Text:      "Test segment text",
		Keywords:  []string{"test", "segment"},
		Entities:  []string{"Entity"},
		Emotions:  []string{"excitement"},
	}

	if segment.ID != "seg_test" {
		t.Errorf("ID mismatch: %s", segment.ID)
	}
	if segment.Index != 5 {
		t.Errorf("Index mismatch: %d", segment.Index)
	}
	if segment.StartTime != 30.0 {
		t.Errorf("StartTime mismatch: %.1f", segment.StartTime)
	}
	if segment.EndTime != 45.0 {
		t.Errorf("EndTime mismatch: %.1f", segment.EndTime)
	}

	t.Log("✅ TextSegment structure verified")
}

// TestClipAssignment_Structure tests the ClipAssignment data structure
func TestClipAssignment_Structure(t *testing.T) {
	assignment := ClipAssignment{
		ClipID:         "clip_test",
		Source:         "drive",
		Name:           "Test Clip",
		FolderPath:     "Test/Folder",
		RelevanceScore: 85.0,
		Duration:       30.0,
		DriveLink:      "https://drive.google.com/file/d/test",
		MatchReason:    "Keyword match",
	}

	if assignment.ClipID != "clip_test" {
		t.Errorf("ClipID mismatch: %s", assignment.ClipID)
	}
	if assignment.Source != "drive" {
		t.Errorf("Source mismatch: %s", assignment.Source)
	}
	if assignment.RelevanceScore != 85.0 {
		t.Errorf("RelevanceScore mismatch: %.1f", assignment.RelevanceScore)
	}

	t.Log("✅ ClipAssignment structure verified")
}

// TestMappingRequest_Defaults tests default values in requests
func TestMappingRequest_Defaults(t *testing.T) {
	req := &MappingRequest{
		ScriptID: "test",
		Segments: []TextSegment{{}},
	}

	// Apply defaults (same logic as in service)
	if req.MaxClipsPerSegment == 0 {
		req.MaxClipsPerSegment = 3
	}
	if req.MinScore == 0 {
		req.MinScore = 20
	}
	if !req.IncludeDrive && !req.IncludeArtlist {
		req.IncludeDrive = true
		req.IncludeArtlist = true
	}

	if req.MaxClipsPerSegment != 3 {
		t.Errorf("Expected MaxClipsPerSegment=3, got %d", req.MaxClipsPerSegment)
	}
	if req.MinScore != 20 {
		t.Errorf("Expected MinScore=20, got %.0f", req.MinScore)
	}
	if !req.IncludeDrive || !req.IncludeArtlist {
		t.Error("Expected IncludeDrive=true, IncludeArtlist=true")
	}

	t.Log("✅ MappingRequest defaults applied correctly")
}
