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

func TestResolveFontPreset_YoungFamilyIsDistinct(t *testing.T) {
	base := ResolveFontPreset("subs-young")
	if base.FontName != "Poppins" || base.FontSize != 60 {
		t.Fatalf("subs-young preset = %+v; want Poppins @ 60", base)
	}

	pop := ResolveFontPreset("subs-young-pop")
	if pop.FontName != "Poppins" || pop.FontSize <= base.FontSize || pop.Shadow <= base.Shadow {
		t.Errorf("subs-young-pop = %+v; want a bolder/larger Poppins variant than %+v", pop, base)
	}

	clean := ResolveFontPreset("subs-young-clean")
	if clean.FontName != "Montserrat" || clean.Shadow >= base.Shadow {
		t.Errorf("subs-young-clean = %+v; want the lighter Montserrat variant", clean)
	}

	// Centring is an alignment axis owned by CompileASSContent, not a
	// typography change: the center variant keeps the base preset.
	if center := ResolveFontPreset("subs-young-center"); center != base {
		t.Errorf("subs-young-center = %+v; want the base young typography %+v", center, base)
	}

	// No young id may silently fall back to the montserrat default just
	// because the substring order changed.
	fallback := ResolveFontPreset("shorts-v1")
	for _, id := range []string{"subs-young", "subs-young-pop", "subs-young-clean", "subs-young-center"} {
		if got := ResolveFontPreset(id); got == fallback {
			t.Errorf("ResolveFontPreset(%q) fell back to the default preset %+v", id, got)
		}
	}
}

func TestCompileASSContent_YoungVariantsDifferAndCenterAligns(t *testing.T) {
	base, err := CompileASSContent(testCues(), "subs-young")
	if err != nil {
		t.Fatalf("CompileASSContent(subs-young): %v", err)
	}
	if !strings.Contains(base, "Style: subs-young,Poppins,60,") {
		t.Fatalf("expected the young style row, got:\n%s", base)
	}
	// Bold, outline 4.0, shadow 5, alignment 2 (bottom-center), marginV 40.
	if !strings.Contains(base, ",1,4.0,5,2,10,10,40,1") {
		t.Fatalf("expected bottom-center alignment for the base young preset, got:\n%s", base)
	}

	center, err := CompileASSContent(testCues(), "subs-young-center")
	if err != nil {
		t.Fatalf("CompileASSContent(subs-young-center): %v", err)
	}
	// Alignment 5 (true screen center) is derived from the "center" token.
	if !strings.Contains(center, ",1,4.0,5,5,10,10,40,1") {
		t.Fatalf("expected centered alignment for subs-young-center, got:\n%s", center)
	}

	clean, err := CompileASSContent(testCues(), "subs-young-clean")
	if err != nil {
		t.Fatalf("CompileASSContent(subs-young-clean): %v", err)
	}
	if clean == base || clean == center {
		t.Fatalf("young variants must produce distinct ASS bytes")
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
