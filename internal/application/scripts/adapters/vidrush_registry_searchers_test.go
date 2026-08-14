package adapters

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/domain/media"
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

// fanoutGate coordinates the two provider searchers so a test can prove they
// start concurrently rather than sequentially.
type fanoutGate struct {
	entered chan string
	release chan struct{}
}

type gatedArtlistSearcher struct {
	calls atomic.Int32
	gate  *fanoutGate
}

func (s *gatedArtlistSearcher) SearchClips(ctx context.Context, _ string, phrases []string) ([]ArtlistClipMatch, error) {
	s.calls.Add(1)
	s.gate.entered <- "artlist"
	select {
	case <-s.gate.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return []ArtlistClipMatch{{Phrase: phrases[0], ClipNames: []string{"clip-1"}, ClipDriveLinks: []string{"https://cdn.example/clip-1"}}}, nil
}

type gatedImageSearcher struct {
	calls atomic.Int32
	gate  *fanoutGate
}

func (s *gatedImageSearcher) SearchImages(ctx context.Context, req InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	s.calls.Add(1)
	s.gate.entered <- "internet_images"
	select {
	case <-s.gate.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return []scriptpkg.SegmentAssetCandidate{{AssetID: "img-1", Provider: "internet_images", SourceURL: "https://cdn.example/img-1", Query: req.Query}}, nil
}

func TestVidRushProviderFanoutRunsProvidersInParallel(t *testing.T) {
	gate := &fanoutGate{entered: make(chan string, 2), release: make(chan struct{})}
	artlist := &gatedArtlistSearcher{gate: gate}
	images := &gatedImageSearcher{gate: gate}
	fanout := NewVidRushProviderFanout(artlist, images)

	plan := &scriptpkg.ResolvedGenerationPlan{
		Title: "parallel",
		MediaPlan: mediadomain.MediaPlanSpec{
			ProviderPolicy: mediadomain.MediaProviderPolicy{
				Artlist:        mediadomain.MediaToggleEnabled,
				InternetImages: mediadomain.MediaToggleEnabled,
			},
		},
	}
	segment := scriptpkg.VidRushSegmentResult{
		SegmentID: "seg-1", TextHash: "hash", Text: "scene text",
		Insights: scriptpkg.SegmentInsights{
			SegmentID:      "seg-1",
			ArtlistQueries: []string{"artlist query"},
			ImageQueries:   []string{"image query"},
		},
	}

	resultCh := make(chan error, 1)
	var result scriptpkg.VidRushSegmentResult
	go func() {
		var err error
		result, err = fanout.ResolveProviders(context.Background(), plan, segment)
		resultCh <- err
	}()

	// Both providers must have entered before either is released — this proves
	// the fanout launches them concurrently rather than serially.
	entered := make(map[string]bool, 2)
	for len(entered) < 2 {
		select {
		case name := <-gate.entered:
			entered[name] = true
		case err := <-resultCh:
			t.Fatalf("ResolveProviders returned before both providers started: %v", err)
		case <-time.After(2 * time.Second):
			t.Fatal("providers did not both start")
		}
	}
	close(gate.release)
	if err := <-resultCh; err != nil {
		t.Fatalf("ResolveProviders error: %v", err)
	}

	if artlist.calls.Load() != 1 || images.calls.Load() != 1 {
		t.Fatalf("artlist calls=%d images calls=%d, want 1 each", artlist.calls.Load(), images.calls.Load())
	}
	if result.Assets.PrimaryVideo == nil || result.Assets.PrimaryVideo.Provider != "artlist" {
		t.Fatalf("primary video = %+v, want artlist primary", result.Assets.PrimaryVideo)
	}
	if len(result.Assets.SecondaryImages) == 0 {
		t.Fatal("expected internet image candidates in secondary images")
	}
}
