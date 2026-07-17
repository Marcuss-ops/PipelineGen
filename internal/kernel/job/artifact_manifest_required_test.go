// Package job — artifact_manifest_required_test.go (split surface: RequiredArtifacts).
//
// RequiredArtifacts() filter tests: regular, nil-receiver guard, all-non-required
// short-circuit. Pure relocation from artifact_manifest_test.go; no behavior change.
package job

import (
	"testing"
)

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
