package multilingual

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/texttracks"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

func TestRenderVariantFingerprint_DeterministicAndSensitive(t *testing.T) {
	base := []string{
		"source_sha", "transcript_sha", "it", "model-v1", "style-v1", asset.RenderProfileFFmpegAss1080pV1,
	}
	fp := asset.RenderVariantFingerprint(base[0], base[1], base[2], base[3], base[4], base[5])
	if fp == "" {
		t.Fatal("fingerprint must be non-empty")
	}
	if fp2 := asset.RenderVariantFingerprint(base[0], base[1], base[2], base[3], base[4], base[5]); fp2 != fp {
		t.Fatalf("fingerprint must be deterministic: %q != %q", fp, fp2)
	}

	// Every fingerprint input must be load-bearing: changing any one of them
	// (except render profile, which is constant per path) changes the output.
	cases := []struct {
		name  string
		index int
		value string
	}{
		{"source_clip_sha256", 0, "other_source_sha"},
		{"transcript_sha256", 1, "other_transcript_sha"},
		{"target_language", 2, "es"},
		{"translation_version", 3, "model-v2"},
		{"subtitle_style_version", 4, "style-v2"},
	}
	for _, c := range cases {
		args := append([]string{}, base...)
		args[c.index] = c.value
		if got := asset.RenderVariantFingerprint(args[0], args[1], args[2], args[3], args[4], args[5]); got == fp {
			t.Errorf("%s: fingerprint must change when the input changes", c.name)
		}
	}
}

func TestCuesWithText_KeepsTimingAndDistributesText(t *testing.T) {
	timing := []asset.TimedCue{
		{StartMs: 0, EndMs: 1000, Text: "a"},
		{StartMs: 1000, EndMs: 2000, Text: "b"},
		{StartMs: 2000, EndMs: 3000, Text: "c"},
	}
	out := texttracks.CuesWithText(timing, "uno due tre quattro")
	if len(out) != len(timing) {
		t.Fatalf("cue count changed: got %d want %d", len(out), len(timing))
	}
	for i := range timing {
		if out[i].StartMs != timing[i].StartMs || out[i].EndMs != timing[i].EndMs {
			t.Fatalf("timing drift at %d: %+v -> %+v", i, timing[i], out[i])
		}
	}
	if out[0].Text == "" || out[2].Text == "" {
		t.Fatalf("translated text must be distributed across cues: %+v", out)
	}
}
