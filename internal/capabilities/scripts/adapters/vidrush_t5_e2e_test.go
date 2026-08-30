package adapters

import (
	"context"
	"testing"

	scriptgeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type t5ResearchResolver struct{}

func (t5ResearchResolver) Resolve(_ context.Context, source scriptpkg.SourceSpec, _ scriptpkg.SourceResolutionContext) (*scriptpkg.ResolvedSource, error) {
	return &scriptpkg.ResolvedSource{
		ResearchReport: &scriptpkg.ResearchReport{
			Status: "completed", Mode: "online_search", SearchEnabled: true, Searched: true,
			Queries: []string{"Mars rover Perseverance Mars missions private space companies"},
			Sources: []scriptpkg.ResearchWebSource{{
				ID: "mars-source-1", Title: "Mars exploration missions", URL: "https://research.example/mars",
				Excerpt: "New missions and rovers are changing Mars exploration.",
			}},
			PagesRequested: 1, PagesFetched: 1, AcceptedSources: 1,
			QualityGatePassed: true, EvidenceScore: .9,
		},
		Type:       source.Type,
		Topic:      source.Topic,
		SourceText: source.Query,
	}, nil
}

type t5Enricher struct{}

func (t5Enricher) Enrich(_ context.Context, _ *scriptpkg.ResolvedGenerationPlan, scene scriptpkg.SpecScene) (scriptpkg.VidRushSegmentResult, error) {
	hash := scriptgeneration.SceneTextHash(scene.Text)
	return scriptpkg.VidRushSegmentResult{
		SegmentID: scene.ID, SceneID: scene.ID, Position: scene.Index, Text: scene.Text, TextHash: hash,
		Insights: scriptpkg.SegmentInsights{
			SegmentID: scene.ID, TextHash: hash,
			Entities: []scriptpkg.ExtractedEntity{
				{Value: "Mars", Type: "PLACE", Confidence: .99},
				{Value: "Perseverance", Type: "ORG", Confidence: .95},
			},
			ImportantPhrases: []string{"Mars exploration"},
			ImportantWords:   []string{"rover", "missions", "exploration"},
			YouTubeQueries:   []string{"Mars rover Perseverance mission"},
			ArtlistQueries:   []string{"Mars surface rover space technology"},
			ImageQueries:     []string{"Mars rover Perseverance", "Mars surface"},
		},
	}, nil
}

type t5Resolver struct{ calls int }

func (r *t5Resolver) ResolveProviders(_ context.Context, _ *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult) (scriptpkg.VidRushSegmentResult, error) {
	r.calls++
	segment.Assets.Candidates = []scriptpkg.SegmentAssetCandidate{
		{AssetID: "mars-youtube", Provider: scriptpkg.VidRushProviderYouTube, Query: segment.Insights.YouTubeQueries[0], RelevanceScore: .94},
		{AssetID: "mars-artlist", Provider: scriptpkg.VidRushProviderArtlist, Query: segment.Insights.ArtlistQueries[0], RelevanceScore: .88},
		{AssetID: "mars-image", Provider: scriptpkg.VidRushProviderInternetImages, Query: segment.Insights.ImageQueries[0], RelevanceScore: .91},
	}
	return segment, nil
}

func TestVidRushGoldenT5ResearchNLPPlanningAndProviderResolution(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		Title: "Esplorazione di Marte", Language: "it", Model: "small-model", PromptVersion: "t5-v1",
		MediaPlan: mediadomain.MediaPlanSpec{ProviderPolicy: mediadomain.MediaProviderPolicy{
			YouTube: mediadomain.MediaToggleEnabled, Artlist: mediadomain.MediaToggleEnabled,
			InternetImages: mediadomain.MediaToggleEnabled,
		}},
	}
	text := "Negli ultimi anni l'esplorazione di Marte è cambiata rapidamente grazie a nuove missioni, rover e aziende private."
	resolver := &t5Resolver{}
	coordinator := scriptgeneration.NewVidRushIncrementalCoordinator(t5Enricher{}, plan, 1)
	coordinator.SetSegmentResearcher(NewVidRushSegmentResearchAdapter(t5ResearchResolver{}))
	coordinator.SetSegmentProviderResolver(resolver)

	err := coordinator.OnSceneCommitted(context.Background(), scriptgeneration.SceneCommitted{
		RunID: "t5-run", SceneID: "scene-t5", SceneIndex: 0, Text: text,
		TextHash: scriptgeneration.SceneTextHash(text), Revision: 1, Language: "it",
	})
	if err != nil {
		t.Fatal(err)
	}
	results, err := coordinator.WaitForVidRush(context.Background(), "t5-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results=%d, want 1", len(results))
	}
	result := results[0]

	if len(result.Insights.ResearchSources) != 1 || result.Insights.ResearchSources[0].ID != "mars-source-1" {
		t.Fatalf("research evidence=%+v, want one online source", result.Insights.ResearchSources)
	}
	if len(result.Insights.Entities) != 2 {
		t.Fatalf("entities=%d, want Mars and Perseverance", len(result.Insights.Entities))
	}
	if len(result.Insights.YouTubeQueries) == 0 || len(result.Insights.ArtlistQueries) == 0 || len(result.Insights.ImageQueries) != 2 {
		t.Fatalf("query plan missing provider-specific queries: %+v", result.Insights)
	}
	if resolver.calls != 1 {
		t.Fatalf("provider resolver calls=%d, want 1", resolver.calls)
	}
	if len(result.Assets.Candidates) != 3 {
		t.Fatalf("provider candidates=%d, want 3", len(result.Assets.Candidates))
	}
	providers := map[string]bool{}
	for _, candidate := range result.Assets.Candidates {
		providers[candidate.Provider] = true
	}
	for _, provider := range []string{scriptpkg.VidRushProviderYouTube, scriptpkg.VidRushProviderArtlist, scriptpkg.VidRushProviderInternetImages} {
		if !providers[provider] {
			t.Fatalf("provider %q missing from resolved candidates: %+v", provider, result.Assets.Candidates)
		}
	}

	windows, err := PlanVisualWindows(VisualWindowPlanningInput{
		SceneID: result.SceneID, SegmentID: result.SegmentID, Text: result.Text, DurationMs: 24000,
		PhraseTimings: []VisualPhraseTiming{
			{Text: "Mars rover", StartMs: 0, EndMs: 8000},
			{Text: "new missions", StartMs: 8000, EndMs: 16000},
			{Text: "private space companies", StartMs: 16000, EndMs: 24000},
		},
		Profile: scriptpkg.SegmentSemanticProfile{SegmentID: result.SegmentID, TextHash: result.TextHash},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) != 3 || windows[0].StartMs != 0 || windows[len(windows)-1].EndMs != 24000 {
		t.Fatalf("visual windows=%+v, want exact 24s coverage", windows)
	}
	for i := 1; i < len(windows); i++ {
		if windows[i-1].EndMs != windows[i].StartMs {
			t.Fatalf("visual window gap/overlap between %d and %d: %+v", i-1, i, windows)
		}
	}
}
