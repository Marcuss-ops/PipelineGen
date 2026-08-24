// processor_translation_translateto_test.go — TDD regression guard
// for Bug 1: TranslationProcessor MUST use plan.TranslateTo as the
// primary target-language source, before plan.Languages[0] and
// plan.Language.
//
// PR-TRANSLATION-PIPELINE-2026-07-09.
package adapters

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// translatetoRecorder captures the language passed to Translate so
// the test can assert which target language the processor resolved.
// (renamed from recordingTranslator to avoid collision with the
// more comprehensive recordingTranslator in processor_translation_test.go).
type translatetoRecorder struct {
	lastLang string
}

func (r *translatetoRecorder) Translate(_ context.Context, text, lang string) (string, error) {
	r.lastLang = lang
	return "tradotto: " + text, nil
}

// stubTranslationUseCase implements ports.TranslationUseCase for
// this test file. It delegates to the embedded translator port so
// the translatetoRecorder can capture the target language. The
// implementation mimics the real TranslateScriptSpec: calls the
// translator for each text field and returns the translated envelope.
type stubTranslationUseCase struct {
	translator *translatetoRecorder
}

func (s *stubTranslationUseCase) TranslateScriptSpec(
	ctx context.Context,
	in *scriptpkg.ModelScriptOutputV1,
	evidence *scriptpkg.ClipEvidence,
	targetLang string,
	translator ports.ScriptTranslator,
) (*scriptpkg.ModelScriptOutputV1, []string, error) {
	if translator == nil {
		return nil, nil, scriptpkg.ErrPostprocessFailed
	}

	// Translate the main text
	translatedText, err := translator.Translate(ctx, in.Text, targetLang)
	if err != nil {
		return nil, nil, err
	}

	// Translate each scene's text
	out := &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: in.SchemaVersion,
		Text:          translatedText,
		SpecScene:     in.SpecScene,
	}
	for i, sc := range out.SpecScene.Scenes {
		t, err := translator.Translate(ctx, sc.Text, targetLang)
		if err != nil {
			return nil, nil, err
		}
		out.SpecScene.Scenes[i].Text = t
	}
	return out, nil, nil
}

// TestTranslationProcessor_UsesPlanTranslateTo is the canonical
// regression guard for Bug 1: when a request specifies
// {language:"en", translate_to:"it"} with no Languages array,
// the processor MUST translate to "it", NOT to "en".
func TestTranslationProcessor_UsesPlanTranslateTo_ViaUseCase(t *testing.T) {
	rec := &translatetoRecorder{}
	proc := NewTranslationProcessor(
		rec,
		nil, // metrics: nil → noop fallback
		&stubTranslationUseCase{translator: rec},
		nil, // classifier: nil → noop fallback
		zap.NewNop(),
	)

	plan := &scriptpkg.ResolvedGenerationPlan{
		Language:    "en",
		TranslateTo: "it",
	}

	input := ProcessInput{
		Text: "The boxer enters the ring.",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{
				{
					ID:    "scene-1",
					Index: 0,
					Kind:  scriptpkg.SceneNarration,
					Text:  "The boxer enters the ring.",
				},
			},
		},
	}

	_, err := proc.Process(context.Background(), plan, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if rec.lastLang != "it" {
		t.Errorf("expected target language 'it' from plan.TranslateTo, got %q", rec.lastLang)
	}
}

// TestTranslationProcessor_TranslateToTakesPrecedenceOverLanguages
// verifies that plan.TranslateTo takes priority over plan.Languages[0]
// when both are set.
func TestTranslationProcessor_TranslateToTakesPrecedenceOverLanguages_ViaUseCase(t *testing.T) {
	rec := &translatetoRecorder{}
	proc := NewTranslationProcessor(
		rec,
		nil,
		&stubTranslationUseCase{translator: rec},
		nil,
		zap.NewNop(),
	)

	plan := &scriptpkg.ResolvedGenerationPlan{
		Language:    "en",
		TranslateTo: "it",
		Languages:   []string{"es", "fr"}, // should be ignored
	}

	input := ProcessInput{
		Text: "Hello world.",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{
				{ID: "s1", Index: 0, Text: "Hello world."},
			},
		},
	}

	_, err := proc.Process(context.Background(), plan, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if rec.lastLang != "it" {
		t.Errorf("expected 'it' from plan.TranslateTo, got %q (plan.Languages[0]='es' was picked instead)", rec.lastLang)
	}
}

// TestTranslationProcessor_FallsBackToLanguagesWhenTranslateToEmpty
// verifies the fallback chain: TranslateTo="" → Languages[0] → Language.
func TestTranslationProcessor_FallsBackToLanguagesWhenTranslateToEmpty_ViaUseCase(t *testing.T) {
	rec := &translatetoRecorder{}
	proc := NewTranslationProcessor(
		rec,
		nil,
		&stubTranslationUseCase{translator: rec},
		nil,
		zap.NewNop(),
	)

	plan := &scriptpkg.ResolvedGenerationPlan{
		Language:  "en",
		Languages: []string{"fr"},
	}

	input := ProcessInput{
		Text: "Hello.",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{
				{ID: "s1", Index: 0, Text: "Hello."},
			},
		},
	}

	_, err := proc.Process(context.Background(), plan, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if rec.lastLang != "fr" {
		t.Errorf("expected 'fr' from plan.Languages[0] fallback, got %q", rec.lastLang)
	}
}

// TestTranslationProcessor_TranslatedTextReachesOutput verifies that
// when TranslationProcessor succeeds, the PostProcessResult contains
// the translated text AND TranslatedSpecScene. This is the surface
// that mergePostProcessResult reads to propagate translated content
// to downstream processors (voiceover, document, persistence).
//
// Note: Process() takes input by VALUE, so in-place mutations of
// input.Text / input.SpecScene inside the processor are NOT visible
// to the caller. The production Run() loop handles this via
// mergePostProcessResult which reads PostProcessResult.TranslatedText
// + TranslatedSpecScene + FinalSpecScene.
//
// This is Bug 2 from the user's analysis: the translation must feed
// into the voiceover pipeline.
func TestTranslationProcessor_TranslatedTextReachesOutput_ViaUseCase(t *testing.T) {
	rec := &translatetoRecorder{}
	proc := NewTranslationProcessor(
		rec,
		nil,
		&stubTranslationUseCase{translator: rec},
		nil,
		zap.NewNop(),
	)

	plan := &scriptpkg.ResolvedGenerationPlan{
		Language:    "en",
		TranslateTo: "it",
	}

	input := ProcessInput{
		Text: "The boxer enters the ring.",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{
				{
					ID:    "scene-1",
					Index: 0,
					Text:  "The boxer enters the ring.",
				},
			},
		},
	}

	result, err := proc.Process(context.Background(), plan, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 1. PostProcessResult must have TranslatedText
	if result.TranslatedText == "" {
		t.Error("expected non-empty TranslatedText in PostProcessResult")
	}

	// 2. TranslatedSpecScene must be populated (so mergePostProcessResult
	// can propagate it to FinalSpecScene → buildGenerationResult)
	if len(result.TranslatedSpecScene.Scenes) == 0 {
		t.Error("expected non-empty TranslatedSpecScene in PostProcessResult")
	}

	// 3. The translated scene text must differ from the original
	if len(result.TranslatedSpecScene.Scenes) > 0 {
		if result.TranslatedSpecScene.Scenes[0].Text == "The boxer enters the ring." {
			t.Error("expected TranslatedSpecScene scene text to be translated; still original")
		}
	}

	// 4. Changed must be true
	if !result.Changed {
		t.Error("expected Changed=true when translation succeeds")
	}
}
