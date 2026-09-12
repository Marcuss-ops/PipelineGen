package usecase

import (
	"strings"
	"testing"

	processor "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters/processor"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase/gencore"
)

func TestSourceTextLogFields_NeverContainsRawText(t *testing.T) {
	secret := "this is a secret source text that must not leak into logs"
	fields := gencore.SourceTextLogFields(secret, processor.NormalizationConfig{LogSourceTextPreview: true, SourceTextPreviewChars: 80})

	raw, _ := fields["source_text_preview"]
	if raw == secret {
		t.Errorf("source_text_preview must not contain the full raw text")
	}

	for _, key := range []string{"source_text_hash", "source_text_chars", "source_text_bytes", "source_text_tokens"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("missing expected field %q", key)
		}
	}

	if fields["source_text_chars"] != len(secret) {
		t.Errorf("source_text_chars mismatch: got %v, want %d", fields["source_text_chars"], len(secret))
	}
}

func TestSourceTextLogFields_PreviewTruncated(t *testing.T) {
	text := strings.Repeat("a", 200)
	fields := gencore.SourceTextLogFields(text, processor.NormalizationConfig{LogSourceTextPreview: true, SourceTextPreviewChars: 10})
	preview, ok := fields["source_text_preview"].(string)
	if !ok {
		t.Fatalf("source_text_preview must be a string")
	}
	if len(preview) != 10 {
		t.Errorf("preview length mismatch: got %d, want 10", len(preview))
	}
	if preview == text {
		t.Errorf("preview must not equal the full text")
	}
}

func TestSourceTextLogFields_PreviewDisabled(t *testing.T) {
	fields := gencore.SourceTextLogFields("some source text", processor.NormalizationConfig{LogSourceTextPreview: false, SourceTextPreviewChars: 80})
	if _, ok := fields["source_text_preview"]; ok {
		t.Errorf("source_text_preview must be omitted when preview is disabled")
	}
}

func TestSourceTextLogFields_EmptyText(t *testing.T) {
	fields := gencore.SourceTextLogFields("", processor.NormalizationConfig{LogSourceTextPreview: true, SourceTextPreviewChars: 80})
	if _, ok := fields["source_text_preview"]; ok {
		t.Errorf("source_text_preview must be omitted for empty text")
	}
	if fields["source_text_chars"] != 0 {
		t.Errorf("source_text_chars must be 0 for empty text")
	}
}
