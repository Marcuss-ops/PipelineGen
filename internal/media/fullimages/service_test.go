package fullimages

import (
	"strings"
	"testing"
)

func TestBuildTags_IncludesStyle(t *testing.T) {
	sec := Section{Title: "Castle", Style: "gothic"}
	tags := buildTags(sec, "Castle", "Medieval")
	if !containsTag(tags, "style:gothic") {
		t.Fatalf("expected 'style:gothic' in tags, got %v", tags)
	}
}

func TestBuildTags_NoStyle(t *testing.T) {
	sec := Section{Title: "Castle", Style: ""}
	tags := buildTags(sec, "Castle", "Medieval")
	for _, tag := range tags {
		if strings.HasPrefix(tag, "style:") {
			t.Fatalf("expected no style tag when style is empty, got %v", tags)
		}
	}
}

func TestBuildTags_IncludesSubject(t *testing.T) {
	sec := Section{Title: "Dragon", Style: "fantasy"}
	tags := buildTags(sec, "Dragon", "")
	if !containsTag(tags, "Dragon") {
		t.Fatalf("expected subject 'Dragon' in tags, got %v", tags)
	}
}

func TestBuildTags_IncludesTopic(t *testing.T) {
	sec := Section{Title: "Castle", Style: "gothic"}
	tags := buildTags(sec, "Castle", "Renaissance")
	if !containsTag(tags, "Renaissance") {
		t.Fatalf("expected topic 'Renaissance' in tags, got %v", tags)
	}
}

func TestBuildTags_StyleTagFormat(t *testing.T) {
	sec := Section{Title: "Dragon", Style: "stickman"}
	tags := buildTags(sec, "Dragon", "")
	if !containsTag(tags, "style:stickman") {
		t.Fatalf("expected 'style:stickman' in tags, got %v", tags)
	}
}

func TestSectionImage_StyleFieldPreserved(t *testing.T) {
	img := SectionImage{
		SectionIndex: 0,
		Title:        "Castle",
		Style:        "gothic",
		Error:        "something went wrong",
	}
	if img.Style != "gothic" {
		t.Fatalf("expected style 'gothic' in error response, got %q", img.Style)
	}
}

func TestSectionImage_EmptyStyleInResponse(t *testing.T) {
	img := SectionImage{
		SectionIndex: 0,
		Title:        "Castle",
		Error:        "failed",
	}
	if img.Style != "" {
		t.Fatalf("expected empty style, got %q", img.Style)
	}
}

func TestSection_StyleField(t *testing.T) {
	sec := Section{Title: "Warrior", Style: "medievale"}
	if sec.Style != "medievale" {
		t.Fatalf("expected style 'medievale', got %q", sec.Style)
	}
}

func TestSection_StyleFieldEmpty(t *testing.T) {
	sec := Section{Title: "Warrior"}
	if sec.Style != "" {
		t.Fatalf("expected empty style, got %q", sec.Style)
	}
}

func TestBuildTags_AlwaysIncludesSubject(t *testing.T) {
	sec := Section{Title: "Knight", Style: "medievale"}
	tags := buildTags(sec, "Knight", "")
	if len(tags) < 2 {
		t.Fatalf("expected at least 2 tags (subject + style), got %v", tags)
	}
}

func containsTag(tags []string, target string) bool {
	for _, tag := range tags {
		if tag == target {
			return true
		}
	}
	return false
}
