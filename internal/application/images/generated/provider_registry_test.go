// Package generated — provider_registry_test.go locks the Step 8
// contract for the GenerationProviderRegistry: default-provider
// fallback, model-based dispatch, err propagation, lookup, and
// nil-safety.
package generated

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"go.uber.org/zap"
)

// fakeBackend is a hand-rolled ImageGeneratorPort stub. Each test
// populates the captured fields it wants to assert.
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

// TestGenerationRegistry_DefaultProvider_NoModel verifies an empty
// Model routes to the FIRST registered provider (default).
func TestGenerationRegistry_DefaultProvider_NoModel(t *testing.T) {
	be := &fakeBackend{result: &PortGeneratedImage{
		Data:     []byte("png-bytes"),
		Format:   "png",
		Provider: "google-slides",
		Model:    "",
	}}
	reg := NewGenerationProviderRegistry(zap.NewNop(), []GenerationProvider{
		NewGoogleSlidesProvider(be, zap.NewNop()),
		NewFluxProvider(nil, zap.NewNop()),
		NewNvidiaProvider(nil, "", "", zap.NewNop()),
	})
	out, err := reg.Generate(context.Background(), GenerateRequest{Prompt: "x"}, GenerateOptions{})
	if err != nil {
		t.Fatalf("default Generate errored: %v", err)
	}
	if out == nil || len(out.Data) != 9 {
		t.Fatalf("expected backend data to surface, got %+v", out)
	}
	if len(be.captures) != 1 || be.captures[0].Prompt != "x" {
		t.Fatalf("expected backend called once with prompt=x, got %+v", be.captures)
	}
}

// TestGenerationRegistry_FluxModelDispatch verifies a Flux model
// name routes to the FluxProvider even when GoogleSlides is
// registered first.
func TestGenerationRegistry_FluxModelDispatch(t *testing.T) {
	be := &fakeBackend{result: &PortGeneratedImage{
		Data:     []byte("flux-bytes"),
		Provider: "flux",
		Model:    "flux-1-dev",
	}}
	gs := NewGoogleSlidesProvider(&fakeBackend{}, zap.NewNop())
	reg := NewGenerationProviderRegistry(zap.NewNop(), []GenerationProvider{gs, NewFluxProvider(be, zap.NewNop())})
	out, err := reg.Generate(context.Background(), GenerateRequest{Prompt: "y", Model: "flux-1-dev"}, GenerateOptions{})
	if err != nil {
		t.Fatalf("flux dispatch errored: %v", err)
	}
	if out == nil || string(out.Data) != "flux-bytes" {
		t.Fatalf("expected Flux data, got %+v", out)
	}
}

// TestGenerationRegistry_NvidiaModelDispatch verifies an NVIDIA
// model name routes to the NvidiaProvider.
func TestGenerationRegistry_NvidiaModelDispatch(t *testing.T) {
	be := &fakeBackend{result: &PortGeneratedImage{
		Data:     []byte("nvidia-bytes"),
		Provider: "nvidia",
		Model:    "nvidia-picasso",
	}}
	reg := NewGenerationProviderRegistry(zap.NewNop(), []GenerationProvider{
		NewGoogleSlidesProvider(&fakeBackend{}, zap.NewNop()),
		NewNvidiaProvider(be, "fake-key", "", zap.NewNop()),
	})
	out, err := reg.Generate(context.Background(), GenerateRequest{Model: "nvidia-picasso"}, GenerateOptions{})
	if err != nil {
		t.Fatalf("nvidia dispatch errored: %v", err)
	}
	if string(out.Data) != "nvidia-bytes" {
		t.Fatalf("expected NVIDIA data, got %q", out.Data)
	}
}

// TestGenerationRegistry_NoMatchModel_ReturnsError verifies a request
// for an unknown model returns ErrProviderModelMismatch.
func TestGenerationRegistry_NoMatchModel_ReturnsError(t *testing.T) {
	reg := NewGenerationProviderRegistry(zap.NewNop(), []GenerationProvider{
		NewGoogleSlidesProvider(nil, zap.NewNop()),
	})
	_, err := reg.Generate(context.Background(), GenerateRequest{Model: "made-up-model"}, GenerateOptions{})
	if err == nil || !errors.Is(err, ErrProviderModelMismatch) {
		t.Fatalf("expected ErrProviderModelMismatch, got %v", err)
	}
}

// TestGenerationRegistry_Flux_StubReturnsUnavailable verifies the
// FluxProvider fails closed when its backend is nil.
func TestGenerationRegistry_Flux_StubReturnsUnavailable(t *testing.T) {
	p := NewFluxProvider(nil, zap.NewNop())
	got, err := p.Generate(context.Background(), GenerateRequest{Model: "flux-1-dev"}, GenerateOptions{})
	if err == nil {
		t.Fatalf("expected error from stub FluxProvider")
	}
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("expected ErrProviderUnavailable, got %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil result on stub, got %+v", got)
	}
}

// TestGenerationRegistry_Nvidia_StubReturnsUnavailable verifies the
// NvidiaProvider fails closed when both delegate and apiKey are nil.
func TestGenerationRegistry_Nvidia_StubReturnsUnavailable(t *testing.T) {
	p := NewNvidiaProvider(nil, "", "", zap.NewNop())
	got, err := p.Generate(context.Background(), GenerateRequest{Model: "nvidia-picasso"}, GenerateOptions{})
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("expected ErrProviderUnavailable, got %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil result on stub, got %+v", got)
	}
}

// TestGenerationRegistry_GoogleSlides_NotWired verifies
// GoogleSlidesProvider fail-closes when its delegate is nil.
func TestGenerationRegistry_GoogleSlides_NotWired(t *testing.T) {
	p := NewGoogleSlidesProvider(nil, zap.NewNop())
	_, err := p.Generate(context.Background(), GenerateRequest{}, GenerateOptions{})
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("expected ErrProviderUnavailable, got %v", err)
	}
	if err := p.Healthy(context.Background()); err == nil {
		t.Fatalf("expected Healthy error when delegate nil")
	}
}

// TestGenerationRegistry_NilRegistry verifies a nil registry
// returns ErrProviderUnavailable without panicking.
func TestGenerationRegistry_NilRegistry(t *testing.T) {
	var reg *GenerationProviderRegistry
	_, err := reg.Generate(context.Background(), GenerateRequest{}, GenerateOptions{})
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("expected ErrProviderUnavailable on nil-registry, got %v", err)
	}
}

// TestGenerationRegistry_ProviderByName verifies lookup by
// ImageProvider constant.
func TestGenerationRegistry_ProviderByName(t *testing.T) {
	reg := NewDefaultProviderRegistry(zap.NewNop(), nil, "")
	if reg.ProviderByName(asset.ProviderGoogleSlides) == nil {
		t.Fatalf("expected GoogleSlides provider, got nil")
	}
	if reg.ProviderByName(asset.ProviderFlux) == nil {
		t.Fatalf("expected Flux provider, got nil")
	}
	if reg.ProviderByName(asset.ProviderNvidia) == nil {
		t.Fatalf("expected Nvidia provider, got nil")
	}
	if reg.ProviderByName(asset.ProviderWikipedia) != nil {
		t.Fatalf("expected nil for retrieval-only provider, got %+v", reg.ProviderByName(asset.ProviderWikipedia))
	}
}

// TestGenerationRegistry_ProviderErrorPropagates verifies a
// non-nil error from a provider's Generate surfaces unchanged to
// the caller (no wrap loss).
func TestGenerationRegistry_ProviderErrorPropagates(t *testing.T) {
	want := errors.New("upstream quota exceeded")
	be := &fakeBackend{err: want}
	reg := NewGenerationProviderRegistry(zap.NewNop(), []GenerationProvider{
		NewGoogleSlidesProvider(be, zap.NewNop()),
	})
	_, err := reg.Generate(context.Background(), GenerateRequest{}, GenerateOptions{})
	if err == nil || !errors.Is(err, want) {
		t.Fatalf("expected error to wrap %v, got %v", want, err)
	}
}

// TestGenerationRegistry_SupportedModels_Defaults verifies each
// provider reports its supported model names — used by the
// registry to build the dispatch table.
func TestGenerationRegistry_SupportedModels_Defaults(t *testing.T) {
	tests := []struct {
		provider GenerationProvider
		want     []string // subset check
	}{
		{NewGoogleSlidesProvider(nil, zap.NewNop()), []string{"nano-banana-pro"}},
		{NewFluxProvider(nil, zap.NewNop()), []string{"flux-1-dev"}},
		{NewNvidiaProvider(nil, "", "", zap.NewNop()), []string{"nvidia-picasso"}},
	}
	for _, tc := range tests {
		got := tc.provider.SupportedModels()
		found := false
		for _, g := range got {
			for _, w := range tc.want {
				if g == w {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			t.Fatalf("provider %s: expected supported model to contain one of %v, got %v",
				tc.provider.Name(), tc.want, got)
		}
	}
}
