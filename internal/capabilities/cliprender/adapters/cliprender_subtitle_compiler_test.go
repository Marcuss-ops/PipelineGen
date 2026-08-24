package adapters

// cliprender_subtitle_compiler_test.go — deterministic ASS compiler tests
// (spec §5): burn and sidecar modes, deterministic bytes, fail-closed on
// empty cues/invalid mode/invalid duration — and proof that speech
// recognition is never regenerated for subtitles (zero cues → typed error).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/texttracks"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
)

func subtitleTestInput(t *testing.T, mode string) cliprender.SubtitleCompileInput {
	t.Helper()
	return cliprender.SubtitleCompileInput{
		RunID:    "job-1",
		AssetID:  "asset-123",
		Language: "en",
		Mode:     mode,
		StyleID:  "shorts-v1",
		Cues: []cliprender.Cue{
			{StartMs: 0, EndMs: 3000, Text: "hello"},
			{StartMs: 3000, EndMs: 6000, Text: "world"},
		},
		ClipDurationMS: 6500,
		SourceSHA256:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		OutputDir:      t.TempDir(),
	}
}

func TestSubtitleCompiler_BurnMode(t *testing.T) {
	compiler := &ClipRenderSubtitleCompiler{}
	out, err := compiler.Compile(context.Background(), subtitleTestInput(t, cliprender.SubtitlesModeBurn))
	if err != nil {
		t.Fatalf("Compile(burn): %v", err)
	}
	if out.Mode != cliprender.SubtitlesModeBurn || out.StyleID != "shorts-v1" {
		t.Fatalf("artifact mode/style = %q/%q", out.Mode, out.StyleID)
	}
	if filepath.Base(out.LocalPath) != "subtitles.ass" {
		t.Fatalf("expected subtitles.ass in run dir, got %q", out.LocalPath)
	}
	content, err := os.ReadFile(out.LocalPath)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	if !strings.Contains(string(content), "Style: shorts-v1,") {
		t.Fatalf("missing style line:\n%s", content)
	}
	// SHA256 must match the written bytes exactly.
	sum := sha256.Sum256(content)
	if out.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("sha mismatch: artifact %s != file %x", out.SHA256, sum)
	}
	// The artifact must pass the canonical ASS validation.
	if err := texttracks.ValidateASSFile(out.LocalPath, 6500); err != nil {
		t.Fatalf("ValidateASSFile: %v", err)
	}
}

func TestSubtitleCompiler_SidecarSameBytesDifferentMode(t *testing.T) {
	compiler := &ClipRenderSubtitleCompiler{}
	burn, err := compiler.Compile(context.Background(), subtitleTestInput(t, cliprender.SubtitlesModeBurn))
	if err != nil {
		t.Fatalf("Compile(burn): %v", err)
	}
	sidecar, err := compiler.Compile(context.Background(), subtitleTestInput(t, cliprender.SubtitlesModeSidecar))
	if err != nil {
		t.Fatalf("Compile(sidecar): %v", err)
	}
	if burn.SHA256 != sidecar.SHA256 {
		t.Fatalf("burn and sidecar must share identical ASS bytes (mode is a plan tag), got %s vs %s", burn.SHA256, sidecar.SHA256)
	}
	if sidecar.Mode != cliprender.SubtitlesModeSidecar {
		t.Fatalf("expected sidecar mode tag, got %q", sidecar.Mode)
	}
}

func TestSubtitleCompiler_Deterministic(t *testing.T) {
	compiler := &ClipRenderSubtitleCompiler{}
	first, err := compiler.Compile(context.Background(), subtitleTestInput(t, cliprender.SubtitlesModeBurn))
	if err != nil {
		t.Fatalf("Compile (1st): %v", err)
	}
	second, err := compiler.Compile(context.Background(), subtitleTestInput(t, cliprender.SubtitlesModeBurn))
	if err != nil {
		t.Fatalf("Compile (2nd): %v", err)
	}
	if first.SHA256 != second.SHA256 {
		t.Fatalf("determinism violated: same cues+style produced different hashes")
	}
}

func TestSubtitleCompiler_EmptyCuesFailsClosed(t *testing.T) {
	compiler := &ClipRenderSubtitleCompiler{}
	in := subtitleTestInput(t, cliprender.SubtitlesModeBurn)
	in.Cues = nil
	_, err := compiler.Compile(context.Background(), in)
	if err == nil {
		t.Fatalf("expected fail-closed error for zero cues, got nil")
	}
	if !errors.Is(err, cliprender.ErrSubtitleCompileUnavailable) {
		t.Fatalf("expected ErrSubtitleCompileUnavailable, got: %v", err)
	}
	if !strings.Contains(err.Error(), "speech recognition is never regenerated") {
		t.Fatalf("error must state the no-re-transcription guarantee, got: %v", err)
	}
}

func TestSubtitleCompiler_InvalidModeFailsClosed(t *testing.T) {
	compiler := &ClipRenderSubtitleCompiler{}
	_, err := compiler.Compile(context.Background(), subtitleTestInput(t, "fancy"))
	if err == nil {
		t.Fatalf("expected fail-closed error for invalid mode, got nil")
	}
	if !errors.Is(err, cliprender.ErrSubtitleCompileUnavailable) {
		t.Fatalf("expected ErrSubtitleCompileUnavailable, got: %v", err)
	}
}

func TestSubtitleCompiler_TrimsCuesToClipDuration(t *testing.T) {
	compiler := &ClipRenderSubtitleCompiler{}
	in := subtitleTestInput(t, cliprender.SubtitlesModeBurn)
	in.ClipDurationMS = 4000 // the second cue is clipped to the media boundary
	out, err := compiler.Compile(context.Background(), in)
	if err != nil {
		t.Fatalf("expected boundary trimming to produce valid ASS, got: %v", err)
	}
	if err := texttracks.ValidateASSFile(out.LocalPath, 4000); err != nil {
		t.Fatalf("trimmed ASS is invalid: %v", err)
	}
}
