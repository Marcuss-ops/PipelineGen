package generation_test

import (
	"context"
	"errors"
	imggeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/generation"
	"testing"

	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"go.uber.org/zap"
)

type fakeBackend struct {
	captures []imggeneration.GenerateImageRequest
	result   *imggeneration.GeneratedImage
	err      error
}

func (f *fakeBackend) Generate(_ context.Context, req imggeneration.GenerateImageRequest) (*imggeneration.GeneratedImage, error) {
	f.captures = append(f.captures, req)
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

func (f *fakeBackend) TriggerPrewarm(_ context.Context, _ string, _ int) {}

func TestGenerationRegistry_DefaultsToGoogleSlidesNanoBananaPro(t *testing.T) {
	backend := &fakeBackend{result: &imggeneration.GeneratedImage{Data: []byte("png-bytes"), Format: "png"}}
	registry := imggeneration.NewDefaultRegistry(zap.NewNop(), backend)

	result, err := registry.Generate(context.Background(), imggeneration.GenerateRequest{Prompt: "x"}, imggeneration.GenerateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Provider != detail.ProviderGoogleSlides {
		t.Fatalf("provider = %q, want %q", result.Provider, detail.ProviderGoogleSlides)
	}
	if result.Model != imggeneration.CanonicalGoogleSlidesModel {
		t.Fatalf("model = %q, want %q", result.Model, imggeneration.CanonicalGoogleSlidesModel)
	}
	if len(backend.captures) != 1 {
		t.Fatalf("expected 1 backend dispatch, got %d", len(backend.captures))
	}
}

func TestGenerationRegistry_NotWired(t *testing.T) {
	registry := imggeneration.NewDefaultRegistry(zap.NewNop(), nil)
	_, err := registry.Generate(context.Background(), imggeneration.GenerateRequest{}, imggeneration.GenerateOptions{})
	if !errors.Is(err, imggeneration.ErrProviderUnavailable) {
		t.Fatalf("err = %v, want imggeneration.ErrProviderUnavailable", err)
	}
}

func TestGenerationRegistry_ExposesOnlyGoogleSlides(t *testing.T) {
	registry := imggeneration.NewDefaultRegistry(zap.NewNop(), nil)
	providers := registry.Providers()
	if len(providers) != 1 || providers[0].Name() != detail.ProviderGoogleSlides {
		t.Fatalf("providers = %+v", providers)
	}
	if registry.ProviderByName(detail.ProviderGoogleSlides) == nil {
		t.Fatal("google-slides provider missing")
	}
	if registry.ProviderByName(detail.ImageProvider("removed-provider")) != nil {
		t.Fatal("removed provider must not resolve")
	}
}

func TestGenerationRegistry_DiagnosticsOnlyGoogleSlides(t *testing.T) {
	registry := imggeneration.NewDefaultRegistry(zap.NewNop(), nil)
	diagnostics := registry.Diagnostics(context.Background())
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics entries = %d, want 1", len(diagnostics))
	}
	if _, ok := diagnostics[detail.ProviderGoogleSlides]; !ok {
		t.Fatal("google-slides diagnostics entry missing")
	}
}

func TestGenerationRegistry_ProviderErrorPropagates(t *testing.T) {
	want := errors.New("upstream quota exceeded")
	registry := imggeneration.NewDefaultRegistry(zap.NewNop(), &fakeBackend{err: want})
	_, err := registry.Generate(context.Background(), imggeneration.GenerateRequest{}, imggeneration.GenerateOptions{})
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want wrapped %v", err, want)
	}
}
