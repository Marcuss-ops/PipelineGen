package jobs

import "testing"

// TestCompose_CompletionPolicyHasOneCanonicalProjection pins the load-bearing
// contract used by both the worker and SQLite completion gate: the typed
// ProducesArtifacts accessor and its map projection must describe exactly the
// same registered job set.
func TestCompose_CompletionPolicyHasOneCanonicalProjection(t *testing.T) {
	t.Parallel()

	reg := Compose()
	artifactTypes := reg.ProducesArtifactsMap()
	for _, jobType := range reg.AllTypes() {
		want := reg.ProducesArtifacts(jobType)
		_, got := artifactTypes[jobType]
		if want != got {
			t.Fatalf("completion policy drift for %q: ProducesArtifacts=%t, map membership=%t", jobType, want, got)
		}
	}
}

// TestCompose_CompletionPolicyPinsCanonicalOwners documents the intentional
// split between jobs whose worker completion owns artifact finalization and
// jobs whose application finalizer owns the artifact transaction.
func TestCompose_CompletionPolicyPinsCanonicalOwners(t *testing.T) {
	t.Parallel()

	reg := Compose()
	cases := []struct {
		name string
		typ  string
		want bool
	}{
		{name: "stock uses JobFinalizer spine", typ: TypeMediaStock, want: true},
		{name: "script parent uses JobFinalizer spine", typ: TypeScriptGenerate, want: true},
		{name: "voiceover batch uses application finalizer", typ: TypeVoiceoverBatch, want: false},
		{name: "voiceover child uses application finalizer", typ: TypeVoiceoverGenerateItem, want: false},
		{name: "youtube clip uses application finalizer", typ: TypeYouTubeClipExtract, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry, ok := reg.Get(tc.typ)
			if !ok {
				t.Fatalf("job type %q is not registered", tc.typ)
			}
			if entry.ProducesArtifacts != tc.want {
				t.Fatalf("job type %q ProducesArtifacts=%t, want %t", tc.typ, entry.ProducesArtifacts, tc.want)
			}
		})
	}
}
