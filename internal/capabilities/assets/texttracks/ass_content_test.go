package assets

// ass_content_test.go — determinism + fail-closed contract for the canonical
// ASS content generator (CompileASSContent), the single owner of ASS content
// generation shared by the durable materializer and clip.render's subtitle
// compiler. Identical cues + style must ALWAYS produce identical bytes.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

func testCues() []asset.TimedCue {
	return []asset.TimedCue{
		{StartMs: 0, EndMs: 3280, Text: "hello"},
		{StartMs: 3280, EndMs: 6000, Text: "world"},
	}
}

func TestCompileASSContent_Deterministic(t *testing.T) {
	first, err := CompileASSContent(testCues(), "shorts-v1")
	if err != nil {
		t.Fatalf("CompileASSContent: %v", err)
	}
	second, err := CompileASSContent(testCues(), "shorts-v1")
	if err != nil {
		t.Fatalf("CompileASSContent (2nd): %v", err)
	}
	if first != second {
		t.Fatalf("determinism violated: identical cues+style produced different bytes")
	}
	if !strings.HasPrefix(first, "[Script Info]") {
		t.Fatalf("expected ASS [Script Info] header, got: %q", first[:min(len(first), 40)])
	}
	if !strings.Contains(first, "Style: shorts-v1,") {
		t.Fatalf("expected style line with shorts-v1, got:\n%s", first)
	}
	if strings.Count(first, "Dialogue:") != 2 {
		t.Fatalf("expected 2 Dialogue lines, got:\n%s", first)
	}
}

func TestCompileASSContent_StyleChangesBytes(t *testing.T) {
	base, _ := CompileASSContent(testCues(), "shorts-v1")
	other, _ := CompileASSContent(testCues(), "shorts-v2")
	if base == other {
		t.Fatalf("different styles must produce different bytes")
	}
}

func TestCompileASSContent_EmptyCuesFailsClosed(t *testing.T) {
	if _, err := CompileASSContent(nil, "shorts-v1"); err == nil {
		t.Fatalf("expected error for empty cues, got nil")
	}
}

func TestCompileASSContent_ValidatesThroughValidateASSFile(t *testing.T) {
	content, err := CompileASSContent(testCues(), "")
	if err != nil {
		t.Fatalf("CompileASSContent: %v", err)
	}
	path := filepath.Join(t.TempDir(), "subtitles.ass")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Default style applied; last cue ends at 6000ms — within a 6250ms clip.
	if err := ValidateASSFile(path, 6250); err != nil {
		t.Fatalf("ValidateASSFile: %v", err)
	}
	// Beyond duration + 250ms tolerance → fail closed.
	if err := ValidateASSFile(path, 5000); err == nil {
		t.Fatalf("expected validation error when last cue exceeds clip duration, got nil")
	}
}
