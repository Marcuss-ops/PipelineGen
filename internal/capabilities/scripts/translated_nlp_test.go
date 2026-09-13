package scriptgeneration

import (
	"context"
	"testing"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type translatedNLPTestNER struct{}

func (translatedNLPTestNER) Extract(_ context.Context, text string, _ int) ([]VisualEntity, error) {
	return []VisualEntity{{Text: "Dolly Parton", Type: scriptpkg.EntityTypePerson, Score: 0.99}, {Text: "Tennessee", Type: scriptpkg.EntityTypeLocation, Score: 0.95}, {Text: "Imagination Library", Type: scriptpkg.EntityTypeWork, Score: 0.90}}, nil
}

type translatedNLPTestPhrases struct{}

func (translatedNLPTestPhrases) ExtractImportantPhrases(_ context.Context, _ string, _ int, _, _ string) ([]string, error) {
	return []string{"creative work", "literacy and education", "not present"}, nil
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
