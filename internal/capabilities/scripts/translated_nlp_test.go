package scriptgeneration

import (
	"context"
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

type translatedNLPNameNER struct {
	always []VisualEntity
}

func (n translatedNLPNameNER) Extract(_ context.Context, text string, _ int) ([]VisualEntity, error) {
	entities := append([]VisualEntity(nil), n.always...)
	for _, name := range []string{"Mike Tyson", "Muhammad Ali"} {
		if strings.Contains(text, name) {
			entities = append(entities, VisualEntity{Text: name, Type: scriptpkg.EntityTypePerson, Score: 0.99})
		}
	}
	if strings.Contains(text, "Las Vegas") {
		entities = append(entities, VisualEntity{Text: "Las Vegas", Type: scriptpkg.EntityTypeLocation, Score: 0.98})
	}
	return entities, nil
}

// translatedNLPLocalizedSurfaceNER reports the entity surface exactly as it
// appears in the TRANSLATED text — the shape a language-aware NER produces in
// production, where the model never sees the English canonical name.
type translatedNLPLocalizedSurfaceNER struct{ surfaces []VisualEntity }

func (n translatedNLPLocalizedSurfaceNER) Extract(_ context.Context, text string, _ int) ([]VisualEntity, error) {
	out := make([]VisualEntity, 0, len(n.surfaces))
	for _, entity := range n.surfaces {
		if strings.Contains(text, entity.Text) {
			out = append(out, entity)
		}
	}
	return out, nil
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

func assertGroundedScenePhrases(t *testing.T, result *GenerateResult, sceneIndex int, lang Language) {
	t.Helper()
	annotations := result.Scenes[sceneIndex].LocalizedAnnotations[lang]
	if annotations == nil || len(annotations.ImportantPhrases) == 0 {
		t.Fatalf("scene %d/%s has no deterministic phrases: %+v", sceneIndex, lang, annotations)
	}
	text := strings.ToLower(result.Scenes[sceneIndex].Text[lang])
	for _, phrase := range annotations.ImportantPhrases {
		if !strings.Contains(text, strings.ToLower(phrase.Text)) {
			t.Errorf("scene %d/%s phrase %q is not grounded in its translated text", sceneIndex, lang, phrase.Text)
		}
		if len(strings.Fields(phrase.Text)) > 4 {
			t.Errorf("scene %d/%s phrase %q exceeds the short-overlay word bound", sceneIndex, lang, phrase.Text)
		}
	}
}

// TestRunTranslatedNLPUsesDeterministicPhrasesAndKeepsNER pins the cutover:
// phrase selection is deterministic over the translated text (there is no
// model-owned phrase surface left to call), while VisualNER still extracts
// translated names for every scene.
func TestRunTranslatedNLPUsesDeterministicPhrasesAndKeepsNER(t *testing.T) {
	runner := &Runner{vidRushPipeline: &VidRushPipeline{NERPort: translatedNLPTestNER{}}}
	req := GenerateRequest{SourceLanguage: "en", Languages: []Language{"it", "de"}, Model: "test-model", MediaPlan: translatedNLPMediaPlan()}
	result := translatedNLPMultiSceneResult([]Language{"it", "de"}, 6)

	if err := runner.runTranslatedNLP(context.Background(), req, result); err != nil {
		t.Fatal(err)
	}

	for i := range result.Scenes {
		assertGroundedScenePhrases(t, result, i, "it")
		assertGroundedScenePhrases(t, result, i, "de")
		if len(result.Scenes[i].LocalizedAnnotations["it"].PrimaryEntities) == 0 {
			t.Fatalf("scene %d/it lost its per-scene entities", i)
		}
	}
}

func TestRunTranslatedNLPProjectsGroundedWordsAndSpecialNamesPerLanguage(t *testing.T) {
	runner := &Runner{vidRushPipeline: &VidRushPipeline{NERPort: translatedNLPNameNER{}}}
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
	for _, lang := range []Language{"it", "de"} {
		annotations := result.Scenes[0].LocalizedAnnotations[lang]
		if annotations == nil {
			t.Fatalf("missing %s localized annotations", lang)
		}
		if len(annotations.ImportantPhrases) == 0 {
			t.Errorf("%s deterministic important phrases are empty", lang)
		}
		text := strings.ToLower(result.Scenes[0].Text[lang])
		for _, phrase := range annotations.ImportantPhrases {
			if !strings.Contains(text, strings.ToLower(phrase.Text)) {
				t.Errorf("%s phrase %q is not verbatim in localized text", lang, phrase.Text)
			}
		}
		if len(annotations.ImportantWords) == 0 {
			t.Errorf("%s deterministic important words are empty", lang)
		}
		if len(annotations.SpecialNames) != 3 {
			t.Errorf("%s special names = %+v, want grounded Tyson, Las Vegas and Ali", lang, annotations.SpecialNames)
		} else {
			names := map[string]bool{}
			for _, name := range annotations.SpecialNames {
				names[name.Text] = true
			}
			if !names["Mike Tyson"] || !names["Las Vegas"] || !names["Muhammad Ali"] {
				t.Errorf("%s special names = %+v, missing a complete grounded entity name", lang, annotations.SpecialNames)
			}
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

func TestMergeTranslatedNamedEntitiesKeepsDeterministicLocations(t *testing.T) {
	visual := []VisualEntity{
		{Text: "Las Vegas", Type: scriptpkg.EntityTypeLocation, Score: 0.95},
		{Text: "Many Opponents", Type: scriptpkg.EntityTypePerson, Score: 0.72},
	}
	named := []VisualEntity{{Text: "Trevor Berbick", Type: scriptpkg.EntityTypePerson, Score: 0.98}}

	got := mergeTranslatedNamedEntities(visual, named)
	if len(got) != 2 {
		t.Fatalf("merged entities = %+v, want the model person and VisualNER location", got)
	}
	if got[0].Text != "Las Vegas" || got[0].Type != scriptpkg.EntityTypeLocation {
		t.Fatalf("first merged entity = %+v, want grounded Las Vegas location", got[0])
	}
	if got[1].Text != "Trevor Berbick" || got[1].Type != scriptpkg.EntityTypePerson {
		t.Fatalf("second merged entity = %+v, want typed Trevor Berbick person", got[1])
	}
}

func TestRunTranslatedNLPGroundsSourceNamesInTheTranslatedSurface(t *testing.T) {
	ner := translatedNLPNameNER{}
	ner.always = []VisualEntity{
		{Text: "Viele Gegner", Type: scriptpkg.EntityTypePerson, Score: 0.99},
		{Text: "Las Vegas", Type: scriptpkg.EntityTypeLocation, Score: 0.95},
	}
	runner := &Runner{vidRushPipeline: &VidRushPipeline{NERPort: ner}}
	req := GenerateRequest{
		SourceLanguage: "en",
		Languages:      []Language{"de"},
		MediaPlan: mediadomain.MediaPlanSpec{Extraction: mediadomain.MediaExtractionPolicy{
			Include:               []string{mediadomain.ExtractionIncludeEntities, mediadomain.ExtractionIncludeImportantPhrases},
			MaxEntitiesPerSegment: 5, MaxImportantPhrasesPerSegment: 3,
		}},
	}
	result := &GenerateResult{Scenes: []Scene{{
		ID: "scene-1", Index: 0,
		Text: map[Language]string{"en": "Mike Tyson’s story continued in Las Vegas.", "de": "Mike Tysons Geschichte führte ihn nach Las Vegas. Viele Gegner sahen zu."},
		Annotations: &scriptpkg.SceneAnnotations{Language: "en", PrimaryEntities: []scriptpkg.AnnotatedEntity{
			{Text: "Mike Tyson", CanonicalName: "Mike Tyson’s", Type: "PERSON", Confidence: 0.98},
			{Text: "Las Vegas", CanonicalName: "Las Vegas", Type: "GPE", Confidence: 0.95},
		}},
	}}}

	if err := runner.runTranslatedNLP(context.Background(), req, result); err != nil {
		t.Fatal(err)
	}
	annotations := result.Scenes[0].LocalizedAnnotations["de"]
	if annotations == nil {
		t.Fatal("missing German annotations")
	}
	hasTyson, hasLasVegas, hasFalsePerson := false, false, false
	for _, entity := range annotations.PrimaryEntities {
		hasTyson = hasTyson || entity.CanonicalName == "Mike Tysons" && entity.Type == "PERSON"
		hasLasVegas = hasLasVegas || entity.CanonicalName == "Las Vegas" && entity.Type == "GPE"
		hasFalsePerson = hasFalsePerson || entity.CanonicalName == "Viele Gegner"
	}
	if !hasTyson || !hasLasVegas || hasFalsePerson {
		t.Fatalf("localized primary entities = %+v, want grounded Tyson and Las Vegas with no heuristic German noun as a person", annotations.PrimaryEntities)
	}
	names := map[string]bool{}
	for _, name := range annotations.SpecialNames {
		names[name.Text] = true
	}
	if len(names) != 2 || !names["Mike Tysons"] || !names["Las Vegas"] {
		t.Fatalf("localized special names = %+v, want complete spoken names only", annotations.SpecialNames)
	}
}

func TestGroundLocalizedSourceEntitiesProjectsPolishInflection(t *testing.T) {
	text := "W Brooklynie historia Mike’a Tysona zmieniła boks."
	source := &scriptpkg.SceneAnnotations{Language: "en", PrimaryEntities: []scriptpkg.AnnotatedEntity{{
		Text: "Mike Tyson", CanonicalName: "Mike Tyson", Type: "PERSON", Confidence: 0.98,
	}}}

	got := matchLocalizedSourceEntities(text, "pl", source)
	if len(got) != 1 || got[0].Kind != scriptpkg.EntityTypePerson || got[0].Surface != "Mike’a Tysona" {
		t.Fatalf("localized source matches = %+v, want the grounded Polish surface Mike’a Tysona", got)
	}
	span, ok := findEntitySpan(text, got[0].Surface)
	if !ok || string([]rune(text)[span.StartRune:span.EndRune]) != got[0].Surface {
		t.Fatalf("projected entity %q does not retain a grounded translated span", got[0].Surface)
	}
	if got[0].Span.StartRune != span.StartRune || got[0].Span.EndRune != span.EndRune {
		t.Fatalf("match span = [%d,%d), want the grounded span [%d,%d)", got[0].Span.StartRune, got[0].Span.EndRune, span.StartRune, span.EndRune)
	}

	// A prefix resemblance alone is not enough to project a source identity.
	if falsePositive := matchLocalizedSourceEntities("Mikea Tysonic opowieść.", "pl", source); len(falsePositive) != 0 {
		t.Fatalf("unrelated Polish tokens were projected as Mike Tyson: %+v", falsePositive)
	}
}

func TestRunTranslatedNLPKeepsGermanMentionSurfaceForTiming(t *testing.T) {
	runner := &Runner{vidRushPipeline: &VidRushPipeline{}}
	req := GenerateRequest{
		SourceLanguage: "en", Languages: []Language{"de"}, Model: "test-model",
		MediaPlan: translatedNLPMediaPlan(),
	}
	result := &GenerateResult{Scenes: []Scene{{
		ID: "scene-1", Index: 0,
		Text: map[Language]string{
			"en": "Mike Tyson changed boxing forever.",
			"de": "Mike Tysons Karriere veränderte den Boxsport.",
		},
		Annotations: &scriptpkg.SceneAnnotations{Language: "en", PrimaryEntities: []scriptpkg.AnnotatedEntity{{
			Text: "Mike Tyson", CanonicalName: "Mike Tyson", Type: "PERSON",
			CanonicalEntityID: "person:mike-tyson",
		}}},
	}}}

	if err := runner.runTranslatedNLP(context.Background(), req, result); err != nil {
		t.Fatal(err)
	}
	annotations := result.Scenes[0].LocalizedAnnotations["de"]
	if annotations == nil || len(annotations.PrimaryEntities) != 1 {
		t.Fatalf("German annotations = %+v, want one grounded entity", annotations)
	}
	entity := annotations.PrimaryEntities[0]
	if entity.CanonicalEntityID != "person:mike-tyson" {
		t.Fatalf("canonical identity = %q, want source identity person:mike-tyson", entity.CanonicalEntityID)
	}
	if entity.CanonicalName != "Mike Tysons" {
		t.Fatalf("localized canonical name = %q, want spoken surface Mike Tysons", entity.CanonicalName)
	}
	if len(entity.Mentions) == 0 || entity.Mentions[0].Text != "Mike Tysons" {
		t.Fatalf("localized mentions = %+v, want the exact German span Mike Tysons first", entity.Mentions)
	}

	sources := entitySourcesFromAnnotations(annotations, result.Scenes[0].Text["de"])
	if len(sources) != 1 || sources[0].SpokenName != "Mike Tysons" {
		t.Fatalf("entity timing source = %+v, want spoken surface Mike Tysons", sources)
	}
}

// TestRunTranslatedNLPLocalizedAnnotationsInheritSourceIdentity pins the
// identity contract of the translated surface: a localized annotation inherits
// the SOURCE entity's canonical_entity_id and its identity-scoped image
// binding. Before this contract, a Polish document re-minted an identity from
// the inflected surface ("person:Mike'a-Tysona") and had to re-resolve the
// person's image downstream — the reconstruction the canonical entity identity
// exists to remove.
func TestRunTranslatedNLPLocalizedAnnotationsInheritSourceIdentity(t *testing.T) {
	runner := &Runner{vidRushPipeline: &VidRushPipeline{NERPort: translatedNLPLocalizedSurfaceNER{surfaces: []VisualEntity{
		{Text: "Mike’a Tysona", Type: scriptpkg.EntityTypePerson, Score: 0.98},
	}}}}
	req := GenerateRequest{SourceLanguage: "en", Languages: []Language{"pl"}, Model: "test-model", MediaPlan: translatedNLPMediaPlan()}
	image := &scriptpkg.EntityImageBinding{
		Status: "resolved", AssetID: "asset-mike-tyson", SHA256: strings.Repeat("a", 64),
		PreviewURL: "https://drive.google.com/uc?export=download&id=entity-image-person-mike-tyson",
		DriveLink:  "https://drive.google.com/file/d/entity-image-person-mike-tyson/view",
	}
	result := &GenerateResult{Scenes: []Scene{{
		ID: "scene-1", Index: 0,
		Text: map[Language]string{"en": "Mike Tyson changed boxing forever.", "pl": "W Brooklynie historia Mike’a Tysona zmieniła boks."},
		Annotations: &scriptpkg.SceneAnnotations{Language: "en", PrimaryEntities: []scriptpkg.AnnotatedEntity{{
			Text: "Mike Tyson", CanonicalName: "Mike Tyson", Type: "PERSON", Confidence: 0.98,
			CanonicalEntityID: "person:mike-tyson", Image: image,
		}}},
	}}}

	if err := runner.runTranslatedNLP(context.Background(), req, result); err != nil {
		t.Fatal(err)
	}
	annotations := result.Scenes[0].LocalizedAnnotations["pl"]
	if annotations == nil {
		t.Fatal("missing Polish annotations")
	}
	found := false
	for _, entity := range annotations.PrimaryEntities {
		if !strings.EqualFold(entity.CanonicalName, "Mike’a Tysona") {
			continue
		}
		found = true
		if entity.CanonicalEntityID != "person:mike-tyson" {
			t.Errorf("localized canonical_entity_id = %q, want the source identity person:mike-tyson", entity.CanonicalEntityID)
		}
		if entity.Image == nil {
			t.Fatal("the localized entity lost the identity-scoped image binding")
		}
		if entity.Image.AssetID != image.AssetID || entity.Image.SHA256 != image.SHA256 {
			t.Errorf("localized image binding = %+v, want the source identity-scoped image", entity.Image)
		}
	}
	if !found {
		t.Fatalf("localized annotations = %+v, want the grounded Polish surface Mike’a Tysona", annotations.PrimaryEntities)
	}
}

// TestRunTranslatedNLPSelectsPhrasesWithoutAnyModelSurface keeps the phrase
// contract honest after the demolition: with NO phrase port wired at all, every
// scene still gets grounded, short, verbatim phrases per language.
func TestRunTranslatedNLPSelectsPhrasesWithoutAnyModelSurface(t *testing.T) {
	runner := &Runner{vidRushPipeline: &VidRushPipeline{NERPort: translatedNLPTestNER{}}}
	req := GenerateRequest{SourceLanguage: "en", Languages: []Language{"it", "de"}, Model: "test-model", MediaPlan: translatedNLPMediaPlan()}
	result := translatedNLPMultiSceneResult([]Language{"it", "de"}, 3)

	if err := runner.runTranslatedNLP(context.Background(), req, result); err != nil {
		t.Fatalf("deterministic phrase selection must not fail the phase: %v", err)
	}
	for i := range result.Scenes {
		assertGroundedScenePhrases(t, result, i, "it")
		assertGroundedScenePhrases(t, result, i, "de")
	}
}

func TestRunTranslatedNLPStoresGroundedPerLanguageAnnotations(t *testing.T) {
	runner := &Runner{vidRushPipeline: &VidRushPipeline{
		NERPort: translatedNLPTestNER{},
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

// ── Redundant translated-NER gate (audit P5/C3) ──────────────────────
//
// The gate exists to stop calling VisualNER on a translation whose source
// annotations already ground every entity slot. These tests pin BOTH halves:
// the skip when the precondition holds, and the refusal to skip when it does
// not (so the optimisation can never silently change the emitted entities).

// countingTranslatedNER counts Extract calls and returns a model entity that
// would only ever occupy a slot if the source matches did NOT fill the window.
type countingTranslatedNER struct {
	mu    sync.Mutex
	calls int
}

func (n *countingTranslatedNER) Extract(_ context.Context, _ string, _ int) ([]VisualEntity, error) {
	n.mu.Lock()
	n.calls++
	n.mu.Unlock()
	return []VisualEntity{{Text: "Model Only Name", Type: scriptpkg.EntityTypePerson, Score: 0.9}}, nil
}

func (n *countingTranslatedNER) callCount() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.calls
}

const translatedNERCoverageITText = "Mike Tyson, Muhammad Ali e Joe Frazier hanno definito la boxe."

func translatedNERCoverageResult(source []scriptpkg.AnnotatedEntity) *GenerateResult {
	return &GenerateResult{Scenes: []Scene{{
		ID: "scene-0", Index: 0,
		Text: map[Language]string{
			"en": "Mike Tyson, Muhammad Ali and Joe Frazier defined boxing.",
			"it": translatedNERCoverageITText,
		},
		Annotations: &scriptpkg.SceneAnnotations{Language: "en", PrimaryEntities: source},
	}}}
}

func translatedNERCoverageRequest() GenerateRequest {
	return GenerateRequest{
		SourceLanguage: "en", Languages: []Language{"it"}, Model: "test-model",
		MediaPlan: mediadomain.MediaPlanSpec{Extraction: mediadomain.MediaExtractionPolicy{
			Include:               []string{mediadomain.ExtractionIncludeEntities},
			MaxEntitiesPerSegment: 3,
		}},
	}
}

func personEntity(text string) scriptpkg.AnnotatedEntity {
	return scriptpkg.AnnotatedEntity{Text: text, CanonicalName: text, Type: "PERSON", Confidence: 0.98}
}

// TestTranslatedNLPSkipsRedundantNERWhenSourceCoversTheLimit pins the win: with
// three source-grounded PERSONs already present verbatim in the translation, a
// translated NER call can only produce candidates the entity window discards, so
// it must not be made at all — while the localized annotation still carries the
// three grounded identities.
func TestTranslatedNLPSkipsRedundantNERWhenSourceCoversTheLimit(t *testing.T) {
	ner := &countingTranslatedNER{}
	runner := &Runner{vidRushPipeline: &VidRushPipeline{NERPort: ner}}
	result := translatedNERCoverageResult([]scriptpkg.AnnotatedEntity{
		personEntity("Mike Tyson"), personEntity("Muhammad Ali"), personEntity("Joe Frazier"),
	})

	if err := runner.runTranslatedNLP(context.Background(), translatedNERCoverageRequest(), result); err != nil {
		t.Fatal(err)
	}
	if got := ner.callCount(); got != 0 {
		t.Fatalf("translated NER calls = %d, want 0: the source annotations already ground every entity slot", got)
	}
	annotations := result.Scenes[0].LocalizedAnnotations["it"]
	if annotations == nil {
		t.Fatal("missing Italian annotations")
	}
	if len(annotations.PrimaryEntities) != 3 {
		t.Fatalf("localized primary entities = %+v, want the 3 source-grounded persons", annotations.PrimaryEntities)
	}
}

// TestTranslatedNLPCallsNERWhenSourceDoesNotCoverTheLimit is the negative
// control: two grounded persons leave a slot the model may legitimately fill.
func TestTranslatedNLPCallsNERWhenSourceDoesNotCoverTheLimit(t *testing.T) {
	ner := &countingTranslatedNER{}
	runner := &Runner{vidRushPipeline: &VidRushPipeline{NERPort: ner}}
	result := translatedNERCoverageResult([]scriptpkg.AnnotatedEntity{
		personEntity("Mike Tyson"), personEntity("Muhammad Ali"),
	})

	if err := runner.runTranslatedNLP(context.Background(), translatedNERCoverageRequest(), result); err != nil {
		t.Fatal(err)
	}
	if got := ner.callCount(); got != 1 {
		t.Fatalf("translated NER calls = %d, want 1: the source matches leave an entity slot open", got)
	}
}

// TestTranslatedNLPCallsNERWhenAMatchIsNotAPerson pins the narrowness of the
// precondition. A non-PERSON match (here a location) can still be displaced by a
// model entity of the same priority class, so the gate must refuse to skip even
// when the match count already equals the limit.
func TestTranslatedNLPCallsNERWhenAMatchIsNotAPerson(t *testing.T) {
	ner := &countingTranslatedNER{}
	runner := &Runner{vidRushPipeline: &VidRushPipeline{NERPort: ner}}
	result := &GenerateResult{Scenes: []Scene{{
		ID: "scene-0", Index: 0,
		Text: map[Language]string{
			"en": "Mike Tyson trained in Las Vegas with Joe Frazier.",
			"it": "Mike Tyson si allenava a Las Vegas con Joe Frazier.",
		},
		Annotations: &scriptpkg.SceneAnnotations{Language: "en", PrimaryEntities: []scriptpkg.AnnotatedEntity{
			personEntity("Mike Tyson"),
			{Text: "Las Vegas", CanonicalName: "Las Vegas", Type: "GPE", Confidence: 0.95},
			personEntity("Joe Frazier"),
		}},
	}}}

	if err := runner.runTranslatedNLP(context.Background(), translatedNERCoverageRequest(), result); err != nil {
		t.Fatal(err)
	}
	if got := ner.callCount(); got != 1 {
		t.Fatalf("translated NER calls = %d, want 1: a non-PERSON match can still be displaced inside the limit window", got)
	}
}
