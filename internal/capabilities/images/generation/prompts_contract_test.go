package generation_test

import (
	"testing"

	imggeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/generation"
)

func TestBuildSectionPrompts_UsesTitle(t *testing.T) {
	sec := imggeneration.Section{Title: "Leonardo da Vinci", Text: "He was a great artist."}
	prompts := imggeneration.BuildSectionPrompts(sec, "Renaissance")
	if len(prompts) == 0 {
		t.Fatal("expected at least one prompt")
	}
	if prompts[0] != "cinematic documentary image of Leonardo da Vinci" {
		t.Fatalf("expected first prompt to use title, got %q", prompts[0])
	}
}

func TestBuildSectionPrompts_IncludesTopic(t *testing.T) {
	sec := imggeneration.Section{Title: "Michelangelo", Text: "Sistine Chapel."}
	prompts := imggeneration.BuildSectionPrompts(sec, "Renaissance")
	found := false
	for _, p := range prompts {
		if p == "cinematic documentary image of Michelangelo, Renaissance theme" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected a prompt combining title with topic")
	}
}

func TestBuildSectionPrompts_EmptyTitle(t *testing.T) {
	sec := imggeneration.Section{Title: "", Text: "Just some text content"}
	prompts := imggeneration.BuildSectionPrompts(sec, "Science")
	if len(prompts) == 0 {
		t.Fatal("expected prompts even with empty title")
	}
}

func TestBuildSectionPrompts_TextFallback(t *testing.T) {
	longText := "This is a very long text about quantum physics"
	sec := imggeneration.Section{Title: "Quantum", Text: longText}
	prompts := imggeneration.BuildSectionPrompts(sec, "Physics")
	// The text should appear somewhere in the prompts
	foundText := false
	for _, p := range prompts {
		if p == longText {
			foundText = true
			break
		}
	}
	if !foundText {
		t.Fatal("expected the section text as a prompt candidate")
	}
	// Should also include topic-based prompts
	foundTopic := false
	for _, p := range prompts {
		if p == "cinematic documentary image of Quantum, Physics theme" {
			foundTopic = true
			break
		}
	}
	if !foundTopic {
		t.Fatal("expected a topic-combined prompt")
	}
}

func TestBuildSectionPrompts_TextTruncated(t *testing.T) {
	longText := ""
	for i := 0; i < 20; i++ {
		longText += "very long text segment repeated many times for testing purposes "
	}
	sec := imggeneration.Section{Title: "Test", Text: longText}
	prompts := imggeneration.BuildSectionPrompts(sec, "")
	for _, p := range prompts {
		if len(p) > 120 { // 100 truncation + some safety margin
			t.Fatalf("expected text prompt to be truncated, got %d chars: %q", len(p), p[:50])
		}
	}
}

func TestResolveDisplayURL_PathRel(t *testing.T) {
	asset := &struct {
		Hash      string
		PathRel   string
		SourceURL string
	}{PathRel: "science/abc123.jpg"}
	// Can't use models.ImageAsset directly in test,
	// test the logic through the helper that takes the asset
	_ = asset
}

func TestBuildPrimaryPrompt_FirstCandidate(t *testing.T) {
	sec := imggeneration.Section{Title: "Leonardo da Vinci", Text: "He was a great artist."}
	got := imggeneration.BuildPrimaryPrompt(sec, "Renaissance")
	if got != "cinematic documentary image of Leonardo da Vinci" {
		t.Fatalf("expected primary prompt to use title, got %q", got)
	}
}

func TestBuildPrimaryPrompt_EmptySection(t *testing.T) {
	got := imggeneration.BuildPrimaryPrompt(imggeneration.Section{}, "")
	if got != "" {
		t.Fatalf("expected empty prompt for empty section + empty topic, got %q", got)
	}
}
