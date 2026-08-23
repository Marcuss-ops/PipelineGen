package adapters_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	adapters "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	localnlp "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/nlp/local"
)

// TestMain installs the same repository lexicon the composition root loads,
// because the local NLP extractor resolves stop/function words through
// linguistics.DefaultLexicon(). No test-only word lists are allowed.
func TestMain(m *testing.M) {
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "../../../../config/lexicons"))
	registry, err := linguistics.NewLexiconRegistry(root)
	if err != nil {
		panic(err)
	}
	if err := linguistics.SetDefaultLexicon(registry); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

// liveCertScene is one controlled scene from the NLP certification payload.
// The "actual configured NLP" is the composition root's local fallback
// (HybridExtractor → CPU deterministic extractor) that runs when Ollama is
// absent, so the E2E path here is exactly the production fallback path.
type liveCertScene struct {
	id     string
	text   string
	person string
	// place is the expected GPE.
	place string
}

func certScenes() []liveCertScene {
	return []liveCertScene{
		{
			id: "scene-dwayne", person: "Dwayne Johnson", place: "Los Angeles",
			text: "Dwayne Johnson trained in Los Angeles. In 2025, Dwayne Johnson described professional wrestling as a discipline built on athletic training, dramatic storytelling, audience connection, and repeated performance under pressure. Wrestling demands training, storytelling, discipline, performance, and resilience.",
		},
		{
			id: "scene-serena", person: "Serena Williams", place: "New York",
			text: "Serena Williams appears in New York. In 2024, Serena Williams described competitive tennis as a combination of disciplined training, mental resilience, tactical preparation, and consistent performance. Tennis rewards preparation, resilience, discipline, competition, and precision.",
		},
		{
			id: "scene-tom", person: "Tom Holland", place: "London",
			text: "Tom Holland works in London. In 2025, Tom Holland described acting as a craft requiring preparation, character development, emotional control, creative storytelling, and repeated performance. Acting depends on storytelling, preparation, creativity, character, and performance.",
		},
		{
			id: "scene-gordon", person: "Gordon Ramsay", place: "London",
			text: "Gordon Ramsay works in London. In 2025, Gordon Ramsay described professional cooking as a discipline combining kitchen technique, ingredient knowledge, preparation, precision, and consistent execution. Cooking requires technique, preparation, precision, ingredients, and discipline.",
		},
		{
			id: "scene-adele", person: "Adele Adkins", place: "London",
			text: "Adele Adkins performs in London. In 2025, Adele Adkins described music as a combination of vocal technique, songwriting, emotional storytelling, careful performance, and audience connection. Music depends on songwriting, vocals, storytelling, performance, and emotion.",
		},
		{
			id: "scene-keanu", person: "Keanu Reeves", place: "Los Angeles",
			text: "Keanu Reeves works in Los Angeles. In 2025, Keanu Reeves described cinema as a collaborative craft involving physical preparation, character work, stunt training, visual storytelling, and disciplined performance. Cinema requires preparation, storytelling, training, character, and performance.",
		},
		{
			id: "scene-lewis", person: "Lewis Hamilton", place: "London",
			text: "Lewis Hamilton appears in London. In 2025, Lewis Hamilton described racing as a discipline combining engineering knowledge, strategic preparation, physical concentration, technical precision, and consistent performance. Racing depends on strategy, engineering, preparation, precision, and performance.",
		},
		{
			id: "scene-taylor", person: "Taylor Swift", place: "New York",
			text: "Taylor Swift appears in New York. In 2025, Taylor Swift described songwriting as a process combining language, musical structure, emotional storytelling, creative revision, and live performance. Songwriting depends on storytelling, creativity, language, revision, and performance.",
		},
		{
			id: "scene-emma", person: "Emma Watson", place: "London",
			text: "Emma Watson appears in London. In 2025, Emma Watson described education as a process involving communication, research, learning, critical thinking, and sustained personal development. Education depends on learning, communication, research, development, and thinking.",
		},
		{
			id: "scene-messi", person: "Lionel Messi", place: "United States",
			text: "Lionel Messi appears in the United States. In 2025, Lionel Messi described football as a sport combining technical control, tactical awareness, coordinated teamwork, physical preparation, and consistent performance. Football depends on technique, teamwork, preparation, awareness, and performance.",
		},
	}
}

// certPlan is the resolved plan that mirrors the certification payload's
// extraction policy: one important phrase and up to five important words per
// segment, forced refresh so the E2E never reads a stale cache.
func certPlan() *scriptpkg.ResolvedGenerationPlan {
	return &scriptpkg.ResolvedGenerationPlan{
		Title:    "NLP Online Images Certification",
		Language: "en",
		Model:    "local",
		MediaPlan: mediadomain.MediaPlanSpec{
			Extraction: mediadomain.MediaExtractionPolicy{
				Enabled:                       true,
				Device:                        "cpu",
				MaxEntitiesPerSegment:         5,
				MaxImportantPhrasesPerSegment: 1,
				MaxImportantWordsPerSegment:   5,
			},
			ForceRefreshExtraction: true,
		},
	}
}

func toSpecScene(s liveCertScene, index int) scriptpkg.SpecScene {
	return scriptpkg.SpecScene{ID: s.id, SegmentID: s.id, Index: index, Text: s.text}
}

func entityValues(seg scriptpkg.VidRushSegmentResult) map[string]string {
	values := make(map[string]string, len(seg.Insights.Entities))
	for _, e := range seg.Insights.Entities {
		values[e.Value] = e.Type
	}
	return values
}

// TestLiveNLPPerSegmentInsightsAcrossTenScenes certifies the incremental
// coordinator path: ten scenes enriched one-by-one must yield ten immutable
// VidRushSegmentResults, each carrying its own scene-scoped important phrase,
// important words, and primary entity. This is the per-segment surface the
// certification must read — never the global aggregate, which is capped at 5.
func TestLiveNLPPerSegmentInsightsAcrossTenScenes(t *testing.T) {
	extractor := localnlp.NewHybridExtractor()
	enricher := adapters.NewVidRushSegmentEnricher(extractor, nil)
	plan := certPlan()
	scenes := certScenes()

	results := make([]scriptpkg.VidRushSegmentResult, 0, len(scenes))
	for i, sc := range scenes {
		res, err := enricher.Enrich(context.Background(), plan, toSpecScene(sc, i))
		if err != nil {
			t.Fatalf("Enrich(%s): %v", sc.id, err)
		}
		results = append(results, res)
	}

	if len(results) != 10 {
		t.Fatalf("per-segment results = %d, want 10", len(results))
	}

	personByScene := make(map[string]string, len(scenes))
	for _, sc := range scenes {
		personByScene[sc.id] = sc.person
	}

	for i, sc := range scenes {
		sc, i := sc, i
		t.Run(sc.id, func(t *testing.T) {
			seg := results[i]
			if seg.SegmentID != sc.id || seg.SceneID != sc.id {
				t.Fatalf("segment identity = %q/%q, want %q", seg.SegmentID, seg.SceneID, sc.id)
			}

			if len(seg.Insights.ImportantPhrases) != 1 {
				t.Fatalf("important phrases = %v, want exactly one per segment", seg.Insights.ImportantPhrases)
			}
			phrase := seg.Insights.ImportantPhrases[0]
			if !strings.Contains(phrase, sc.person) {
				t.Fatalf("phrase %q does not mention scene person %q", phrase, sc.person)
			}

			if n := len(seg.Insights.ImportantWords); n < 3 || n > 5 {
				t.Fatalf("important words = %v (len %d), want 3-5", seg.Insights.ImportantWords, n)
			}

			values := entityValues(seg)
			if _, ok := values[sc.person]; !ok {
				t.Fatalf("entities = %+v, want PERSON %q", seg.Insights.Entities, sc.person)
			}
			if sc.place != "" {
				if _, ok := values[sc.place]; !ok {
					t.Fatalf("entities = %+v, want GPE %q", seg.Insights.Entities, sc.place)
				}
			}

			// Cross-scene contamination: this scene's entities must never
			// contain another scene's person.
			for otherID, otherPerson := range personByScene {
				if otherID == sc.id {
					continue
				}
				if _, ok := values[otherPerson]; ok {
					t.Fatalf("cross-scene contamination: %s entities contain %s (%q)", sc.id, otherID, otherPerson)
				}
			}
		})
	}
}

// TestLiveNLPGlobalAggregateCappedWhileSegmentsKeepAll certifies the contrast
// the spec calls out: the batch path merges per-segment insights into a global
// aggregate that is capped at five phrases/words, while VidRushSegments
// preserves all ten scene-scoped results. Reading per-segment is therefore the
// only correct certification surface.
func TestLiveNLPGlobalAggregateCappedWhileSegmentsKeepAll(t *testing.T) {
	extractor := localnlp.NewHybridExtractor()
	processor := adapters.NewEntitiesProcessorWithCache(extractor, nil)
	plan := certPlan()
	scenes := certScenes()

	specScenes := make([]scriptpkg.SpecScene, 0, len(scenes))
	var joined strings.Builder
	for i, sc := range scenes {
		specScenes = append(specScenes, toSpecScene(sc, i))
		joined.WriteString(sc.text)
		joined.WriteString("\n\n")
	}

	result, err := processor.Process(context.Background(), plan, adapters.ProcessInput{
		Text:      joined.String(),
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: specScenes},
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	if len(result.VidRushSegments) != 10 {
		t.Fatalf("VidRushSegments = %d, want 10 per-segment results", len(result.VidRushSegments))
	}
	if result.Entities == nil {
		t.Fatal("global aggregate is nil")
	}
	if len(result.Entities.ImportantPhrases) > 5 {
		t.Fatalf("global aggregate phrases = %d, want <= 5", len(result.Entities.ImportantPhrases))
	}
	if len(result.Entities.ImportantWords) > 5 {
		t.Fatalf("global aggregate words = %d, want <= 5", len(result.Entities.ImportantWords))
	}

	// The ten per-segment phrases are distinct and scene-scoped, so the
	// aggregate cap demonstrably drops information the segments retain.
	distinct := make(map[string]bool, len(result.VidRushSegments))
	for _, seg := range result.VidRushSegments {
		if len(seg.Insights.ImportantPhrases) != 1 {
			t.Fatalf("segment %s phrases = %v, want exactly one", seg.SegmentID, seg.Insights.ImportantPhrases)
		}
		distinct[seg.Insights.ImportantPhrases[0]] = true
	}
	if len(distinct) != 10 {
		t.Fatalf("distinct per-segment phrases = %d, want 10", len(distinct))
	}
}

// TestLiveNLP_MichaelJordanEntitySurface runs the production local NLP path
// against the certification sentence used by the overlay pipeline. The test
// validates the durable contract for every entity the extractor declares:
// non-empty type/confidence and an exact source-text occurrence. The target
// table is logged rather than hard-coded as a perfect-NER requirement because
// the configured CPU fallback may legitimately omit or classify optional
// candidates differently from an external NER backend.
func TestLiveNLP_MichaelJordanEntitySurface(t *testing.T) {
	text := "Michael Jordan worked with Nike in Chicago. Nike sold ten million Air Jordan shoes in 1996. Michael Jordan became one of the most famous athletes in the world."
	targets := []string{"Michael Jordan", "Nike", "Chicago", "ten million", "Air Jordan", "1996"}

	extractor := localnlp.NewHybridExtractor()
	result, err := extractor.ExtractEntities(context.Background(), scriptpkg.EntityExtractionRequest{
		SegmentID:   "michael-jordan-certification",
		Text:        text,
		Title:       "Michael Jordan and Nike",
		Language:    "en",
		Device:      localnlp.DeviceCPU,
		EntityCount: 20,
	})
	if err != nil {
		t.Fatalf("live NLP extraction failed: %v", err)
	}
	if result == nil {
		t.Fatal("live NLP returned a nil result")
	}

	entities := make([]scriptpkg.Entity, 0, len(result.Persons)+len(result.Places)+len(result.Concepts))
	entities = append(entities, result.Persons...)
	entities = append(entities, result.Places...)
	entities = append(entities, result.Concepts...)
	if len(entities) == 0 {
		t.Fatal("live NLP returned no entities")
	}

	found := make(map[string]scriptpkg.Entity, len(entities))
	lowerText := strings.ToLower(text)
	for _, entity := range entities {
		if strings.TrimSpace(entity.Value) == "" {
			t.Fatalf("live NLP returned an entity with an empty value: %+v", entity)
		}
		if strings.TrimSpace(entity.Type) == "" {
			t.Fatalf("live NLP entity %q has no type", entity.Value)
		}
		if entity.Score <= 0 {
			t.Fatalf("live NLP entity %q has invalid confidence %v", entity.Value, entity.Score)
		}
		// Important words are intentionally normalized to lowercase by the
		// extractor; named entities must still be exact source spans.
		if entity.Type == "KEYWORD" {
			if !strings.Contains(lowerText, strings.ToLower(entity.Value)) {
				t.Fatalf("live NLP invented keyword %q; source=%q", entity.Value, text)
			}
		} else if !strings.Contains(text, entity.Value) {
			t.Fatalf("live NLP invented named entity %q; source=%q", entity.Value, text)
		}
		found[strings.ToLower(entity.Value)] = entity
		t.Logf("entity=%q type=%s confidence=%.2f", entity.Value, entity.Type, entity.Score)
	}

	// Michael Jordan is the mandatory anchor for this certification. The
	// remaining targets are reported individually so the result distinguishes
	// an NLP omission from a downstream overlay loss.
	if entity, ok := found[strings.ToLower("Michael Jordan")]; !ok || entity.Type != "PERSON" {
		t.Fatalf("missing mandatory PERSON Michael Jordan: %+v", entities)
	}
	for _, target := range targets {
		entity, ok := found[strings.ToLower(target)]
		if !ok {
			t.Logf("target=%q status=NOT_EXTRACTED", target)
			continue
		}
		t.Logf("target=%q status=EXTRACTED type=%s confidence=%.2f", target, entity.Type, entity.Score)
	}
	if len(result.ImportantPhrases) != 1 {
		t.Fatalf("important phrases=%v, want exactly one", result.ImportantPhrases)
	}
	t.Logf("important_phrase=%q important_words=%v", result.ImportantPhrases[0], result.ImportantWords)
}
