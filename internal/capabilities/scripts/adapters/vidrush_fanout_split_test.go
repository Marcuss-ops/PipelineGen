package adapters

import (
	"strings"
	"testing"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestVidRushFanoutPlanPreservesProviderPolicyAndInputs(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		Title: "scene title",
		MediaPlan: mediadomain.MediaPlanSpec{
			Planner: mediadomain.MediaPlannerPolicy{CandidateLimit: 75},
			ProviderPolicy: mediadomain.MediaProviderPolicy{
				Artlist:        mediadomain.MediaToggleEnabled,
				InternetImages: mediadomain.MediaToggleEnabled,
			},
		},
	}
	segment := scriptpkg.VidRushSegmentResult{
		SegmentID: "segment-1", TextHash: "hash-1", Text: "scene",
		Insights: scriptpkg.SegmentInsights{
			ArtlistQueries: []string{" artlist "}, ImageQueries: []string{" image "},
		},
	}
	fanout := buildVidRushFanoutPlan(plan, segment, &gatedArtlistSearcher{}, &gatedImageSearcher{}, nil)
	if !fanout.artlistEnabled || !fanout.imagesEnabled {
		t.Fatalf("provider enablement = artlist:%v images:%v", fanout.artlistEnabled, fanout.imagesEnabled)
	}
	if fanout.perQueryLimit != 50 {
		t.Fatalf("per-query limit = %d, want capped at 50", fanout.perQueryLimit)
	}
	if len(fanout.artlistQueries) != 1 || fanout.artlistQueries[0] != "artlist" {
		t.Fatalf("artlist queries = %#v, want normalized canonical query", fanout.artlistQueries)
	}
}

func TestVidRushFanoutPlanEntityImagesExcludeVisualConceptQueries(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		Title: "Trump scene",
		MediaPlan: mediadomain.MediaPlanSpec{
			Extraction: mediadomain.MediaExtractionPolicy{
				EntityImages: mediadomain.EntityImagePolicy{Enabled: true},
			},
			ProviderPolicy: mediadomain.MediaProviderPolicy{InternetImages: mediadomain.MediaToggleEnabled},
		},
	}
	segment := scriptpkg.VidRushSegmentResult{
		SegmentID: "scene-1", TextHash: "hash-1", Text: "Donald Trump's public life",
		Insights: scriptpkg.SegmentInsights{
			Entities:                []scriptpkg.ExtractedEntity{{Value: "Donald Trump's", Type: "PERSON"}, {Value: "Donald Trump’s", Type: "PERSON"}},
			ImageQueries:            []string{"Donald Trump's", "important public life"},
			ImageEntityCanonicalIDs: map[string]string{"donald trump's": "person:donald-trump"},
		},
	}
	fanout := buildVidRushFanoutPlan(plan, segment, nil, &gatedImageSearcher{}, nil)
	if len(fanout.imageQueries) != 2 || fanout.imageQueries[0] != "Donald Trump's" || fanout.imageQueries[1] != "Donald Trump’s" {
		t.Fatalf("entity image queries = %#v, want both PERSON surfaces", fanout.imageQueries)
	}
}

func TestVidRushFanoutPlanIncludesBrandImageQueries(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{MediaPlan: mediadomain.MediaPlanSpec{
		Extraction:     mediadomain.MediaExtractionPolicy{Include: []string{mediadomain.ExtractionIncludeBrands}},
		ProviderPolicy: mediadomain.MediaProviderPolicy{InternetImages: mediadomain.MediaToggleEnabled},
	}}
	segment := scriptpkg.VidRushSegmentResult{
		SegmentID: "brand-scene", Text: "Apple released a new product.",
		Insights: scriptpkg.SegmentInsights{
			Entities:     []scriptpkg.ExtractedEntity{{Value: "Apple", Type: "LOGO"}, {Value: "25%", Type: "PERCENT"}},
			ImageQueries: []string{"Apple", "25%"},
		},
	}
	fanout := buildVidRushFanoutPlan(plan, segment, nil, &gatedImageSearcher{}, nil)
	if len(fanout.imageQueries) != 1 || fanout.imageQueries[0] != "Apple" {
		t.Fatalf("brand image queries = %#v, want only Apple", fanout.imageQueries)
	}
}

func TestVidRushFanoutPlanPerSceneImagesUsesSceneSpecificQuery(t *testing.T) {
	if !(mediadomain.EntityImagePolicy{Scope: "per_scene"}).PerScene() {
		t.Fatal("per_scene scope was not recognized")
	}
	plan := &scriptpkg.ResolvedGenerationPlan{
		MediaPlan: mediadomain.MediaPlanSpec{
			Extraction:     mediadomain.MediaExtractionPolicy{EntityImages: mediadomain.EntityImagePolicy{Enabled: true, Scope: "per_scene"}},
			ProviderPolicy: mediadomain.MediaProviderPolicy{InternetImages: mediadomain.MediaToggleEnabled},
		},
	}
	base := scriptpkg.VidRushSegmentResult{
		SegmentID: "scene-2", TextHash: "hash-2", Text: "Milton Leite compareceu ao tribunal para a audiência de custódia.",
		SourceText: "A operação Vectura Corrupta cumpriu dezesseis mandados e prendeu Milton Leite.",
		Insights:   scriptpkg.SegmentInsights{Entities: []scriptpkg.ExtractedEntity{{Value: "Milton Leite", Type: "PERSON"}}, ImageQueries: []string{"Milton Leite"}},
	}
	first := buildVidRushFanoutPlan(plan, base, nil, &gatedImageSearcher{}, nil)
	base.SegmentID, base.TextHash = "scene-3", "hash-3"
	base.Text = "Milton Leite contestou a decisão do Superior Tribunal de Justiça."
	base.SourceText = "A defesa questionou a prisão temporária e contestou os fundamentos da decisão."
	second := buildVidRushFanoutPlan(plan, base, nil, &gatedImageSearcher{}, nil)
	if len(first.imageQueries) == 0 || len(second.imageQueries) == 0 || first.imageQueries[0] == second.imageQueries[0] {
		t.Fatalf("per-scene image queries are not scene-specific: first=%q second=%q", first.imageQueries, second.imageQueries)
	}
	if len(first.imageQueries) != 1 || len(second.imageQueries) != 1 {
		t.Fatalf("per-scene image fanout must stay bounded to one query: first=%q second=%q", first.imageQueries, second.imageQueries)
	}
	if strings.Contains(strings.ToLower(first.imageQueries[0]), "boxing") || strings.Contains(strings.ToLower(second.imageQueries[0]), "boxing") {
		t.Fatalf("scene image queries contain unrelated boxing suffix: first=%q second=%q", first.imageQueries, second.imageQueries)
	}
}

func TestVidRushFanoutMergeKeepsCandidatesWithoutSelectingWinner(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{}
	updated := scriptpkg.VidRushSegmentResult{SegmentID: "segment-1"}
	profile := updated.CanonicalSemanticProfile()
	outcome := vidRushProviderOutcome{
		provider: scriptpkg.VidRushProviderArtlist,
		candidates: []scriptpkg.SegmentAssetCandidate{{
			AssetID: "clip-1", Provider: scriptpkg.VidRushProviderArtlist,
			SourceURL: "https://cdn.example/clip-1.m3u8", RelevanceScore: 0.9,
		}},
	}
	if err := mergeVidRushProviderOutcome(&updated, outcome, plan, profile, updated.SegmentID); err != nil {
		t.Fatal(err)
	}
	if len(updated.Assets.Candidates) != 1 {
		t.Fatalf("candidate count = %d, want 1", len(updated.Assets.Candidates))
	}
	if updated.Assets.PrimaryVideo != nil {
		t.Fatalf("provider discovery selected a primary: %+v", updated.Assets.PrimaryVideo)
	}
}
