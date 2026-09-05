package generation_test

import (
	"errors"
	imggeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/generation"
	"testing"
)

func TestResolve_KnownID_ReturnsProvider(t *testing.T) {
	r := imggeneration.NewRegistry(nil, imggeneration.NewGoogleSlidesProvider(nil, nil))
	got, err := r.Resolve("google-slides")
	if err != nil {
		t.Errorf("Resolve(google-slides) err = %v; want nil", err)
	}
	if got == nil {
		t.Fatalf("Resolve(google-slides) = nil; want google-slides provider")
	}
}

func TestResolve_MissingID_ReturnsErrProviderNotFound(t *testing.T) {
	r := imggeneration.NewRegistry(nil, imggeneration.NewGoogleSlidesProvider(nil, nil))
	for _, id := range []string{"flux", "nvidia", "nonexistent-model"} {
		_, err := r.Resolve(id)
		if err == nil {
			t.Fatalf("Resolve(%q) err = nil; want imggeneration.ErrProviderNotFound", id)
		}
		if !errors.Is(err, imggeneration.ErrProviderNotFound) {
			t.Errorf("Resolve(%q) err = %v; want errors.Is(_, imggeneration.ErrProviderNotFound)", id, err)
		}
	}
}

func TestResolve_EmptyID_ReturnsErrProviderNotFound(t *testing.T) {
	r := imggeneration.NewRegistry(nil, imggeneration.NewGoogleSlidesProvider(nil, nil))
	_, err := r.Resolve("")
	if err == nil {
		t.Fatalf("Resolve(\"\") err = nil; want imggeneration.ErrProviderNotFound")
	}
	if !errors.Is(err, imggeneration.ErrProviderNotFound) {
		t.Errorf("Resolve(\"\") err = %v; want errors.Is(_, imggeneration.ErrProviderNotFound)", err)
	}
}
