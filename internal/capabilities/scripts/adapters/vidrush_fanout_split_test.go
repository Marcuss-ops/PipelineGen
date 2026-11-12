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
				InternetImages: mediadomain.MediaToggleEnabled,
			},
		},
	}
	segment := scriptpkg.VidRushSegmentResult{
		SegmentID: "segment-1", TextHash: "hash-1", Text: "scene",
		Insights: scriptpkg.SegmentInsights{
			ImageQueries: []string{" image "},
		},
	}
	fanout := buildVidRushFanoutPlan(plan, segment, &gatedImageSearcher{}, nil)
	if !fanout.imagesEnabled {
		t.Fatalf("provider enablement = images:%v", fanout.imagesEnabled)
	}
	if fanout.perQueryLimit != 50 {
		t.Fatalf("per-query limit = %d, want capped at 50", fanout.perQueryLimit)
	}
	if len(fanout.imageQueries) == 0 {
		t.Fatalf("image queries = %#v, want the scene's canonical query", fanout.imageQueries)
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
	fanout := buildVidRushFanoutPlan(plan, segment, &gatedImageSearcher{}, nil)
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
	fanout := buildVidRushFanoutPlan(plan, segment, &gatedImageSearcher{}, nil)
	if len(fanout.imageQueries) != 1 || fanout.imageQueries[0] != "Apple" {
		t.Fatalf("brand image queries = %#v, want only Apple", fanout.imageQueries)
	}
}

func TestVidRushFanoutPlanPerSceneImagesUsesCanonicalEntityQuery(t *testing.T) {
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
	first := buildVidRushFanoutPlan(plan, base, &gatedImageSearcher{}, nil)
	base.SegmentID, base.TextHash = "scene-3", "hash-3"
	base.Text = "Milton Leite contestou a decisão do Superior Tribunal de Justiça."
	base.SourceText = "A defesa questionou a prisão temporária e contestou os fundamentos da decisão."
	second := buildVidRushFanoutPlan(plan, base, &gatedImageSearcher{}, nil)
	if len(first.imageQueries) != 1 || len(second.imageQueries) != 1 || first.imageQueries[0] != "Milton Leite" || second.imageQueries[0] != "Milton Leite" {
		t.Fatalf("per-scene image queries must use the canonical entity identity: first=%q second=%q", first.imageQueries, second.imageQueries)
	}
}

func TestVidRushFanoutPlanPerSceneImagesKeepsBothPeople(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{MediaPlan: mediadomain.MediaPlanSpec{
		Extraction:     mediadomain.MediaExtractionPolicy{EntityImages: mediadomain.EntityImagePolicy{Enabled: true, Scope: "per_scene"}},
		ProviderPolicy: mediadomain.MediaProviderPolicy{InternetImages: mediadomain.MediaToggleEnabled},
	}}
	segment := scriptpkg.VidRushSegmentResult{
		SegmentID: "pair-01", TextHash: "pair-hash",
		Insights: scriptpkg.SegmentInsights{
			Entities: []scriptpkg.ExtractedEntity{
				{Value: "Alberto Stasi", Type: "PERSON"},
				{Value: "Giada Bocellari", Type: "PERSON"},
			},
			ImageQueries: []string{"Alberto Stasi", "Giada Bocellari"},
		},
	}
	fanout := buildVidRushFanoutPlan(plan, segment, &gatedImageSearcher{}, nil)
	if len(fanout.imageQueries) != 2 || fanout.imageQueries[0] != "Alberto Stasi" || fanout.imageQueries[1] != "Giada Bocellari" {
		t.Fatalf("per-scene image queries = %#v, want one query per person", fanout.imageQueries)
	}
}

func TestVidRushFanoutPlanPerSceneImageQueryIgnoresNarrativeSuffix(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{MediaPlan: mediadomain.MediaPlanSpec{
		Extraction:     mediadomain.MediaExtractionPolicy{EntityImages: mediadomain.EntityImagePolicy{Enabled: true, Scope: "per_scene"}},
		ProviderPolicy: mediadomain.MediaProviderPolicy{InternetImages: mediadomain.MediaToggleEnabled},
	}}
	segment := scriptpkg.VidRushSegmentResult{
		SegmentID: "delitto-e-luogo", TextHash: "hash", SourceText: "Presenta Chiara Poggi, ventiseienne uccisa nella sua abitazione a Garlasco.",
		Insights: scriptpkg.SegmentInsights{
			Entities:     []scriptpkg.ExtractedEntity{{Value: "Chiara Poggi", Type: "PERSON"}},
			ImageQueries: []string{"Chiara Poggi Presenta Chiara Poggi, ventiseienne uccisa"},
		},
	}
	fanout := buildVidRushFanoutPlan(plan, segment, &gatedImageSearcher{}, nil)
	if len(fanout.imageQueries) != 1 || fanout.imageQueries[0] != "Chiara Poggi" {
		t.Fatalf("entity image query = %q, want canonical identity only", fanout.imageQueries)
	}
}

func TestVidRushFanoutMergeKeepsCandidatesWithoutSelectingWinner(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{}
	updated := scriptpkg.VidRushSegmentResult{SegmentID: "segment-1"}
	profile := updated.CanonicalSemanticProfile()
	outcome := vidRushProviderOutcome{
		provider: scriptpkg.VidRushProviderYouTube,
		candidates: []scriptpkg.SegmentAssetCandidate{{
			AssetID: "clip-1", Provider: scriptpkg.VidRushProviderYouTube,
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

// An explicit scene-image budget larger than the provider default must fill
// with generic scene imagery even when identity images are scoped per scene.
// Entity searches yield one image per identity, so a single-place scene would
// otherwise stop at one frame despite images_per_scene=10.
func TestVidRushFanoutPlanPerSceneExplicitBudgetAddsGenericLadderAfterEntities(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		ImagesPerScene: 10,
		MediaPlan: mediadomain.MediaPlanSpec{
			Planner: mediadomain.MediaPlannerPolicy{MaxImagesPerScene: 10},
			Extraction: mediadomain.MediaExtractionPolicy{EntityImages: mediadomain.EntityImagePolicy{
				Enabled: true, Scope: "per_scene", EntityTypes: []string{"PERSON", "GPE"},
			}},
			ProviderPolicy: mediadomain.MediaProviderPolicy{InternetImages: mediadomain.MediaToggleEnabled},
		},
	}
	segment := scriptpkg.VidRushSegmentResult{
		SegmentID: "scene-1", TextHash: "hash-1", Text: "Garlasco, piazza duomo",
		Insights: scriptpkg.SegmentInsights{
			Entities:     []scriptpkg.ExtractedEntity{{Value: "Alberto Stasi", Type: "PERSON"}, {Value: "Garlasco", Type: "GPE"}},
			NounChunks:   []string{"piazza duomo"},
			ImageQueries: []string{"Alberto Stasi", "Garlasco"},
		},
	}
	fanout := buildVidRushFanoutPlan(plan, segment, &gatedImageSearcher{}, nil)
	if len(fanout.imageQueries) < 4 {
		t.Fatalf("image queries = %#v, want the generic ladder after the two identities", fanout.imageQueries)
	}
	if fanout.imageQueries[0] != "Alberto Stasi" || fanout.imageQueries[1] != "Garlasco" {
		t.Fatalf("entity queries must stay first: %#v", fanout.imageQueries)
	}
	found := false
	for _, query := range fanout.imageQueries {
		if strings.Contains(query, "piazza duomo") {
			found = true
		}
	}
	if !found {
		t.Fatalf("image queries = %#v, want a generic scene query", fanout.imageQueries)
	}
}

// Manual image pins are an exclusive retrieval contract. When the payload
// authorizes explicit searches, NER entities, the semantic ladder and the
// scene-sentence fallback must not widen the provider query list — even when
// the explicit list is longer than the per-segment budget.
func TestVidRushFanoutPlanExplicitImageQueriesAreExclusive(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		Title: "explicit pins",
		MediaPlan: mediadomain.MediaPlanSpec{
			Extraction: mediadomain.MediaExtractionPolicy{
				EntityImages:              mediadomain.EntityImagePolicy{Enabled: true, Scope: "per_scene"},
				MaxImageQueriesPerSegment: 1,
			},
			ProviderPolicy: mediadomain.MediaProviderPolicy{InternetImages: mediadomain.MediaToggleEnabled},
			Searches: []mediadomain.SegmentMediaSearch{
				{SegmentID: "scene-1", Slot: mediadomain.SlotSecondaryImage, Query: "Alberto Stasi portrait"},
				{SegmentID: "scene-1", Slot: mediadomain.SlotSecondaryImage, Query: "Garlasco piazza"},
			},
		},
	}
	segment := scriptpkg.VidRushSegmentResult{
		SegmentID: "scene-1", TextHash: "hash-1",
		Text: "Alberto Stasi arriva in tribunale a Garlasco con i giornalisti.",
		Insights: scriptpkg.SegmentInsights{
			Entities:     []scriptpkg.ExtractedEntity{{Value: "Alberto Stasi", Type: "PERSON"}},
			ImageQueries: []string{"Alberto Stasi", "Garlasco", "tribunale"},
		},
	}
	fanout := buildVidRushFanoutPlan(plan, segment, &gatedImageSearcher{}, nil)
	want := []string{"Alberto Stasi portrait", "Garlasco piazza"}
	if len(fanout.imageQueries) != len(want) {
		t.Fatalf("explicit image queries = %#v, want only %#v", fanout.imageQueries, want)
	}
	for i := range want {
		if fanout.imageQueries[i] != want[i] {
			t.Fatalf("explicit image queries = %#v, want only %#v", fanout.imageQueries, want)
		}
	}
}

func TestVidRushFanoutPlanDropsUnusableImageQueries(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{MediaPlan: mediadomain.MediaPlanSpec{
		ProviderPolicy: mediadomain.MediaProviderPolicy{InternetImages: mediadomain.MediaToggleEnabled},
	}}
	segment := scriptpkg.VidRushSegmentResult{
		SegmentID: "scene-16", TextHash: "hash-16", Text: "Il pannello mostra il dettaglio della perizia.",
		Insights: scriptpkg.SegmentInsights{ImageQueries: []string{"I", "Overlay 25 100 1 2", "Garlasco piazza"}},
	}
	fanout := buildVidRushFanoutPlan(plan, segment, &gatedImageSearcher{}, nil)
	for _, query := range fanout.imageQueries {
		if query == "I" || strings.EqualFold(query, "Overlay 25 100 1 2") {
			t.Fatalf("unusable image query survived: %#v", fanout.imageQueries)
		}
	}
}
