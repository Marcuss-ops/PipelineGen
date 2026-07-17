// Package job — artifact_manifest_accessor_test.go (split surface: P0 Commit 5 (C5) LocalPath accessor).
//
// Single-method accessor contract test for Artifact.LocalPath.
// Pure relocation from artifact_manifest_test.go; no behavior change.
package job

import (
	"testing"
)

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
