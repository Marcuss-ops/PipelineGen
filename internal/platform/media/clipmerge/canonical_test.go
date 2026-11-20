package clipmerge

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media/clipsequence"
)

// TestMergeCanonicalVideoUsesTheCertifiedRecipe pins the argument block the
// sequence lane emits. The sequence is an assembly input, so profile/level,
// refs, B-frames, GOP, timescale and the bt709/tv VUI must all be present: they
// are exactly the facts that change the avcC the copy-only assembler compares.
func TestMergeCanonicalVideoUsesTheCertifiedRecipe(t *testing.T) {
	clips := []clipsequence.Clip{
		{AssetID: "a", Path: "a.mp4", DurationMS: 1_000},
		{AssetID: "b", Path: "b.mp4", DurationMS: 1_000},
	}
	group := clipsequence.Group{
		ID: "g", ClipIndexes: []int{0, 1}, Merged: true, DurationMS: 1_800,
		Transitions: []clipsequence.GroupTransition{{
			Preset: clipsequence.PresetFlashWhite, DurationMS: 200, FromClip: 0, ToClip: 1,
		}},
	}
	args, err := mergeArgs(clips, group, Options{
		OutputDir:      "out",
		Encoder:        media.EncoderPolicy{Codec: media.CanonicalVideoCodec, Preset: media.CanonicalVideoPreset, CRF: media.CanonicalVideoCRF},
		CanonicalVideo: true,
	}, videoSize{width: 1920, height: 1080}, filepath.Join("out", "g.mp4"))
	if err != nil {
		t.Fatalf("mergeArgs: %v", err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"-c:v libx264",
		"-preset veryfast",
		"-crf 23",
		"-refs 1",
		"-profile:v high",
		"-level:v 4.0",
		"-pix_fmt yuv420p",
		"-g 48",
		"-bf 0",
		"-colorspace bt709",
		"-color_primaries bt709",
		"-color_trc bt709",
		"-color_range tv",
		"-video_track_timescale 90000",
		"-r 24",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("canonical merge args are missing %q:\n%s", want, joined)
		}
	}
}

// TestMergeCanonicalVideoRejectsANonCanonicalPolicy keeps the flag fail-closed:
// opting into canonical output with an NVENC policy must stop before any file
// is created, not silently emit a different SPS/PPS.
func TestMergeCanonicalVideoRejectsANonCanonicalPolicy(t *testing.T) {
	_, err := Merge(context.Background(), clipsequence.Sequence{}, clipsequence.Plan{}, Options{
		OutputDir:      t.TempDir(),
		Encoder:        media.EncoderPolicy{Codec: "h264_nvenc", Preset: "p1", CRF: 23},
		CanonicalVideo: true,
	})
	if err == nil {
		t.Fatal("a non-canonical encoder must be rejected")
	}
	if !strings.Contains(err.Error(), "canonical video requires") {
		t.Fatalf("error = %v, want the canonical lane named", err)
	}
}
