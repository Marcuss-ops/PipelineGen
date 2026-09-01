package scriptgeneration

import (
	"context"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediacert"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/stockintelligence"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/require"
)

// stubVisualNER is a VisualNERPort stub returning a fixed entity set. It
// returns the source-grounded Greek-salad anchors so the
// SceneIRSegmentEnricher produces a VidRushSegmentResult with exactly 3
// source-grounded entities + 3 image queries.
type stubVisualNER struct {
	entities []VisualEntity
	err      error
}

func (s stubVisualNER) Extract(_ context.Context, _ string, _ int) ([]VisualEntity, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.entities, nil
}

// stubLocalStock is a LocalStockResolverPort stub returning a fixed
// ResolveResult with the given candidate set + provider request count.
type stubLocalStock struct {
	result stockintelligence.ResolveResult
	err    error
}

func (s stubLocalStock) Resolve(_ context.Context, _ stockintelligence.ResolveRequest) (stockintelligence.ResolveResult, error) {
	if s.err != nil {
		return stockintelligence.ResolveResult{}, s.err
	}
	return s.result, nil
}

// stubMediaSampler is a MediaSamplerPort stub that records the candidates it
// received and returns a fixed winner id.
type stubMediaSampler struct {
	winnerID  string
	err       error
	calls     int
	lastCands []scriptpkg.SegmentAssetCandidate
}

func (s *stubMediaSampler) Sample(_ context.Context, _, _ string, _ []string, candidates []scriptpkg.SegmentAssetCandidate, _ bool) (string, error) {
	s.calls++
	s.lastCands = candidates
	if s.err != nil {
		return "", s.err
	}
	return s.winnerID, nil
}

var _ scriptports.MediaSamplerPort = (*stubMediaSampler)(nil)

// stubMediaCertifier is a MediaCertifierPort stub returning a fixed report.
type stubMediaCertifier struct {
	report mediacert.Report
	err    error
}

func (s stubMediaCertifier) Certify(_ context.Context, _ mediacert.Spec, _ mediacert.MediaResult) (mediacert.Report, error) {
	if s.err != nil {
		return mediacert.Report{}, s.err
	}
	return s.report, nil
}

// stubBarrier is a VidRushBarrier stub returning a fixed segment set.
type stubBarrier struct {
	segments []scriptpkg.VidRushSegmentResult
	err      error
}

func (s stubBarrier) WaitForVidRush(_ context.Context, _ string) ([]scriptpkg.VidRushSegmentResult, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.segments, nil
}

func greekSaladEntities() []VisualEntity {
	text := "Greek salad contains tomatoes, feta cheese and olives."
	span := func(value string, score float32) VisualEntity {
		start := strings.Index(text, value)
		return VisualEntity{Text: value, Score: score, Start: start, End: start + len(value), Evidence: value}
	}
	return []VisualEntity{
		span("feta", 0.9),
		span("tomatoes", 0.85),
		span("olives", 0.8),
	}
}

// TestSceneIRSegmentEnricherCompilesIdentityAndExtractsEntities pins the
// Fase 1 + Fase 3 enricher: the returned VidRushSegmentResult carries the
// immutable SceneIR identity (segment_id + source_text + hash), a non-null
// VisualProfile, exactly 3 source-grounded entities, and 3 image queries
// (one per entity).
func TestSceneIRSegmentEnricherCompilesIdentityAndExtractsEntities(t *testing.T) {
	ner := stubVisualNER{entities: greekSaladEntities()}
	enricher, err := NewSceneIRSegmentEnricher(ner)
	require.NoError(t, err)

	scene := scriptpkg.SpecScene{
		ID:    "mediterranean-01-greek-salad",
		Index: 0,
		Text:  "Greek salad contains tomatoes, feta cheese and olives.",
	}
	result, err := enricher.Enrich(context.Background(), nil, scene)
	require.NoError(t, err)

	// Immutable identity preserved (Fase 1).
	require.Equal(t, "mediterranean-01-greek-salad", result.SegmentID)
	require.Equal(t, scene.Text, result.Text)
	require.NotEmpty(t, result.TextHash)

	// Visual profile never null (Fase 1: fixes visual_profile=null 5/5).
	require.NotNil(t, result.Insights.VisualProfile)
	require.NotEmpty(t, result.Insights.VisualProfile.Subject)
	require.NotEmpty(t, result.Insights.VisualProfile.Terms)

	// Exactly 3 source-grounded entities + 3 image queries (Fase 3).
	require.Equal(t, 3, len(result.Insights.Entities))
	require.Equal(t, 3, len(result.Insights.ImageQueries))
	for _, e := range result.Insights.Entities {
		require.NotEmpty(t, e.Value)
	}
}

// TestSceneIRSegmentEnricherPrefersCanonicalSegmentID prevents the legacy
// scene-N identifier from replacing the source segment identity.
func TestSceneIRSegmentEnricherPrefersCanonicalSegmentID(t *testing.T) {
	enricher, err := NewSceneIRSegmentEnricher(stubVisualNER{entities: greekSaladEntities()})
	require.NoError(t, err)

	result, err := enricher.Enrich(context.Background(), nil, scriptpkg.SpecScene{
		ID:        "scene-1",
		SegmentID: "mediterranean-01-greek-salad",
		Index:     0,
		Text:      "Greek salad contains tomatoes, feta cheese and olives.",
	})
	require.NoError(t, err)
	require.Equal(t, "mediterranean-01-greek-salad", result.SegmentID)
	require.Equal(t, "scene-1", result.SceneID)
}

// TestSemanticProviderResolverBindsWinnerFromLocalFirst pins the Fase 4 +
// Fase 5 resolver: it resolves candidates LOCAL FIRST (0 provider requests),
// ranks them via the MediaSampler, and binds the winner as the primary asset.
func TestSemanticProviderResolverBindsWinnerFromLocalFirst(t *testing.T) {
	stock := stubLocalStock{result: stockintelligence.ResolveResult{
		SegmentID:            "mediterranean-01-greek-salad",
		LocalCandidateCount:  15,
		ProviderLiveRequests: 0,
		Candidates: []stockintelligence.Candidate{
			{AssetID: "artlist-greek-salad-001", Label: "greek salad", GenericSimilarity: 0.9, OwnerSegmentID: "mediterranean-01-greek-salad", Source: "local"},
			{AssetID: "artlist-boxing-001", Label: "woman boxing", GenericSimilarity: 0.72, OwnerSegmentID: "mediterranean-01-greek-salad", Source: "local"},
		},
		WinnerAssetID: "artlist-greek-salad-001",
	}}
	sampler := &stubMediaSampler{winnerID: "artlist-greek-salad-001"}
	resolver, err := NewSemanticProviderResolver(stock, sampler)
	require.NoError(t, err)

	segment := scriptpkg.VidRushSegmentResult{
		SegmentID: "mediterranean-01-greek-salad",
		Position:  0,
		Text:      "Greek salad contains tomatoes, feta cheese and olives.",
		TextHash:  scriptpkg.ComputeCanonicalSegmentTextHash("Greek salad contains tomatoes, feta cheese and olives."),
		Insights: scriptpkg.SegmentInsights{
			VisualProfile: &scriptpkg.SegmentVisualProfile{Subject: "greek salad", Terms: []string{"feta", "tomatoes", "olives"}},
		},
	}
	res, err := resolver.ResolveProviders(context.Background(), nil, segment)
	require.NoError(t, err)

	// The winner is bound as the primary asset.
	require.NotNil(t, res.Assets.PrimaryVideo)
	require.Equal(t, "artlist-greek-salad-001", res.Assets.PrimaryVideo.AssetID)
	require.Equal(t, "greek salad", res.Assets.PrimaryVideo.Entity)

	// The MediaSampler received both candidates.
	require.Equal(t, 1, sampler.calls)
	require.Len(t, sampler.lastCands, 2)

	// LOCAL FIRST: 0 provider live requests recorded.
	require.Equal(t, 0, res.Cache.InternetImagesProviderSearches)
}

// TestMediaCertBarrierFailsJobWhenCertifiedFalse pins the Fase 2 barrier
// hook: a SUCCEEDED run with a boxing clip bound to Greek Salad must fail
// the job (CERTIFIED=false surfaces as a barrier error), even though the
// inner barrier returned no error.
func TestMediaCertBarrierFailsJobWhenCertifiedFalse(t *testing.T) {
	inner := stubBarrier{segments: []scriptpkg.VidRushSegmentResult{
		{
			SegmentID: "mediterranean-01-greek-salad",
			Position:  0,
			Text:      "Greek salad contains tomatoes, feta cheese and olives.",
			TextHash:  scriptpkg.ComputeCanonicalSegmentTextHash("Greek salad contains tomatoes, feta cheese and olives."),
			Insights: scriptpkg.SegmentInsights{
				VisualProfile: &scriptpkg.SegmentVisualProfile{Subject: "greek salad", Terms: []string{"feta", "tomatoes", "olives"}},
				Entities: []scriptpkg.ExtractedEntity{
					{Value: "feta", Type: "CONCEPT", Confidence: 0.9},
					{Value: "tomatoes", Type: "CONCEPT", Confidence: 0.85},
					{Value: "olives", Type: "CONCEPT", Confidence: 0.8},
				},
				ImageQueries: []string{"feta", "tomatoes", "olives"},
			},
			Assets: scriptpkg.SegmentAssetSelection{
				// The wrong winner: boxing bound to Greek Salad.
				PrimaryVideo: &scriptpkg.SegmentAssetCandidate{
					SegmentID: "mediterranean-01-greek-salad",
					AssetID:   "artlist-boxing-001",
					Provider:  scriptpkg.VidRushProviderArtlist,
					Entity:    "woman boxing",
					Query:     "woman boxing gloves",
				},
			},
		},
	}}
	certifier := stubMediaCertifier{report: mediacert.Report{
		JobStatus: "SUCCEEDED",
		Certified: false,
		Checks: []mediacert.CheckResult{
			{Name: mediacert.CheckArtlistRelevance, Passed: false},
		},
	}}
	spec := mediacert.Spec{Segments: 1, VideoProvider: scriptpkg.VidRushProviderArtlist}
	barrier, err := NewMediaCertBarrier(inner, certifier, spec)
	require.NoError(t, err)

	_, err = barrier.WaitForVidRush(context.Background(), "run-1")
	require.Error(t, err, "a CERTIFIED=false run must fail the job")
	require.Contains(t, err.Error(), "CERTIFIED=false")
	require.Contains(t, err.Error(), "ARTLIST RELEVANCE")
}

// TestMediaCertBarrierPassesCertifiedRun pins the positive path: when
// mediacert returns CERTIFIED=true, the barrier returns the segments with
// no error.
func TestMediaCertBarrierPassesCertifiedRun(t *testing.T) {
	inner := stubBarrier{segments: []scriptpkg.VidRushSegmentResult{
		{
			SegmentID: "mediterranean-01-greek-salad",
			Position:  0,
			Text:      "Greek salad contains tomatoes, feta cheese and olives.",
			TextHash:  scriptpkg.ComputeCanonicalSegmentTextHash("Greek salad contains tomatoes, feta cheese and olives."),
			Insights: scriptpkg.SegmentInsights{
				VisualProfile: &scriptpkg.SegmentVisualProfile{Subject: "greek salad", Terms: []string{"feta", "tomatoes", "olives"}},
				Entities: []scriptpkg.ExtractedEntity{
					{Value: "feta", Type: "CONCEPT", Confidence: 0.9},
					{Value: "tomatoes", Type: "CONCEPT", Confidence: 0.85},
					{Value: "olives", Type: "CONCEPT", Confidence: 0.8},
				},
				ImageQueries: []string{"feta", "tomatoes", "olives"},
			},
			Assets: scriptpkg.SegmentAssetSelection{
				PrimaryVideo: &scriptpkg.SegmentAssetCandidate{
					SegmentID: "mediterranean-01-greek-salad",
					AssetID:   "artlist-greek-salad-001",
					Provider:  scriptpkg.VidRushProviderArtlist,
					Entity:    "greek salad",
				},
			},
		},
	}}
	certifier := stubMediaCertifier{report: mediacert.Report{JobStatus: "SUCCEEDED", Certified: true}}
	spec := mediacert.Spec{Segments: 1, VideoProvider: scriptpkg.VidRushProviderArtlist}
	barrier, err := NewMediaCertBarrier(inner, certifier, spec)
	require.NoError(t, err)

	segs, err := barrier.WaitForVidRush(context.Background(), "run-1")
	require.NoError(t, err)
	require.Len(t, segs, 1)
}
