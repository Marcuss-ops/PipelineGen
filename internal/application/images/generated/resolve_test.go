package generated

import (
	"errors"
	"reflect"
	"testing"
)

func TestResolve_MissingID_ReturnsErrProviderNotFound(t *testing.T) {
	r := NewGenerationProviderRegistry(nil, nil)
	for _, id := range []string{"google-slides", "flux", "nvidia", "nonexistent-model"} {
		_, err := r.Resolve(id)
		if err == nil {
			t.Fatalf("Resolve(%q) err = nil; want ErrProviderNotFound", id)
		}
		if !errors.Is(err, ErrProviderNotFound) {
			t.Errorf("Resolve(%q) err = %v; want errors.Is(_, ErrProviderNotFound)", id, err)
		}
	}
}

func TestResolve_EmptyID_ReturnsErrProviderNotFound(t *testing.T) {
	r := NewGenerationProviderRegistry(nil, nil)
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
