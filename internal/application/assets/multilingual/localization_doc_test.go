package multilingual

import (
	"strings"
	"testing"
)

func TestAssembleLocalizationEntries_PriorityOrderNotCompletionOrder(t *testing.T) {
	// Simulate a completion-order input: ES finished first, EN second. The
	// manifest must still list EN (priority 0) before ES (priority 1).
	variants := []VariantResult{
		{Language: "es", Priority: 1, DriveLink: "https://drive/es.mp4", Status: "ready"},
		{Language: "en", Priority: 0, DriveLink: "https://drive/en.mp4", Status: "ready"},
	}
	entries := AssembleLocalizationEntries(variants)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Language != "en" || entries[1].Language != "es" {
		t.Fatalf("order not by requested priority: %+v", entries)
	}
	if entries[0].DriveLink != "https://drive/en.mp4" || entries[1].DriveLink != "https://drive/es.mp4" {
		t.Fatalf("links misattributed: %+v", entries)
	}
}

func TestAssembleLocalizationEntries_KeepsFailedLanguageInPlace(t *testing.T) {
	variants := []VariantResult{
		{Language: "es", Priority: 1, Status: "failed"},
		{Language: "en", Priority: 0, DriveLink: "https://drive/en.mp4", Status: "ready"},
	}
	entries := AssembleLocalizationEntries(variants)
	if entries[0].Language != "en" || entries[1].Language != "es" {
		t.Fatalf("failed language must keep its requested slot: %+v", entries)
	}
	if entries[1].DriveLink != "" {
		t.Fatalf("failed ES must have empty link: %+v", entries[1])
	}
}

func TestRenderLocalizationDoc_EnglishBeforeSpanish(t *testing.T) {
	entries := []LocalizationDocEntry{
		{Priority: 0, Language: "en", DriveLink: "https://drive/en.mp4", Status: "ready"},
		{Priority: 1, Language: "es", DriveLink: "https://drive/es.mp4", Status: "ready"},
	}
	doc := RenderLocalizationDoc("Localized clips", entries)

	enPos := strings.Index(doc, "English")
	esPos := strings.Index(doc, "Spanish")
	if enPos == -1 || esPos == -1 {
		t.Fatalf("language headings missing: %s", doc)
	}
	if enPos > esPos {
		t.Fatalf("English must precede Spanish in the doc: %s", doc)
	}
	if !strings.Contains(doc, `href="https://drive/en.mp4"`) || !strings.Contains(doc, `href="https://drive/es.mp4"`) {
		t.Fatalf("MP4 links missing: %s", doc)
	}
}

func TestLanguageLabel(t *testing.T) {
	if LanguageLabel("en") != "English" || LanguageLabel("es") != "Spanish" {
		t.Fatalf("known labels wrong: en=%q es=%q", LanguageLabel("en"), LanguageLabel("es"))
	}
	if LanguageLabel("xx") != "xx" {
		t.Fatalf("unknown code must fall back to itself, got %q", LanguageLabel("xx"))
	}
}
