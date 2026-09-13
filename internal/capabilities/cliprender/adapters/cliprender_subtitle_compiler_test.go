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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

type subtitleArtifactRepoStub struct {
	current *detail.SubtitleArtifact
}

func (s *subtitleArtifactRepoStub) Upsert(_ context.Context, _ *detail.SubtitleArtifact) error {
	return nil
}

func (s *subtitleArtifactRepoStub) FindCurrent(_ context.Context, _ string, _ string, _ detail.SubtitleFormat) (*detail.SubtitleArtifact, error) {
	return s.current, nil
}

func (s *subtitleArtifactRepoStub) ListByAsset(_ context.Context, _ string) ([]detail.SubtitleArtifact, error) {
	return nil, nil
}

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

// TestSubtitleCompiler_NormalizesLongWhisperSegments locks the short-form
// caption contract on the artifact the worker actually burns: a 7.44s Whisper
// segment (the real defect) must not reach the renderer as one three-line wall.
func TestSubtitleCompiler_NormalizesLongWhisperSegments(t *testing.T) {
	compiler := &ClipRenderSubtitleCompiler{}
	in := subtitleTestInput(t, cliprender.SubtitlesModeBurn)
	in.Cues = []cliprender.Cue{
		{StartMs: 0, EndMs: 5600, Text: "Yeah, I didn't know that I was afraid of heights till I mean I did these stunts like I jumped"},
		{StartMs: 13040, EndMs: 20480, Text: "this is fine and I did it and but I was in Dubai in 2004 and went to the top of this building"},
	}
	in.ClipDurationMS = 20480
	out, err := compiler.Compile(context.Background(), in)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	content, err := os.ReadFile(out.LocalPath)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	policy := texttracks.DefaultShortFormPolicy()
	dialogues := 0
	longest := int64(0)
	for _, line := range strings.Split(string(content), "\n") {
		if !strings.HasPrefix(line, "Dialogue:") {
			continue
		}
		dialogues++
		fields := strings.SplitN(strings.TrimSpace(strings.TrimPrefix(line, "Dialogue:")), ",", 10)
		if len(fields) < 10 {
			t.Fatalf("malformed dialogue line: %q", line)
		}
		body := fields[9]
		if got := strings.Count(body, `\N`) + 1; got > policy.MaxLines {
			t.Fatalf("dialogue wraps to %d lines, want <= %d: %q", got, policy.MaxLines, body)
		}
		if n := utf8.RuneCountInString(strings.ReplaceAll(body, `\N`, " ")); n > policy.MaxCharsPerCue() {
			t.Fatalf("dialogue carries %d runes, want <= %d: %q", n, policy.MaxCharsPerCue(), body)
		}
		start, end := assTimestampMs(t, fields[1]), assTimestampMs(t, fields[2])
		if end <= start {
			t.Fatalf("dialogue has an empty window: %q", body)
		}
		if end-start > longest {
			longest = end - start
		}
	}
	if dialogues < 4 {
		t.Fatalf("two long segments produced %d dialogues, want at least 4 readable captions:\n%s", dialogues, content)
	}
	// The 7.44s segment must no longer hold one caption for its whole window.
	if longest > 3000 {
		t.Fatalf("longest caption dwells %dms: the long segment was not refreshed\n%s", longest, content)
	}
	if err := texttracks.ValidateASSFile(out.LocalPath, in.ClipDurationMS); err != nil {
		t.Fatalf("ValidateASSFile: %v", err)
	}
}

func assTimestampMs(t *testing.T, raw string) int64 {
	t.Helper()
	parts := strings.Split(strings.TrimSpace(raw), ":")
	if len(parts) != 3 {
		t.Fatalf("bad ASS timestamp %q", raw)
	}
	sec := strings.Split(parts[2], ".")
	if len(sec) != 2 {
		t.Fatalf("bad ASS timestamp %q", raw)
	}
	var h, m, s, cs int64
	fmt.Sscanf(parts[0], "%d", &h)
	fmt.Sscanf(parts[1], "%d", &m)
	fmt.Sscanf(sec[0], "%d", &s)
	fmt.Sscanf(sec[1], "%d", &cs)
	return ((h*60+m)*60+s)*1000 + cs*10
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

func TestSubtitleCompiler_RegeneratesWhenCurrentStyleDiffers(t *testing.T) {
	compiler := &ClipRenderSubtitleCompiler{}
	compiler.SetArtifactRepository(&subtitleArtifactRepoStub{current: &detail.SubtitleArtifact{
		AssetID:      "asset-123",
		LanguageCode: "en",
		Format:       detail.SubtitleFormatASS,
		Status:       detail.SubtitleStatusReady,
		StyleVersion: "legacy-style",
		LocalPath:    "/does/not/exist/subtitles.ass",
		DriveFileID:  "drive-ass-legacy",
	}})

	out, err := compiler.Compile(context.Background(), subtitleTestInput(t, cliprender.SubtitlesModeBurn))
	if err != nil {
		t.Fatalf("style mismatch must be a cache miss, not a failure: %v", err)
	}
	if out.StyleID != "shorts-v1" {
		t.Fatalf("regenerated artifact style = %q, want shorts-v1", out.StyleID)
	}
	content, err := os.ReadFile(out.LocalPath)
	if err != nil {
		t.Fatalf("read regenerated artifact: %v", err)
	}
	if !strings.Contains(string(content), "Style: shorts-v1,") {
		t.Fatalf("regenerated ASS does not contain requested style:\n%s", content)
	}
}
