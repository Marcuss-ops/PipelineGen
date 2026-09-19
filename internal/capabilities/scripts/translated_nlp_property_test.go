package scriptgeneration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
	phrasepkg "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/phrases"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

var translatedPropertyLanguages = []Language{"it", "en", "pl", "ru", "de", "es", "pt-BR", "fr", "tr", "id"}

// Three new multilingual texts keep this property test independent from the
// Mike Tyson oracle and from the phrase strings embedded in generation
// fixtures. Only entity surfaces are supplied as blocked semantic input.
var translatedPropertyTexts = []map[Language]string{
	{
		"it": "Mike Tyson allena movimenti disciplinati a Brooklyn.", "en": "Mike Tyson trains disciplined movement in Brooklyn.", "pl": "Mike Tyson trenuje zdyscyplinowane ruchy w Brooklyn.", "ru": "Mike Tyson тренирует дисциплинированные движения в Brooklyn.", "de": "Mike Tyson trainiert disziplinierte Bewegungen in Brooklyn.", "es": "Mike Tyson entrena movimientos disciplinados en Brooklyn.", "pt-BR": "Mike Tyson treina movimentos disciplinados em Brooklyn.", "fr": "Mike Tyson entraîne des mouvements disciplinés à Brooklyn.", "tr": "Mike Tyson Brooklyn'de disiplinli hareketler çalışır.", "id": "Mike Tyson melatih gerakan disiplin di Brooklyn.",
	},
	{
		"it": "NASA coordina missioni scientifiche da Cape Canaveral.", "en": "NASA coordinates scientific missions from Cape Canaveral.", "pl": "NASA koordynuje misje naukowe z Cape Canaveral.", "ru": "NASA координирует научные миссии из Cape Canaveral.", "de": "NASA koordiniert wissenschaftliche Missionen aus Cape Canaveral.", "es": "NASA coordina misiones científicas desde Cape Canaveral.", "pt-BR": "NASA coordena missões científicas de Cape Canaveral.", "fr": "NASA coordonne des missions scientifiques depuis Cape Canaveral.", "tr": "NASA Cape Canaveral'dan bilim görevlerini koordine eder.", "id": "NASA mengoordinasikan misi ilmiah dari Cape Canaveral.",
	},
	{
		"it": "Christopher Wray dirige politiche pubbliche a Washington.", "en": "Christopher Wray directs public policy in Washington.", "pl": "Christopher Wray kieruje polityką publiczną w Washington.", "ru": "Christopher Wray руководит государственной политикой в Washington.", "de": "Christopher Wray leitet öffentliche Politik in Washington.", "es": "Christopher Wray dirige políticas públicas en Washington.", "pt-BR": "Christopher Wray dirige políticas públicas em Washington.", "fr": "Christopher Wray dirige la politique publique à Washington.", "tr": "Christopher Wray Washington'da kamu politikasını yönetir.", "id": "Christopher Wray mengarahkan kebijakan publik di Washington.",
	},
}

func TestTranslatedNLPPhrasePropertiesDoNotDependOnFixturePhrases(t *testing.T) {
	for caseIndex, texts := range translatedPropertyTexts {
		first := runTranslatedPropertyCase(t, caseIndex, texts)
		second := runTranslatedPropertyCase(t, caseIndex, texts)
		for _, language := range translatedPropertyLanguages {
			firstPhrases := propertyPhrasesForLanguage(t, first, caseIndex, language, texts[language])
			secondPhrases := propertyPhrasesForLanguage(t, second, caseIndex, language, texts[language])
			if len(firstPhrases) == 0 {
				t.Fatalf("case %d/%s produced zero phrases", caseIndex, language)
			}
			if got, want := propertyHash(firstPhrases), propertyHash(secondPhrases); got != want {
				t.Fatalf("case %d/%s is not deterministic: %s != %s", caseIndex, language, got, want)
			}
			assertTranslatedPropertyPhrases(t, caseIndex, language, texts[language], firstPhrases, propertyEntitySurfaces(first, caseIndex, language, texts[language]))
		}
	}
}

func runTranslatedPropertyCase(t *testing.T, caseIndex int, texts map[Language]string) *GenerateResult {
	t.Helper()
	entities := propertyEntitiesForCase(caseIndex, texts["en"])
	scene := Scene{ID: "property-scene", Index: 0, Text: texts, Annotations: &scriptpkg.SceneAnnotations{Language: "en"}}
	for _, entity := range entities {
		scene.Annotations.PrimaryEntities = append(scene.Annotations.PrimaryEntities, scriptpkg.AnnotatedEntity{Text: entity, CanonicalName: entity, Type: propertyEntityType(entity)})
	}
	result := &GenerateResult{SourceLanguage: "en", Scenes: []Scene{scene}}
	runner := &Runner{vidRushPipeline: &VidRushPipeline{NERPort: propertyNER{}}}
	request := GenerateRequest{SourceLanguage: "en", Languages: translatedPropertyLanguages, MediaPlan: mediadomainPropertyPlan()}
	if err := runner.runTranslatedNLP(context.Background(), request, result); err != nil {
		t.Fatalf("case %d translated NLP: %v", caseIndex, err)
	}
	return result
}

func propertyPhrasesForLanguage(t *testing.T, result *GenerateResult, caseIndex int, language Language, text string) []string {
	t.Helper()
	if language == "en" {
		return phrasepkg.ImportantPhrases(text, propertyEntitySpans(text, propertyEntitiesForCase(caseIndex, text)), 5, string(language))
	}
	annotations := result.Scenes[0].LocalizedAnnotations[language]
	if annotations == nil {
		t.Fatalf("missing localized annotations for %s", language)
	}
	phrases := make([]string, 0, len(annotations.ImportantPhrases))
	for _, phrase := range annotations.ImportantPhrases {
		phrases = append(phrases, phrase.Text)
	}
	return phrases
}

func propertyEntitySurfaces(result *GenerateResult, caseIndex int, language Language, text string) []string {
	if language == "en" {
		return propertyEntitiesForCase(caseIndex, text)
	}
	annotations := result.Scenes[0].LocalizedAnnotations[language]
	if annotations == nil {
		return nil
	}
	var surfaces []string
	for _, entity := range append(append([]scriptpkg.AnnotatedEntity(nil), annotations.PrimaryEntities...), annotations.SecondaryEntities...) {
		if strings.TrimSpace(entity.Text) != "" {
			surfaces = append(surfaces, entity.Text)
		}
	}
	return surfaces
}

func assertTranslatedPropertyPhrases(t *testing.T, caseIndex int, language Language, text string, phrases []string, entities []string) {
	t.Helper()
	profile := propertyLexicon(language)
	blocked := propertyEntitySpans(text, entities)
	for _, phrase := range phrases {
		if !strings.Contains(text, phrase) {
			t.Fatalf("case %d/%s phrase %q is not contained in localized text", caseIndex, language, phrase)
		}
		words := strings.Fields(phrase)
		if len(words) < 2 || len(words) > 4 {
			t.Fatalf("case %d/%s phrase %q has %d words, want 2..4", caseIndex, language, phrase, len(words))
		}
		first := strings.ToLower(words[0])
		last := strings.ToLower(strings.Trim(words[len(words)-1], ".,!?;:"))
		if profile != nil {
			if _, ok := profile.StopWords[first]; ok {
				t.Fatalf("case %d/%s phrase %q starts with stop word", caseIndex, language, phrase)
			}
			if _, ok := profile.FunctionWords[first]; ok {
				t.Fatalf("case %d/%s phrase %q starts with function word", caseIndex, language, phrase)
			}
			if _, ok := profile.StopWords[last]; ok {
				t.Fatalf("case %d/%s phrase %q ends with stop word", caseIndex, language, phrase)
			}
			if _, ok := profile.FunctionWords[last]; ok {
				t.Fatalf("case %d/%s phrase %q ends with function word", caseIndex, language, phrase)
			}
		}
		phraseStart := strings.Index(text, phrase)
		phraseSpan := [2]int{utf8.RuneCountInString(text[:phraseStart]), utf8.RuneCountInString(text[:phraseStart+len(phrase)])}
		for _, entitySpan := range blocked {
			if phraseSpan[0] < entitySpan[1] && entitySpan[0] < phraseSpan[1] {
				t.Fatalf("case %d/%s phrase %q overlaps an entity span", caseIndex, language, phrase)
			}
		}
	}
}

type propertyNER struct{}

func (propertyNER) Extract(context.Context, string, int) ([]VisualEntity, error) { return nil, nil }

func propertyEntitiesForCase(index int, text string) []string {
	all := [][]string{{"Mike Tyson", "Brooklyn"}, {"NASA", "Cape Canaveral"}, {"Christopher Wray", "Washington"}}
	entities := all[index]
	out := make([]string, 0, len(entities))
	for _, entity := range entities {
		if strings.Contains(text, entity) {
			out = append(out, entity)
		}
	}
	return out
}

func propertyEntityType(entity string) string {
	if entity == "NASA" || entity == "Washington" || entity == "Brooklyn" || entity == "Cape Canaveral" {
		return "GPE"
	}
	return "PERSON"
}

func propertyEntitySpans(text string, entities []string) [][2]int {
	var spans [][2]int
	for _, entity := range entities {
		start := strings.Index(text, entity)
		if start < 0 {
			continue
		}
		spans = append(spans, [2]int{utf8.RuneCountInString(text[:start]), utf8.RuneCountInString(text[:start+len(entity)])})
	}
	return spans
}

func propertyLexicon(language Language) *linguistics.LexiconProfile {
	registry := linguistics.DefaultLexiconOrNil()
	if registry == nil {
		return nil
	}
	if registry.HasProfile(string(language)) {
		return registry.Resolve(string(language))
	}
	return registry.Resolve("fallback")
}

func propertyHash(phrases []string) string {
	payload, _ := json.Marshal(phrases)
	hash := sha256.Sum256(payload)
	return hex.EncodeToString(hash[:])
}

func mediadomainPropertyPlan() mediadomain.MediaPlanSpec {
	return mediadomain.MediaPlanSpec{Extraction: mediadomain.MediaExtractionPolicy{Include: []string{mediadomain.ExtractionIncludeEntities, mediadomain.ExtractionIncludeImportantPhrases}, MaxEntitiesPerSegment: 5, MaxImportantPhrasesPerSegment: 5}}
}
