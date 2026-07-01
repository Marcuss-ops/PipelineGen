// Package job — artifact_manifest_test.go (Creator Blocco 2.1, July 2026).
//
// Round-trip + validation tests for ArtifactManifest.
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

// ── Validate ─────────────────────────────────────────────────────────

func TestArtifactManifest_Validate_Valid(t *testing.T) {
	m := &ArtifactManifest{
		SchemaVersion: SchemaVersionArtifactManifestV1,
		WorkflowID:    "wf_1",
		JobID:         "job_1",
		Artifacts: []Artifact{
			{
				ID: "job_1:script", Kind: ArtifactKindScriptJSON,
				Path: "/tmp/script.json", Filename: "script.json",
				MIMEType: "application/json", Required: true,
			},
		},
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestArtifactManifest_Validate_Nil(t *testing.T) {
	var m *ArtifactManifest
	err := m.Validate()
	if err == nil {
		t.Fatal("expected error for nil manifest")
	}
}

func TestArtifactManifest_Validate_EmptySchemaVersion(t *testing.T) {
	m := &ArtifactManifest{
		SchemaVersion: "",
		Artifacts: []Artifact{
			{ID: "x", Kind: ArtifactKindScriptJSON, Path: "/x", Filename: "x", Required: true},
		},
	}
	err := m.Validate()
	if err == nil {
		t.Fatal("expected error for empty schema_version")
	}
	if !strings.Contains(err.Error(), "schema_version") {
		t.Errorf("error should mention schema_version, got: %v", err)
	}
}

func TestArtifactManifest_Validate_WhitespaceSchemaVersion(t *testing.T) {
	m := &ArtifactManifest{
		SchemaVersion: "   ",
		Artifacts: []Artifact{
			{ID: "x", Kind: ArtifactKindScriptJSON, Path: "/x", Filename: "x", Required: true},
		},
	}
	err := m.Validate()
	if err == nil {
		t.Fatal("expected error for whitespace-only schema_version")
	}
}

func TestArtifactManifest_Validate_ZeroArtifacts(t *testing.T) {
	m := &ArtifactManifest{
		SchemaVersion: SchemaVersionArtifactManifestV1,
		Artifacts:     []Artifact{},
	}
	err := m.Validate()
	if err == nil {
		t.Fatal("expected error for zero artifacts")
	}
	if !strings.Contains(err.Error(), "zero artifacts") {
		t.Errorf("error should mention zero artifacts, got: %v", err)
	}
}

func TestArtifactManifest_Validate_RequiredArtifactEmptyPath(t *testing.T) {
	m := &ArtifactManifest{
		SchemaVersion: SchemaVersionArtifactManifestV1,
		Artifacts: []Artifact{
			{
				ID: "job:script", Kind: ArtifactKindScriptJSON,
				Path: "", Filename: "script.json",
				Required: true,
			},
		},
	}
	err := m.Validate()
	if err == nil {
		t.Fatal("expected error for required artifact with empty path")
	}
	if !strings.Contains(err.Error(), "required but path is empty") {
		t.Errorf("error should mention required + path, got: %v", err)
	}
}

func TestArtifactManifest_Validate_NonRequiredArtifactEmptyPath_OK(t *testing.T) {
	m := &ArtifactManifest{
		SchemaVersion: SchemaVersionArtifactManifestV1,
		Artifacts: []Artifact{
			{
				ID: "job:image", Kind: ArtifactKindImage,
				Path: "", Filename: "image.png", // best-effort, may not exist
				Required: false,
			},
			{
				ID: "job:script", Kind: ArtifactKindScriptJSON,
				Path: "/tmp/script.json", Filename: "script.json",
				Required: true,
			},
		},
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil (non-required empty path is ok)", err)
	}
}

func TestArtifactManifest_Validate_PathSetButFilenameEmpty(t *testing.T) {
	m := &ArtifactManifest{
		SchemaVersion: SchemaVersionArtifactManifestV1,
		Artifacts: []Artifact{
			{
				ID: "job:script", Kind: ArtifactKindScriptJSON,
				Path: "/tmp/script.json", Filename: "",
				Required: true,
			},
		},
	}
	err := m.Validate()
	if err == nil {
		t.Fatal("expected error when path set but filename empty")
	}
	if !strings.Contains(err.Error(), "filename is empty") {
		t.Errorf("error should mention filename, got: %v", err)
	}
}

func TestArtifactManifest_Validate_EmptyID(t *testing.T) {
	m := &ArtifactManifest{
		SchemaVersion: SchemaVersionArtifactManifestV1,
		Artifacts: []Artifact{
			{
				ID: "", Kind: ArtifactKindScriptJSON,
				Path: "/tmp/x", Filename: "x", Required: true,
			},
		},
	}
	err := m.Validate()
	if err == nil {
		t.Fatal("expected error for empty artifact ID")
	}
	if !strings.Contains(err.Error(), "id is empty") {
		t.Errorf("error should mention empty id, got: %v", err)
	}
}

func TestArtifactManifest_Validate_EmptyKind(t *testing.T) {
	m := &ArtifactManifest{
		SchemaVersion: SchemaVersionArtifactManifestV1,
		Artifacts: []Artifact{
			{
				ID: "job:x", Kind: "",
				Path: "/tmp/x", Filename: "x", Required: true,
			},
		},
	}
	err := m.Validate()
	if err == nil {
		t.Fatal("expected error for empty kind")
	}
	if !strings.Contains(err.Error(), "kind is empty") {
		t.Errorf("error should mention empty kind, got: %v", err)
	}
}

// ── RequiredArtifacts ─────────────────────────────────────────────────

func TestArtifactManifest_RequiredArtifacts(t *testing.T) {
	m := &ArtifactManifest{
		SchemaVersion: SchemaVersionArtifactManifestV1,
		Artifacts: []Artifact{
			{ID: "a", Kind: ArtifactKindScriptJSON, Required: true},
			{ID: "b", Kind: ArtifactKindVoiceover, Required: true},
			{ID: "c", Kind: ArtifactKindImage, Required: false},
			{ID: "d", Kind: ArtifactKindImage, Required: false},
		},
	}
	required := m.RequiredArtifacts()
	if len(required) != 2 {
		t.Fatalf("RequiredArtifacts length = %d, want 2", len(required))
	}
	if required[0].ID != "a" || required[1].ID != "b" {
		t.Errorf("RequiredArtifacts IDs = %v, want [a b]", []string{required[0].ID, required[1].ID})
	}
}

func TestArtifactManifest_RequiredArtifacts_NilManifest(t *testing.T) {
	var m *ArtifactManifest
	required := m.RequiredArtifacts()
	if required != nil {
		t.Errorf("RequiredArtifacts on nil manifest should return nil, got %v", required)
	}
}

func TestArtifactManifest_RequiredArtifacts_AllNonRequired(t *testing.T) {
	m := &ArtifactManifest{
		SchemaVersion: SchemaVersionArtifactManifestV1,
		Artifacts: []Artifact{
			{ID: "a", Kind: ArtifactKindImage, Required: false},
		},
	}
	required := m.RequiredArtifacts()
	if len(required) != 0 {
		t.Fatalf("RequiredArtifacts length = %d, want 0", len(required))
	}
}

// ── Decode ────────────────────────────────────────────────────────────

func TestDecode_NilResult(t *testing.T) {
	m, err := Decode(nil)
	if err != nil {
		t.Fatalf("Decode(nil): %v", err)
	}
	if m != nil {
		t.Errorf("Decode(nil) should return nil manifest, got %v", m)
	}
}

func TestDecode_AbsentKey(t *testing.T) {
	result := map[string]any{"other": "value"}
	m, err := Decode(result)
	if err != nil {
		t.Fatalf("Decode(absent key): %v", err)
	}
	if m != nil {
		t.Errorf("Decode(absent key) should return nil, got %v", m)
	}
}

func TestDecode_AlreadyTyped(t *testing.T) {
	original := &ArtifactManifest{
		SchemaVersion: SchemaVersionArtifactManifestV1,
		Artifacts:     []Artifact{{ID: "x", Kind: ArtifactKindScriptJSON}},
	}
	result := map[string]any{ManifestKey: original}
	m, err := Decode(result)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if m != original {
		t.Errorf("Decode(*ArtifactManifest) should return same pointer")
	}
}

func TestDecode_JSONBytes(t *testing.T) {
	original := &ArtifactManifest{
		SchemaVersion: SchemaVersionArtifactManifestV1,
		WorkflowID:    "wf_1",
		Artifacts:     []Artifact{{ID: "x", Kind: ArtifactKindScriptJSON}},
	}
	data, _ := json.Marshal(original)
	result := map[string]any{ManifestKey: data}
	m, err := Decode(result)
	if err != nil {
		t.Fatalf("Decode(json.RawMessage): %v", err)
	}
	if m.SchemaVersion != original.SchemaVersion {
		t.Errorf("SchemaVersion = %q, want %q", m.SchemaVersion, original.SchemaVersion)
	}
	if m.WorkflowID != original.WorkflowID {
		t.Errorf("WorkflowID = %q, want %q", m.WorkflowID, original.WorkflowID)
	}
}

func TestDecode_JSONString(t *testing.T) {
	original := &ArtifactManifest{
		SchemaVersion: SchemaVersionArtifactManifestV1,
		Artifacts:     []Artifact{{ID: "y", Kind: ArtifactKindVoiceover}},
	}
	data, _ := json.Marshal(original)
	result := map[string]any{ManifestKey: string(data)}
	m, err := Decode(result)
	if err != nil {
		t.Fatalf("Decode(string): %v", err)
	}
	if len(m.Artifacts) != 1 {
		t.Fatalf("artifact count = %d, want 1", len(m.Artifacts))
	}
	if m.Artifacts[0].ID != "y" {
		t.Errorf("artifact[0].ID = %q, want %q", m.Artifacts[0].ID, "y")
	}
}

func TestDecode_MapLiteral(t *testing.T) {
	result := map[string]any{
		ManifestKey: map[string]any{
			"schema_version": SchemaVersionArtifactManifestV1,
			"workflow_id":    "wf_map",
			"artifacts": []any{
				map[string]any{
					"id":       "z",
					"kind":     ArtifactKindMetadata,
					"required": true,
				},
			},
		},
	}
	m, err := Decode(result)
	if err != nil {
		t.Fatalf("Decode(map): %v", err)
	}
	if m.WorkflowID != "wf_map" {
		t.Errorf("WorkflowID = %q, want %q", m.WorkflowID, "wf_map")
	}
	if len(m.Artifacts) != 1 || m.Artifacts[0].ID != "z" {
		t.Errorf("artifacts = %v, want [z]", m.Artifacts)
	}
}

func TestDecode_UnexpectedType(t *testing.T) {
	result := map[string]any{ManifestKey: 42} // int, not supported
	_, err := Decode(result)
	if err == nil {
		t.Fatal("expected error for unexpected type")
	}
	if !strings.Contains(err.Error(), "unexpected type") {
		t.Errorf("error should mention unexpected type, got: %v", err)
	}
}

func TestDecode_MalformedJSON(t *testing.T) {
	result := map[string]any{ManifestKey: "{not valid json"}
	_, err := Decode(result)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}
