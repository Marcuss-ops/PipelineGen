// Package scriptgeneration — runner_tts_pool_test.go certifies the TTS
// voiceover worker pool: the voiceover phase fans out scene×language
// synthesis through a bounded pool (r.ttsConcurrency) so TTS runs in parallel
// with SceneAnalysis from the SceneTextReady boundary, and buildVoiceoverWork
// flattens the scene×language grid deterministically (skipping empty text and
// already-generated scenes).
package scriptgeneration

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// barrierVoiceoverGenerator blocks each Generate call until `all` concurrent
// calls have started, then releases them all. It can only complete if the TTS
// pool fans out to at least `all` concurrent workers: a serial (concurrency 1)
// pool would deadlock waiting for calls that never start.
type barrierVoiceoverGenerator struct {
	all     int
	started atomic.Int32
	release chan struct{}
	once    sync.Once
}

func newBarrierVoiceoverGenerator(all int) *barrierVoiceoverGenerator {
	return &barrierVoiceoverGenerator{all: all, release: make(chan struct{})}
}

func (g *barrierVoiceoverGenerator) Generate(ctx context.Context, input VoiceoverInput) (AudioReference, error) {
	if g.started.Add(1) == int32(g.all) {
		g.once.Do(func() { close(g.release) })
	}
	select {
	case <-g.release:
	case <-ctx.Done():
		return AudioReference{}, ctx.Err()
	}
	return AudioReference{ID: "vo-" + input.SceneID + "-" + string(input.Language), Duration: 1.0}, nil
}

// TestVoiceoverPhase_FansOutTTSConcurrently proves the TTS pool actually runs
// in parallel: with 3 scenes, a single language, and TTS concurrency 3, a
// barrier generator that requires all 3 calls to start before any completes
// still finishes. A serial voiceover phase would deadlock and time out.
func TestVoiceoverPhase_FansOutTTSConcurrently(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	runner.voiceoverGen = newBarrierVoiceoverGenerator(3)
	runner.SetTTSConcurrency(3)

	req := defaultTestRequest()
	req.Languages = []Language{"en"} // single language → exactly 3 TTS calls

	runID := "run-tts-pool-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID:           runID,
		Request:      req,
		Status:       RunStatusPending,
		CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)

	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	require.Equal(t, RunStatusCompleted, final.Status, "voiceover phase must fan out and complete")
	require.NotNil(t, final.Result)
	require.Len(t, final.Result.Scenes, 3)
	for i, s := range final.Result.Scenes {
		require.NotEmpty(t, s.Voiceover["en"].ID, "scene %d must have an EN voiceover", i)
	}
	require.Equal(t, 3, final.Result.AudioMetrics.TTSCalls, "TTS calls must be counted per scene")
}

// TestSetTTSConcurrency_ZeroFallsBackToDefault pins the configurable pool's
// fail-safe: a non-positive override restores the certified default.
func TestSetTTSConcurrency_ZeroFallsBackToDefault(t *testing.T) {
	runner, _, _, _, _, _, _ := newTestRunner()
	runner.SetTTSConcurrency(0)
	require.Equal(t, DefaultTTSConcurrency, runner.ttsConcurrency)
	runner.SetTTSConcurrency(7)
	require.Equal(t, 7, runner.ttsConcurrency)
}

// TestBuildVoiceoverWork_SkipsEmptyAndExisting pins the flattening contract:
// empty text and already-generated scenes are skipped, language order is
// deterministic, and work items carry the immutable scene text.
func TestBuildVoiceoverWork_SkipsEmptyAndExisting(t *testing.T) {
	scenes := []Scene{
		{
			ID: "s0", Index: 0,
			Text:      map[Language]string{"en": "hello", "es": ""},
			Voiceover: map[Language]AudioReference{"en": {ID: "existing"}},
		},
		{
			ID: "s1", Index: 1,
			Text: map[Language]string{"en": "world"},
		},
	}

	work := buildVoiceoverWork(scenes, "en", []Language{"es"})
	// s0/en is already generated (skipped), s0/es is empty (skipped);
	// only s1/en remains.
	require.Len(t, work, 1)
	require.Equal(t, "s1", work[0].sceneID)
	require.Equal(t, Language("en"), work[0].lang)
	require.Equal(t, "world", work[0].text)
	require.Same(t, &scenes[1], work[0].scene, "work item must point at the same scene")
}

// TestBuildVoiceoverWork_DispatchesSourceBeforeTargets pins the dispatch
// priority key: languages are ordered by (scene_index, language_priority),
// NOT alphabetically — the source language dispatches before a target even
// when it sorts later, so output-asset lineage ordinals follow the caller's
// language order.
func TestBuildVoiceoverWork_DispatchesSourceBeforeTargets(t *testing.T) {
	scenes := []Scene{{
		ID: "s0", Index: 0,
		Text: map[Language]string{"es": "hola", "en": "hello"},
	}}

	// Alphabetical order would be en, es; priority order is es (source), en.
	work := buildVoiceoverWork(scenes, "es", []Language{"en"})
	require.Len(t, work, 2)
	require.Equal(t, Language("es"), work[0].lang)
	require.Equal(t, Language("en"), work[1].lang)
}
