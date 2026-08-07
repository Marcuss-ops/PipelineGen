package config

import "testing"

func TestVideoConfigCanonicalClip(t *testing.T) {
	got := (VideoConfig{
		Width: 1280, Height: 720, FPS: 30,
		Codec: "h264_nvenc", Preset: "p1", CRF: 26,
		KeyframeInterval: 60, AudioCodec: "mp3", AudioBitrate: "96k",
		ClipDuration: 7,
	}).CanonicalClip()

	if got.Width != 1920 || got.Height != 1080 || got.FPS != 24 {
		t.Fatalf("unexpected canonical geometry/timing: %+v", got)
	}
	if got.Codec != "h264_nvenc" || got.Preset != "p1" || got.CRF != 26 || got.KeyframeInterval != 48 {
		t.Fatalf("unexpected canonical video encoding: %+v", got)
	}
	if got.AudioCodec != "aac" || got.AudioBitrate != "128k" {
		t.Fatalf("unexpected canonical audio encoding: %+v", got)
	}
	if got.ClipDuration != 7 {
		t.Fatalf("CanonicalClip changed a planning value: got clip duration %d", got.ClipDuration)
	}
}

func TestVideoConfigCanonicalClipPreservesRuntimePolicy(t *testing.T) {
	for _, policy := range []string{"auto", "h264_nvenc", "nvenc"} {
		got := (VideoConfig{Codec: policy}).CanonicalClip()
		if got.Codec != policy {
			t.Fatalf("CanonicalClip codec for policy %q = %q, want policy preserved", policy, got.Codec)
		}
	}
}
