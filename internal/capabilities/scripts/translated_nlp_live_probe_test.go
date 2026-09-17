package scriptgeneration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	ollamaplatform "github.com/Marcuss-ops/PipelineGen/internal/platform/ollama"
	ollamaclient "github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// mikeTysonProbeLiveEnv is the CANONICAL live gate — the same variable the
// tests/e2e certificates (youtube_multilingual_live_test.go,
// cliprender_multilingual_live_test.go, youtube_whisper_argos_live_test.go)
// already honour. This certificate deliberately grows no private opt-in flag:
// the repo has exactly one way to say "spend real Ollama/GPU time", and a
// second one would drift out of sync with the others.
//
// It is an OPT-IN live measurement, not a unit test: it translates the
// 500-word Mike Tyson source into the configured languages through a real
// Ollama server, runs the real release visualner binary and writes a report
// under tests/operational/results/multilingual-nlp/. Without the gate it
// skips, so `go test ./...` stays hermetic and never depends on a developer's
// local Ollama.
//
// Run it with:
//
//	VELOX_E2E_LIVE=1 go test ./internal/capabilities/scripts/ -run TestLiveMikeTyson500WordMultilingualNLPNoRendering -v
//
// VELOX_E2E_LANGUAGES narrows the staged rollout (five languages first, then
// the canonical ten) exactly as it does for the tests/e2e certificates, and
// VELOX_E2E_NLP_REUSE_TRANSLATIONS=1 reuses the translation cache so an
// NLP-only iteration does not pay for translation a second time.
// VELOX_E2E_NLP_REPORT_PATH selects the report path; the default includes a
// UTC timestamp so repeated runs never overwrite an earlier certificate.
const mikeTysonProbeLiveEnv = "VELOX_E2E_LIVE"

func mikeTysonProbeReportPath(repoRoot string, started time.Time, configured string) (string, error) {
	repoRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return "", fmt.Errorf("resolve report repository root: %w", err)
	}
	if configured != "" {
		if filepath.IsAbs(configured) {
			return filepath.Clean(configured), nil
		}
		return filepath.Join(repoRoot, configured), nil
	}
	name := fmt.Sprintf("mike-tyson-500w-%s.json", started.UTC().Format("2006-01-02T15-04-05.000000000Z"))
	return filepath.Join(repoRoot, "tests", "operational", "results", "multilingual-nlp", name), nil
}

func TestMikeTysonProbeReportPathDoesNotReuseFixedOutput(t *testing.T) {
	root := t.TempDir()
	started := time.Date(2026, 9, 17, 18, 30, 0, 123, time.UTC)
	first, err := mikeTysonProbeReportPath(root, started, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := mikeTysonProbeReportPath(root, started.Add(time.Second), "")
	if err != nil {
		t.Fatal(err)
	}
	if first == second || filepath.Base(first) == "mike-tyson-500w-2026-09-16.json" {
		t.Fatalf("default report paths can overwrite a fixed report: first=%q second=%q", first, second)
	}
	if !strings.HasPrefix(first, root+string(filepath.Separator)) {
		t.Fatalf("default report escaped repo root: %q", first)
	}

	custom, err := mikeTysonProbeReportPath(root, started, "tests/operational/results/custom.json")
	if err != nil {
		t.Fatal(err)
	}
	if custom != filepath.Join(root, "tests", "operational", "results", "custom.json") {
		t.Fatalf("relative report override = %q", custom)
	}
}

func mikeTysonProbeEnabled() bool {
	return strings.TrimSpace(os.Getenv(mikeTysonProbeLiveEnv)) != ""
}

// mikeTysonProbeLanguages resolves the staged translation set. It reads the
// SAME VELOX_E2E_LANGUAGES variable as every tests/e2e certificate, so the
// rollout is narrowed in one place and every certificate follows; the default
// is the canonical configured set (config.yaml media.multilingual.languages).
// The parser is deliberately a twin of tests/e2e.liveLanguagesFromEnv — the
// two live packages cannot import each other's unexported helpers, and a
// shared home for six lines would be a new package for no gain.
func mikeTysonProbeLanguages() []Language {
	const canonical = "it,en,pl,ru,de,es,pt-BR,fr,tr,id"
	raw := strings.TrimSpace(os.Getenv("VELOX_E2E_LANGUAGES"))
	if raw == "" {
		raw = canonical
	}
	out := make([]Language, 0, 10)
	seen := make(map[Language]struct{}, 10)
	for _, code := range strings.Split(raw, ",") {
		language := Language(strings.TrimSpace(code))
		if language == "" {
			continue
		}
		if _, dup := seen[language]; dup {
			continue
		}
		seen[language] = struct{}{}
		out = append(out, language)
	}
	if len(out) == 0 {
		for _, code := range strings.Split(canonical, ",") {
			out = append(out, Language(code))
		}
	}
	return out
}

type mikeTysonProbeNER struct {
	binary string
	// calls counts every invocation of the release visualner binary. Together
	// with the phrase extractor's counter it covers the ONLY two outbound
	// boundaries this certificate owns (the Rust binary and Ollama), which is
	// what turns "no rendering happened" from a comment into a measurement.
	calls *atomic.Int64
}

func (n mikeTysonProbeNER) Extract(ctx context.Context, text string, limit int) ([]VisualEntity, error) {
	if n.calls != nil {
		n.calls.Add(1)
	}
	request, err := json.Marshal(map[string]any{"source_text": text, "entity_count": limit})
	if err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, n.binary)
	command.Stdin = strings.NewReader(string(request) + "\n")
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("visualner process: %w", err)
	}
	var response struct {
		Entities []VisualEntity `json:"entities"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		return nil, err
	}
	return response.Entities, nil
}

type mikeTysonProbePhraseExtractor struct {
	client *ollamaclient.Client
	// calls counts every model-backed NLP extraction this certificate issues.
	// It is paired with mikeTysonProbeNER.calls so the two outbound
	// boundaries of the Goal-2 certificate are measured, not assumed.
	calls *atomic.Int64
}

func (p mikeTysonProbePhraseExtractor) ExtractImportantPhrases(ctx context.Context, text string, limit int, language, model string) ([]string, error) {
	result, err := p.ExtractSceneNLP(ctx, text, limit, language, model)
	return result.ImportantPhrases, err
}

func (p mikeTysonProbePhraseExtractor) ExtractSceneNLP(ctx context.Context, text string, limit int, language, model string) (SceneNLPExtraction, error) {
	if p.calls != nil {
		p.calls.Add(1)
	}
	result, err := p.client.ExtractEntitiesFromSegmentWithModel(ctx, detail.EntityExtractionRequest{SegmentText: text, EntityCount: limit, Language: language}, model)
	if err != nil || result == nil {
		return SceneNLPExtraction{}, err
	}
	return mikeTysonProbeExtraction(result), nil
}

func mikeTysonProbeExtraction(result *detail.EntityExtractionResult) SceneNLPExtraction {
	out := SceneNLPExtraction{ImportantPhrases: result.FrasiImportanti, ImportantWords: result.ParoleImportanti}
	for _, candidate := range result.NomiSpeciali {
		label, value, found := strings.Cut(candidate, ":")
		if !found {
			out.SpecialNames = append(out.SpecialNames, candidate)
			continue
		}
		value = strings.TrimSpace(value)
		var kind scriptpkg.EntityType
		switch strings.ToUpper(strings.TrimSpace(label)) {
		case "PERSON":
			kind = scriptpkg.EntityTypePerson
		case "PLACE", "LOCATION":
			kind = scriptpkg.EntityTypeLocation
		case "ORGANIZATION", "ORG":
			kind = scriptpkg.EntityTypeOrganization
		case "EVENT":
			kind = scriptpkg.EntityTypeEvent
		case "WORK":
			kind = scriptpkg.EntityTypeWork
		case "PRODUCT":
			kind = scriptpkg.EntityTypeProduct
		default:
			out.SpecialNames = append(out.SpecialNames, candidate)
			continue
		}
		out.SpecialNames = append(out.SpecialNames, value)
		out.Entities = append(out.Entities, VisualEntity{Text: value, Type: kind, Score: 0.95})
	}
	return out
}

func (p mikeTysonProbePhraseExtractor) ExtractSceneNLPBatch(ctx context.Context, texts []string, limit int, language, model string) ([]SceneNLPExtraction, error) {
	if p.calls != nil {
		p.calls.Add(1)
	}
	out := make([]SceneNLPExtraction, len(texts))
	for start := 0; start < len(texts); start += ollamaclient.EntityExtractionBatchLimit {
		end := min(start+ollamaclient.EntityExtractionBatchLimit, len(texts))
		batch, err := p.client.ExtractEntitiesFromBatchWithModel(ctx, texts[start:end], limit, model, language)
		if err != nil {
			return nil, err
		}
		if len(batch) != end-start {
			return nil, fmt.Errorf("batch returned %d results for %d scenes", len(batch), end-start)
		}
		for i, result := range batch {
			if result != nil {
				out[start+i] = mikeTysonProbeExtraction(result)
			}
		}
	}
	return out, nil
}

type mikeTysonProbeTranslation struct {
	Scene int      `json:"scene"`
	Lang  Language `json:"language"`
	Text  string   `json:"text"`
	Wall  int64    `json:"wall_ms"`
}

type mikeTysonProbeProvenance struct {
	Provider          string `json:"provider"`
	Model             string `json:"model,omitempty"`
	SourceLanguage    string `json:"source_language"`
	TargetLanguage    string `json:"target_language"`
	SourceTextSHA256  string `json:"source_text_sha256"`
	TranslatedSHA256  string `json:"translated_text_sha256"`
	TranslationWallMS int64  `json:"translation_wall_ms"`
}

type mikeTysonProbeScene struct {
	ID                    string                      `json:"id"`
	Index                 int                         `json:"index"`
	Text                  string                      `json:"text"`
	TranslationProvenance mikeTysonProbeProvenance    `json:"translation_provenance"`
	Entities              []scriptpkg.AnnotatedEntity `json:"entities"`
	ImportantPhrases      []scriptpkg.AnnotationSpan  `json:"important_phrases"`
	ImportantWords        []scriptpkg.AnnotationSpan  `json:"important_words"`
	SpecialNames          []scriptpkg.AnnotationSpan  `json:"special_names"`
	LocalizedAnnotations  *scriptpkg.SceneAnnotations `json:"localized_annotations,omitempty"`
}

type mikeTysonProbeDocument struct {
	Language string                `json:"language"`
	Scenes   []mikeTysonProbeScene `json:"scenes"`
}

type mikeTysonProbeLanguageTiming struct {
	TranslationCalls          int   `json:"translation_calls"`
	TranslationCacheEntries   int   `json:"translation_cache_entries"`
	TranslationSumSceneWallMS int64 `json:"translation_sum_scene_wall_ms"`
}

type mikeTysonProbeReport struct {
	StartedAtUTC                  string                                  `json:"started_at_utc"`
	CompletedAtUTC                string                                  `json:"completed_at_utc"`
	SourceLanguage                string                                  `json:"source_language"`
	SourceWordCount               int                                     `json:"source_word_count"`
	SourceModel                   string                                  `json:"source_model"`
	TranslationModel              string                                  `json:"translation_model"`
	NLPModel                      string                                  `json:"nlp_model"`
	SceneCount                    int                                     `json:"scene_count"`
	TranslationCalls              int                                     `json:"translation_calls"`
	TranslationCacheEntries       int                                     `json:"translation_cache_entries"`
	TranslationsReusedFromCache   bool                                    `json:"translations_reused_from_cache"`
	CachedCorpusTranslationWallMS int64                                   `json:"cached_corpus_translation_wall_ms"`
	TranslationWallMS             int64                                   `json:"translation_wall_ms"`
	SourceNLPWallMS               int64                                   `json:"source_nlp_wall_ms"`
	TranslatedNLPWallMS           int64                                   `json:"translated_nlp_wall_ms"`
	TotalStageWallMS              int64                                   `json:"total_stage_wall_ms"`
	TranslatedNERSceneCalls       int                                     `json:"translated_ner_scene_calls"`
	PhraseBatches                 int                                     `json:"phrase_batches"`
	Languages                     []string                                `json:"languages"`
	VisualNERInvocations          int                                     `json:"visual_ner_invocations"`
	ExpectedVisualNERInvocations  int                                     `json:"expected_visual_ner_invocations"`
	NLPModelInvocations           int                                     `json:"nlp_model_invocations"`
	OverlayRendering              bool                                    `json:"overlay_rendering"`
	ClipRendering                 bool                                    `json:"clip_rendering"`
	VideoRendering                bool                                    `json:"video_rendering"`
	PerLanguageTiming             map[string]mikeTysonProbeLanguageTiming `json:"per_language_timing"`
	SemanticChecks                map[string][]string                     `json:"semantic_checks"`
	AllImportantPhrasesGrounded   bool                                    `json:"all_important_phrases_grounded"`
	AllImportantWordsGrounded     bool                                    `json:"all_important_words_grounded"`
	AllSpecialNamesGrounded       bool                                    `json:"all_special_names_grounded"`
	Documents                     []mikeTysonProbeDocument                `json:"documents"`
}

type mikeTysonProbeTranslationCache struct {
	WallMS       int64                       `json:"wall_ms"`
	Translations []mikeTysonProbeTranslation `json:"translations"`
}

func TestLiveMikeTyson500WordMultilingualNLPNoRendering(t *testing.T) {
	if !mikeTysonProbeEnabled() {
		t.Skipf("%s not set; this live certificate needs a real Ollama at 127.0.0.1:11434 and the release visualner binary", mikeTysonProbeLiveEnv)
	}
	const model = "gemma4:e4b"
	sceneTexts := []string{
		"In Brooklyn, Mike Tyson’s story began far from championship lights. As a teenager, he found structure in boxing, where practice gave his energy direction. Trainer Cus D’Amato taught him to study distance, defend carefully, and attack with purpose. Tyson’s crouched stance made it easier to slip punches and explode forward with short hooks. The style looked simple from the seats, but it depended on balance, timing, and repetition. His amateur bouts brought attention, yet the lesson was discipline: confidence had to be earned. He learned how pressure could become a tool.",
		"Tyson turned professional in 1985 and built a reputation for speed and power. Many opponents were stopped early, so each victory added to the sense that an unstoppable force was approaching. On November 22, 1986, in Las Vegas, he faced Trevor Berbick for the WBC heavyweight title. Tyson won by second-round technical knockout and, at twenty years old, became the youngest heavyweight world champion. The record drew headlines, but the ring offered no guarantee that success would feel easy. A belt could prove achievement; it could not decide how a person handled fame, money, expectation, or the next difficult round.",
		"During the next years, Tyson united heavyweight titles and became one of boxing’s most recognizable figures. His fights combined forward movement, compact punches, and an ability to make opponents react before they could settle into a plan. In June 1988, he met Michael Spinks in Atlantic City. The bout ended in ninety-one seconds, a striking image of Tyson at his peak. Yet a highlight is only one frame of a career. Behind the quick finish were roadwork, sparring, preparation, and a demanding approach to competition. Inside the ropes, success still depended on choices made one exchange at a time.",
		"The defining reversal arrived in Tokyo on February 11, 1990. Tyson entered the contest against James “Buster” Douglas as the reigning, unbeaten champion, while Douglas was underestimated. The fight lasted longer than expected. Douglas used his reach, jab, and combinations to keep Tyson from controlling the distance. In the tenth round, he knocked Tyson down and won by knockout. The result became one of boxing’s best-known upsets, and it changed the story people told about invincibility. Tyson’s defeat showed that reputation could not block a punch. It also gave Douglas a place in the sport’s history, earned through resolve.",
		"Tyson’s later career included returns to the ring, championships, setbacks, and years spent rebuilding his life. Like Muhammad Ali, he became a figure whose fame reached beyond boxing, though their careers were distinct. His record cannot be reduced to one knockout or one defeat. It holds physical talent alongside public controversy and personal struggle, and those parts should be described without turning hardship into spectacle. The lesson is not that discipline makes anyone invulnerable. Skill grows through repeated work, while fame can magnify good decisions and mistakes. Tyson remains a heavyweight figure because his rise, fall, and return invite conversation about ambition, pressure, accountability, and what a second chapter can mean.",
	}
	wordCount := 0
	for _, text := range sceneTexts {
		wordCount += len(strings.Fields(text))
	}
	if wordCount != 500 {
		t.Fatalf("Mike Tyson source has %d words, want 500", wordCount)
	}

	started := time.Now().UTC()
	lexiconRoot, err := filepath.Abs("../../../config/lexicons")
	if err != nil {
		t.Fatal(err)
	}
	lexicon, err := linguistics.NewLexiconRegistry(lexiconRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := linguistics.SetDefaultLexicon(lexicon); err != nil {
		t.Fatal(err)
	}
	languages := mikeTysonProbeLanguages()
	if len(languages) == 0 {
		t.Fatal("no target languages resolved; VELOX_E2E_LANGUAGES is empty")
	}
	ollamaClient := ollamaclient.NewClient("http://127.0.0.1:11434", model, 600)
	generator := ollamaplatform.NewGenerator(ollamaClient)
	nerPath, err := filepath.Abs("../../../rust/target/release/visualner")
	if err != nil {
		t.Fatal(err)
	}
	var nerCalls, nlpCalls atomic.Int64
	ner := mikeTysonProbeNER{binary: nerPath, calls: &nerCalls}
	phrases := mikeTysonProbePhraseExtractor{client: ollamaClient, calls: &nlpCalls}
	detailed := BatchSceneNLPExtractor(phrases)

	result := &GenerateResult{Scenes: make([]Scene, len(sceneTexts))}
	for i, text := range sceneTexts {
		result.Scenes[i] = Scene{ID: fmt.Sprintf("scene-%02d", i+1), Index: i, Text: map[Language]string{"en": text}}
	}

	type translationTask struct {
		scene int
		lang  Language
	}
	tasks := make([]translationTask, 0, len(sceneTexts)*len(languages))
	for scene := range sceneTexts {
		for _, lang := range languages {
			tasks = append(tasks, translationTask{scene: scene, lang: lang})
		}
	}
	cachePath := filepath.Join(os.TempDir(), "mike-tyson-500w-translations.json")
	var translated []mikeTysonProbeTranslation
	var translationWall int64
	var cachedCorpusTranslationWall int64
	translationsReused := os.Getenv("VELOX_E2E_NLP_REUSE_TRANSLATIONS") == "1"
	if translationsReused {
		cacheBytes, readErr := os.ReadFile(cachePath)
		if readErr != nil {
			t.Fatalf("read cached translations: %v", readErr)
		}
		var cache mikeTysonProbeTranslationCache
		if err := json.Unmarshal(cacheBytes, &cache); err != nil {
			t.Fatalf("decode cached translations: %v", err)
		}
		cachedCorpusTranslationWall = cache.WallMS
		activeLanguages := make(map[Language]struct{}, len(languages))
		for _, lang := range languages {
			activeLanguages[lang] = struct{}{}
		}
		for _, translation := range cache.Translations {
			if _, active := activeLanguages[translation.Lang]; active {
				translated = append(translated, translation)
			}
		}
	} else {
		translationStart := time.Now()
		translated, err = concurrent.Map(context.Background(), tasks, 4, func(ctx context.Context, _ int, task translationTask) (mikeTysonProbeTranslation, error) {
			start := time.Now()
			text, translateErr := generator.TranslateTextWithModel(ctx, sceneTexts[task.scene], string(task.lang), model)
			if translateErr != nil {
				return mikeTysonProbeTranslation{}, fmt.Errorf("translate %s/%s: %w", result.Scenes[task.scene].ID, task.lang, translateErr)
			}
			if strings.TrimSpace(text) == "" {
				return mikeTysonProbeTranslation{}, fmt.Errorf("translate %s/%s returned empty text", result.Scenes[task.scene].ID, task.lang)
			}
			return mikeTysonProbeTranslation{Scene: task.scene, Lang: task.lang, Text: text, Wall: time.Since(start).Milliseconds()}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		translationWall = time.Since(translationStart).Milliseconds()
		cache, marshalErr := json.Marshal(mikeTysonProbeTranslationCache{WallMS: translationWall, Translations: translated})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if writeErr := os.WriteFile(cachePath, cache, 0o600); writeErr != nil {
			t.Fatalf("write translation cache: %v", writeErr)
		}
	}
	provenanceBySceneLanguage := make(map[string]mikeTysonProbeProvenance, len(translated)+len(sceneTexts))
	perLanguageTiming := make(map[string]mikeTysonProbeLanguageTiming, len(languages))
	for _, lang := range languages {
		calls := len(sceneTexts)
		if translationsReused {
			calls = 0
		}
		perLanguageTiming[string(lang)] = mikeTysonProbeLanguageTiming{TranslationCalls: calls}
	}
	for _, translation := range translated {
		text := strings.TrimSpace(translation.Text)
		result.Scenes[translation.Scene].Text[translation.Lang] = text
		key := fmt.Sprintf("%d/%s", translation.Scene, translation.Lang)
		provenanceBySceneLanguage[key] = mikeTysonProbeProvenance{
			Provider: "ollama", Model: model, SourceLanguage: "en", TargetLanguage: string(translation.Lang),
			SourceTextSHA256: digest.SHA256String(sceneTexts[translation.Scene]),
			TranslatedSHA256: digest.SHA256String(text), TranslationWallMS: translation.Wall,
		}
		timing := perLanguageTiming[string(translation.Lang)]
		timing.TranslationSumSceneWallMS += translation.Wall
		if translationsReused {
			timing.TranslationCacheEntries++
		}
		perLanguageTiming[string(translation.Lang)] = timing
	}

	// Source-language NLP is the existing source surface, using the same
	// production adapters and source-span projection as translated NLP.
	sourceNLPStart := time.Now()
	sourceEntities := make([][]VisualEntity, len(result.Scenes))
	sourcePhraseInputs := append([]string(nil), sceneTexts...)
	for i, text := range sceneTexts {
		sourceEntities[i], err = ner.Extract(context.Background(), text, 5)
		if err != nil {
			t.Fatalf("source VisualNER scene %d: %v", i, err)
		}
	}
	sourceExtractions, err := detailed.ExtractSceneNLPBatch(context.Background(), sourcePhraseInputs, 3, "en", model)
	if err != nil {
		t.Fatalf("source phrase batch: %v", err)
	}
	if len(sourceExtractions) != len(result.Scenes) {
		t.Fatalf("source NLP returned %d scene results, want %d", len(sourceExtractions), len(result.Scenes))
	}
	for i, scene := range result.Scenes {
		entities := mergeTranslatedNamedEntities(sourceEntities[i], groundNamedVisualEntities(scene.Text["en"], sourceExtractions[i].Entities))
		insights := scriptpkg.SegmentInsights{SegmentID: scene.ID, TextHash: SceneTextHash(scene.Text["en"]),
			ImportantPhrases: groundImportantPhrases(scene.Text["en"], entities, sourceExtractions[i].ImportantPhrases, 3),
			ImportantWords:   limitTranslatedNLPStrings(sourceExtractions[i].ImportantWords, 3),
			SpecialNames:     limitTranslatedNLPStrings(sourceExtractions[i].SpecialNames, 5)}
		for _, entity := range entities {
			insights.Entities = append(insights.Entities, scriptpkg.ExtractedEntity{Value: entity.Text, Type: string(entity.Type), Confidence: float64(entity.Score)})
		}
		scene.Annotations = projectEntityAnnotations(scene.Text["en"], "en", scriptpkg.VidRushSegmentResult{SegmentID: scene.ID, SceneID: scene.ID, Position: scene.Index, Text: scene.Text["en"], TextHash: SceneTextHash(scene.Text["en"]), Insights: insights})
		result.Scenes[i] = scene
	}
	sourceNLPWall := time.Since(sourceNLPStart).Milliseconds()

	translatedNLPStart := time.Now()
	req := GenerateRequest{SourceLanguage: "en", Languages: languages, Model: model,
		MediaPlan: mediadomain.MediaPlanSpec{Extraction: mediadomain.MediaExtractionPolicy{
			Include:               []string{mediadomain.ExtractionIncludeEntities, mediadomain.ExtractionIncludeSpecialNames, mediadomain.ExtractionIncludeImportantPhrases, mediadomain.ExtractionIncludeImportantWords},
			MaxEntitiesPerSegment: 5, MaxImportantPhrasesPerSegment: 3, MaxImportantWordsPerSegment: 3,
		}}}
	pipeline := &VidRushPipeline{NERPort: ner, PhraseExtractor: phrases}
	assertNoRenderOrAcquisitionWiring(t, pipeline)
	runner := &Runner{vidRushPipeline: pipeline}
	if err := runner.runTranslatedNLP(context.Background(), req, result); err != nil {
		t.Fatalf("runTranslatedNLP: %v", err)
	}
	translatedNLPWall := time.Since(translatedNLPStart).Milliseconds()

	// ── No-render / no-acquisition proof (Goal 2's actual definition) ──
	// The pipeline carries ONLY the NLP ports (asserted above), and the two
	// outbound boundaries this certificate owns are counted, so an accidental
	// acquisition or render surface would appear as an uncounted boundary or
	// a wiring failure instead of passing silently. The extraction contract is
	// one release-visualner invocation per (scene, language) plus one per
	// scene for the source surface.
	wantNERCalls := int64(len(result.Scenes) * (1 + len(languages)))
	if got := nerCalls.Load(); got != wantNERCalls {
		t.Errorf("release visualner invocations = %d, want %d (one per scene for the source surface plus one per scene per translated language): the certificate reached a surface other than translated NLP", got, wantNERCalls)
	}
	if got := nlpCalls.Load(); got < int64(1+len(languages)) {
		t.Errorf("model NLP invocations = %d, want at least %d (one source batch plus one per translated language): a language was not NLP-extracted", got, 1+len(languages))
	}
	languageCodes := make([]string, 0, len(languages))
	for _, lang := range languages {
		languageCodes = append(languageCodes, string(lang))
	}

	translationCalls := len(translated)
	translationCacheEntries := 0
	if translationsReused {
		translationCalls = 0
		translationCacheEntries = len(translated)
	}
	report := mikeTysonProbeReport{
		StartedAtUTC: started.Format(time.RFC3339), CompletedAtUTC: time.Now().UTC().Format(time.RFC3339),
		SourceLanguage: "en", SourceWordCount: wordCount, SourceModel: "caller-authored script",
		TranslationModel: model, NLPModel: model, SceneCount: len(result.Scenes), TranslationCalls: translationCalls, TranslationCacheEntries: translationCacheEntries, TranslationWallMS: translationWall,
		TranslationsReusedFromCache: translationsReused, CachedCorpusTranslationWallMS: cachedCorpusTranslationWall,
		SourceNLPWallMS: sourceNLPWall, TranslatedNLPWallMS: translatedNLPWall,
		TotalStageWallMS: translationWall + sourceNLPWall + translatedNLPWall, TranslatedNERSceneCalls: len(result.Scenes) * len(languages),
		PhraseBatches: len(languages) + 1, Languages: languageCodes,
		VisualNERInvocations: int(nerCalls.Load()), ExpectedVisualNERInvocations: int(wantNERCalls), NLPModelInvocations: int(nlpCalls.Load()),
		// These three stay false BY CONSTRUCTION, and
		// assertNoRenderOrAcquisitionWiring plus the call counters above are
		// what make the claim checkable: the Goal-2 certificate stops at
		// LocalizedAnnotations for every language and never builds an
		// overlay/clip/video render, a Chronon job, a Drive write or an image
		// acquisition.
		OverlayRendering: false, ClipRendering: false, VideoRendering: false,
		PerLanguageTiming: perLanguageTiming, SemanticChecks: make(map[string][]string),
		AllImportantPhrasesGrounded: true, AllImportantWordsGrounded: true, AllSpecialNamesGrounded: true,
	}
	outputLanguages := append([]Language{"en"}, languages...)
	for _, lang := range outputLanguages {
		document := mikeTysonProbeDocument{Language: string(lang), Scenes: make([]mikeTysonProbeScene, 0, len(result.Scenes))}
		for i, scene := range result.Scenes {
			var annotations *scriptpkg.SceneAnnotations
			var provenance mikeTysonProbeProvenance
			if lang == "en" {
				annotations = scene.Annotations
				provenance = mikeTysonProbeProvenance{Provider: "source_script", SourceLanguage: "en", TargetLanguage: "en", SourceTextSHA256: digest.SHA256String(scene.Text["en"]), TranslatedSHA256: digest.SHA256String(scene.Text["en"])}
			} else {
				annotations = scene.LocalizedAnnotations[lang]
				provenance = provenanceBySceneLanguage[fmt.Sprintf("%d/%s", i, lang)]
			}
			output := mikeTysonProbeScene{ID: scene.ID, Index: scene.Index, Text: scene.Text[lang], TranslationProvenance: provenance, LocalizedAnnotations: annotations}
			if annotations != nil {
				output.Entities = append(output.Entities, annotations.PrimaryEntities...)
				output.Entities = append(output.Entities, annotations.SecondaryEntities...)
				output.ImportantPhrases = append([]scriptpkg.AnnotationSpan(nil), annotations.ImportantPhrases...)
				output.ImportantWords = append([]scriptpkg.AnnotationSpan(nil), annotations.ImportantWords...)
				output.SpecialNames = append([]scriptpkg.AnnotationSpan(nil), annotations.SpecialNames...)
				for _, phrase := range output.ImportantPhrases {
					if !strings.Contains(strings.ToLower(output.Text), strings.ToLower(phrase.Text)) {
						report.AllImportantPhrasesGrounded = false
					}
				}
				for _, word := range output.ImportantWords {
					if !strings.Contains(strings.ToLower(output.Text), strings.ToLower(word.Text)) {
						report.AllImportantWordsGrounded = false
					}
				}
				for _, name := range output.SpecialNames {
					if !strings.Contains(strings.ToLower(output.Text), strings.ToLower(name.Text)) {
						report.AllSpecialNamesGrounded = false
					}
				}
			}
			document.Scenes = append(document.Scenes, output)
		}
		for _, expected := range expectedProbeEntities(lang) {
			if !documentHasExpectedEntity(document, expected) {
				message := fmt.Sprintf("missing %s entity in scene %d: %s", expected.Type, expected.Scene, expected.Name)
				report.SemanticChecks[string(lang)] = append(report.SemanticChecks[string(lang)], message)
				t.Errorf("localized %s: %s", lang, message)
			}
		}
		report.Documents = append(report.Documents, document)
	}

	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	repoRoot, err := filepath.Abs("../../../")
	if err != nil {
		t.Fatal(err)
	}
	outputPath, err := mikeTysonProbeReportPath(repoRoot, started, os.Getenv("VELOX_E2E_NLP_REPORT_PATH"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outputPath, append(payload, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("500-word multilingual NLP probe wrote %s; translation=%dms source NLP=%dms translated NLP=%dms", outputPath, translationWall, sourceNLPWall, translatedNLPWall)
	t.Logf("grounding phrases=%t words=%t names=%t semantic checks=%v", report.AllImportantPhrasesGrounded, report.AllImportantWordsGrounded, report.AllSpecialNamesGrounded, report.SemanticChecks)
}

// assertNoRenderOrAcquisitionWiring fails when the pipeline handed to
// runTranslatedNLP carries any port that could acquire, verify or finalize a
// media candidate. Those ports are the image-acquisition chain (local-stock
// resolver, MediaSampler, semantic provider resolver, materializer) and the
// legacy segment enricher; overlay/clip rendering and Drive delivery live in
// the app layer and are not even representable on this struct. A non-nil port
// here would mean the Goal-2 certificate could perform acquisition work while
// still claiming OverlayRendering=false, so this is asserted rather than
// documented.
func assertNoRenderOrAcquisitionWiring(t *testing.T, pipeline *VidRushPipeline) {
	t.Helper()
	for name, wired := range map[string]bool{
		"Enricher":          pipeline.Enricher != nil,
		"ProviderResolver":  pipeline.ProviderResolver != nil,
		"Materializer":      pipeline.Materializer != nil,
		"StockResolverPort": pipeline.StockResolverPort != nil,
		"SamplerPort":       pipeline.SamplerPort != nil,
	} {
		if wired {
			t.Errorf("Goal-2 certificate wired pipeline.%s; the no-rendering/no-acquisition certificate must carry NLP ports only", name)
		}
	}
}

type mikeTysonExpectedEntity struct {
	Scene   int
	Name    string
	Aliases []string
	Type    string
}

func expectedProbeEntities(language Language) []mikeTysonExpectedEntity {
	aliases := map[Language]map[string][]string{
		"en":    {"Mike Tyson": {"mike tyson"}, "Muhammad Ali": {"muhammad ali"}, "Las Vegas": {"las vegas"}},
		"it":    {"Mike Tyson": {"mike tyson"}, "Muhammad Ali": {"muhammad ali"}, "Las Vegas": {"las vegas"}},
		"de":    {"Mike Tyson": {"mike tyson"}, "Muhammad Ali": {"muhammad ali"}, "Las Vegas": {"las vegas"}},
		"es":    {"Mike Tyson": {"mike tyson"}, "Muhammad Ali": {"muhammad ali"}, "Las Vegas": {"las vegas"}},
		"fr":    {"Mike Tyson": {"mike tyson"}, "Muhammad Ali": {"muhammad ali"}, "Las Vegas": {"las vegas"}},
		"pt-BR": {"Mike Tyson": {"mike tyson"}, "Muhammad Ali": {"muhammad ali"}, "Las Vegas": {"las vegas"}},
		"pl":    {"Mike Tyson": {"mike tyson", "mike'a tysona", "mike’a tysona"}, "Muhammad Ali": {"muhammad ali", "muhammada aliego"}, "Las Vegas": {"las vegas"}},
		"ru":    {"Mike Tyson": {"mike tyson", "майк тайсон", "майка тайсона"}, "Muhammad Ali": {"muhammad ali", "мухаммед али", "мухаммеду али"}, "Las Vegas": {"las vegas", "лас-вегас", "лас вегас"}},
		"tr":    {"Mike Tyson": {"mike tyson"}, "Muhammad Ali": {"muhammad ali"}, "Las Vegas": {"las vegas"}},
		"id":    {"Mike Tyson": {"mike tyson"}, "Muhammad Ali": {"muhammad ali"}, "Las Vegas": {"las vegas"}},
	}
	byName := aliases[language]
	return []mikeTysonExpectedEntity{
		{Scene: 1, Name: "Mike Tyson", Aliases: byName["Mike Tyson"], Type: "PERSON"},
		{Scene: 2, Name: "Las Vegas", Aliases: byName["Las Vegas"], Type: "GPE"},
		{Scene: 5, Name: "Muhammad Ali", Aliases: byName["Muhammad Ali"], Type: "PERSON"},
	}
}

func documentHasExpectedEntity(document mikeTysonProbeDocument, expected mikeTysonExpectedEntity) bool {
	if expected.Scene < 1 || expected.Scene > len(document.Scenes) {
		return false
	}
	for _, entity := range document.Scenes[expected.Scene-1].Entities {
		if !probeEntityTypeMatches(strings.ToUpper(entity.Type), strings.ToUpper(expected.Type)) {
			continue
		}
		name := normalizeProbeEntityName(entity.CanonicalName)
		for _, alias := range expected.Aliases {
			if strings.Contains(name, normalizeProbeEntityName(alias)) {
				return true
			}
		}
	}
	return false
}

func probeEntityTypeMatches(actual, expected string) bool {
	if expected == "GPE" || expected == "LOCATION" || expected == "PLACE" {
		return actual == "GPE" || actual == "LOCATION" || actual == "PLACE"
	}
	return actual == expected
}

func TestDocumentHasExpectedEntityTreatsLocalizedPlaceTypesAsGPE(t *testing.T) {
	document := mikeTysonProbeDocument{Scenes: []mikeTysonProbeScene{{Entities: []scriptpkg.AnnotatedEntity{{
		CanonicalName: "Лас-Вегасе", Type: "LOCATION",
	}}}}}
	expected := mikeTysonExpectedEntity{Scene: 1, Name: "Las Vegas", Aliases: []string{"лас-вегасе"}, Type: "GPE"}
	if !documentHasExpectedEntity(document, expected) {
		t.Fatal("localized LOCATION entity should satisfy the expected GPE entity")
	}
}

func normalizeProbeEntityName(value string) string {
	value = strings.ToLower(strings.NewReplacer("’", "'", "–", "-", "—", "-").Replace(value))
	return strings.Join(strings.Fields(value), " ")
}
