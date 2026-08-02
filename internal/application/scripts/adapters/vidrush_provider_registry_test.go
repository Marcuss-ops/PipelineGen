package adapters

import (
	"context"
	"errors"
	"testing"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

type registryProvider struct{ name string }

func (p registryProvider) Name() string { return p.name }
func (p registryProvider) Search(context.Context, scriptports.VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	return []scriptpkg.SegmentAssetCandidate{{AssetID: "candidate", Provider: p.name}}, nil
}
func (p registryProvider) Acquire(context.Context, scriptpkg.SegmentAssetCandidate) (scriptports.LocalArtifact, error) {
	return scriptports.LocalArtifact{}, nil
}
func (p registryProvider) Verify(context.Context, scriptports.LocalArtifact) (scriptports.VerifiedArtifact, error) {
	return scriptports.VerifiedArtifact{}, nil
}

func TestVidRushAssetProviderRegistryIsClosedAndDeterministic(t *testing.T) {
	r := NewVidRushAssetProviderRegistry()
	if err := r.Register(registryProvider{name: "ARTLIST"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := r.Register(registryProvider{name: scriptpkg.VidRushProviderArtlist}); !errors.Is(err, scriptports.ErrVidRushProviderDuplicate) {
		t.Fatalf("duplicate error = %v", err)
	}
	if err := r.Register(registryProvider{name: "not-a-provider"}); !errors.Is(err, scriptports.ErrVidRushProviderNotFound) {
		t.Fatalf("unknown provider error = %v", err)
	}
	r.Freeze()
	if err := r.Register(registryProvider{name: scriptpkg.VidRushProviderInternetImages}); !errors.Is(err, scriptports.ErrVidRushProviderRegistryFrozen) {
		t.Fatalf("frozen error = %v", err)
	}
	if got := r.Names(); len(got) != 1 || got[0] != scriptpkg.VidRushProviderArtlist {
		t.Fatalf("names = %v", got)
	}
	if _, err := r.Search(context.Background(), "internet_images", scriptports.VidRushSearchRequest{}); !errors.Is(err, scriptports.ErrVidRushProviderNotFound) {
		t.Fatalf("missing provider search error = %v", err)
	}
}
