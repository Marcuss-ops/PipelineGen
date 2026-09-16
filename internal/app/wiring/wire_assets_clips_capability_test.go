package wiring

import (
	"reflect"
	"testing"
)

func TestClipsCapabilityDeps_IsNarrowTypedBundle(t *testing.T) {
	typ := reflect.TypeOf(ClipsCapabilityDeps{})
	want := []string{
		"Repositories",
		"ArtifactService",
		"AssetTreeService",
		"MediaProcessor",
		"Publisher",
		"ClipIndexerService",
	}
	if typ.NumField() != len(want) {
		t.Fatalf("ClipsCapabilityDeps has %d fields, want %d", typ.NumField(), len(want))
	}
	for i, name := range want {
		if got := typ.Field(i).Name; got != name {
			t.Fatalf("ClipsCapabilityDeps field %d is %q, want %q", i, got, name)
		}
	}

	// MediaSearch (5th, P2-9 2026-09-16) is the media SSOT read port for the
	// ListClips text-search branch. It replaced the retired clipsRepo read of
	// the operational clip_search_terms index, so it is a media read port and
	// belongs in this bundle — not a new capability dependency.
	repos := reflect.TypeOf(ClipsRepositoryDeps{})
	wantRepos := []string{"ClipsRepo", "VoiceoverRepo", "ImageRepo", "AssetRepo", "MediaSearch"}
	if repos.NumField() != len(wantRepos) {
		t.Fatalf("ClipsRepositoryDeps has %d fields, want %d", repos.NumField(), len(wantRepos))
	}
	for i, name := range wantRepos {
		if got := repos.Field(i).Name; got != name {
			t.Fatalf("ClipsRepositoryDeps field %d is %q, want %q", i, got, name)
		}
	}
}

func TestBuildClipsParams_DoesNotAcceptAssetsModuleDeps(t *testing.T) {
	typ := reflect.TypeOf(buildClipsParams{})
	if _, ok := typ.FieldByName("Deps"); ok {
		t.Fatal("buildClipsParams must not reintroduce the broad AssetsModuleDeps field")
	}
	field, ok := typ.FieldByName("Clips")
	if !ok {
		t.Fatal("buildClipsParams must expose the typed Clips capability bundle")
	}
	if field.Type != reflect.TypeOf(ClipsCapabilityDeps{}) {
		t.Fatalf("buildClipsParams.Clips has type %v, want %v", field.Type, reflect.TypeOf(ClipsCapabilityDeps{}))
	}
}
