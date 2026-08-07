package config

import "testing"

func TestCanonicalVideoProfileContainsArtifactRequirementsOnly(t *testing.T) {
	profile := (VideoConfig{
		Width: 1280, Height: 720, FPS: 30,
		Codec: "h264_nvenc", Preset: "p1", CRF: 26,
		KeyframeInterval: 60, AudioCodec: "mp3", AudioBitrate: "96k",
		ClipDuration: 7,
	}).CanonicalVideoProfile()

	if profile.Width != 1920 || profile.Height != 1080 || profile.FPS != 24 {
		t.Fatalf("unexpected canonical geometry/timing: %+v", profile)
	}
	if profile.KeyframeInterval != 48 || profile.AudioCodec != "aac" || profile.AudioBitrate != "128k" {
		t.Fatalf("unexpected canonical profile: %+v", profile)
	}
}

func TestVideoEncoderPolicyPreservesRuntimePolicy(t *testing.T) {
	policy := (VideoConfig{Codec: "h264_nvenc", Preset: "p1", CRF: 26}).EncoderPolicy()
	if policy.Codec != "h264_nvenc" || policy.Preset != "p1" || policy.CRF != 26 {
		t.Fatalf("unexpected encoder policy: %+v", policy)
	}
}

func TestVideoConfigCanonicalClipPreservesLegacyCompositeCompatibility(t *testing.T) {
	got := (VideoConfig{Codec: "h264_nvenc", Preset: "p1", CRF: 26, ClipDuration: 7}).CanonicalClip()
	if got.Width != 1920 || got.Height != 1080 || got.FPS != 24 || got.KeyframeInterval != 48 {
		t.Fatalf("unexpected legacy canonical profile: %+v", got)
	}
	if got.Codec != "h264_nvenc" || got.Preset != "p1" || got.CRF != 26 || got.ClipDuration != 7 {
		t.Fatalf("legacy composite did not preserve policy/planning values: %+v", got)
	}
}
