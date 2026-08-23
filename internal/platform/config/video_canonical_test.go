package config

import "testing"

func TestCanonicalVideoProfileContainsArtifactRequirementsOnly(t *testing.T) {
	profile := (VideoConfig{
		Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1,
		Codec: "h264_nvenc", Preset: "p1", CRF: 26,
		KeyframeInterval: 60, AudioCodec: "mp3", AudioBitrate: "96k",
		SampleRate: 44100, Channels: 1, ClipDuration: 7,
	}).CanonicalVideoProfile()

	if profile.Width != 1280 || profile.Height != 720 || profile.FPSNum != 30 || profile.FPSDen != 1 {
		t.Fatalf("unexpected resolved geometry/timing: %+v", profile)
	}
	if profile.KeyframeInterval != 60 || profile.AudioCodec != "mp3" || profile.AudioBitrate != "96k" || profile.SampleRate != 44100 || profile.Channels != 1 {
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
	if got.Width != 1920 || got.Height != 1080 || got.FPSNum != 24 || got.FPSDen != 1 || got.KeyframeInterval != 48 {
		t.Fatalf("unexpected legacy canonical profile: %+v", got)
	}
	if got.Codec != "h264_nvenc" || got.Preset != "p1" || got.CRF != 26 || got.ClipDuration != 7 {
		t.Fatalf("legacy composite did not preserve policy/planning values: %+v", got)
	}
}

func TestVideoConfigCanonicalClipDoesNotChooseEncoder(t *testing.T) {
	got := (VideoConfig{}).CanonicalClip()
	if got.Codec != "" || got.Preset != "veryfast" || got.CRF != 23 {
		t.Fatalf("CanonicalClip must preserve empty codec while defaulting other policy fields safely: %+v", got)
	}
	if got.Width != 1920 || got.Height != 1080 || got.FPSNum != 24 || got.FPSDen != 1 || got.Duration != 7 {
		t.Fatalf("CanonicalClip must still provide canonical profile/default planning fields: %+v", got)
	}
}

func TestVideoConfigCanonicalClipPreservesPartialEncoderPolicySafely(t *testing.T) {
	got := (VideoConfig{Codec: "h264_nvenc"}).CanonicalClip()
	if got.Codec != "h264_nvenc" || got.Preset != "veryfast" || got.CRF != 23 {
		t.Fatalf("CanonicalClip must preserve explicit codec and default missing policy fields: %+v", got)
	}
}
