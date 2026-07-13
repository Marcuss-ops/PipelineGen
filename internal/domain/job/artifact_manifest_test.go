// Package job — artifact_manifest_test.go (Creator Blocco 2.1, July 2026).
//
// Round-trip + validation tests for ArtifactManifest.
package job

import (
	"encoding/json"
	"errors"
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
	// FASE 1 close-out typed-error contract: nil receiver MUST
	// surface the typed job.ErrArtifactManifestInvalid sentinel.
	if !errors.Is(err, ErrArtifactManifestInvalid) {
		t.Errorf("nil receiver should wrap ErrArtifactManifestInvalid, got %T: %v", err, err)
	}
}

// TestArtifactManifest_Validate_EmptySchemaVersion_TypedSentinel pins
// the FASE 1 close-out typed-error contract on the schema_version
// branch. The pre-FASE-1 raw-error format is now wrapped with
// ErrArtifactManifestInvalid.
func TestArtifactManifest_Validate_EmptySchemaVersion_TypedSentinel(t *testing.T) {
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
	if !errors.Is(err, ErrArtifactManifestInvalid) {
		t.Errorf("empty schema_version should wrap ErrArtifactManifestInvalid, got %T: %v", err, err)
	}
}

// TestArtifactManifest_ToRemote_NilManifest_TypedSentinel pins the
// FASE 1 close-out typed-error contract on the nil-receiver branch
// of ToRemote. The pre-FASE-1 raw-error format is now wrapped with
// ErrArtifactManifestInvalid.
func TestArtifactManifest_ToRemote_NilManifest_TypedSentinel(t *testing.T) {
	var m *ArtifactManifest
	result, err := m.ToRemote(nil)
	if err == nil {
		t.Fatal("ToRemote(nil receiver) should return error")
	}
	if result != nil {
		t.Errorf("ToRemote(nil receiver) should return nil RemoteArtifactManifest, got %+v", result)
	}
	if !errors.Is(err, ErrArtifactManifestInvalid) {
		t.Errorf("nil receiver should wrap ErrArtifactManifestInvalid, got %T: %v", err, err)
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
	// FASE 1 close-out: the required-but-empty-path case MUST
	// surface the typed job.ErrRequiredArtifactMissing sentinel
	// so producer-side callers (extractStagedArtifacts + the
	// publisher-side CompleteWithArtifacts spine) can branch
	// via errors.Is without string-matching.
	if !errors.Is(err, ErrRequiredArtifactMissing) {
		t.Errorf("error should wrap ErrRequiredArtifactMissing, got %T: %v", err, err)
	}
	// The job-package alias for the finalization canonical sentinel
	// must resolve identically (same pointer per godlike/06 SSOT
	// re-export contract).
	if !errors.Is(err, ErrRequiredArtifactMissing) {
		t.Errorf("error should wrap job.ErrRequiredArtifactMissing alias, got %T: %v", err, err)
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

// ── WithRemoteLocations ──────────────────────────────────────────────

func TestWithRemoteLocations_AllReady(t *testing.T) {
	m := &ArtifactManifest{
		SchemaVersion: SchemaVersionArtifactManifestV1,
		WorkflowID:    "wf_1",
		JobID:         "job_1",
		Artifacts: []Artifact{
			{ID: "job_1:script", Kind: ArtifactKindScriptJSON, Filename: "script.json", MIMEType: "application/json", SHA256: "abc123", Required: true},
			{ID: "job_1:voiceover:it", Kind: ArtifactKindVoiceover, Filename: "voiceover-it.mp3", MIMEType: "audio/mpeg", SHA256: "def456", Required: true},
		},
	}
	uploaded := map[string]RemoteAsset{
		"job_1:script":       {RemoteAssetID: "asset_789", SHA256: "abc123"},
		"job_1:voiceover:it": {RemoteAssetID: "asset_790", SHA256: "def456"},
	}
	result, err := m.WithRemoteLocations(uploaded)
	if err != nil {
		t.Fatalf("WithRemoteLocations: %v", err)
	}
	if len(result.Artifacts) != 2 {
		t.Fatalf("artifact count = %d, want 2", len(result.Artifacts))
	}

	// First artefact: ready
	if result.Artifacts[0].RemoteAssetID != "asset_789" {
		t.Errorf("artifact[0].RemoteAssetID = %q, want asset_789", result.Artifacts[0].RemoteAssetID)
	}
	if result.Artifacts[0].Status != "ready" {
		t.Errorf("artifact[0].Status = %q, want ready", result.Artifacts[0].Status)
	}

	// Second artefact: ready
	if result.Artifacts[1].RemoteAssetID != "asset_790" {
		t.Errorf("artifact[1].RemoteAssetID = %q, want asset_790", result.Artifacts[1].RemoteAssetID)
	}
	if result.Artifacts[1].Status != "ready" {
		t.Errorf("artifact[1].Status = %q, want ready", result.Artifacts[1].Status)
	}

	// Verify no local paths leak
	data, _ := json.Marshal(result)
	if strings.Contains(string(data), "/tmp/") {
		t.Error("UploadedManifest contains local paths — must not leak")
	}
}

func TestWithRemoteLocations_RequiredMissing_Error(t *testing.T) {
	m := &ArtifactManifest{
		SchemaVersion: SchemaVersionArtifactManifestV1,
		Artifacts: []Artifact{
			{ID: "job_1:script", Kind: ArtifactKindScriptJSON, Filename: "script.json", Required: true},
		},
	}
	// Script artefact is required but not in the uploaded map.
	_, err := m.WithRemoteLocations(map[string]RemoteAsset{})
	if err == nil {
		t.Fatal("expected error for required artefact not uploaded")
	}
	if !strings.Contains(err.Error(), "required but was not uploaded") {
		t.Errorf("error should mention required + not uploaded, got: %v", err)
	}
}

func TestWithRemoteLocations_NonRequiredSkipped(t *testing.T) {
	m := &ArtifactManifest{
		SchemaVersion: SchemaVersionArtifactManifestV1,
		Artifacts: []Artifact{
			{ID: "job_1:script", Kind: ArtifactKindScriptJSON, Filename: "script.json", Required: true, SHA256: "abc"},
			{ID: "job_1:image:0", Kind: ArtifactKindImage, Filename: "image.png", Required: false, SHA256: "def"},
		},
	}
	// Only the required script was uploaded; image is best-effort and missing.
	uploaded := map[string]RemoteAsset{
		"job_1:script": {RemoteAssetID: "asset_1", SHA256: "abc"},
	}
	result, err := m.WithRemoteLocations(uploaded)
	if err != nil {
		t.Fatalf("WithRemoteLocations: %v", err)
	}
	if len(result.Artifacts) != 2 {
		t.Fatalf("artifact count = %d, want 2", len(result.Artifacts))
	}
	if result.Artifacts[0].Status != "ready" {
		t.Errorf("required artifact should be ready, got %q", result.Artifacts[0].Status)
	}
	if result.Artifacts[1].Status != "skipped" {
		t.Errorf("non-required missing artifact should be skipped, got %q", result.Artifacts[1].Status)
	}
	if result.Artifacts[1].RemoteAssetID != "" {
		t.Errorf("skipped artifact should have empty RemoteAssetID, got %q", result.Artifacts[1].RemoteAssetID)
	}
}

func TestWithRemoteLocations_NilManifest(t *testing.T) {
	var m *ArtifactManifest
	_, err := m.WithRemoteLocations(nil)
	if err == nil {
		t.Fatal("expected error for nil manifest")
	}
}

func TestWithRemoteLocations_PreservesMetadata(t *testing.T) {
	m := &ArtifactManifest{
		SchemaVersion: SchemaVersionArtifactManifestV1,
		WorkflowID:    "wf_meta",
		JobID:         "job_meta",
		Artifacts: []Artifact{
			{ID: "x:script", Kind: ArtifactKindScriptJSON, Filename: "s.json", MIMEType: "application/json", SHA256: "sha", Required: true},
		},
	}
	uploaded := map[string]RemoteAsset{"x:script": {RemoteAssetID: "r1", SHA256: "sha"}}
	result, _ := m.WithRemoteLocations(uploaded)
	if result.SchemaVersion != m.SchemaVersion {
		t.Errorf("SchemaVersion = %q, want %q", result.SchemaVersion, m.SchemaVersion)
	}
	if result.WorkflowID != m.WorkflowID {
		t.Errorf("WorkflowID = %q, want %q", result.WorkflowID, m.WorkflowID)
	}
	if result.JobID != m.JobID {
		t.Errorf("JobID = %q, want %q", result.JobID, m.JobID)
	}
	if result.Artifacts[0].Kind != ArtifactKindScriptJSON {
		t.Errorf("Kind = %q, want %q", result.Artifacts[0].Kind, ArtifactKindScriptJSON)
	}
	if result.Artifacts[0].Filename != "s.json" {
		t.Errorf("Filename = %q, want s.json", result.Artifacts[0].Filename)
	}
	if result.Artifacts[0].MIMEType != "application/json" {
		t.Errorf("MIMEType = %q, want application/json", result.Artifacts[0].MIMEType)
	}
	if result.Artifacts[0].SHA256 != "sha" {
		t.Errorf("SHA256 = %q, want sha", result.Artifacts[0].SHA256)
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

// ── P0 Commit 5 (C5): LocalPath accessor + RemoteArtifactManifest alias ─

// TestArtifact_LocalPathAccessor pins the canonical accessor contract
// that the C5 spec introduces. The existing Artifact.Path field is
// preserved as the JSON tag (no wire-format regression); LocalPath()
// is the typed accessor that surfaces "this is the on-disk path" as
// a method and is INTENTIONALLY absent from the RemoteArtifact type
// (Sender-side types cannot expose a LocalPath by construction).
func TestArtifact_LocalPathAccessor(t *testing.T) {
	a := Artifact{
		ID:   "job_1:script",
		Kind: ArtifactKindScriptJSON,
		Path: "/tmp/pipelinegen/jobs/job_1/script.json",
	}
	if got := a.LocalPath(); got != a.Path {
		t.Errorf("LocalPath() = %q, want %q", got, a.Path)
	}
	// Empty path is also surfaced faithfully (no silent substitution).
	empty := Artifact{ID: "x"}
	if got := empty.LocalPath(); got != "" {
		t.Errorf("LocalPath() on empty-path Artifact = %q, want empty string", got)
	}
}

// ── P0 Commit 5 (C5): ToRemote canonical adapter ────────────────────

// TestToRemote_AllReady_SchemaVersionV1 is the happy-path round-trip:
// every required artefact has an uploaded entry, SchemaVersion is the
// canonical V1, and the returned RemoteArtifactManifest contains no
// LocalPath leak.
func TestToRemote_AllReady_SchemaVersionV1(t *testing.T) {
	m := &ArtifactManifest{
		SchemaVersion: SchemaVersionArtifactManifestV1,
		WorkflowID:    "wf_C5",
		JobID:         "job_C5",
		Artifacts: []Artifact{
			{
				ID: "job_C5:script", Kind: ArtifactKindScriptJSON,
				Path:     "/tmp/pipelinegen/jobs/job_C5/script.json",
				Filename: "script.json", MIMEType: "application/json",
				SHA256: "abc123", Required: true,
			},
			{
				ID: "job_C5:voiceover:it", Kind: ArtifactKindVoiceover,
				Path:     "/tmp/pipelinegen/jobs/job_C5/voiceover-it.mp3",
				Filename: "voiceover-it.mp3", MIMEType: "audio/mpeg",
				SHA256: "def456", Required: true,
			},
		},
	}
	uploaded := map[string]RemoteAssetIDAdapter{
		"job_C5:script":       {RemoteAssetID: "asset_C5_1", SHA256: "abc123"},
		"job_C5:voiceover:it": {RemoteAssetID: "asset_C5_2", SHA256: "def456"},
	}

	result, err := m.ToRemote(uploaded)
	if err != nil {
		t.Fatalf("ToRemote: %v", err)
	}
	if result == nil {
		t.Fatal("ToRemote returned nil RemoteArtifactManifest")
	}
	if result.SchemaVersion != SchemaVersionArtifactManifestV1 {
		t.Errorf("SchemaVersion = %q, want %q", result.SchemaVersion, SchemaVersionArtifactManifestV1)
	}
	if result.WorkflowID != "wf_C5" {
		t.Errorf("WorkflowID = %q, want wf_C5", result.WorkflowID)
	}
	if len(result.Artifacts) != 2 {
		t.Fatalf("artifact count = %d, want 2", len(result.Artifacts))
	}
	if result.Artifacts[0].RemoteAssetID != "asset_C5_1" {
		t.Errorf("Artifacts[0].RemoteAssetID = %q, want asset_C5_1", result.Artifacts[0].RemoteAssetID)
	}
	if result.Artifacts[0].Status != StatusReady {
		t.Errorf("Artifacts[0].Status = %q, want %q", result.Artifacts[0].Status, StatusReady)
	}
	if result.Artifacts[0].Requirement != ArtifactRequirementRequired {
		t.Errorf("Artifacts[0].Requirement = %v, want %v", result.Artifacts[0].Requirement, ArtifactRequirementRequired)
	}
	if result.Artifacts[1].RemoteAssetID != "asset_C5_2" {
		t.Errorf("Artifacts[1].RemoteAssetID = %q, want asset_C5_2", result.Artifacts[1].RemoteAssetID)
	}
	if result.Artifacts[1].Requirement != ArtifactRequirementRequired {
		t.Errorf("Artifacts[1].Requirement = %v, want %v", result.Artifacts[1].Requirement, ArtifactRequirementRequired)
	}

	// Raw-byte guard: no "/tmp/" substring anywhere in the remote
	// manifest's JSON serialisation (the canonical local-path leak
	// marker for /tmp/pipelinegen/jobs/<jobid>/...).
	data, _ := json.Marshal(result)
	if strings.Contains(string(data), "/tmp/") {
		t.Errorf("RemoteArtifactManifest JSON MUST NOT leak /tmp/ local paths; got: %s", string(data))
	}
}

// TestToRemote_SchemaVersionNotV1_Rejects locks the C5 gate: any
// SchemaVersion other than the canonical V1 is REJECTED at the
// ToRemote emit boundary. The sentinel error chain is asserted so
// callers can errors.Is(err, ErrRemoteSchemaVersionUnsupported).
func TestToRemote_SchemaVersionNotV1_Rejects(t *testing.T) {
	cases := []struct {
		name    string
		version string
	}{
		{"empty", ""},
		{"whitespace", "   "},
		{"v2_explicit", "pipelinegen.artifacts.v2"},
		{"random", "unknown-schema"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &ArtifactManifest{
				SchemaVersion: tc.version,
				Artifacts: []Artifact{
					{ID: "x", Kind: ArtifactKindScriptJSON, Required: true},
				},
			}
			uploaded := map[string]RemoteAssetIDAdapter{
				"x": {RemoteAssetID: "asset_x", SHA256: "sha"},
			}
			result, err := m.ToRemote(uploaded)
			if err == nil {
				t.Fatalf("SchemaVersion=%q should be rejected; got result=%+v", tc.version, result)
			}
			if !errors.Is(err, ErrRemoteSchemaVersionUnsupported) {
				t.Errorf("error should wrap ErrRemoteSchemaVersionUnsupported, got: %v", err)
			}
		})
	}
}

// TestToRemote_RequiredMissing_RejectsBeforeEmit locks the C5 invariant:
// a Required artefact that is NOT in the `uploaded` map causes ToRemote
// to return a non-nil error BEFORE emitting any RemoteArtifactManifest.
// The error message must identify the missing artefact so an operator can
// audit the failure.
func TestToRemote_RequiredMissing_RejectsBeforeEmit(t *testing.T) {
	m := &ArtifactManifest{
		SchemaVersion: SchemaVersionArtifactManifestV1,
		Artifacts: []Artifact{
			{
				ID: "job_M:script", Kind: ArtifactKindScriptJSON,
				Filename: "script.json", Required: true,
			},
		},
	}
	// Empty uploaded map — the required artefact is missing.
	result, err := m.ToRemote(map[string]RemoteAssetIDAdapter{})
	if err == nil {
		t.Fatalf("ToRemote should reject when required missing; got result=%+v", result)
	}
	if result != nil {
		t.Errorf("ToRemote should return nil RemoteArtifactManifest on rejection; got %+v", result)
	}
	if !strings.Contains(err.Error(), "required but was not uploaded") {
		t.Errorf("error should mention 'required but was not uploaded', got: %v", err)
	}
	if !strings.Contains(err.Error(), "job_M:script") {
		t.Errorf("error should mention the missing artefact ID; got: %v", err)
	}
	// FASE 1 close-out typed-error contract: the required-missing
	// ToRemote error MUST wrap the typed job.ErrRequiredArtifactMissing
	// sentinel so callers can errors.Is without string-matching.
	if !errors.Is(err, ErrRequiredArtifactMissing) {
		t.Errorf("error should wrap ErrRequiredArtifactMissing, got %T: %v", err, err)
	}
}

// TestToRemote_NonRequiredSkipped_StatusSkipped preserves the
// best-effort semantics: non-required artefacts not in `uploaded`
// are emitted with Status="skipped" (an empty RemoteAssetID).
func TestToRemote_NonRequiredSkipped_StatusSkipped(t *testing.T) {
	m := &ArtifactManifest{
		SchemaVersion: SchemaVersionArtifactManifestV1,
		Artifacts: []Artifact{
			{ID: "x:script", Kind: ArtifactKindScriptJSON, Filename: "s.json", Required: true, SHA256: "abc"},
			{ID: "x:image:0", Kind: ArtifactKindImage, Filename: "img.png", Required: false, SHA256: "def"},
		},
	}
	uploaded := map[string]RemoteAssetIDAdapter{
		"x:script": {RemoteAssetID: "asset_1", SHA256: "abc"},
		// x:image:0 intentionally missing (best-effort).
	}
	result, err := m.ToRemote(uploaded)
	if err != nil {
		t.Fatalf("ToRemote: %v", err)
	}
	if result.Artifacts[0].Status != StatusReady {
		t.Errorf("required artifact should be StatusReady, got %q", result.Artifacts[0].Status)
	}
	if result.Artifacts[1].Status != StatusSkipped {
		t.Errorf("non-required missing artifact should be StatusSkipped, got %q", result.Artifacts[1].Status)
	}
	if result.Artifacts[1].RemoteAssetID != "" {
		t.Errorf("StatusSkipped artifact should have empty RemoteAssetID, got %q", result.Artifacts[1].RemoteAssetID)
	}
}

// TestToRemote_NilManifest_ReturnsError pins the defensive nil-guard.
func TestToRemote_NilManifest_ReturnsError(t *testing.T) {
	var m *ArtifactManifest
	result, err := m.ToRemote(nil)
	if err == nil {
		t.Fatal("ToRemote(nil receiver) should return error")
	}
	if result != nil {
		t.Errorf("ToRemote(nil receiver) should return nil RemoteArtifactManifest, got %+v", result)
	}
}

// TestToRemote_PreservesMetadata asserts the per-artefact metadata
// (Kind / Filename / MIMEType / SHA256) survives the local→remote
// emit (no schema drift on non-locator fields).
func TestToRemote_PreservesMetadata(t *testing.T) {
	m := &ArtifactManifest{
		SchemaVersion: SchemaVersionArtifactManifestV1,
		WorkflowID:    "wf_meta_C5",
		JobID:         "job_meta_C5",
		Artifacts: []Artifact{
			{
				ID: "meta:script", Kind: ArtifactKindScriptJSON,
				Filename: "s.json", MIMEType: "application/json",
				SHA256: "sha_meta", Required: true,
			},
		},
	}
	uploaded := map[string]RemoteAssetIDAdapter{
		"meta:script": {RemoteAssetID: "r_meta", SHA256: "sha_meta"},
	}
	result, err := m.ToRemote(uploaded)
	if err != nil {
		t.Fatalf("ToRemote: %v", err)
	}
	if result.Artifacts[0].Kind != ArtifactKindScriptJSON {
		t.Errorf("Kind = %q, want %q", result.Artifacts[0].Kind, ArtifactKindScriptJSON)
	}
	if result.Artifacts[0].Filename != "s.json" {
		t.Errorf("Filename = %q, want s.json", result.Artifacts[0].Filename)
	}
	if result.Artifacts[0].MIMEType != "application/json" {
		t.Errorf("MIMEType = %q, want application/json", result.Artifacts[0].MIMEType)
	}
	if result.Artifacts[0].SHA256 != "sha_meta" {
		t.Errorf("SHA256 = %q, want sha_meta", result.Artifacts[0].SHA256)
	}
}

// TestRemoteArtifactManifest_JSON_NoLocalPathField is the C5 invariant
// at the JSON-bytes level: the RemoteArtifactManifest serialisation
// MUST NOT contain a LocalPath / path key on any artefact entry, and
// MUST NOT carry any /tmp/ substring (the canonical local-path leak
// marker for /tmp/pipelinegen/jobs/<jobid>/...).
//
// This is the canonical structural enforcement of the dual-type
// discipline (per P0 §4): the remote type has no LocalPath / Path
// field, so json.Marshal cannot serialise one even if a future
// contributor adds one by mistake (the field would have to be added
// at the type level first, which is reviewable — vs. relying on
// human discipline alone).
func TestRemoteArtifactManifest_JSON_NoLocalPathField(t *testing.T) {
	result := &RemoteArtifactManifest{
		SchemaVersion: SchemaVersionArtifactManifestV1,
		WorkflowID:    "wf_no_leak",
		JobID:         "job_no_leak",
		Artifacts: []RemoteArtifact{
			{
				ID: "no_leak:script", Kind: ArtifactKindScriptJSON,
				Filename: "script.json", MIMEType: "application/json",
				SHA256: "no_leak_sha", RemoteAssetID: "asset_no_leak",
				Status: StatusReady,
			},
		},
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	rawJSON := string(data)

	// Two checks: no /tmp/ (canonical local-path marker) AND no
	// "local_path" JSON key in any artefact entry.
	if strings.Contains(rawJSON, "/tmp/") {
		t.Errorf("RemoteArtifactManifest JSON MUST NOT contain '/tmp/' (the canonical local-path marker for /tmp/pipelinegen/jobs/); got: %s", rawJSON)
	}
	if strings.Contains(rawJSON, "\"local_path\"") {
		t.Errorf("RemoteArtifactManifest JSON MUST NOT contain 'local_path' key; got: %s", rawJSON)
	}
	if strings.Contains(rawJSON, "\"path\"") {
		t.Errorf("RemoteArtifactManifest JSON MUST NOT contain a top-level per-artefact 'path' key; got: %s", rawJSON)
	}
	if strings.Contains(rawJSON, "\"size_bytes\"") {
		t.Errorf("RemoteArtifactManifest JSON MUST NOT contain 'size_bytes' (sized by Sender on read); got: %s", rawJSON)
	}

	// Positive control: the structural identifiers ARE present.
	for _, must := range []string{
		"\"schema_version\"", "\"workflow_id\"", "\"job_id\"",
		"\"id\"", "\"kind\"", "\"filename\"", "\"mime_type\"",
		"\"sha256\"", "\"remote_asset_id\"", "\"status\"",
		"no_leak:script", "asset_no_leak", StatusReady,
	} {
		if !strings.Contains(rawJSON, must) {
			t.Errorf("RemoteArtifactManifest JSON should contain %q (positive control broken); got: %s", must, rawJSON)
		}
	}
}

// ── P0 Commit 5 (C5): cross-check the legacy alias still works ──────

// TestWithRemoteLocations_LegacyAlias delegates to ToRemote per the C5
// back-compat shape. Pre-C5 tests in TestWithRemoteLocations_* are
// preserved (existing assertions still pass); this test pins the
// explicit C5 behavioural contract for the legacy method.
func TestWithRemoteLocations_LegacyAlias_DelegatesToToRemote(t *testing.T) {
	m := &ArtifactManifest{
		SchemaVersion: SchemaVersionArtifactManifestV1,
		Artifacts: []Artifact{
			{ID: "x", Kind: ArtifactKindScriptJSON, Filename: "x.json", Required: true, SHA256: "x_sha"},
		},
	}
	uploaded := map[string]RemoteAsset{
		"x": {RemoteAssetID: "asset_x", SHA256: "x_sha"},
	}
	// The legacy entry point still works (no breakage).
	resultLegacy, errLegacy := m.WithRemoteLocations(uploaded)
	if errLegacy != nil {
		t.Fatalf("WithRemoteLocations: %v", errLegacy)
	}
	// The canonical C5 entry point produces an equivalent result.
	resultNew, errNew := m.ToRemote(uploaded)
	if errNew != nil {
		t.Fatalf("ToRemote: %v", errNew)
	}
	if len(resultLegacy.Artifacts) != 1 || resultLegacy.Artifacts[0].RemoteAssetID != "asset_x" {
		t.Errorf("legacy result shape unintended: %+v", resultLegacy)
	}
	if len(resultNew.Artifacts) != 1 || resultNew.Artifacts[0].RemoteAssetID != "asset_x" {
		t.Errorf("new result shape unintended: %+v", resultNew)
	}
}
