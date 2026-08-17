// Package job — artifact_manifest_roundtrip_test.go (split surface: JSON round-trip).
//
// JSON marshal+unmarshal round-trip tests for ArtifactManifest and UploadedManifest.
// Pure relocation from artifact_manifest_test.go; no behavior change.
package job

import (
	"encoding/json"
	"strings"
	"testing"
)

// ── JSON round-trip ──────────────────────────────────────────────────

func TestArtifactManifest_JSONRoundTrip(t *testing.T) {
	original := ArtifactManifest{
		SchemaVersion: SchemaVersionArtifactManifestV1,
		WorkflowID:    "wf_123",
		JobID:         "job_456",
		Artifacts: []Artifact{
			{
				ID:       "job_456:script",
				Kind:     ArtifactKindScriptJSON,
				Path:     "/tmp/pipelinegen/jobs/job_456/script.json",
				Filename: "script.json",
				MIMEType: "application/json",
				Required: true,
			},
			{
				ID:       "job_456:voiceover:it",
				Kind:     ArtifactKindVoiceover,
				Path:     "/tmp/pipelinegen/jobs/job_456/voiceover-it.mp3",
				Filename: "voiceover-it.mp3",
				MIMEType: "audio/mpeg",
				Required: true,
			},
			{
				ID:       "job_456:image:0",
				Kind:     ArtifactKindImage,
				Path:     "/tmp/pipelinegen/jobs/job_456/images/scene_0.png",
				Filename: "scene_0.png",
				MIMEType: "image/png",
				Required: false,
			},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded ArtifactManifest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.SchemaVersion != original.SchemaVersion {
		t.Errorf("SchemaVersion = %q, want %q", decoded.SchemaVersion, original.SchemaVersion)
	}
	if decoded.WorkflowID != original.WorkflowID {
		t.Errorf("WorkflowID = %q, want %q", decoded.WorkflowID, original.WorkflowID)
	}
	if decoded.JobID != original.JobID {
		t.Errorf("JobID = %q, want %q", decoded.JobID, original.JobID)
	}
	if len(decoded.Artifacts) != len(original.Artifacts) {
		t.Fatalf("artifact count = %d, want %d", len(decoded.Artifacts), len(original.Artifacts))
	}

	for i, a := range original.Artifacts {
		d := decoded.Artifacts[i]
		if d.ID != a.ID {
			t.Errorf("artifact[%d].ID = %q, want %q", i, d.ID, a.ID)
		}
		if d.Kind != a.Kind {
			t.Errorf("artifact[%d].Kind = %q, want %q", i, d.Kind, a.Kind)
		}
		if d.Filename != a.Filename {
			t.Errorf("artifact[%d].Filename = %q, want %q", i, d.Filename, a.Filename)
		}
		if d.MIMEType != a.MIMEType {
			t.Errorf("artifact[%d].MIMEType = %q, want %q", i, d.MIMEType, a.MIMEType)
		}
		if d.Required != a.Required {
			t.Errorf("artifact[%d].Required = %v, want %v", i, d.Required, a.Required)
		}
	}
}

// ── Drive-published identity fields (drive_file_id / drive_link) ─────

func TestArtifact_DriveFields_JSONRoundTrip(t *testing.T) {
	original := Artifact{
		ID:          "job_1:overlay",
		Kind:        ArtifactKindOverlay,
		Filename:    "overlay.mov",
		MIMEType:    "video/quicktime",
		SizeBytes:   1234567,
		SHA256:      "deadbeef",
		DriveFileID: "1a2b3c4d5e6f",
		DriveLink:   "https://drive.google.com/file/d/1a2b3c4d5e6f/view",
		Required:    true,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded Artifact
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.DriveFileID != original.DriveFileID {
		t.Errorf("DriveFileID = %q, want %q", decoded.DriveFileID, original.DriveFileID)
	}
	if decoded.DriveLink != original.DriveLink {
		t.Errorf("DriveLink = %q, want %q", decoded.DriveLink, original.DriveLink)
	}
	if decoded.SizeBytes != original.SizeBytes {
		t.Errorf("SizeBytes = %d, want %d", decoded.SizeBytes, original.SizeBytes)
	}
	if decoded.SHA256 != original.SHA256 {
		t.Errorf("SHA256 = %q, want %q", decoded.SHA256, original.SHA256)
	}
}

// ── UploadedManifest JSON round-trip ─────────────────────────────────

func TestUploadedManifest_JSONRoundTrip(t *testing.T) {
	original := UploadedManifest{
		SchemaVersion: SchemaVersionArtifactManifestV1,
		WorkflowID:    "wf_1",
		JobID:         "job_1",
		Artifacts: []UploadedArtifact{
			{ID: "job_1:script", Kind: ArtifactKindScriptJSON, Filename: "script.json", MIMEType: "application/json", SHA256: "abc", Requirement: ArtifactRequirementRequired, RemoteAssetID: "asset_1", Status: "ready"},
			{ID: "job_1:image:0", Kind: ArtifactKindImage, Filename: "img.png", MIMEType: "image/png", SHA256: "def", Requirement: ArtifactRequirementOptional, RemoteAssetID: "", Status: "skipped"},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded UploadedManifest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if len(decoded.Artifacts) != 2 {
		t.Fatalf("artifact count = %d, want 2", len(decoded.Artifacts))
	}
	if decoded.Artifacts[0].RemoteAssetID != "asset_1" {
		t.Errorf("artifact[0].RemoteAssetID = %q, want asset_1", decoded.Artifacts[0].RemoteAssetID)
	}
	if decoded.Artifacts[0].Status != "ready" {
		t.Errorf("artifact[0].Status = %q, want ready", decoded.Artifacts[0].Status)
	}
	if decoded.Artifacts[0].Requirement != ArtifactRequirementRequired {
		t.Errorf("artifact[0].Requirement = %v, want %v", decoded.Artifacts[0].Requirement, ArtifactRequirementRequired)
	}
	if decoded.Artifacts[1].Status != "skipped" {
		t.Errorf("artifact[1].Status = %q, want skipped", decoded.Artifacts[1].Status)
	}
	if decoded.Artifacts[1].Requirement != ArtifactRequirementOptional {
		t.Errorf("artifact[1].Requirement = %v, want %v", decoded.Artifacts[1].Requirement, ArtifactRequirementOptional)
	}

	// Verify no Path or SizeBytes fields leak
	rawJSON := string(data)
	if strings.Contains(rawJSON, "\"path\"") {
		t.Error("UploadedManifest JSON should not contain 'path' field")
	}
	if strings.Contains(rawJSON, "\"size_bytes\"") {
		t.Error("UploadedManifest JSON should not contain 'size_bytes' field")
	}
}
