package scripts

import (
	"strings"
	"testing"
)

// ─── Block 9: IsYouTubeURL ────────────────────────────────────────────

func TestIsYouTubeURL(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"https://www.youtube.com/watch?v=abc123", true},
		{"https://youtu.be/abc123", true},
		{"https://youtube.com/watch?v=xyz789&t=30", true},
		{"https://www.youtube.com/embed/abc123", true},
		{"https://example.com/article", false},
		{"Caitlin Clark WNBA controversy", false},
		{"", false},
		{"just some random text about youtube", false},
		{"https://vimeo.com/123456", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := IsYouTubeURL(tt.input)
			if got != tt.want {
				t.Fatalf("IsYouTubeURL(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// ─── Block 9: ResearchSource.ToDB ─────────────────────────────────────

func TestResearchSourceToDB(t *testing.T) {
	source := ResearchSource{
		Query:          "test query",
		URL:            "https://example.com",
		Title:          "Test title",
		Snippet:        "Test snippet",
		SourceType:     "web",
		UsedInSections: "[1,2,3]",
	}

	dbSource := source.ToDB()

	if dbSource.Query != "test query" {
		t.Fatalf("wrong query: %s", dbSource.Query)
	}
	if dbSource.URL != "https://example.com" {
		t.Fatalf("wrong URL: %s", dbSource.URL)
	}
	if dbSource.Title != "Test title" {
		t.Fatalf("wrong title: %s", dbSource.Title)
	}
	if dbSource.Snippet != "Test snippet" {
		t.Fatalf("wrong snippet: %s", dbSource.Snippet)
	}
	if dbSource.SourceType != "web" {
		t.Fatalf("wrong source type: %s", dbSource.SourceType)
	}
	if !strings.Contains(dbSource.UsedInSections, "1") {
		t.Fatalf("used sections not serialized correctly: %s", dbSource.UsedInSections)
	}
}

func TestResearchSourceToDB_YoutubeType(t *testing.T) {
	source := ResearchSource{
		Query:          "YouTube transcript",
		URL:            "https://youtube.com/watch?v=abc123",
		Title:          "Interview",
		Snippet:        "Transcript snippet...",
		SourceType:     "youtube",
		UsedInSections: "[3]",
	}

	dbSource := source.ToDB()

	if dbSource.SourceType != "youtube" {
		t.Fatalf("expected youtube source_type, got %s", dbSource.SourceType)
	}
	if dbSource.ScriptID != 0 {
		t.Fatalf("ToDB should not set ScriptID, got %d", dbSource.ScriptID)
	}
}

func TestResearchSourceToDB_TranscriptType(t *testing.T) {
	source := ResearchSource{
		SourceType: "transcript",
	}

	dbSource := source.ToDB()
	if dbSource.SourceType != "transcript" {
		t.Fatalf("expected transcript source_type, got %s", dbSource.SourceType)
	}
}
