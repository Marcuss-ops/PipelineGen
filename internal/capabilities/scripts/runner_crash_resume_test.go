// Package scriptgeneration — runner_crash_resume_test.go certifies the
// resume-reale contract: a crash (kill -9) mid-phase must NOT restart the
// whole pipeline. On restart the completed scenes are REUSEd (provider never
// re-invoked), the in-flight scene is RETRIEd, and the remaining scenes
// CONTINUE — verified by per-unit durable checkpoints written DURING the
// concurrent fan-out, not after the whole batch.
package scriptgeneration

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// callsFor returns the number of Translate calls recorded for a scene.
func (t *stubTranslator) callsFor(sceneID string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.callCounts[sceneID]
}

// crashTTSGenerator records per-(scene, language) synthesis calls and can fail
// the first call to a specific scene, simulating a provider crash on that
// scene while earlier scenes have already completed.
type crashTTSGenerator struct {
	mu      sync.Mutex
	failKey string // sceneID + "/" + language that fails on its first call
	calls   map[string]int
}

func newCrashTTSGenerator(failScene, failLang string) *crashTTSGenerator {
	return &crashTTSGenerator{failKey: failScene + "/" + failLang, calls: map[string]int{}}
}

func (g *crashTTSGenerator) Generate(_ context.Context, in VoiceoverInput) (AudioReference, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	key := in.SceneID + "/" + string(in.Language)
	g.calls[key]++
	if key == g.failKey && g.calls[key] == 1 {
		return AudioReference{}, fmt.Errorf("tts provider unavailable (simulated crash)")
	}
	return AudioReference{
		ID:       "vo-" + in.SceneID + "-" + string(in.Language),
		FilePath: "/tmp/voiceover-" + in.SceneID + "-" + string(in.Language) + ".mp3",
		Duration: 1.0,
	}, nil
}

func (g *crashTTSGenerator) callsFor(sceneID, lang string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls[sceneID+"/"+lang]
}

func fourSceneText() []Scene {
	return []Scene{
		{ID: "scene-0", Index: 0, Text: map[Language]string{"en": "First scene text"}},
		{ID: "scene-1", Index: 1, Text: map[Language]string{"en": "Second scene text"}},
		{ID: "scene-2", Index: 2, Text: map[Language]string{"en": "Third scene text"}},
		{ID: "scene-3", Index: 3, Text: map[Language]string{"en": "Fourth scene text"}},
	}
}

// TestRunner_TranslationCrashResume_ReusesCompletedScenes is the decisive
// resume-reale test for translation:
//
//	scene-0 translated  → REUSE
//	scene-1 translated  → REUSE
//	scene-2 in-flight   → RETRY (fails on first attempt)
//	scene-3 not reached → CONTINUE
//
// The first attempt fails at scene-2; the per-unit checkpoints written during
// the fan-out must preserve scene-0 and scene-1, so the restart never
// re-translates them.
func TestRunner_TranslationCrashResume_ReusesCompletedScenes(t *testing.T) {
	runner, repo, _, translator, _, _, _ := newTestRunner()
	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeNone
	req.Docs = DocumentsConfig{}
	req.Languages = []Language{"en", "es"}
	runner.textGen = newStubTextGenerator(fourSceneText())
	// Deterministic order: scene-0 → scene-1 → scene-2 → scene-3.
	runner.SetTranslationConcurrency(1)

	// scene-2 fails on its first attempt (the in-flight scene at kill time).
	translator.failAfter["scene-2"] = 0

	runID := "run-translation-crash"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID:           runID,
		Request:      req,
		Status:       RunStatusPending,
		CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	failed := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusFailed, failed.Status)
	require.Equal(t, StageTranslatingScenes, failed.FailedStage)

	// The crash preserved scene-0 & scene-1 (per-unit checkpoint during the
	// fan-out); scene-2 (in-flight) and scene-3 (not reached) are empty.
	require.NotNil(t, failed.Result)
	require.NotEmpty(t, failed.Result.Scenes[0].Text["es"], "scene-0 ES text must survive the crash")
	require.NotEmpty(t, failed.Result.Scenes[1].Text["es"], "scene-1 ES text must survive the crash")
	require.Empty(t, failed.Result.Scenes[2].Text["es"], "scene-2 was in-flight and must be retried")
	require.Empty(t, failed.Result.Scenes[3].Text["es"], "scene-3 was not reached")

	require.Equal(t, 1, translator.callsFor("scene-0"))
	require.Equal(t, 1, translator.callsFor("scene-1"))
	require.Equal(t, 1, translator.callsFor("scene-2"))
	require.Equal(t, 0, translator.callsFor("scene-3"))

	// "Restart": clear the fault and re-execute the same run.
	translator.failAfter = map[string]int{}

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status)

	// REUSE: scene-0 & scene-1 are NOT re-translated (still 1 call each).
	require.Equal(t, 1, translator.callsFor("scene-0"), "scene-0 must be reused, not re-translated")
	require.Equal(t, 1, translator.callsFor("scene-1"), "scene-1 must be reused, not re-translated")
	// RETRY: scene-2 is re-translated exactly once (2 total calls).
	require.Equal(t, 2, translator.callsFor("scene-2"), "scene-2 must be retried once")
	// CONTINUE: scene-3 is translated once.
	require.Equal(t, 1, translator.callsFor("scene-3"), "scene-3 must continue")

	for i := range final.Result.Scenes {
		require.NotEmpty(t, final.Result.Scenes[i].Text["es"], "scene %d must have ES text", i)
	}
}

// TestRunner_TTSCrashResume_ReusesCompletedScenes is the resume-reale test for
// TTS. With concurrency 1 the synthesis order is deterministic:
//
//	scene-0/en, scene-0/es, scene-1/en, scene-1/es  → REUSE
//	scene-2/en (first attempt)                       → RETRY (fails)
//	scene-2/es                                       → CONTINUE
func TestRunner_TTSCrashResume_ReusesCompletedScenes(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	req := defaultTestRequest()
	req.Languages = []Language{"en", "es"}
	runner.textGen = newStubTextGenerator(fourSceneText())
	ttsGen := newCrashTTSGenerator("scene-2", "en")
	runner.voiceoverGen = ttsGen
	// Deterministic synthesis order: scene-0/en → scene-0/es → … → scene-2/es.
	runner.SetTTSConcurrency(1)
	runner.SetTranslationConcurrency(1)

	runID := "run-tts-crash"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID:           runID,
		Request:      req,
		Status:       RunStatusPending,
		CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	failed := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusFailed, failed.Status)
	require.Equal(t, StageGeneratingVoiceovers, failed.FailedStage)

	// The crash preserved scene-0 & scene-1 voiceovers (per-unit checkpoint).
	require.NotNil(t, failed.Result)
	require.NotEmpty(t, failed.Result.Scenes[0].Voiceover["en"].ID)
	require.NotEmpty(t, failed.Result.Scenes[0].Voiceover["es"].ID)
	require.NotEmpty(t, failed.Result.Scenes[1].Voiceover["en"].ID)
	require.NotEmpty(t, failed.Result.Scenes[1].Voiceover["es"].ID)
	require.Empty(t, failed.Result.Scenes[2].Voiceover["en"].ID, "scene-2/en was in-flight and must be retried")

	require.Equal(t, 1, ttsGen.callsFor("scene-0", "en"))
	require.Equal(t, 1, ttsGen.callsFor("scene-0", "es"))
	require.Equal(t, 1, ttsGen.callsFor("scene-1", "en"))
	require.Equal(t, 1, ttsGen.callsFor("scene-1", "es"))
	require.Equal(t, 1, ttsGen.callsFor("scene-2", "en"))

	// "Restart": the crashTTSGenerator only fails the FIRST call to scene-2,
	// so the retry succeeds.
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status)

	// REUSE: scene-0 & scene-1 voiceovers are not re-synthesized.
	require.Equal(t, 1, ttsGen.callsFor("scene-0", "en"), "scene-0/en must be reused")
	require.Equal(t, 1, ttsGen.callsFor("scene-0", "es"), "scene-0/es must be reused")
	require.Equal(t, 1, ttsGen.callsFor("scene-1", "en"), "scene-1/en must be reused")
	require.Equal(t, 1, ttsGen.callsFor("scene-1", "es"), "scene-1/es must be reused")
	// RETRY: scene-2/en re-synthesized once; CONTINUE: scene-2/es once.
	require.Equal(t, 2, ttsGen.callsFor("scene-2", "en"), "scene-2/en must be retried once")
	require.Equal(t, 1, ttsGen.callsFor("scene-2", "es"), "scene-2/es must continue")

	for i := range final.Result.Scenes {
		require.NotEmpty(t, final.Result.Scenes[i].Voiceover["en"].ID, "scene %d must have EN voiceover", i)
		require.NotEmpty(t, final.Result.Scenes[i].Voiceover["es"].ID, "scene %d must have ES voiceover", i)
	}
}
