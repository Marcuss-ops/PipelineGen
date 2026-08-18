package localization

import (
	"encoding/json"
	"testing"
)

// TestLocalizedClipPlanVersion pins the canonical contract version literal.
func TestLocalizedClipPlanVersion(t *testing.T) {
	if got, want := LocalizedClipPlanVersion, "localized-clip-plan.v1"; got != want {
		t.Fatalf("LocalizedClipPlanVersion: got %q, want %q", got, want)
	}
}

// TestLocalizedClipPlan_JSONRoundTrip pins every field's wire tag so the
// contract shape cannot drift silently. The payload carries all fields in the
// canonical JSON spelling.
func TestLocalizedClipPlan_JSONRoundTrip(t *testing.T) {
	plan := LocalizedClipPlan{
		Version:           LocalizedClipPlanVersion,
		JobID:             "job-1",
		SceneID:           "scene-7",
		ClipID:            "clip-42",
		SourceAssetID:     "source-asset-1",
		SourceSHA256:      "abc123",
		SourceLanguage:    "en",
		TargetLanguage:    "es",
		TranscriptTrackID: 101,
		TranscriptSHA256:  "transcript-hash",
		SubtitleTrackID:   202,
		SubtitleSHA256:    "subtitle-hash",
		SubtitleStyleHash: "style-hash",
		DurationMS:        8432,
		OutputProfileHash: "profile-hash",
		RendererVersion:   "renderer-v1",
		Priority:          1,
		Fingerprint:       "fp",
	}

	out, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Pin the exact wire keys: no field may be renamed or dropped.
	var wire map[string]any
	if err := json.Unmarshal(out, &wire); err != nil {
		t.Fatalf("unmarshal into map: %v", err)
	}
	wantKeys := []string{
		"version", "job_id", "scene_id", "clip_id",
		"source_asset_id", "source_sha256",
		"source_language", "target_language",
		"transcript_track_id", "transcript_sha256",
		"subtitle_track_id", "subtitle_sha256",
		"subtitle_style_hash",
		"duration_ms",
		"output_profile_hash", "renderer_version",
		"priority", "fingerprint",
	}
	for _, k := range wantKeys {
		if _, ok := wire[k]; !ok {
			t.Errorf("wire payload missing key %q", k)
		}
	}
	if len(wire) != len(wantKeys) {
		t.Errorf("wire payload has %d keys, want %d (unexpected extra field)", len(wire), len(wantKeys))
	}

	// Round-trip: unmarshal must reproduce the plan byte-for-byte in value.
	var back LocalizedClipPlan
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back != plan {
		t.Fatalf("round-trip mismatch:\n got %+v\nwant %+v", back, plan)
	}
}
