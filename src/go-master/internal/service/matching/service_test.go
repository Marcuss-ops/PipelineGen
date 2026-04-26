package matching

import (
	"testing"
)

func TestMatcher_ScoreAsset(t *testing.T) {
	matcher := NewMatcher()

	tests := []struct {
		name     string
		phrase   string
		asset    string
		filename string
		folder   string
		tags     string
		minScore float64
		maxScore float64
	}{
		{
			name:     "exact name match",
			phrase:   "ocean waves",
			asset:    "ocean waves",
			filename: "ocean_waves.mp4",
			folder:   "nature",
			tags:     "ocean, water, waves",
			minScore: 100,
			maxScore: 100,
		},
		{
			name:     "partial match",
			phrase:   "ocean waves",
			asset:    "ocean",
			filename: "ocean.mp4",
			folder:   "nature",
			tags:     "ocean",
			minScore: 50,
			maxScore: 100,
		},
		{
			name:     "no match",
			phrase:   "ocean waves",
			asset:    "desert",
			filename: "desert.mp4",
			folder:   "landscape",
			tags:     "desert, sand",
			minScore: 0,
			maxScore: 0,
		},
		{
			name:     "filename match",
			phrase:   "ocean waves",
			asset:    "video1",
			filename: "ocean_waves.mp4",
			folder:   "nature",
			tags:     "",
			minScore: 80,
			maxScore: 100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score, reason := matcher.ScoreAsset(tt.phrase, tt.asset, tt.filename, tt.folder, tt.tags)
			
			if score < tt.minScore || score > tt.maxScore {
				t.Errorf("ScoreAsset() score = %v, want between %v and %v, reason = %v", score, tt.minScore, tt.maxScore, reason)
			}
			
			if score > 0 && reason == "" {
				t.Error("ScoreAsset() should return a reason when score > 0")
			}
		})
	}
}

func TestMatcher_ScoreAsset_EmptyPhrase(t *testing.T) {
	matcher := NewMatcher()
	
	score, reason := matcher.ScoreAsset("", "asset", "file.mp4", "folder", "tag")
	if score != 0 || reason != "" {
		t.Errorf("ScoreAsset() with empty phrase should return 0, got score=%v, reason=%v", score, reason)
	}
}

func TestMatcher_Tokenize(t *testing.T) {
	matcher := NewMatcher()
	
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"simple", "hello world", 2},
		{"extra spaces", "hello   world", 2},
		{"single", "hello", 1},
		{"empty", "", 0},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matcher.tokenize(tt.input)
			if len(got) != tt.want {
				t.Errorf("tokenize() len = %v, want %v", len(got), tt.want)
			}
		})
	}
}
