package adapters

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

type boundedVidRushSearchProvider struct {
	active    atomic.Int32
	maxActive atomic.Int32
	queries   atomic.Int32
	failQuery string
}

type rateLimitedVidRushSearchProvider struct {
	calls atomic.Int32
}

func (p *rateLimitedVidRushSearchProvider) Name() string { return scriptpkg.VidRushProviderArtlist }

func (p *rateLimitedVidRushSearchProvider) Search(_ context.Context, req scriptports.VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	if p.calls.Add(1) == 1 {
		return nil, errors.New("artlist: invalid response: status 429")
	}
	return []scriptpkg.SegmentAssetCandidate{{
		AssetID: "retry-asset", Provider: p.Name(), SourceURL: "https://cdn.example/retry-asset",
		Query: req.Query,
	}}, nil
}

func (*rateLimitedVidRushSearchProvider) Acquire(context.Context, scriptpkg.SegmentAssetCandidate) (scriptports.LocalArtifact, error) {
	return scriptports.LocalArtifact{}, nil
}
func (*rateLimitedVidRushSearchProvider) Verify(context.Context, scriptports.LocalArtifact) (scriptports.VerifiedArtifact, error) {
	return scriptports.VerifiedArtifact{}, nil
}

func (p *boundedVidRushSearchProvider) Name() string { return scriptpkg.VidRushProviderArtlist }

func (p *boundedVidRushSearchProvider) Search(ctx context.Context, req scriptports.VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	if req.Query == p.failQuery {
		return nil, errors.New("provider query failed")
	}
	current := p.active.Add(1)
	defer p.active.Add(-1)
	p.queries.Add(1)
	for {
		previous := p.maxActive.Load()
		if current <= previous || p.maxActive.CompareAndSwap(previous, current) {
			break
		}
	}
	select {
	case <-time.After(10 * time.Millisecond):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return []scriptpkg.SegmentAssetCandidate{{
		AssetID: "asset-" + req.Query, Provider: p.Name(),
		SourceURL: "https://cdn.example/" + req.Query,
	}}, nil
}

func (*boundedVidRushSearchProvider) Acquire(context.Context, scriptpkg.SegmentAssetCandidate) (scriptports.LocalArtifact, error) {
	return scriptports.LocalArtifact{}, nil
}
func (*boundedVidRushSearchProvider) Verify(context.Context, scriptports.LocalArtifact) (scriptports.VerifiedArtifact, error) {
	return scriptports.VerifiedArtifact{}, nil
}

func TestVidRushRegistryClipSearcherUsesBoundedFanout(t *testing.T) {
	provider := &boundedVidRushSearchProvider{}
	registry := NewVidRushAssetProviderRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()

	phrases := []string{"one", "two", "three", "four", "five"}
	matches, err := (&VidRushRegistryClipSearcher{Registry: registry}).SearchClips(context.Background(), "scene", phrases)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != len(phrases) {
		t.Fatalf("matches = %d, want %d", len(matches), len(phrases))
	}
	if got := provider.queries.Load(); got != int32(len(phrases)) {
		t.Fatalf("provider queries = %d, want %d", got, len(phrases))
	}
	if got := provider.maxActive.Load(); got > 3 {
		t.Fatalf("max concurrent provider calls = %d, want <= 3", got)
	}
}

func TestVidRushRegistryClipSearcherPreservesPartialResultsAndError(t *testing.T) {
	provider := &boundedVidRushSearchProvider{failQuery: "bad"}
	registry := NewVidRushAssetProviderRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()

	matches, err := (&VidRushRegistryClipSearcher{Registry: registry}).SearchClips(context.Background(), "scene", []string{"good", "bad"})
	if err == nil || !strings.Contains(err.Error(), "bad") {
		t.Fatalf("error = %v, want failed query context", err)
	}
	if len(matches) != 1 || matches[0].Phrase != "good" {
		t.Fatalf("partial matches = %#v, want the successful query", matches)
	}
}

func TestVidRushRegistryClipSearcherRetriesRateLimit(t *testing.T) {
	provider := &rateLimitedVidRushSearchProvider{}
	registry := NewVidRushAssetProviderRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()

	matches, err := (&VidRushRegistryClipSearcher{Registry: registry}).SearchClips(context.Background(), "scene", []string{"coastal road"})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || provider.calls.Load() != 2 {
		t.Fatalf("matches=%d calls=%d, want one match after one bounded retry", len(matches), provider.calls.Load())
	}
}
