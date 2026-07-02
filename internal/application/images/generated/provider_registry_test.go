package generated

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
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

func TestGenerationRegistry_DefaultsToGoogleSlidesNanoBananaPro(t *testing.T) {
	be := &fakeBackend{result: &PortGeneratedImage{
		Data:     []byte("png-bytes"),
		Format:   "png",
		Provider: "ignored-provider-value",
	}}
	reg := NewDefaultProviderRegistry(zap.NewNop(), be)

	out, err := reg.Generate(context.Background(), GenerateRequest{Prompt: "x"}, GenerateOptions{})
	if err != nil {
		t.Fatalf("Generate errored: %v", err)
	}
	if out == nil || string(out.Data) != "png-bytes" {
		t.Fatalf("unexpected result: %+v", out)
	}
	if out.Provider != asset.ProviderGoogleSlides {
		t.Fatalf("provider = %q, want %q", out.Provider, asset.ProviderGoogleSlides)
	}
	if out.Model != CanonicalGoogleSlidesModel {
		t.Fatalf("model = %q, want %q", out.Model, CanonicalGoogleSlidesModel)
	}
	if len(be.captures) != 1 || be.captures[0].Model != CanonicalGoogleSlidesModel {
		t.Fatalf("backend model = %+v, want %q", be.captures, CanonicalGoogleSlidesModel)
	}
}

func TestGenerationRegistry_AcceptsCanonicalModelCaseInsensitive(t *testing.T) {
	be := &fakeBackend{result: &PortGeneratedImage{Data: []byte("ok")}}
	reg := NewDefaultProviderRegistry(zap.NewNop(), be)

	_, err := reg.Generate(context.Background(), GenerateRequest{
		Prompt: "x",
		Model:  "NANO-BANANA-PRO",
	}, GenerateOptions{})
	if err != nil {
		t.Fatalf("canonical model rejected: %v", err)
	}
	if got := be.captures[0].Model; got != CanonicalGoogleSlidesModel {
		t.Fatalf("normalized model = %q, want %q", got, CanonicalGoogleSlidesModel)
	}
}

func TestGenerationRegistry_RejectsFormerProviderModels(t *testing.T) {
	reg := NewDefaultProviderRegistry(zap.NewNop(), &fakeBackend{result: &PortGeneratedImage{}})
	for _, model := range []string{"flux-1-dev", "nvidia-picasso", "imagen-3", "nano-banana"} {
		_, err := reg.Generate(context.Background(), GenerateRequest{Prompt: "x", Model: model}, GenerateOptions{})
		if !errors.Is(err, ErrUnsupportedModel) {
			t.Errorf("model %q: err = %v, want ErrUnsupportedModel", model, err)
		}
	}
}

func TestGenerationRegistry_GoogleSlidesNotWired(t *testing.T) {
	reg := NewDefaultProviderRegistry(zap.NewNop(), nil)
	_, err := reg.Generate(context.Background(), GenerateRequest{}, GenerateOptions{})
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("err = %v, want ErrProviderUnavailable", err)
	}
}

func TestGenerationRegistry_NilRegistry(t *testing.T) {
	var reg *GenerationProviderRegistry
	_, err := reg.Generate(context.Background(), GenerateRequest{}, GenerateOptions{})
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("err = %v, want ErrProviderUnavailable", err)
	}
}

func TestGenerationRegistry_ExposesOnlyGoogleSlides(t *testing.T) {
	reg := NewDefaultProviderRegistry(zap.NewNop(), nil)
	providers := reg.Providers()
	if len(providers) != 1 {
		t.Fatalf("provider count = %d, want 1", len(providers))
	}
	if providers[0].Name() != asset.ProviderGoogleSlides {
		t.Fatalf("provider = %q, want %q", providers[0].Name(), asset.ProviderGoogleSlides)
	}
	if reg.ProviderByName(asset.ProviderGoogleSlides) == nil {
		t.Fatal("google-slides provider missing")
	}
	if reg.ProviderByName(asset.ProviderFlux) != nil {
		t.Fatal("legacy flux provider must not be available")
	}
	if reg.ProviderByName(asset.ProviderNvidia) != nil {
		t.Fatal("legacy nvidia provider must not be available")
	}
}

func TestGenerationRegistry_DiagnosticsOnlyGoogleSlides(t *testing.T) {
	reg := NewDefaultProviderRegistry(zap.NewNop(), nil)
	diag := reg.Diagnostics(context.Background())
	if len(diag) != 1 {
		t.Fatalf("diagnostics entries = %d, want 1", len(diag))
	}
	if _, ok := diag[asset.ProviderGoogleSlides]; !ok {
		t.Fatal("google-slides diagnostics entry missing")
	}
}

func TestGenerationRegistry_ProviderErrorPropagates(t *testing.T) {
	want := errors.New("upstream quota exceeded")
	reg := NewDefaultProviderRegistry(zap.NewNop(), &fakeBackend{err: want})
	_, err := reg.Generate(context.Background(), GenerateRequest{}, GenerateOptions{})
	if err == nil || !errors.Is(err, want) {
		t.Fatalf("err = %v, want wrapped %v", err, want)
	}
}
