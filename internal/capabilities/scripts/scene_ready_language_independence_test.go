// Package scriptgeneration — scene_ready_language_independence_test.go is the
// regression gate for the per-(scene, language) SceneTextReady pipeline: each
// language advances independently, so the SOURCE language never waits for its
// scene's target translations.
//
// Under the previous form ("join every translation of the scene, then fan out
// every TTS") the source language paid for the slowest target translation in
// its own scene, even though the source language has no translation dependency
// at all. The decisive assertion below blocks the target translation and
// requires the source language's voiceover to be synthesized anyway — it hangs
// (and therefore fails) on the joined form.
package scriptgeneration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// blockingTargetTranslator holds every translation open until the test closes
// release, so a test can keep a scene's target translation in flight while
// observing the source language's independent TTS.
type blockingTargetTranslator struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (t *blockingTargetTranslator) Translate(ctx context.Context, in TranslationInput) (string, error) {
	t.once.Do(func() { close(t.started) })
	select {
	case <-t.release:
		return "translated " + in.SourceText, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// languageRecordingVoiceover records every synthesized language in completion
// order and signals the first one, so a test can assert both which language
// started first and that no language was dropped.
type languageRecordingVoiceover struct {
	mu    sync.Mutex
	langs []Language
	first chan Language
	once  sync.Once
}

func (g *languageRecordingVoiceover) Generate(_ context.Context, in VoiceoverInput) (AudioReference, error) {
	g.mu.Lock()
	g.langs = append(g.langs, in.Language)
	g.mu.Unlock()
	g.once.Do(func() { g.first <- in.Language })
	return AudioReference{ID: "vo-" + in.SceneID + "-" + string(in.Language), FilePath: "/tmp/vo.mp3", Duration: 1}, nil
}

func (g *languageRecordingVoiceover) recorded() []Language {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]Language(nil), g.langs...)
}

// TestSceneReadyCoordinator_SourceTTSDoesNotWaitForTargetTranslation pins the
// independence contract: with the target translation blocked, the source
// language's voiceover must already be synthesized. A regression to the
// translation→TTS barrier makes this test time out, not merely reorder.
func TestSceneReadyCoordinator_SourceTTSDoesNotWaitForTargetTranslation(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	streamer := newGatedStreamingTextGenerator(defaultTestScenes())
	translator := &blockingTargetTranslator{started: make(chan struct{}), release: make(chan struct{})}
	voiceover := &languageRecordingVoiceover{first: make(chan Language, 1)}
	runner.textGen = streamer
	runner.translator = translator
	runner.voiceoverGen = voiceover

	req := defaultTestRequest() // source "en", requested {"en", "es"} → scene 0 translates only "es"
	runID := "run-language-independence-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	done := make(chan struct{})
	go func() { defer close(done); runner.Execute(context.Background(), runID, req) }()

	select {
	case <-streamer.emitted:
	case <-time.After(5 * time.Second):
		t.Fatal("scene 0 was not emitted")
	}
	select {
	case <-translator.started:
	case <-time.After(5 * time.Second):
		t.Fatal("scene 0 target translation did not start")
	}

	// The target translation is now blocked indefinitely. The source language
	// has no translation dependency, so its voiceover must already run.
	select {
	case lang := <-voiceover.first:
		require.Equal(t, Language("en"), lang,
			"the source language must be the one that starts without waiting for a translation")
	case <-time.After(2 * time.Second):
		t.Fatal("source-language TTS waited for the scene's target translation")
	}

	// Release the target translation: the target language must still be
	// synthesized — independence must not mean the target is dropped.
	close(translator.release)
	close(streamer.release)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not complete after releasing the target translation")
	}
	final := awaitCompletion(t, repo, runID, time.Second)
	require.Equal(t, RunStatusCompleted, final.Status)

	recorded := voiceover.recorded()
	require.Contains(t, recorded, Language("en"), "source language voiceover must be produced")
	require.Contains(t, recorded, Language("es"), "target language voiceover must still be produced")
}

// TestBuildSceneLanguageWork_SourceHasNoTranslationDependency pins the work
// projection that makes the source language independent: the source language is
// never a translation item, and a target is a translation item only while its
// text is still empty.
func TestBuildSceneLanguageWork_SourceHasNoTranslationDependency(t *testing.T) {
	text := map[Language]string{"en": "hello", "es": "", "fr": "deja traduit"}

	work := buildSceneLanguageWork([]Language{"en", "es", "fr"}, text, "en")
	require.Len(t, work, 3)
	require.Equal(t, Language("en"), work[0].lang)
	require.False(t, work[0].needsTranslation, "the source language must never carry a translation dependency")
	require.Equal(t, Language("es"), work[1].lang)
	require.True(t, work[1].needsTranslation, "an empty target text must be translated")
	require.Equal(t, Language("fr"), work[2].lang)
	require.False(t, work[2].needsTranslation, "an existing target text must not be re-translated")
}
