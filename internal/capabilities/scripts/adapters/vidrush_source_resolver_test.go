package adapters

import (
	"context"
	"testing"

	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type canonicalAssetSearcherStub struct {
	calls int
	hits  []assetsearch.AssetSearchHit
}

func (s *canonicalAssetSearcherStub) SearchAssets(_ context.Context, _ assetsearch.AssetSearchQuery) ([]assetsearch.AssetSearchHit, error) {
	s.calls++
	return s.hits, nil
}

type sourceResolverStub struct {
	stage string
	calls int
}

func (s *sourceResolverStub) Resolve(_ context.Context, _ SourceResolutionRequest) (*SourceResolutionCandidate, error) {
	s.calls++
	return &SourceResolutionCandidate{
		Source:   mediadomain.SegmentMediaSource{AssetID: s.stage + "-asset", Provider: s.stage},
		Provider: s.stage,
	}, nil
}

func resolverRequest() SourceResolutionRequest {
	return SourceResolutionRequest{
		Plan: &scriptpkg.ResolvedGenerationPlan{
			MediaPlan: mediadomain.MediaPlanSpec{ProviderPolicy: mediadomain.MediaProviderPolicy{
				YouTube: mediadomain.MediaToggleEnabled, Artlist: mediadomain.MediaToggleEnabled, InternetImages: mediadomain.MediaToggleEnabled,
			}},
		},
		Segment: scriptpkg.CanonicalSegment{ID: "segment-001"},
		Slot:    mediadomain.SlotPrimaryVideo,
		Profile: scriptpkg.SegmentSemanticProfile{Topic: "tractor history"},
	}
}

func TestVidRushSourceResolver_PriorityOrder(t *testing.T) {
	local := &sourceResolverStub{stage: "local_stock"}
	r := VidRushSourceResolver{
		LocalStock:    local,
		YouTube:       &sourceResolverStub{stage: "youtube"},
		Artlist:       &sourceResolverStub{stage: "artlist"},
		ImageFallback: &sourceResolverStub{stage: "images"},
	}
	got, err := r.Resolve(context.Background(), resolverRequest())
	if err != nil || got.Stage != "local_stock" {
		t.Fatalf("got=%#v err=%v, want local stock", got, err)
	}
	if local.calls != 1 {
		t.Fatalf("local calls=%d, want 1", local.calls)
	}
}

func TestVidRushSourceResolver_LockedAssignmentWins(t *testing.T) {
	req := resolverRequest()
	req.Plan.MediaPlan.Assignments = []mediadomain.SegmentMediaAssignment{{
		SegmentID: "segment-001", Slot: mediadomain.SlotPrimaryVideo, Locked: true,
		Asset: mediadomain.MediaRef{AssetID: "locked-asset", Provider: "youtube"},
	}}
	local := &sourceResolverStub{stage: "local_stock"}
	got, err := (VidRushSourceResolver{LocalStock: local}).Resolve(context.Background(), req)
	if err != nil || got.Stage != "locked" || got.Source.AssetID != "locked-asset" {
		t.Fatalf("got=%#v err=%v, want locked asset", got, err)
	}
	if local.calls != 0 {
		t.Fatal("locked assignment must not call providers")
	}
}

func TestVidRushSourceResolver_CanonicalStockPrecedesRemoteProviders(t *testing.T) {
	req := resolverRequest()
	catalog := &canonicalAssetSearcherStub{hits: []assetsearch.AssetSearchHit{{AssetID: "catalog-asset", Source: "stock", Score: .95}}}
	remote := &sourceResolverStub{stage: "youtube"}
	got, err := (VidRushSourceResolver{Canonical: catalog, YouTube: remote}).Resolve(context.Background(), req)
	if err != nil || got.Stage != "canonical_stock" || got.Source.AssetID != "catalog-asset" {
		t.Fatalf("got=%#v err=%v, want canonical stock hit", got, err)
	}
	if catalog.calls != 1 || remote.calls != 0 {
		t.Fatalf("catalog calls=%d remote calls=%d, want 1/0", catalog.calls, remote.calls)
	}
}

func TestVidRushSourceResolver_CanonicalMissFallsThroughToRemote(t *testing.T) {
	req := resolverRequest()
	catalog := &canonicalAssetSearcherStub{}
	remote := &sourceResolverStub{stage: "youtube"}
	got, err := (VidRushSourceResolver{Canonical: catalog, YouTube: remote}).Resolve(context.Background(), req)
	if err != nil || got.Stage != "youtube" {
		t.Fatalf("got=%#v err=%v, want YouTube fallback", got, err)
	}
	if catalog.calls != 1 || remote.calls != 1 {
		t.Fatalf("catalog calls=%d remote calls=%d, want 1/1", catalog.calls, remote.calls)
	}
}

func TestVidRushSourceResolver_SuggestedAssetIDPrecedesProviders(t *testing.T) {
	req := resolverRequest()
	req.Plan.MediaPlan.Sources = []mediadomain.SegmentMediaSource{{
		SegmentID: "segment-001", Slot: mediadomain.SlotPrimaryVideo, Provider: "youtube", AssetID: "suggested-asset",
	}}
	local := &sourceResolverStub{stage: "local_stock"}
	got, err := (VidRushSourceResolver{LocalStock: local}).Resolve(context.Background(), req)
	if err != nil || got.Stage != "suggested_asset_id" || got.Source.AssetID != "suggested-asset" {
		t.Fatalf("got=%#v err=%v, want suggested asset", got, err)
	}
	if local.calls != 0 {
		t.Fatal("suggested AssetID must not call local provider")
	}
}

func TestVidRushSourceResolver_FallsThroughYouTubeArtlistImages(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*VidRushSourceResolver)
		stage string
	}{
		{"youtube", func(r *VidRushSourceResolver) { r.YouTube = &sourceResolverStub{stage: "youtube"} }, "youtube"},
		{"artlist", func(r *VidRushSourceResolver) { r.Artlist = &sourceResolverStub{stage: "artlist"} }, "artlist"},
		{"images", func(r *VidRushSourceResolver) { r.ImageFallback = &sourceResolverStub{stage: "images"} }, "image_fallback"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := VidRushSourceResolver{}
			tc.setup(&r)
			got, err := r.Resolve(context.Background(), resolverRequest())
			if err != nil || got.Stage != tc.stage {
				t.Fatalf("got=%#v err=%v, want stage %q", got, err, tc.stage)
			}
		})
	}
}

func TestVidRushSourceResolver_RespectsProviderPolicy(t *testing.T) {
	req := resolverRequest()
	req.Plan.MediaPlan.ProviderPolicy.YouTube = mediadomain.MediaToggleDisabled
	youtube := &sourceResolverStub{stage: "youtube"}
	artlist := &sourceResolverStub{stage: "artlist"}
	got, err := (VidRushSourceResolver{YouTube: youtube, Artlist: artlist}).Resolve(context.Background(), req)
	if err != nil || got.Stage != "artlist" {
		t.Fatalf("got=%#v err=%v, want Artlist after disabled YouTube", got, err)
	}
	if youtube.calls != 0 {
		t.Fatal("disabled YouTube provider was called")
	}
}
