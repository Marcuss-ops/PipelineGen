package texttracks

// ass_content_test.go — determinism + fail-closed contract for the canonical
// ASS content generator (CompileASSContent), the single owner of ASS content
// generation shared by the durable materializer and clip.render's subtitle
// compiler. Identical cues + style must ALWAYS produce identical bytes.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

func testCues() []detail.TimedCue {
	return []detail.TimedCue{
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

func TestCompileASSContent_PoppinsPresetStrongerShadow(t *testing.T) {
	content, err := CompileASSContent(testCues(), "matt-damon-benchmark-v1-poppins")
	if err != nil {
		t.Fatalf("CompileASSContent: %v", err)
	}
	// Style row must carry the Poppins font name and the stronger ASS shadow.
	// Format: Name, Fontname, Fontsize, ..., BorderStyle, Outline, Shadow, ...
	if !strings.Contains(content, "Style: matt-damon-benchmark-v1-poppins,Poppins,58,") {
		t.Fatalf("expected Poppins style row, got:\n%s", content)
	}
	if !strings.Contains(content, ",1,0,0,0,100,100,0,0,1,4.0,6,") {
		t.Fatalf("expected Poppins outline 4.0 + shadow 6 in the style row, got:\n%s", content)
	}
}

func TestResolveFontPreset_PoppinsHasStrongerShadowThanMontserrat(t *testing.T) {
	poppins := ResolveFontPreset("matt-damon-benchmark-v1-poppins")
	if poppins.FontName != "Poppins" || poppins.Shadow <= 3 {
		t.Fatalf("poppins preset = %+v; want FontName Poppins with shadow > 3", poppins)
	}
	montserrat := ResolveFontPreset("shorts-v1")
	if montserrat.FontName != "Montserrat" || montserrat.Shadow >= poppins.Shadow {
		t.Fatalf("montserrat fallback = %+v; want default shadow strictly smaller than poppins", montserrat)
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
