package capabilities

import (
	"reflect"
	"testing"
)

func TestSoundeffectCapabilityDeps_IsNarrowTypedBundle(t *testing.T) {
	typ := reflect.TypeOf(SoundeffectCapabilityDeps{})
	want := []string{"ClipsRepo", "MetaWriter", "Publisher", "Dispatcher"}
	if typ.NumField() != len(want) {
		t.Fatalf("SoundeffectCapabilityDeps has %d fields, want %d", typ.NumField(), len(want))
	}
	for i, name := range want {
		if got := typ.Field(i).Name; got != name {
			t.Fatalf("SoundeffectCapabilityDeps field %d is %q, want %q", i, got, name)
		}
	}
}

func TestBuildSoundeffectBundle_RejectsNilConfig(t *testing.T) {
	if _, err := buildSoundeffectBundle(buildSoundeffectParams{}); err == nil {
		t.Fatal("buildSoundeffectBundle must fail closed when config is nil")
	}
}

func TestBuildSoundeffectParams_DoesNotAcceptAssetsModuleDeps(t *testing.T) {
	typ := reflect.TypeOf(buildSoundeffectParams{})
	if _, ok := typ.FieldByName("Deps"); ok {
		t.Fatal("buildSoundeffectParams must not reintroduce the broad AssetsModuleDeps field")
	}
	field, ok := typ.FieldByName("Soundeffect")
	if !ok {
		t.Fatal("buildSoundeffectParams must expose the typed soundeffect capability bundle")
	}
	if field.Type != reflect.TypeOf(SoundeffectCapabilityDeps{}) {
		t.Fatalf("buildSoundeffectParams.Soundeffect has type %v, want %v", field.Type, reflect.TypeOf(SoundeffectCapabilityDeps{}))
	}
}
