package scriptgeneration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// ── Pure dispatch-priority helpers ─────────────────────────────────────

func TestDispatchLanguagePriority(t *testing.T) {
	source := Language("en")
	targets := []Language{"es", "it"}
	require.Equal(t, 0, dispatchLanguagePriority(source, targets, "en"), "source language is priority 0")
	require.Equal(t, 1, dispatchLanguagePriority(source, targets, "es"), "first target is priority 1")
	require.Equal(t, 2, dispatchLanguagePriority(source, targets, "it"), "second target is priority 2")
	require.Equal(t, 4, dispatchLanguagePriority(source, targets, "fr"), "undeclared language falls after all targets")
}

func TestOrderedSceneLanguages(t *testing.T) {
	text := map[Language]string{
		"it": "ciao", "es": "hola", "en": "hello", "fr": "bonjour",
	}
	got := orderedSceneLanguages(text, "en", []Language{"es", "it"})
	// Priority: en (source) → es → it (targets in caller order) → fr (undeclared).
	require.Equal(t, []Language{"en", "es", "it", "fr"}, got)
}

// ── Translation dispatch order (end-to-end, deterministic via concurrency 1) ──

type orderRecordingTranslator struct {
	mu    sync.Mutex
	order []string
}

func (t *orderRecordingTranslator) Translate(_ context.Context, in TranslationInput) (string, error) {
	t.mu.Lock()
	t.order = append(t.order, in.SceneID+":"+string(in.TargetLanguage))
	t.mu.Unlock()
	return "translated " + in.SourceText, nil
}

func (t *orderRecordingTranslator) snapshot() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.order...)
}

// TestTranslationPhaseDispatchesInSceneLanguagePriorityOrder pins the
// deterministic dispatch contract: with a single-slot pool the provider is
// called in (scene_index, language_priority) order — every scene's languages
// before the next scene, targets in the caller's req.Languages order — never
// an arbitrary interleaving across scenes.
func TestTranslationPhaseDispatchesInSceneLanguagePriorityOrder(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	recorder := &orderRecordingTranslator{}
	runner.translator = recorder
	runner.SetTranslationConcurrency(1)

	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeNone
	req.Docs = DocumentsConfig{}
	req.Languages = []Language{"en", "es", "it"}

	runID := "run-translation-dispatch-order"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID:           runID,
		Request:      req,
		Status:       RunStatusPending,
		CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status)

	want := []string{
		"scene-0:es", "scene-0:it",
		"scene-1:es", "scene-1:it",
		"scene-2:es", "scene-2:it",
	}
	require.Equal(t, want, recorder.snapshot(), "dispatch order must be (scene_index, language_priority)")
}
