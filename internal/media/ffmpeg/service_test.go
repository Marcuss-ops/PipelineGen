package ffmpeg

import "testing"

func TestNormalizePresetForCodecMapsNvencPresetToSoftwareCodecPreset(t *testing.T) {
	got := normalizePresetForCodec("libx264", "p1")
	if got != "veryfast" {
		t.Fatalf("expected veryfast, got %q", got)
	}
}

func TestNormalizePresetForCodecKeepsNvencPreset(t *testing.T) {
	got := normalizePresetForCodec("h264_nvenc", "p1")
	if got != "p1" {
		t.Fatalf("expected p1, got %q", got)
	}
}

func TestNormalizePresetForCodecDefaultsWhenEmpty(t *testing.T) {
	got := normalizePresetForCodec("libx264", "")
	if got != "veryfast" {
		t.Fatalf("expected veryfast, got %q", got)
	}
}
