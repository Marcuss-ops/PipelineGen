package localization

import (
	"errors"
	"strings"
	"testing"
)

// validPlan returns a fully-valid LocalizedClipPlan whose Fingerprint is
// computed canonically (so Validate passes). SourceSHA256 is a real 64-hex
// digest.
func validPlan() LocalizedClipPlan {
	p := LocalizedClipPlan{
		Version:           LocalizedClipPlanVersion,
		JobID:             "job-1",
		SceneID:           "scene-1",
		ClipID:            "clip-1",
		SourceAssetID:     "source-asset-1",
		SourceSHA256:      strings.Repeat("a", 64),
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
	p.Fingerprint = Fingerprint(p)
	return p
}

// TestValidate_AcceptsValidPlan verifies a fully-resolved plan with a
// canonical fingerprint passes.
func TestValidate_AcceptsValidPlan(t *testing.T) {
	if err := validPlan().Validate(); err != nil {
		t.Fatalf("valid plan must validate: %v", err)
	}
}

// TestValidate_RejectsEachViolation verifies every mandatory check fails
// closed with a typed error wrapping ErrInvalidLocalizedClipPlan.
func TestValidate_RejectsEachViolation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*LocalizedClipPlan)
	}{
		{"wrong version", func(p *LocalizedClipPlan) { p.Version = "localized-clip-plan.v2" }},
		{"empty version", func(p *LocalizedClipPlan) { p.Version = "" }},
		{"empty job_id", func(p *LocalizedClipPlan) { p.JobID = "" }},
		{"empty clip_id", func(p *LocalizedClipPlan) { p.ClipID = "  " }},
		{"empty source_asset_id", func(p *LocalizedClipPlan) { p.SourceAssetID = "" }},
		{"invalid source_sha256", func(p *LocalizedClipPlan) { p.SourceSHA256 = "not-hex" }},
		{"uppercase source_sha256", func(p *LocalizedClipPlan) { p.SourceSHA256 = strings.Repeat("A", 64) }},
		{"empty target_language", func(p *LocalizedClipPlan) { p.TargetLanguage = "" }},
		{"invalid target_language", func(p *LocalizedClipPlan) { p.TargetLanguage = "portuguese" }},
		{"zero transcript_track_id", func(p *LocalizedClipPlan) { p.TranscriptTrackID = 0 }},
		{"negative transcript_track_id", func(p *LocalizedClipPlan) { p.TranscriptTrackID = -1 }},
		{"empty transcript_sha256", func(p *LocalizedClipPlan) { p.TranscriptSHA256 = " " }},
		{"zero subtitle_track_id", func(p *LocalizedClipPlan) { p.SubtitleTrackID = 0 }},
		{"empty subtitle_sha256", func(p *LocalizedClipPlan) { p.SubtitleSHA256 = "" }},
		{"zero duration_ms", func(p *LocalizedClipPlan) { p.DurationMS = 0 }},
		{"empty renderer_version", func(p *LocalizedClipPlan) { p.RendererVersion = "" }},
		{"empty output_profile_hash", func(p *LocalizedClipPlan) { p.OutputProfileHash = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := validPlan()
			tc.mutate(&p)
			// Mutations other than the fingerprint keep a stale fingerprint;
			// re-derive it so only the mutated field is under test.
			p.Fingerprint = Fingerprint(p)

			err := p.Validate()
			if err == nil {
				t.Fatalf("Validate must reject %s", tc.name)
			}
			if !errors.Is(err, ErrInvalidLocalizedClipPlan) {
				t.Fatalf("error must wrap ErrInvalidLocalizedClipPlan, got %v", err)
			}
		})
	}
}

// TestValidate_RejectsFingerprintMismatch verifies a tampered fingerprint is
// rejected with the dedicated typed error (never ErrInvalidLocalizedClipPlan).
func TestValidate_RejectsFingerprintMismatch(t *testing.T) {
	p := validPlan()
	p.Fingerprint = strings.Repeat("b", 64)

	err := p.Validate()
	if err == nil {
		t.Fatal("Validate must reject a tampered fingerprint")
	}
	if !errors.Is(err, ErrLocalizedClipPlanFingerprintMismatch) {
		t.Fatalf("error must wrap ErrLocalizedClipPlanFingerprintMismatch, got %v", err)
	}
	if errors.Is(err, ErrInvalidLocalizedClipPlan) {
		t.Fatalf("fingerprint mismatch must NOT wrap ErrInvalidLocalizedClipPlan, got %v", err)
	}
}
