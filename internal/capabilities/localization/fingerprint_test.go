package localization

import (
	"strings"
	"testing"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// basePlan returns a fully-populated LocalizedClipPlan whose Fingerprint
// field is left empty (the output, not an input).
func basePlan() LocalizedClipPlan {
	return LocalizedClipPlan{
		Version:           LocalizedClipPlanVersion,
		JobID:             "job-1",
		SceneID:           "scene-1",
		ClipID:            "clip-1",
		SourceAssetID:     "source-asset-1",
		SourceSHA256:      "source-sha",
		SourceLanguage:    "en",
		TargetLanguage:    "es",
		TranscriptTrackID: 101,
		TranscriptSHA256:  "transcript-sha",
		SubtitleTrackID:   202,
		SubtitleSHA256:    "subtitle-sha",
		SubtitleStyleHash: "style-sha",
		DurationMS:        8432,
		OutputProfileHash: "profile-sha",
		RendererVersion:   "renderer-v1",
		Priority:          1,
	}
}

// TestFingerprint_Deterministic verifies the digest is a stable 64-hex
// SHA-256 for identical inputs.
func TestFingerprint_Deterministic(t *testing.T) {
	p := basePlan()
	a := Fingerprint(p)
	b := Fingerprint(p)
	if a != b {
		t.Fatalf("fingerprint must be deterministic: %q vs %q", a, b)
	}
	if len(a) != 64 {
		t.Fatalf("fingerprint must be 64 hex chars, got %d (%q)", len(a), a)
	}
	for _, c := range a {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("fingerprint must be lowercase hex, got %q", a)
		}
	}
}

// TestFingerprint_EachRenderFactChangesDigest verifies every one of the
// eight canonical inputs is folded into the digest.
func TestFingerprint_EachRenderFactChangesDigest(t *testing.T) {
	base := basePlan()
	ref := Fingerprint(base)

	cases := []struct {
		name   string
		mutate func(*LocalizedClipPlan)
	}{
		{"source_sha256", func(p *LocalizedClipPlan) { p.SourceSHA256 = "other-source-sha" }},
		{"transcript_sha256", func(p *LocalizedClipPlan) { p.TranscriptSHA256 = "other-transcript-sha" }},
		{"subtitle_sha256", func(p *LocalizedClipPlan) { p.SubtitleSHA256 = "other-subtitle-sha" }},
		{"target_language", func(p *LocalizedClipPlan) { p.TargetLanguage = "it" }},
		{"subtitle_style_hash", func(p *LocalizedClipPlan) { p.SubtitleStyleHash = "other-style-sha" }},
		{"output_profile_hash", func(p *LocalizedClipPlan) { p.OutputProfileHash = "other-profile-sha" }},
		{"renderer_version", func(p *LocalizedClipPlan) { p.RendererVersion = "renderer-v2" }},
		{"contract_version", func(p *LocalizedClipPlan) { p.Version = "localized-clip-plan.v2" }},
		{"background_mode", func(p *LocalizedClipPlan) { p.BackgroundMode = "blur_source" }},
		{"background_asset", func(p *LocalizedClipPlan) {
			p.BackgroundMode = "asset"
			p.Background = &cliprender.MaterializedAsset{AssetID: "asset-bg", LocalPath: "/x.mp4", SHA256: strings.Repeat("f", 64)}
		}},
		{"subtitle_style", func(p *LocalizedClipPlan) {
			p.SubtitlesStyle = &scriptpkg.VideoVisualStyleSpec{Color: "#FFFFFF", FontSizePX: 54}
		}},
		{"watermark_style", func(p *LocalizedClipPlan) {
			p.Watermark = &cliprender.MaterializedAsset{AssetID: "asset-wm", LocalPath: "/x.png", SHA256: strings.Repeat("e", 64)}
			p.WatermarkSpec = &cliprender.WatermarkSpec{Enabled: true, AssetID: "asset-wm", Position: "top_right", Opacity: 0.9, MarginPX: 24,
				Style: &scriptpkg.VideoVisualStyleSpec{WidthPX: 180, Shadow: &scriptpkg.VideoShadowSpec{Opacity: 0.55, BlurPX: 14, OffsetY: 8}}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := base
			tc.mutate(&p)
			if got := Fingerprint(p); got == ref {
				t.Fatalf("changing %s must change the fingerprint (both %q)", tc.name, got)
			}
		})
	}
}

// TestFingerprint_StyleDeltaChangesDigest verifies fine-grained style deltas
// (a shadow offset, a transition duration, a color) are each folded into the
// digest — a cached render with a different style must never be reused.
func TestFingerprint_StyleDeltaChangesDigest(t *testing.T) {
	styled := basePlan()
	styled.Watermark = &cliprender.MaterializedAsset{AssetID: "asset-wm", LocalPath: "/x.png", SHA256: strings.Repeat("e", 64)}
	styled.WatermarkSpec = &cliprender.WatermarkSpec{Enabled: true, AssetID: "asset-wm", Position: "top_right", Opacity: 0.9, MarginPX: 24,
		Style: &scriptpkg.VideoVisualStyleSpec{
			WidthPX: 180,
			Shadow:  &scriptpkg.VideoShadowSpec{Color: "#000000", Opacity: 0.55, BlurPX: 14, OffsetY: 8},
		}}
	styled.SubtitlesStyle = &scriptpkg.VideoVisualStyleSpec{
		Color:      "#FFFFFF",
		FontSizePX: 54,
		Shadow:     &scriptpkg.VideoShadowSpec{Opacity: 0.7, BlurPX: 10, OffsetY: 5},
	}
	styled.BackgroundMode = "blur_source"
	ref := Fingerprint(styled)

	cases := []struct {
		name   string
		mutate func(*LocalizedClipPlan)
	}{
		{"watermark shadow offset", func(p *LocalizedClipPlan) { p.WatermarkSpec.Style.Shadow.OffsetY = 9 }},
		{"watermark shadow color", func(p *LocalizedClipPlan) { p.WatermarkSpec.Style.Shadow.Color = "#FF0000" }},
		{"watermark transition added", func(p *LocalizedClipPlan) {
			p.WatermarkSpec.Style.TransitionIn = &scriptpkg.VideoTransitionSpec{Preset: "fade_in", DurationMS: 250}
		}},
		{"subtitle color", func(p *LocalizedClipPlan) { p.SubtitlesStyle.Color = "#FF0000" }},
		{"subtitle shadow blur", func(p *LocalizedClipPlan) { p.SubtitlesStyle.Shadow.BlurPX = 12 }},
		{"background mode", func(p *LocalizedClipPlan) { p.BackgroundMode = "asset" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := styled
			tc.mutate(&p)
			if got := Fingerprint(p); got == ref {
				t.Fatalf("changing %s must change the fingerprint (both %q)", tc.name, got)
			}
		})
	}
}

// TestFingerprint_IgnoresIdentityAndEditorialFields verifies non-render
// metadata (identity, source language label, track IDs, duration, priority)
// never changes the digest.
func TestFingerprint_IgnoresIdentityAndEditorialFields(t *testing.T) {
	base := basePlan()
	ref := Fingerprint(base)

	mut := base
	mut.JobID = "job-other"
	mut.SceneID = "scene-other"
	mut.ClipID = "clip-other"
	mut.SourceAssetID = "source-asset-other"
	mut.SourceLanguage = "fr"
	mut.TranscriptTrackID = 999
	mut.SubtitleTrackID = 998
	mut.DurationMS = 1
	mut.Priority = 99

	if got := Fingerprint(mut); got != ref {
		t.Fatalf("identity/editorial fields must not change the fingerprint:\n got %q\nwant %q", got, ref)
	}
}

// TestFingerprint_FieldIsNotAnInput verifies the stored Fingerprint field
// does not feed back into the computation (idempotent, drift-free).
func TestFingerprint_FieldIsNotAnInput(t *testing.T) {
	base := basePlan()
	ref := Fingerprint(base)

	mut := base
	mut.Fingerprint = ref
	if got := Fingerprint(mut); got != ref {
		t.Fatalf("stored fingerprint must not affect the digest: got %q want %q", got, ref)
	}
}
