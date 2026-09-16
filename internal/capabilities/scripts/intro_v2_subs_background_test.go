// Package scriptgeneration — intro_v2_subs_background_test.go
//
// Intro V2 deliverable: the INITIAL clip (fixed media) burns translated
// subtitles in every render language, over the centralised editorial
// background selected once for the whole script.
//
// The pure caption/language helpers are already pinned in
// runner_phase_script_fixed_test.go; this suite pins the ORCHESTRATION that
// actually produces the per-language caption text (sceneReadyCoordinator
// .processFixedDisplayText) and the composition of the two: every language the
// fixed scene fans out to must resolve a caption, translated when available
// and the source caption otherwise — never the BODY narration.
package scriptgeneration

import (
	"context"
	"sync"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// recordingFixedTranslator records every (scene, target language, source text)
// translation and returns a deterministic pseudo-translation.
type recordingFixedTranslator struct {
	mu    sync.Mutex
	calls []TranslationInput
}

func (t *recordingFixedTranslator) Translate(_ context.Context, in TranslationInput) (string, error) {
	t.mu.Lock()
	t.calls = append(t.calls, in)
	t.mu.Unlock()
	return "[" + string(in.TargetLanguage) + "] " + in.SourceText, nil
}

func (t *recordingFixedTranslator) recorded() []TranslationInput {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]TranslationInput(nil), t.calls...)
}

// fixedDisplayTextCoordinator builds a coordinator whose only wired dependency
// this suite exercises is the translator.
func fixedDisplayTextCoordinator(t *testing.T, req GenerateRequest) (*sceneReadyCoordinator, *recordingFixedTranslator) {
	t.Helper()
	runner, _, _, _, _, _, _ := newTestRunner()
	translator := &recordingFixedTranslator{}
	runner.translator = translator
	return newSceneReadyCoordinator(
		context.Background(), runner, "run-intro-v2-001", req,
		scriptpkg.ArtifactRoutingContext{}, ExecutionContext{},
	), translator
}

func fixedIntroScene(displayText map[Language]string) Scene {
	return Scene{
		ID:            "scene-intro",
		Index:         0,
		DurationUS:    3_000_000,
		ExecutionMode: scriptpkg.SceneExecutionFixedMedia,
		Text:          displayText,
	}
}

// TestIntroV2_FixedMediaTranslatesDisplayTextForEveryRenderLanguage pins the
// subtitle surface: the fixed intro's display text is translated into every
// caller target language, the source language is never translated, and no
// voiceover is produced (fixed media carries its own authoritative audio).
func TestIntroV2_FixedMediaTranslatesDisplayTextForEveryRenderLanguage(t *testing.T) {
	req := GenerateRequest{SourceLanguage: "en", Languages: []Language{"it", "es", "en"}}
	coordinator, translator := fixedDisplayTextCoordinator(t, req)

	out, err := coordinator.processFixedDisplayText(fixedIntroScene(map[Language]string{"en": "Welcome"}))
	if err != nil {
		t.Fatalf("processFixedDisplayText: %v", err)
	}

	for _, lang := range []Language{"it", "es"} {
		if out.Text[lang] == "" {
			t.Fatalf("language %q has no caption text: %+v", lang, out.Text)
		}
	}
	if out.Text["en"] != "Welcome" {
		t.Fatalf("source caption = %q, want the untouched display text", out.Text["en"])
	}

	calls := translator.recorded()
	if len(calls) != 2 {
		t.Fatalf("translation calls = %d, want exactly the 2 target languages: %+v", len(calls), calls)
	}
	seen := map[Language]bool{}
	for _, call := range calls {
		seen[call.TargetLanguage] = true
		if call.SourceLanguage != "en" {
			t.Fatalf("translation source language = %q, want en", call.SourceLanguage)
		}
		if call.SourceText != "Welcome" {
			t.Fatalf("translation source text = %q, want the display text", call.SourceText)
		}
	}
	if seen["en"] {
		t.Fatal("the source language must never be translated")
	}
}

// TestIntroV2_FixedMediaWithoutDisplayTextDoesNothing pins the legitimate
// no-op: a fixed intro with no caption has nothing to translate, so no
// provider is called and no empty caption map is fabricated.
func TestIntroV2_FixedMediaWithoutDisplayTextDoesNothing(t *testing.T) {
	req := GenerateRequest{SourceLanguage: "en", Languages: []Language{"it", "es"}}
	coordinator, translator := fixedDisplayTextCoordinator(t, req)

	out, err := coordinator.processFixedDisplayText(fixedIntroScene(map[Language]string{"en": "   "}))
	if err != nil {
		t.Fatalf("processFixedDisplayText: %v", err)
	}
	if len(translator.recorded()) != 0 {
		t.Fatalf("blank display text must not trigger translation: %+v", translator.recorded())
	}
	if out.Text["it"] != "" || out.Text["es"] != "" {
		t.Fatalf("blank display text must not fabricate captions: %+v", out.Text)
	}
}

// TestIntroV2_FixedMediaDoesNotRetranslateExistingCaptions certifies
// idempotence: a language that already carries caption text is left alone.
func TestIntroV2_FixedMediaDoesNotRetranslateExistingCaptions(t *testing.T) {
	req := GenerateRequest{SourceLanguage: "en", Languages: []Language{"it", "es"}}
	coordinator, translator := fixedDisplayTextCoordinator(t, req)

	out, err := coordinator.processFixedDisplayText(fixedIntroScene(map[Language]string{
		"en": "Welcome",
		"it": "Benvenuti",
	}))
	if err != nil {
		t.Fatalf("processFixedDisplayText: %v", err)
	}
	if out.Text["it"] != "Benvenuti" {
		t.Fatalf("existing caption was overwritten: %+v", out.Text)
	}
	calls := translator.recorded()
	if len(calls) != 1 || calls[0].TargetLanguage != "es" {
		t.Fatalf("only the missing language may be translated; got %+v", calls)
	}
}

// TestIntroV2_EveryRenderLanguageResolvesATranslatedCaption is the composition
// pin between the fan-out language list and the caption surface: for every
// language the fixed intro renders, the caption is the translated one when it
// exists, and the source caption otherwise — BODY narration never leaks.
func TestIntroV2_EveryRenderLanguageResolvesATranslatedCaption(t *testing.T) {
	const bodyNarration = "BODY NARRATION MUST NEVER BE A CAPTION"
	req := GenerateRequest{SourceLanguage: "en", Languages: []Language{"it", "es"}}
	scene := fixedIntroScene(map[Language]string{"en": "Welcome to the show"})
	scene.Text["it"] = "[it] Welcome to the show"

	languages := fixedRenderLanguages(req, scene)
	if len(languages) != 3 {
		t.Fatalf("fixedRenderLanguages = %v, want source + 2 targets", languages)
	}
	for _, lang := range languages {
		got := fixedCaptionText(scene, req.SourceLanguage, lang)
		if got == "" {
			t.Fatalf("language %q resolved an empty caption", lang)
		}
		if got == bodyNarration {
			t.Fatalf("language %q leaked BODY narration into the caption", lang)
		}
		switch lang {
		case "it":
			if got != scene.Text["it"] {
				t.Fatalf("it caption = %q, want the translated caption", got)
			}
		default:
			if got != scene.Text["en"] {
				t.Fatalf("%s caption = %q, want the source caption fallback", lang, got)
			}
		}
	}
}
