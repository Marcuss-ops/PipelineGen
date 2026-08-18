package localization

import (
	"encoding/json"
	"testing"
)

// TestLocalizedClipArtifactVersion pins the canonical artifact version.
func TestLocalizedClipArtifactVersion(t *testing.T) {
	if got, want := LocalizedClipArtifactVersion, "localized-clip-artifact.v1"; got != want {
		t.Fatalf("LocalizedClipArtifactVersion: got %q, want %q", got, want)
	}
}

// TestLocalizedClipStatusValues pins every typed status literal.
func TestLocalizedClipStatusValues(t *testing.T) {
	cases := []struct {
		status LocalizedClipStatus
		want   string
	}{
		{LocalizedClipPending, "PENDING"},
		{LocalizedClipReady, "READY"},
		{LocalizedClipQueued, "QUEUED"},
		{LocalizedClipRendering, "RENDERING"},
		{LocalizedClipRendered, "RENDERED"},
		{LocalizedClipUploaded, "UPLOADED"},
		{LocalizedClipFailed, "FAILED"},
	}
	for _, tc := range cases {
		if string(tc.status) != tc.want {
			t.Errorf("status literal: got %q, want %q", tc.status, tc.want)
		}
	}
}

// TestLocalizedClipArtifact_JSONRoundTrip pins every field's wire tag and
// verifies a full round-trip survives without field drift.
func TestLocalizedClipArtifact_JSONRoundTrip(t *testing.T) {
	art := LocalizedClipArtifact{
		Version:         LocalizedClipArtifactVersion,
		JobID:           "job-1",
		SceneID:         "scene-7",
		ClipID:          "clip-42",
		Language:        "es",
		PlanFingerprint: "plan-fp",
		AssetID:         "asset-9",
		LocalPath:       "/tmp/es.mp4",
		SHA256:          "output-sha",
		SizeBytes:       123456,
		DurationMS:      8432,
		VideoCodec:      "h264",
		AudioCodec:      "aac",
		DriveFileID:     "drive-file-1",
		DriveLink:       "https://drive/...",
		Status:          LocalizedClipUploaded,
	}

	out, err := json.Marshal(art)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var wire map[string]any
	if err := json.Unmarshal(out, &wire); err != nil {
		t.Fatalf("unmarshal into map: %v", err)
	}
	wantKeys := []string{
		"version", "job_id", "scene_id", "clip_id",
		"language", "plan_fingerprint", "asset_id",
		"local_path", "sha256", "size_bytes", "duration_ms",
		"video_codec", "audio_codec",
		"drive_file_id", "drive_link", "status",
	}
	for _, k := range wantKeys {
		if _, ok := wire[k]; !ok {
			t.Errorf("wire payload missing key %q", k)
		}
	}
	if len(wire) != len(wantKeys) {
		t.Errorf("wire payload has %d keys, want %d (unexpected extra field)", len(wire), len(wantKeys))
	}

	var back LocalizedClipArtifact
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back != art {
		t.Fatalf("round-trip mismatch:\n got %+v\nwant %+v", back, art)
	}
}

// TestLocalizedClipArtifact_OmitEmptyLocation verifies the godlike/07
// no-fake-availability rule: unset LocalPath / DriveFileID / DriveLink are
// omitted from the wire payload, never serialized as empty strings.
func TestLocalizedClipArtifact_OmitEmptyLocation(t *testing.T) {
	art := LocalizedClipArtifact{
		Version:  LocalizedClipArtifactVersion,
		Language: "es",
		Status:   LocalizedClipRendered,
	}
	out, err := json.Marshal(art)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(out, &wire); err != nil {
		t.Fatalf("unmarshal into map: %v", err)
	}
	for _, k := range []string{"local_path", "drive_file_id", "drive_link"} {
		if _, ok := wire[k]; ok {
			t.Errorf("key %q must be omitted when empty", k)
		}
	}
}
