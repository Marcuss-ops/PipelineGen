package app

import (
	"reflect"
	"testing"
)

func TestClipsCapabilityDeps_IsNarrowTypedBundle(t *testing.T) {
	typ := reflect.TypeOf(ClipsCapabilityDeps{})
	want := []string{
		"ClipsRepo",
		"VoiceoverRepo",
		"ImageRepo",
		"AssetRepo",
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
