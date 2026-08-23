// Package job — artifact_manifest_validate_test.go (split surface: Validate).
//
// Validate() contract tests on ArtifactManifest: nil-receiver guard, schema_version
// required, non-empty artifact list, required-artifact path presence, ID and kind
// non-empty, filename/path coherence. Pure relocation from artifact_manifest_test.go;
// no behavior change.
package job

import (
	"errors"
	"strings"
	"testing"

	finalization "github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
)

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
	// Verify the godlike/06 SSOT re-export contract: the alias
	// chain finalization.ErrRequiredArtifactMissing →
	// artifact_errors → job.ErrRequiredArtifactMissing MUST hold
	// pointer-identity (the second errors.Is against the canonical
	// from a different package would surface any future divergence
	// in the alias re-export).
	if !errors.Is(err, finalization.ErrRequiredArtifactMissing) {
		t.Errorf("error should also satisfy errors.Is against the canonical finalization.ErrRequiredArtifactMissing (godlike/06 SSOT contract), got %T: %v", err, err)
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
