package adapters

import (
	"context"
	"sync/atomic"
	"testing"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type disabledProviderProbe struct{ searches, acquires atomic.Int32 }

func (p *disabledProviderProbe) Name() string { return scriptpkg.VidRushProviderArtlist }
func (p *disabledProviderProbe) Search(context.Context, scriptports.VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	p.searches.Add(1)
	return nil, nil
}
func (p *disabledProviderProbe) SearchClips(context.Context, string, []string) ([]ArtlistClipMatch, error) {
	p.searches.Add(1)
	return nil, nil
}
func (p *disabledProviderProbe) Acquire(context.Context, scriptpkg.SegmentAssetCandidate) (scriptports.LocalArtifact, error) {
	p.acquires.Add(1)
	return scriptports.LocalArtifact{}, nil
}
func (p *disabledProviderProbe) Verify(context.Context, scriptports.LocalArtifact) (scriptports.VerifiedArtifact, error) {
	return scriptports.VerifiedArtifact{}, nil
}

func TestVidRushDisabledProviderProducesNoSearchOrAcquire(t *testing.T) {
	probe := &disabledProviderProbe{}
	fanout := NewVidRushProviderFanout(probe, nil)
	plan := &scriptpkg.ResolvedGenerationPlan{MediaPlan: mediadomain.MediaPlanSpec{ProviderPolicy: mediadomain.MediaProviderPolicy{Artlist: mediadomain.MediaToggleDisabled}}}
	segment := scriptpkg.VidRushSegmentResult{SegmentID: "disabled", Text: "a visual", Insights: scriptpkg.SegmentInsights{ArtlistQueries: []string{"must not run"}}}
	if _, err := fanout.ResolveProviders(context.Background(), plan, segment); err != nil {
		t.Fatal(err)
	}
	if probe.searches.Load() != 0 || probe.acquires.Load() != 0 {
		t.Fatalf("searches=%d acquires=%d", probe.searches.Load(), probe.acquires.Load())
	}
}

func TestVidRushSourceResolverDisabledProviderDoesNotResolve(t *testing.T) {
	probe := &sourceResolverStub{stage: "artlist"}
	plan := &scriptpkg.ResolvedGenerationPlan{MediaPlan: mediadomain.MediaPlanSpec{ProviderPolicy: mediadomain.MediaProviderPolicy{Artlist: mediadomain.MediaToggleDisabled}}}
	resolver := VidRushSourceResolver{Artlist: probe}
	_, err := resolver.Resolve(context.Background(), SourceResolutionRequest{Plan: plan, Segment: scriptpkg.CanonicalSegment{ID: "seg", Text: "topic"}, Slot: mediadomain.SlotPrimaryVideo, Profile: scriptpkg.SegmentSemanticProfile{Topic: "topic"}})
	if err == nil {
		t.Fatal("disabled provider unexpectedly resolved a source")
	}
}
