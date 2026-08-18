package localization

import "testing"

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
