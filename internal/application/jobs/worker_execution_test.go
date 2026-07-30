package jobs

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/remote"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

func TestExtractStagedArtifacts_HappyPath(t *testing.T) {
	// FASE 1 close-out: the happy-path fixture now sets Path on
	// every Required artefact so manifest.Validate() (gated inline
	// in extractStagedArtifacts per FASE 1 spec "the Required-empty-
	// path case → bloccare SUCCEEDED") accepts the manifest and
	// proceeds to the published-artifact projection. Pre-FASE-1
	// the short-circuit `manifest.Artifacts == 0 → []` reached
	// the projection unconditionally; the FASE 1 typed-error
	// gate short-circuits BEFORE Validate, so the projection
	// only runs against a Validate-passed manifest.
	manifest := &job.ArtifactManifest{
		SchemaVersion: job.SchemaVersionArtifactManifestV1,
		WorkflowID:    "wf-001",
		JobID:         "job_test_123:script_json",
		Artifacts: []job.Artifact{
			{
				ID:        "job_test_123:script_json",
				Kind:      job.ArtifactKindScriptJSON,
				Path:      "/tmp/pipelinegen/jobs/job_test_123/script.json",
				Filename:  "script.json",
				MIMEType:  "application/json",
				SizeBytes: 1024,
				SHA256:    "abc123",
				Required:  true,
			},
			{
				ID:        "job_test_123:script_text",
				Kind:      job.ArtifactKindScriptText,
				Path:      "/tmp/pipelinegen/jobs/job_test_123/script.txt",
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

	var artifacts remote.StagedArtifacts
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
	if a.Destination != "script" {
		t.Errorf("Destination: got %q, want script", a.Destination)
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
	if a.Path == "" || a.Filename != "script.json" || a.SizeBytes != 1024 {
		t.Errorf("local artifact projection incomplete: path=%q filename=%q size=%d", a.Path, a.Filename, a.SizeBytes)
	}

	// Source derived from job type prefix
	// Second artifact: optional
	b := artifacts[1]
	if b.Destination != "script" || b.Path == "" {
		t.Errorf("optional artifact projection incomplete: destination=%q path=%q", b.Destination, b.Path)
	}
}

func TestExtractStagedArtifacts_NilResult(t *testing.T) {
	// FASE 1 close-out typed-error contract: a nil handler result
	// surfaces job.ErrArtifactManifestMissing — the spec mandates
	// "il manifest è assente ... bloccare ... SUCCEEDED". The
	// pre-FASE-1 empty-pass behaviour is retired.
	raw, extractErr := extractStagedArtifacts(nil, "script.generate")
	if extractErr == nil {
		t.Fatalf("expected typed ErrArtifactManifestMissing for nil result, got nil err (raw=%s)", string(raw))
	}
	if !errors.Is(extractErr, job.ErrArtifactManifestMissing) {
		t.Fatalf("expected errors.Is(extractErr, ErrArtifactManifestMissing), got %T: %v", extractErr, extractErr)
	}
	if raw != nil {
		t.Fatalf("expected nil json.RawMessage on error path, got %s", string(raw))
	}
}

func TestExtractStagedArtifacts_NoManifestKey(t *testing.T) {
	// FASE 1 close-out typed-error contract: handler result without
	// __artifact_manifest key surfaces job.ErrArtifactManifestMissing.
	result := map[string]any{
		"some_other_key": "value",
		"data":           map[string]any{"score": 0.95},
	}

	raw, extractErr := extractStagedArtifacts(result, "image.generate.google")
	if extractErr == nil {
		t.Fatalf("expected typed ErrArtifactManifestMissing for absent __artifact_manifest key, got nil err (raw=%s)", string(raw))
	}
	if !errors.Is(extractErr, job.ErrArtifactManifestMissing) {
		t.Fatalf("expected errors.Is(extractErr, ErrArtifactManifestMissing), got %T: %v", extractErr, extractErr)
	}
	if raw != nil {
		t.Fatalf("expected nil json.RawMessage on error path, got %s", string(raw))
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

// TestExtractStagedArtifacts_DecodeFailure_TypedSentinel pins the
// FASE 1 close-out JSON-decode failure path. Renamed from
// TestExtractStagedArtifacts_MalformedManifest for clearer failure-
// attribution vs. the ValidateFailure_TypedSentinel below. The
// Decode-failure channel surfaces typed job.ErrArtifactManifestInvalid
// via the dual-%w form (godlike/06 SSOT: typed-sentinel wrap chained
// alongside the inner json error so errors.Is probes don't get a
// silent string-match devolution).
func TestExtractStagedArtifacts_DecodeFailure_TypedSentinel(t *testing.T) {
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
	if raw != nil {
		t.Fatalf("expected nil json.RawMessage on error path, got %s", string(raw))
	}
}

// TestExtractStagedArtifacts_ValidateFailure_TypedSentinel pins
// the FASE 1 close-out Validate-failure path. A manifest that
// DECODES cleanly (valid JSON, shape matches) but fails Validate
// (e.g. empty schema_version, empty id/kind) surfaces typed
// job.ErrArtifactManifestInvalid via the dual-%w form. The inner
// Validate-returned sentinel propagates via errors.Is chain
// traversal, so callers probing sub-mode sentinels (e.g. for
// required-empty-path) resolve identically.
//
// Distinct from TestExtractStagedArtifacts_DecodeFailure_TypedSentinel
// (which exercises JSON-decode failures) — both pin DIFFERENT
// failure channels of the FASE 1 typed-error contract.
func TestExtractStagedArtifacts_ValidateFailure_TypedSentinel(t *testing.T) {
	manifest := &job.ArtifactManifest{
		SchemaVersion: "", // empty schema_version violates Validate
		Artifacts: []job.Artifact{
			{
				ID: "x", Kind: job.ArtifactKindScriptJSON,
				Path: "/tmp/x", Filename: "x", Required: true,
			},
		},
	}

	result := map[string]any{
		job.ManifestKey: manifestToRawJSON(t, manifest),
	}

	raw, extractErr := extractStagedArtifacts(result, "script.generate")
	if extractErr == nil {
		t.Fatalf("expected typed ErrArtifactManifestInvalid for empty-schema_version manifest, got nil err (raw=%s)", string(raw))
	}
	if !errors.Is(extractErr, job.ErrArtifactManifestInvalid) {
		t.Fatalf("expected errors.Is(extractErr, ErrArtifactManifestInvalid) on Validate failure, got %T: %v", extractErr, extractErr)
	}
	if raw != nil {
		t.Fatalf("expected nil json.RawMessage on error path, got %s", string(raw))
	}
}

// TestExtractStagedArtifacts_RequiredMissingPath_ErrRequiredArtifactMissing
// pins the FASE 1 close-out typed-error chain: a handler that emits a
// syntactically-decodable manifest whose required-artifact entry has
// an empty Path fails the worker with the typed
// job.ErrArtifactManifestInvalid sentinel (the Validate-wrapped chain
// that ALSO surfaces job.ErrRequiredArtifactMissing via Go 1.20+
// dual-%w semantics). The worker path can errors.Is against either
// typed sentinel — the publisher-side
// domain/finalization.ErrRequiredArtifactMissing is reachable through
// the alias in internal/domain/job/artifact_errors.go.
func TestExtractStagedArtifacts_RequiredMissingPath_ErrRequiredArtifactMissing(t *testing.T) {
	manifest := &job.ArtifactManifest{
		SchemaVersion: job.SchemaVersionArtifactManifestV1,
		WorkflowID:    "wf_required_missing",
		JobID:         "job_required_missing",
		Artifacts: []job.Artifact{
			{
				ID:       "job_required_missing:script",
				Kind:     job.ArtifactKindScriptJSON,
				Path:     "", // EMPTY PATH for Required => invalid
				Filename: "script.json",
				Required: true,
			},
		},
	}

	result := map[string]any{
		job.ManifestKey: manifestToRawJSON(t, manifest),
	}

	raw, extractErr := extractStagedArtifacts(result, "script.generate")
	if extractErr == nil {
		t.Fatalf("expected typed ErrArtifactManifestInvalid for required-with-empty-path, got nil err (raw=%s)", string(raw))
	}
	if !errors.Is(extractErr, job.ErrArtifactManifestInvalid) {
		t.Fatalf("expected errors.Is(extractErr, ErrArtifactManifestInvalid) for missing-required-path case, got %T: %v", extractErr, extractErr)
	}
	if !errors.Is(extractErr, job.ErrRequiredArtifactMissing) {
		t.Fatalf("expected errors.Is(extractErr, ErrRequiredArtifactMissing) for missing-required-path case (dual-sentinel wrap), got %T: %v", extractErr, extractErr)
	}
	if raw != nil {
		t.Fatalf("expected nil json.RawMessage on error path, got %s", string(raw))
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
