// Package job — artifact_manifest_decode_test.go (split surface: Decode).
//
// Decode() input-shape variant tests: nil result, absent key, already-typed entry,
// JSON bytes, JSON string, map literal, unexpected scalar type, malformed JSON.
// Pure relocation from artifact_manifest_test.go; no behavior change.
package job

import (
	"encoding/json"
	"strings"
	"testing"
)

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
