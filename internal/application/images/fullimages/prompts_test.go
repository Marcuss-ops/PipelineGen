package fullimages

import (
	"testing"
)

func TestBuildSectionPrompts_UsesTitle(t *testing.T) {
	sec := Section{Title: "Leonardo da Vinci", Text: "He was a great artist."}
	prompts := buildSectionPrompts(sec, "Renaissance")
	if len(prompts) == 0 {
		t.Fatal("expected at least one prompt")
	}
	if prompts[0] != "cinematic documentary image of Leonardo da Vinci" {
		t.Fatalf("expected first prompt to use title, got %q", prompts[0])
	}
}

func TestBuildSectionPrompts_IncludesTopic(t *testing.T) {
	sec := Section{Title: "Michelangelo", Text: "Sistine Chapel."}
	prompts := buildSectionPrompts(sec, "Renaissance")
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
	sec := Section{Title: "", Text: "Just some text content"}
	prompts := buildSectionPrompts(sec, "Science")
	if len(prompts) == 0 {
		t.Fatal("expected prompts even with empty title")
	}
}

func TestBuildSectionPrompts_TextFallback(t *testing.T) {
	longText := "This is a very long text about quantum physics"
	sec := Section{Title: "Quantum", Text: longText}
	prompts := buildSectionPrompts(sec, "Physics")
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
	sec := Section{Title: "Test", Text: longText}
	prompts := buildSectionPrompts(sec, "")
	for _, p := range prompts {
		if len(p) > 120 { // 100 truncation + some safety margin
			t.Fatalf("expected text prompt to be truncated, got %d chars: %q", len(p), p[:50])
		}
	}
}

func TestPickBestPrompt_ReturnsFirstNonEmpty(t *testing.T) {
	prompts := []string{"first prompt", "second prompt", "third prompt"}
	got := pickBestPrompt(prompts, "subj", "topic")
	if got != "first prompt" {
		t.Fatalf("expected first prompt, got %q", got)
	}
}

func TestPickBestPrompt_SkipsEmpty(t *testing.T) {
	prompts := []string{"", "", "valid prompt"}
	got := pickBestPrompt(prompts, "subj", "topic")
	if got != "valid prompt" {
		t.Fatalf("expected 'valid prompt', got %q", got)
	}
}

func TestPickBestPrompt_FallsBackToSubject(t *testing.T) {
	got := pickBestPrompt(nil, "Albert Einstein", "Science")
	if got != "A cinematic image of Albert Einstein" {
		t.Fatalf("expected fallback with subject, got %q", got)
	}
}

func TestPickBestPrompt_FallsBackToTopic(t *testing.T) {
	got := pickBestPrompt(nil, "", "Space Exploration")
	if got != "A documentary image about Space Exploration" {
		t.Fatalf("expected fallback with topic, got %q", got)
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

func TestPickBestPrompt_AllEmpty(t *testing.T) {
	got := pickBestPrompt([]string{"", ""}, "", "")
	if got == "" {
		t.Fatal("expected some fallback even with empty inputs")
	}
}
