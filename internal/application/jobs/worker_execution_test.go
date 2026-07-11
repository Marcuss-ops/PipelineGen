package jobs

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

func TestExtractStagedArtifacts_HappyPath(t *testing.T) {
	manifest := &job.ArtifactManifest{
		SchemaVersion: job.SchemaVersionArtifactManifestV1,
		WorkflowID:    "wf-001",
		JobID:         "job_test_123:script_json",
		Artifacts: []job.Artifact{
			{
				ID:        "job_test_123:script_json",
				Kind:      job.ArtifactKindScriptJSON,
				Filename:  "script.json",
				MIMEType:  "application/json",
				SizeBytes: 1024,
				SHA256:    "abc123",
				Required:  true,
			},
			{
				ID:        "job_test_123:script_text",
				Kind:      job.ArtifactKindScriptText,
				Filename:  "script.txt",
				MIMEType:  "text/plain",
				SizeBytes: 512,
				SHA256:    "def456",
				Required:  false,
			},
		},
	}

	result := map[string]any{
		job.ManifestKey: manifestToRawJSON(t, manifest),
	}

	raw, extractErr := extractStagedArtifacts(result, "script.generate")
	if extractErr != nil {
		t.Fatalf("expected nil err for valid manifest, got %v", extractErr)
	}

	if string(raw) == "[]" {
		t.Fatal("expected non-empty staged artifacts for a valid manifest")
	}

	var artifacts []finalization.PublishedArtifact
	if err := json.Unmarshal(raw, &artifacts); err != nil {
		t.Fatalf("failed to unmarshal staged artifacts: %v", err)
	}

	if len(artifacts) != 2 {
		t.Fatalf("expected 2 artifacts, got %d", len(artifacts))
	}

	// First artifact: required
	a := artifacts[0]
	if a.ArtifactID != "job_test_123:script_json" {
		t.Errorf("ArtifactID: got %q, want %q", a.ArtifactID, "job_test_123:script_json")
	}
	if a.Kind != finalization.ArtifactKind(job.ArtifactKindScriptJSON) {
		t.Errorf("Kind: got %q, want %q", a.Kind, job.ArtifactKindScriptJSON)
	}
	if a.Filename != "script.json" {
		t.Errorf("Filename: got %q, want %q", a.Filename, "script.json")
	}
	if a.MIMEType != "application/json" {
		t.Errorf("MIMEType: got %q, want %q", a.MIMEType, "application/json")
	}
	if a.SizeBytes != 1024 {
		t.Errorf("SizeBytes: got %d, want %d", a.SizeBytes, 1024)
	}
	if a.SHA256 != "abc123" {
		t.Errorf("SHA256: got %q, want %q", a.SHA256, "abc123")
	}
	if a.Requirement != finalization.ArtifactRequirementRequired {
		t.Errorf("Requirement: got %v, want Required", a.Requirement)
	}
	if a.IdempotencyKey != "job_test_123:script_json" {
		t.Errorf("IdempotencyKey: got %q, want %q", a.IdempotencyKey, "job_test_123:script_json")
	}

	// Source derived from job type prefix
	if a.Source != "script" {
		t.Errorf("Source: got %q, want %q", a.Source, "script")
	}

	// Second artifact: optional
	b := artifacts[1]
	if b.Requirement != finalization.ArtifactRequirementOptional {
		t.Errorf("Requirement: got %v, want Optional", b.Requirement)
	}
	if b.IdempotencyKey != "job_test_123:script_text" {
		t.Errorf("IdempotencyKey: got %q, want %q", b.IdempotencyKey, "job_test_123:script_text")
	}
}

func TestExtractStagedArtifacts_NilResult(t *testing.T) {
	raw, extractErr := extractStagedArtifacts(nil, "script.generate")
	if extractErr != nil {
		t.Fatalf("expected nil err for nil result, got %v", extractErr)
	}

	if string(raw) != "[]" {
		t.Fatalf("expected empty array for nil result, got %s", string(raw))
	}
}

func TestExtractStagedArtifacts_NoManifestKey(t *testing.T) {
	result := map[string]any{
		"some_other_key": "value",
		"data":           map[string]any{"score": 0.95},
	}

	raw, extractErr := extractStagedArtifacts(result, "image.generate.google")
	if extractErr != nil {
		t.Fatalf("expected nil err when __artifact_manifest key is absent (back-compat: empty result OK), got %v", extractErr)
	}

	if string(raw) != "[]" {
		t.Fatalf("expected empty array when __artifact_manifest key is absent, got %s", string(raw))
	}
}

func TestExtractStagedArtifacts_EmptyArtifactsList(t *testing.T) {
	manifest := &job.ArtifactManifest{
		SchemaVersion: job.SchemaVersionArtifactManifestV1,
		WorkflowID:    "wf-001",
		JobID:         "job_test_empty",
		Artifacts:     []job.Artifact{},
	}

	result := map[string]any{
		job.ManifestKey: manifestToRawJSON(t, manifest),
	}

	raw, extractErr := extractStagedArtifacts(result, "script.generate")
	if extractErr != nil {
		t.Fatalf("expected nil err for empty manifest (back-compat: empty result OK), got %v", extractErr)
	}

	if string(raw) != "[]" {
		t.Fatalf("expected empty array for manifest with zero artifacts, got %s", string(raw))
	}
}

func TestExtractStagedArtifacts_MalformedManifest(t *testing.T) {
	// FASE 1 (c) — typed-error contract: a malformed manifest
	// surfaces job.ErrArtifactManifestInvalid (the typed sentinel)
	// instead of the legacy silent-drop `[]` fallback. The worker
	// fails the job on this path so a malformed manifest can never
	// silently reach SUCCEEDED.
	result := map[string]any{
		job.ManifestKey: "not-valid-json",
	}

	raw, extractErr := extractStagedArtifacts(result, "books.process")
	if extractErr == nil {
		t.Fatalf("expected typed ErrArtifactManifestInvalid for malformed manifest, got nil err (raw=%s)", string(raw))
	}
	if !errors.Is(extractErr, job.ErrArtifactManifestInvalid) {
		t.Fatalf("expected errors.Is(extractErr, ErrArtifactManifestInvalid), got %T: %v", extractErr, extractErr)
	}
}

// manifestToRawJSON marshals a manifest to json.RawMessage for use as a
// handler result value. Uses json.Marshal so job.Decode recognises it as
// a []byte payload and unmarshals it back into *ArtifactManifest.
func manifestToRawJSON(t *testing.T, m *job.ArtifactManifest) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	return json.RawMessage(b)
}
