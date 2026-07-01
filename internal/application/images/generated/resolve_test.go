package generated

import (
	"errors"
	"reflect"
	"testing"
)

func TestResolve_KnownID_ReturnsProvider(t *testing.T) {
	r := NewGenerationProviderRegistry(nil, []GenerationProvider{
		NewGoogleSlidesProvider(nil, nil),
	})
	got, err := r.Resolve("google-slides")
	if err != nil {
		t.Errorf("Resolve(google-slides) err = %v; want nil", err)
	}
	if got == nil {
		t.Fatalf("Resolve(google-slides) = nil; want google-slides provider")
	}
}

func TestResolve_MissingID_ReturnsErrProviderNotFound(t *testing.T) {
	r := NewGenerationProviderRegistry(nil, nil)
	_, err := r.Resolve("nonexistent-model")
	if err == nil {
		t.Fatalf("Resolve(nonexistent-model) err = nil; want ErrProviderNotFound")
	}
	if !errors.Is(err, ErrProviderNotFound) {
		t.Errorf("Resolve(nonexistent-model) err = %v; want errors.Is(_, ErrProviderNotFound)", err)
	}
}

func TestResolve_EmptyID_ReturnsErrProviderNotFound(t *testing.T) {
	r := NewGenerationProviderRegistry(nil, []GenerationProvider{
		NewGoogleSlidesProvider(nil, nil),
	})
	_, err := r.Resolve("")
	if err == nil {
		t.Fatalf("Resolve(\"\") err = nil; want ErrProviderNotFound")
	}
	if !errors.Is(err, ErrProviderNotFound) {
		t.Errorf("Resolve(\"\") err = %v; want errors.Is(_, ErrProviderNotFound)", err)
	}
}

func TestTypeSystem_Provider_AliasMatchesGenerationProvider(t *testing.T) {
	pType := reflect.TypeOf((*Provider)(nil)).Elem()
	gType := reflect.TypeOf((*GenerationProvider)(nil)).Elem()
	if pType != gType {
		t.Errorf("alias broken: Provider (%v) != GenerationProvider (%v)", pType, gType)
	}
}
