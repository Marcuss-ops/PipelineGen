package scriptgeneration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type translatedNLPTestNER struct{}

func (translatedNLPTestNER) Extract(_ context.Context, text string, _ int) ([]VisualEntity, error) {
	return []VisualEntity{{Text: "Dolly Parton", Type: scriptpkg.EntityTypePerson, Score: 0.99}, {Text: "Tennessee", Type: scriptpkg.EntityTypeLocation, Score: 0.95}, {Text: "Imagination Library", Type: scriptpkg.EntityTypeWork, Score: 0.90}}, nil
}

type translatedNLPNameNER struct{}

func (translatedNLPNameNER) Extract(_ context.Context, text string, _ int) ([]VisualEntity, error) {
	var entities []VisualEntity
	for _, name := range []string{"Mike Tyson", "Muhammad Ali", "Las Vegas"} {
		if strings.Contains(text, name) {
			// Simulate the title-case heuristic's known place/person error;
			// the translated model extraction must supply the authoritative type.
			entities = append(entities, VisualEntity{Text: name, Type: scriptpkg.EntityTypePerson, Score: 0.99})
		}
	}
	return entities, nil
}

type translatedNLPDetailed struct{}

func (p translatedNLPDetailed) ExtractImportantPhrases(ctx context.Context, text string, limit int, language, model string) ([]string, error) {
	result, err := p.ExtractSceneNLP(ctx, text, limit, language, model)
	return result.ImportantPhrases, err
}

func (translatedNLPDetailed) ExtractSceneNLP(_ context.Context, text string, _ int, _, _ string) (SceneNLPExtraction, error) {
	phrase, word := "Boxen und Disziplin", "Disziplin"
	if strings.Contains(text, "pugilato") {
		phrase, word = "pugilato e disciplina", "disciplina"
	}
	return SceneNLPExtraction{
		ImportantPhrases: []string{phrase, "invention not in the source"},
		ImportantWords:   []string{word, "unmentioned"},
		SpecialNames:     []string{"Mike Tyson", "Muhammad Ali", "Joe Frazier"},
		Entities: []VisualEntity{
			{Text: "Mike Tyson", Type: scriptpkg.EntityTypePerson, Score: 0.98},
			{Text: "Las Vegas", Type: scriptpkg.EntityTypeLocation, Score: 0.97},
			{Text: "Muhammad Ali", Type: scriptpkg.EntityTypePerson, Score: 0.96},
		},
	}, nil
}

func (p translatedNLPDetailed) ExtractSceneNLPBatch(ctx context.Context, texts []string, limit int, language, model string) ([]SceneNLPExtraction, error) {
	out := make([]SceneNLPExtraction, len(texts))
	for i, text := range texts {
		value, err := p.ExtractSceneNLP(ctx, text, limit, language, model)
		if err != nil {
			return nil, err
		}
		out[i] = value
	}
	return out, nil
}

type translatedNLPTestPhrases struct{}

func (translatedNLPTestPhrases) ExtractImportantPhrases(_ context.Context, _ string, _ int, _, _ string) ([]string, error) {
	return []string{"creative work", "literacy and education", "not present"}, nil
}

// translatedNLPStubPhrase derives a scene-specific, verbatim-present phrase
// from the fixture text. Because every scene carries its own marker phrase, a
// positional mapping error between scenes is detectable in the assertions
// instead of being hidden behind one shared placeholder.
func translatedNLPStubPhrase(sourceText string) string {
	marker := "scena"
	if strings.Contains(sourceText, "szene") {
		marker = "szene"
	}
	digits := ""
	for _, r := range sourceText {
		if r >= '0' && r <= '9' {
			digits += string(r)
		}
	}
	if digits == "" {
		return ""
	}
	return marker + " " + digits
}

// translatedNLPBatchPhrases records how the runner asked for phrases: one
// batched request per language when the batched interface is available, or one
// request per (scene, language) when it is not.
type translatedNLPBatchPhrases struct {
	mu          sync.Mutex
	batchCalls  []int // segment count per batched request
	singleCalls int
	batchErr    error
}

func (p *translatedNLPBatchPhrases) ExtractImportantPhrases(_ context.Context, sourceText string, _ int, _, _ string) ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.singleCalls++
	return []string{translatedNLPStubPhrase(sourceText)}, nil
}

func (p *translatedNLPBatchPhrases) ExtractImportantPhrasesBatch(_ context.Context, sourceTexts []string, _ int, _, _ string) ([][]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.batchErr != nil {
		return nil, p.batchErr
	}
	p.batchCalls = append(p.batchCalls, len(sourceTexts))
	out := make([][]string, len(sourceTexts))
	for i, text := range sourceTexts {
		out[i] = []string{translatedNLPStubPhrase(text)}
	}
	return out, nil
}

func (p *translatedNLPBatchPhrases) counts() (batches []int, singles int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]int(nil), p.batchCalls...), p.singleCalls
}

// translatedNLPSingleOnlyPhrases implements ONLY the single-scene contract, so
// the runner must keep using one call per (scene, language).
type translatedNLPSingleOnlyPhrases struct {
	mu    sync.Mutex
	calls int
}

func (p *translatedNLPSingleOnlyPhrases) ExtractImportantPhrases(_ context.Context, sourceText string, _ int, _, _ string) ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return []string{translatedNLPStubPhrase(sourceText)}, nil
}

func (p *translatedNLPSingleOnlyPhrases) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// translatedNLPMultiSceneResult builds N scenes, each with its own text per
// language, so the phrase fan-out has to be partitioned per scene AND language.
func translatedNLPMultiSceneResult(langs []Language, scenes int) *GenerateResult {
	result := &GenerateResult{}
	for i := 0; i < scenes; i++ {
		texts := make(map[Language]string, len(langs)+1)
		texts["en"] = fmt.Sprintf("Dolly Parton english scene %d here", i)
		for _, lang := range langs {
			if lang == "de" {
				texts[lang] = fmt.Sprintf("Dolly Parton deutsch szene %d hier", i)
				continue
			}
			texts[lang] = fmt.Sprintf("Dolly Parton italiano scena %d finale", i)
		}
		result.Scenes = append(result.Scenes, Scene{ID: fmt.Sprintf("scene-%d", i), Index: i, Text: texts})
	}
	return result
}

func translatedNLPMediaPlan() mediadomain.MediaPlanSpec {
	return mediadomain.MediaPlanSpec{Extraction: mediadomain.MediaExtractionPolicy{
		Include:               []string{mediadomain.ExtractionIncludeEntities, mediadomain.ExtractionIncludeImportantPhrases},
		MaxEntitiesPerSegment: 5, MaxImportantPhrasesPerSegment: 5,
	}}
}

func assertScenePhrase(t *testing.T, result *GenerateResult, sceneIndex int, lang Language, want string) {
	t.Helper()
	annotations := result.Scenes[sceneIndex].LocalizedAnnotations[lang]
	if annotations == nil {
		t.Fatalf("scene %d missing %s annotations", sceneIndex, lang)
	}
	for _, phrase := range annotations.ImportantPhrases {
		if phrase.Text == want {
			return
		}
	}
	t.Fatalf("scene %d/%s phrases = %+v, want %q (per-scene mapping)", sceneIndex, lang, annotations.ImportantPhrases, want)
}

// TestRunTranslatedNLPBatchesPhrasesPerLanguage pins the cost shape: the
// phrase hints of every scene of one language travel in a single request, while
// the per-scene entity extraction (VisualNER) is untouched.
func TestRunTranslatedNLPBatchesPhrasesPerLanguage(t *testing.T) {
	phrases := &translatedNLPBatchPhrases{}
	runner := &Runner{vidRushPipeline: &VidRushPipeline{NERPort: translatedNLPTestNER{}, PhraseExtractor: phrases}}
	req := GenerateRequest{SourceLanguage: "en", Languages: []Language{"it", "de"}, Model: "test-model", MediaPlan: translatedNLPMediaPlan()}
	result := translatedNLPMultiSceneResult([]Language{"it", "de"}, 6)

	if err := runner.runTranslatedNLP(context.Background(), req, result); err != nil {
		t.Fatal(err)
	}

	batches, singles := phrases.counts()
	if singles != 0 {
		t.Fatalf("per-scene phrase calls = %d, want 0 (batched path must own the fan-out)", singles)
	}
	if len(batches) != 2 {
		t.Fatalf("batched phrase calls = %v, want one request per language (2)", batches)
	}
	for _, size := range batches {
		if size != 6 {
			t.Fatalf("batched phrase request covered %d scenes, want all 6 of the language", size)
		}
	}
	for i := range result.Scenes {
		assertScenePhrase(t, result, i, "it", fmt.Sprintf("scena %d", i))
		assertScenePhrase(t, result, i, "de", fmt.Sprintf("szene %d", i))
		if len(result.Scenes[i].LocalizedAnnotations["it"].PrimaryEntities) == 0 {
			t.Fatalf("scene %d/it lost its per-scene entities", i)
		}
	}
}

func TestRunTranslatedNLPProjectsGroundedWordsAndSpecialNamesPerLanguage(t *testing.T) {
	runner := &Runner{vidRushPipeline: &VidRushPipeline{NERPort: translatedNLPNameNER{}, PhraseExtractor: translatedNLPDetailed{}}}
	req := GenerateRequest{
		SourceLanguage: "en",
		Languages:      []Language{"it", "de"},
		Model:          "test-model",
		MediaPlan: mediadomain.MediaPlanSpec{Extraction: mediadomain.MediaExtractionPolicy{
			Include: []string{
				mediadomain.ExtractionIncludeEntities,
				mediadomain.ExtractionIncludeSpecialNames,
				mediadomain.ExtractionIncludeImportantPhrases,
				mediadomain.ExtractionIncludeImportantWords,
			},
			MaxEntitiesPerSegment: 5, MaxImportantPhrasesPerSegment: 5, MaxImportantWordsPerSegment: 5,
		}},
	}
	result := &GenerateResult{Scenes: []Scene{{
		ID: "scene-0", Index: 0,
		Text: map[Language]string{
			"en": "Mike Tyson bridges boxing and discipline. Muhammad Ali inspires athletes.",
			"it": "Mike Tyson lega pugilato e disciplina a Las Vegas. Muhammad Ali ispira molti atleti.",
			"de": "Mike Tyson verbindet Boxen und Disziplin in Las Vegas. Muhammad Ali inspiriert viele Athleten.",
		},
	}}}

	if err := runner.runTranslatedNLP(context.Background(), req, result); err != nil {
		t.Fatal(err)
	}
	for lang, wantPhrase := range map[Language]string{"it": "pugilato e disciplina", "de": "Boxen und Disziplin"} {
		annotations := result.Scenes[0].LocalizedAnnotations[lang]
		if annotations == nil {
			t.Fatalf("missing %s localized annotations", lang)
		}
		if len(annotations.ImportantPhrases) != 1 || annotations.ImportantPhrases[0].Text != wantPhrase {
			t.Errorf("%s important phrases = %+v, want only grounded %q", lang, annotations.ImportantPhrases, wantPhrase)
		}
		wantWord := "disciplina"
		if lang == "de" {
			wantWord = "Disziplin"
		}
		if len(annotations.ImportantWords) != 1 || annotations.ImportantWords[0].Text != wantWord {
			t.Errorf("%s important words = %+v, want only grounded %q", lang, annotations.ImportantWords, wantWord)
		}
		if len(annotations.SpecialNames) != 2 || annotations.SpecialNames[0].Text != "Mike Tyson" || annotations.SpecialNames[1].Text != "Muhammad Ali" {
			t.Errorf("%s special names = %+v, want grounded Tyson and Ali only", lang, annotations.SpecialNames)
		}
		if len(annotations.PrimaryEntities) != 3 {
			t.Errorf("%s primary entities = %+v, want Tyson, Ali and Las Vegas", lang, annotations.PrimaryEntities)
		} else {
			var hasLocation bool
			for _, entity := range annotations.PrimaryEntities {
				if entity.CanonicalName == "Las Vegas" && entity.Type == "GPE" {
					hasLocation = true
				}
			}
			if !hasLocation {
				t.Errorf("%s did not classify Las Vegas as a place: %+v", lang, annotations.PrimaryEntities)
			}
		}
	}
}

// TestRunTranslatedNLPFallsBackToPerSceneWhenBatchFails pins the fail-open
// contract: batching is an optimization, so a batched failure must degrade to
// the per-scene call instead of failing the run or dropping annotations.
func TestRunTranslatedNLPFallsBackToPerSceneWhenBatchFails(t *testing.T) {
	phrases := &translatedNLPBatchPhrases{batchErr: errors.New("batch unavailable")}
	runner := &Runner{vidRushPipeline: &VidRushPipeline{NERPort: translatedNLPTestNER{}, PhraseExtractor: phrases}}
	req := GenerateRequest{SourceLanguage: "en", Languages: []Language{"it"}, Model: "test-model", MediaPlan: translatedNLPMediaPlan()}
	result := translatedNLPMultiSceneResult([]Language{"it"}, 3)

	if err := runner.runTranslatedNLP(context.Background(), req, result); err != nil {
		t.Fatalf("batched failure must not fail the phase: %v", err)
	}
	batches, singles := phrases.counts()
	if len(batches) != 0 {
		t.Fatalf("failed batch recorded as success: %v", batches)
	}
	if singles != 3 {
		t.Fatalf("per-scene fallback calls = %d, want 3", singles)
	}
	for i := range result.Scenes {
		assertScenePhrase(t, result, i, "it", fmt.Sprintf("scena %d", i))
	}
}

// TestRunTranslatedNLPUsesPerSceneCallsWithoutBatchCapability pins that an
// extractor which only implements the single-scene contract keeps working with
// exactly one call per (scene, language).
func TestRunTranslatedNLPUsesPerSceneCallsWithoutBatchCapability(t *testing.T) {
	phrases := &translatedNLPSingleOnlyPhrases{}
	var singleOnly ImportantPhraseExtractor = phrases
	if _, isBatch := singleOnly.(BatchImportantPhraseExtractor); isBatch {
		t.Fatal("test double unexpectedly implements the batched contract")
	}
	runner := &Runner{vidRushPipeline: &VidRushPipeline{NERPort: translatedNLPTestNER{}, PhraseExtractor: singleOnly}}
	req := GenerateRequest{SourceLanguage: "en", Languages: []Language{"it", "de"}, Model: "test-model", MediaPlan: translatedNLPMediaPlan()}
	result := translatedNLPMultiSceneResult([]Language{"it", "de"}, 3)

	if err := runner.runTranslatedNLP(context.Background(), req, result); err != nil {
		t.Fatal(err)
	}
	if got := phrases.count(); got != 6 {
		t.Fatalf("per-scene phrase calls = %d, want 6 (3 scenes x 2 languages)", got)
	}
	for i := range result.Scenes {
		assertScenePhrase(t, result, i, "it", fmt.Sprintf("scena %d", i))
		assertScenePhrase(t, result, i, "de", fmt.Sprintf("szene %d", i))
	}
}

func TestRunTranslatedNLPStoresGroundedPerLanguageAnnotations(t *testing.T) {
	runner := &Runner{vidRushPipeline: &VidRushPipeline{
		NERPort: translatedNLPTestNER{}, PhraseExtractor: translatedNLPTestPhrases{},
	}}
	req := GenerateRequest{
		SourceLanguage: "en",
		Languages:      []Language{"it", "en", "de"},
		Model:          "test-model",
		MediaPlan: mediadomain.MediaPlanSpec{Extraction: mediadomain.MediaExtractionPolicy{
			Include:               []string{mediadomain.ExtractionIncludeEntities, mediadomain.ExtractionIncludeImportantPhrases},
			MaxEntitiesPerSegment: 5, MaxImportantPhrasesPerSegment: 5,
		}},
	}
	result := &GenerateResult{Scenes: []Scene{{ID: "scene-0", Index: 0, Text: map[Language]string{
		"en": "Dolly Parton created the Imagination Library in Tennessee.",
		"it": "Dolly Parton ha creato Imagination Library in Tennessee per sostenere il lavoro creativo.",
		"de": "Dolly Parton gründete die Imagination Library in Tennessee für kreative Arbeit.",
	}}}}

	if err := runner.runTranslatedNLP(context.Background(), req, result); err != nil {
		t.Fatal(err)
	}
	for _, lang := range []Language{"it", "de"} {
		annotations := result.Scenes[0].LocalizedAnnotations[lang]
		if annotations == nil {
			t.Fatalf("missing annotations for %s", lang)
		}
		if annotations.Language != string(lang) {
			t.Fatalf("annotation language = %q, want %q", annotations.Language, lang)
		}
		if len(annotations.PrimaryEntities)+len(annotations.SecondaryEntities) > 5 {
			t.Fatalf("too many entities for %s: %+v", lang, annotations)
		}
		if len(annotations.ImportantPhrases) > 5 {
			t.Fatalf("too many phrases for %s: %+v", lang, annotations)
		}
		for _, phrase := range annotations.ImportantPhrases {
			if phrase.Text == "not present" {
				t.Fatalf("ungrounded phrase returned for %s", lang)
			}
		}
	}
}

func TestAnnotationForLanguageDoesNotLeakSourceAnnotations(t *testing.T) {
	scene := Scene{
		Annotations: &scriptpkg.SceneAnnotations{Language: "en"},
		LocalizedAnnotations: map[Language]*scriptpkg.SceneAnnotations{
			"it": {Language: "it"},
		},
	}
	if got := annotationForLanguage(scene, "it"); got == nil || got.Language != "it" {
		t.Fatalf("localized annotation not selected: %+v", got)
	}
	if got := annotationForLanguage(scene, "fr"); got != nil {
		t.Fatalf("source annotation leaked into fr document: %+v", got)
	}
}
