package adapters

import (
	"context"
	"strings"
	"sync"
	"testing"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

type boundaryEntityExtractor struct {
	calls []string
}

func (e *boundaryEntityExtractor) ExtractEntities(_ context.Context, req scriptpkg.EntityExtractionRequest) (*scriptpkg.EntityResult, error) {
	e.calls = append(e.calls, req.Text)
	return &scriptpkg.EntityResult{Concepts: []scriptpkg.Entity{{Value: strings.Fields(req.Text)[0], Type: "CONCEPT"}}}, nil
}

func TestEntitiesProcessorMaterializesSceneTextWhenTopLevelTextIsEmpty(t *testing.T) {
	extractor := &boundaryEntityExtractor{}
	processor := NewEntitiesProcessor(extractor)
	plan := &scriptpkg.ResolvedGenerationPlan{Language: "en", Title: "boundary", Model: "fake"}
	input := ProcessInput{SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{{
		ID: "scene-0", Index: 0,
		Text: "A scientist examines a fossil.\n\nA chef prepares a dish.\n\nAn architect reviews blueprints.",
	}}}}

	result, err := processor.Process(context.Background(), plan, input)
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
	if got := len(extractor.calls); got != 3 {
		t.Fatalf("extractor calls = %d, want 3", got)
	}
	if got := len(result.VidRushSegments); got != 3 {
		t.Fatalf("returned segments = %d, want 3", got)
	}
	if got := len(result.UpdatedSpecScene.Scenes); got != 3 {
		t.Fatalf("returned scenes = %d, want 3", got)
	}
	if result.Entities == nil || len(result.Entities.Concepts) == 0 {
		t.Fatal("expected non-empty entity aggregate")
	}
}

func TestRegistryPreservesMaterializedSceneCountAfterEntities(t *testing.T) {
	extractor := &boundaryEntityExtractor{}
	registry := NewPostProcessorRegistry(nil)
	if !registry.Register(NewEntitiesProcessor(extractor)) {
		t.Fatal("failed to register entities processor")
	}
	plan := &scriptpkg.ResolvedGenerationPlan{
		Language: "en", Title: "boundary", Model: "fake",
		Postprocessors: []string{string(ProcessorEntities)},
	}
	input := ProcessInput{SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{{
		ID: "scene-0", Index: 0,
		Text: "A scientist examines a fossil.\n\nA chef prepares a dish.\n\nAn architect reviews blueprints.",
	}}}}

	result, err := registry.Run(context.Background(), plan, input)
	if err != nil {
		t.Fatalf("registry Run returned error: %v", err)
	}
	if got := len(result.VidRushSegments); got != 3 {
		t.Fatalf("registry segments = %d, want 3", got)
	}
	if got := len(result.FinalSpecScene.Scenes); got != 3 {
		t.Fatalf("registry final scenes = %d, want 3", got)
	}
}

func TestResolveManualSegmentQueriesFiltersAndDeduplicates(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		MediaPlan: mediadomain.MediaPlanSpec{Searches: []mediadomain.SegmentMediaSearch{
			{SegmentID: "main", Slot: mediadomain.SlotPrimaryVideo, Query: " Maya temples ", Providers: []string{"ARTLIST"}, MediaTypes: []string{"video"}},
			{SegmentID: "main", Slot: mediadomain.SlotPrimaryVideo, Query: "maya temples", Providers: []string{"artlist"}, MediaTypes: []string{"video"}},
			{SegmentID: "main", Slot: mediadomain.SlotSecondaryImage, Query: "Maya pyramid", Providers: []string{"internet_images"}, MediaTypes: []string{"image"}},
		}},
	}
	segment := scriptpkg.CanonicalSegment{ID: "main"}
	if got := ResolveManualSegmentQueries(plan, segment, scriptpkg.VidRushProviderArtlist, mediadomain.SlotPrimaryVideo); len(got) != 1 || got[0] != "Maya temples" {
		t.Fatalf("artlist queries = %v, want one stable deduplicated query", got)
	}
	if got := ResolveManualSegmentQueries(plan, segment, scriptpkg.VidRushProviderInternetImages, mediadomain.SlotSecondaryImage); len(got) != 1 || got[0] != "Maya pyramid" {
		t.Fatalf("image queries = %v, want Maya pyramid", got)
	}
}

func TestResolveManualSegmentQueriesLockedAssignmentWins(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{MediaPlan: mediadomain.MediaPlanSpec{
		Assignments: []mediadomain.SegmentMediaAssignment{{SegmentID: "main", Slot: mediadomain.SlotPrimaryVideo, Locked: true}},
		Searches:    []mediadomain.SegmentMediaSearch{{SegmentID: "main", Slot: mediadomain.SlotPrimaryVideo, Query: "manual query"}},
	}}
	if got := ResolveManualSegmentQueries(plan, scriptpkg.CanonicalSegment{ID: "main"}, scriptpkg.VidRushProviderArtlist, mediadomain.SlotPrimaryVideo); len(got) != 0 {
		t.Fatalf("locked assignment queries = %v, want none", got)
	}
}

func TestBuildVidRushSegmentResultPrefersManualQueries(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{Topic: "fallback topic", MediaPlan: mediadomain.MediaPlanSpec{Searches: []mediadomain.SegmentMediaSearch{
		{SegmentID: "main", Slot: mediadomain.SlotPrimaryVideo, Query: "ancient Maya temples jungle aerial cinematic", Providers: []string{"artlist"}, MediaTypes: []string{"video"}},
		{SegmentID: "main", Slot: mediadomain.SlotSecondaryImage, Query: "Chichen Itza Maya pyramid Yucatan", Providers: []string{"internet_images"}, MediaTypes: []string{"image"}},
	}}}
	result := buildVidRushSegmentResult(plan, scriptpkg.CanonicalSegment{ID: "main", Text: "Maya temples"}, &scriptpkg.EntityResult{}, 8, 1, 5, 5, 5)
	if !strings.Contains(strings.Join(result.Insights.ArtlistQueries, " | "), "ancient Maya temples jungle aerial cinematic") {
		t.Fatalf("Artlist queries = %v, want manual query", result.Insights.ArtlistQueries)
	}
	if !strings.Contains(strings.Join(result.Insights.ImageQueries, " | "), "Chichen Itza Maya pyramid Yucatan") {
		t.Fatalf("image queries = %v, want manual query", result.Insights.ImageQueries)
	}
}

func TestSegmentSpecSceneContextIsolatesCurrentScene(t *testing.T) {
	input := scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{
			{ID: "scene-1", SegmentID: "segment-001", Index: 0, Text: "first", Kind: scriptpkg.SceneClip},
			{ID: "scene-2", SegmentID: "segment-002", Index: 1, Text: "second", Kind: scriptpkg.SceneImage},
		},
	}

	got := segmentSpecSceneContext(input, scriptpkg.CanonicalSegment{
		ID:       "segment-002",
		SceneID:  "scene-2",
		Position: 1,
	})
	if len(got.Scenes) != 1 {
		t.Fatalf("expected one isolated scene, got %d", len(got.Scenes))
	}
	if got.Scenes[0].ID != "scene-2" {
		t.Fatalf("isolated scene id = %q, want scene-2", got.Scenes[0].ID)
	}
	if got.Scenes[0].Index != 0 {
		t.Fatalf("isolated scene index = %d, want 0", got.Scenes[0].Index)
	}
}

func TestSegmentQueryContextPrefersSourceSegmentOverGeneratedProse(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		SourceText: "Aerial drone footage reveals a winding coastal road at golden hour.\n\nA barista crafts latte art in a coffee shop.",
	}
	segment := scriptpkg.CanonicalSegment{ID: "segment-001", Position: 0, Text: "The world unfolds beneath us in a breathtaking tapestry."}
	if got := segmentQueryContext(plan, segment); got != "Aerial drone footage reveals a winding coastal road at golden hour." {
		t.Fatalf("query context = %q, want source paragraph", got)
	}
	result := buildVidRushSegmentResult(plan, segment, &scriptpkg.EntityResult{}, 5, 5, 5, 5, 5, segmentQueryContext(plan, segment))
	if !strings.Contains(strings.Join(result.Insights.ArtlistQueries, " | "), "coastal road") {
		t.Fatalf("Artlist queries = %v, want source-grounded coastal road query", result.Insights.ArtlistQueries)
	}
}

func TestBuildVidRushSegmentResultPreservesEntityType(t *testing.T) {
	result := buildVidRushSegmentResult(
		&scriptpkg.ResolvedGenerationPlan{},
		scriptpkg.CanonicalSegment{ID: "segment-001", Text: "OpenAI research", TextHash: "hash"},
		&scriptpkg.EntityResult{Concepts: []scriptpkg.Entity{{Value: "OpenAI", Type: "ORGANIZATION", Score: 0.98}}},
		5,
		5,
		5,
		5,
		5,
	)
	if len(result.Insights.Entities) != 1 {
		t.Fatalf("expected one entity, got %d", len(result.Insights.Entities))
	}
	if result.Insights.Entities[0].Type != "ORGANIZATION" {
		t.Fatalf("entity type = %q, want ORGANIZATION", result.Insights.Entities[0].Type)
	}
}

func TestEntitiesProcessorEnrichSegmentBuildsSingleSegmentResult(t *testing.T) {
	vidrushExtractionCache = sync.Map{}
	extractor := &boundaryEntityExtractor{}
	processor := NewEntitiesProcessor(extractor)
	plan := &scriptpkg.ResolvedGenerationPlan{Language: "en", Title: "single", Model: "fake"}
	segment := scriptpkg.CanonicalSegment{
		ID: "segment-001", SceneID: "scene-0", Position: 0,
		Text:     "A scientist examines a fossil.",
		TextHash: segmentTextHash("A scientist examines a fossil."),
	}

	outcome := processor.enrichSegment(context.Background(), plan, scriptpkg.SpecSceneOutput{Version: 1}, segment,
		segmentExtractionLimits{entities: 5, phrases: 1, words: 5, artlist: 5, images: 5})
	if outcome.err != nil {
		t.Fatalf("enrichSegment error: %v", outcome.err)
	}
	if outcome.unavailable != nil {
		t.Fatalf("enrichSegment unavailable: %v", outcome.unavailable)
	}
	if outcome.segment.SegmentID != "segment-001" {
		t.Fatalf("segment id = %q, want segment-001", outcome.segment.SegmentID)
	}
	if outcome.segment.SceneID != "scene-0" {
		t.Fatalf("scene id = %q, want scene-0", outcome.segment.SceneID)
	}
	if outcome.segment.TextHash != segment.TextHash {
		t.Fatalf("text hash = %q, want %q", outcome.segment.TextHash, segment.TextHash)
	}
	if len(outcome.segment.Insights.Entities) == 0 {
		t.Fatal("expected at least one extracted entity")
	}
	if outcome.segment.Cache.Extraction != "MISS" {
		t.Fatalf("extraction cache state = %q, want MISS", outcome.segment.Cache.Extraction)
	}
}

func TestEntitiesProcessorEnrichSegmentCacheHit(t *testing.T) {
	vidrushExtractionCache = sync.Map{}
	extractor := &boundaryEntityExtractor{}
	processor := NewEntitiesProcessor(extractor)
	plan := &scriptpkg.ResolvedGenerationPlan{Language: "en", Title: "cache", Model: "fake"}
	segment := scriptpkg.CanonicalSegment{
		ID: "segment-cache", Position: 0,
		Text:     "An architect reviews blueprints.",
		TextHash: segmentTextHash("An architect reviews blueprints."),
	}
	limits := segmentExtractionLimits{entities: 5, phrases: 1, words: 5, artlist: 5, images: 5}

	first := processor.enrichSegment(context.Background(), plan, scriptpkg.SpecSceneOutput{Version: 1}, segment, limits)
	if first.err != nil || first.unavailable != nil {
		t.Fatalf("first enrichSegment failed: err=%v unavailable=%v", first.err, first.unavailable)
	}
	if first.segment.Cache.Extraction != "MISS" {
		t.Fatalf("first extraction state = %q, want MISS", first.segment.Cache.Extraction)
	}

	second := processor.enrichSegment(context.Background(), plan, scriptpkg.SpecSceneOutput{Version: 1}, segment, limits)
	if second.err != nil || second.unavailable != nil {
		t.Fatalf("second enrichSegment failed: err=%v unavailable=%v", second.err, second.unavailable)
	}
	if !second.cached || second.segment.Cache.Extraction != "HIT_EXACT" {
		t.Fatalf("second extraction state = %q (cached=%v), want HIT_EXACT cached=true", second.segment.Cache.Extraction, second.cached)
	}
}

func TestGeneratedRetrievalQueriesAreSearchReady(t *testing.T) {
	artlist := buildArtlistQueries(
		"In the summer of 1969, millions watched as American astronauts prepared for one of the most important missions in human history.",
		[]scriptpkg.ExtractedEntity{{Value: "Apollo 11 astronauts", Type: "EVENT"}},
		[]string{
			"NASA mission control",
			"astronauts prepared for one of the most important missions",
			"TYPE action scene",
		},
		[]string{"Saturn", "V launch", "moon mission"},
		"historical documentary",
	)
	if len(artlist) == 0 || len(artlist) > 5 {
		t.Fatalf("artlist queries = %v, want 1-5 search queries", artlist)
	}
	for _, query := range artlist {
		words := strings.Fields(query)
		if len(words) < 2 || len(words) > 6 {
			t.Errorf("artlist query %q has %d words, want 2-6", query, len(words))
		}
		if strings.ContainsAny(query, ".!?\n") {
			t.Errorf("artlist query %q contains narrative punctuation", query)
		}
		if strings.Contains(query, "cinematic") || strings.Contains(query, "action scene") {
			t.Errorf("artlist query %q contains a generic retrieval suffix", query)
		}
	}
	if strings.Contains(strings.Join(artlist, " | "), "millions watched") {
		t.Errorf("narrative sentence leaked into Artlist queries: %v", artlist)
	}

	images := buildImageQueries(
		"Elon Musk presented the new spacecraft at SpaceX headquarters in Texas.",
		[]scriptpkg.ExtractedEntity{{Value: "Elon Musk", Type: "PERSON"}},
		[]string{"Elon Musk SpaceX presentation", "Subject visual scene"},
		[]string{"spacecraft", "presentation", "Texas"},
		"generic topic",
	)
	for _, query := range images {
		words := strings.Fields(query)
		if len(words) < 2 || len(words) > 8 {
			t.Errorf("image query %q has %d words, want 2-8", query, len(words))
		}
	}
	if strings.Contains(strings.Join(images, " | "), "Subject") {
		t.Errorf("placeholder leaked into image queries: %v", images)
	}
}
