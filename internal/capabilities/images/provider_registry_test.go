package images

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"
)

type fakeBackend struct {
	captures []PortGenerateRequest
	result   *PortGeneratedImage
	err      error
}

func (f *fakeBackend) Generate(_ context.Context, req PortGenerateRequest) (*PortGeneratedImage, error) {
	f.captures = append(f.captures, req)
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

func (f *fakeBackend) TriggerPrewarm(_ context.Context, _ string, _ int) {
	// no-op stub for ImageGeneratorPort contract
}

func TestGenerationRegistry_DefaultsToGoogleSlidesNanoBananaPro(t *testing.T) {
	backend := &fakeBackend{result: &PortGeneratedImage{Data: []byte("png-bytes"), Format: "png"}}
	registry := NewDefaultProviderRegistry(zap.NewNop(), backend)

	result, err := registry.Generate(context.Background(), GenerateRequest{Prompt: "x"}, GenerateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Provider != asset.ProviderGoogleSlides {
		t.Fatalf("provider = %q, want %q", result.Provider, asset.ProviderGoogleSlides)
	}
	if result.Model != CanonicalGoogleSlidesModel {
		t.Fatalf("model = %q, want %q", result.Model, CanonicalGoogleSlidesModel)
	}
	if len(backend.captures) != 1 {
		t.Fatalf("expected 1 backend dispatch, got %d", len(backend.captures))
	}
}

func TestGenerationRegistry_NotWired(t *testing.T) {
	registry := NewDefaultProviderRegistry(zap.NewNop(), nil)
	_, err := registry.Generate(context.Background(), GenerateRequest{}, GenerateOptions{})
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("err = %v, want ErrProviderUnavailable", err)
	}
}

func TestGenerationRegistry_ExposesOnlyGoogleSlides(t *testing.T) {
	registry := NewDefaultProviderRegistry(zap.NewNop(), nil)
	providers := registry.Providers()
	if len(providers) != 1 || providers[0].Name() != asset.ProviderGoogleSlides {
		t.Fatalf("providers = %+v", providers)
	}
	if registry.ProviderByName(asset.ProviderGoogleSlides) == nil {
		t.Fatal("google-slides provider missing")
	}
	if registry.ProviderByName(asset.ImageProvider("removed-provider")) != nil {
		t.Fatal("removed provider must not resolve")
	}
}

func TestGenerationRegistry_DiagnosticsOnlyGoogleSlides(t *testing.T) {
	registry := NewDefaultProviderRegistry(zap.NewNop(), nil)
	diagnostics := registry.Diagnostics(context.Background())
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics entries = %d, want 1", len(diagnostics))
	}
	if _, ok := diagnostics[asset.ProviderGoogleSlides]; !ok {
		t.Fatal("google-slides diagnostics entry missing")
	}
}

func TestGenerationRegistry_ProviderErrorPropagates(t *testing.T) {
	want := errors.New("upstream quota exceeded")
	registry := NewDefaultProviderRegistry(zap.NewNop(), &fakeBackend{err: want})
	_, err := registry.Generate(context.Background(), GenerateRequest{}, GenerateOptions{})
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want wrapped %v", err, want)
	}
}
