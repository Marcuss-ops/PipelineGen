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

func TestBuildVoiceoverWork_ExplicitLanguagesDoNotLimitTranslationInputs(t *testing.T) {
	scenes := []Scene{{
		ID: "s0", Index: 0,
		Text: map[Language]string{"en": "hello", "it": "ciao", "de": "hallo"},
	}}
	work := buildVoiceoverWorkForLanguages(scenes, "en", []Language{"it", "de"}, []Language{"en"})
	require.Len(t, work, 1)
	require.Equal(t, Language("en"), work[0].lang)
	require.Equal(t, "hello", work[0].text)
}

func TestBuildVoiceoverLanguageWork_UsesCanonicalSceneLanguageOrderAndText(t *testing.T) {
	text := map[Language]string{"fr": "bonjour", "it": "ciao", "en": "hello", "de": "hallo", "pt": ""}
	work := buildVoiceoverLanguageWork(text, "en", []Language{"it", "fr"}, nil)
	require.Equal(t, []sceneLanguageWork{
		{lang: "en", text: "hello"},
		{lang: "it", text: "ciao"},
		{lang: "fr", text: "bonjour"},
		{lang: "de", text: "hallo"},
	}, work, "selection follows source, declared targets, then undeclared languages alphabetically and omits blank text")

	explicit := buildVoiceoverLanguageWork(text, "en", []Language{"it", "fr"}, []Language{"fr", "en"})
	require.Equal(t, []sceneLanguageWork{
		{lang: "en", text: "hello"},
		{lang: "fr", text: "bonjour"},
	}, explicit)
}

func TestVoiceoverLanguageFilter_DistinguishesOmittedAndExplicitEmpty(t *testing.T) {
	all := newVoiceoverLanguageFilter(nil)
	none := newVoiceoverLanguageFilter([]Language{})
	require.True(t, all.allows("en"), "omitted selection means all available languages")
	require.False(t, none.allows("en"), "an explicit empty selection means no voiceovers")
}

// countingVoiceoverGenerator records every synthesis request under a
// (scene_id, language) key so a test can prove the runner never asks the
// provider for the same pair twice.
type countingVoiceoverGenerator struct {
	mu   sync.Mutex
	seen map[string]int
}

func newCountingVoiceoverGenerator() *countingVoiceoverGenerator {
	return &countingVoiceoverGenerator{seen: map[string]int{}}
}

func (g *countingVoiceoverGenerator) Generate(_ context.Context, in VoiceoverInput) (AudioReference, error) {
	g.mu.Lock()
	g.seen[in.SceneID+"|"+string(in.Language)]++
	g.mu.Unlock()
	return AudioReference{ID: "vo-" + in.SceneID + "-" + string(in.Language), FilePath: "/tmp/vo.mp3", Duration: 1}, nil
}

func (g *countingVoiceoverGenerator) counts() map[string]int {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make(map[string]int, len(g.seen))
	for k, v := range g.seen {
		out[k] = v
	}
	return out
}

// eagerStreamingTextGenerator implements TextGenerator and SceneTextStreamer
// and emits every scene immediately (no gate), so a full streaming run
// completes without a test-side release. It is the minimum surface required to
// make the runner take the SceneTextReady streaming branch.
type eagerStreamingTextGenerator struct{ scenes []Scene }

func (g *eagerStreamingTextGenerator) GenerateSceneText(_ context.Context, _ GenerateRequest) ([]Scene, error) {
	return g.scenes, nil
}

func (g *eagerStreamingTextGenerator) GenerateSceneTextStream(ctx context.Context, _ GenerateRequest, emit func(Scene) error) error {
	for _, s := range g.scenes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := emit(s); err != nil {
			return err
		}
	}
	return nil
}

// TestVoiceover_SynthesizesEachSceneLanguageExactlyOnce pins the fan-out
// contract that a "TTS is 2× the scene count" regression would violate: one
// (scene, language) pair is synthesized EXACTLY ONCE per attempt, even when the
// streaming SceneTextReady coordinator synthesizes it first and the batch
// voiceover phase then re-walks the very same scene×language grid. The batch
// phase owns the reuse contract (buildVoiceoverWork skips a non-empty ref.ID),
// so a second provider call here is a real cost/latency duplication, never an
// accepted retry (which is observable separately as an error message).
func TestVoiceover_SynthesizesEachSceneLanguageExactlyOnce(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	runner.textGen = &eagerStreamingTextGenerator{scenes: defaultTestScenes()}
	counter := newCountingVoiceoverGenerator()
	runner.voiceoverGen = counter

	// Source "en" + target "es" → one voiceover language per scene beyond the
	// source, so 3 scenes × 2 languages = 6 distinct pairs and 6 syntheses.
	req := defaultTestRequest()
	runID := "run-tts-no-double-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)

	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	require.Equal(t, RunStatusCompleted, final.Status)
	require.NotNil(t, final.Result)

	counts := counter.counts()
	for key, n := range counts {
		require.Equal(t, 1, n, "scene|language %q must be synthesized exactly once, got %d", key, n)
	}
	require.Len(t, counts, 6, "every scene×language pair must be synthesized once (3 scenes × 2 languages)")
	require.Equal(t, 6, final.Result.AudioMetrics.TTSCalls, "TTSCalls must count one synthesis per scene×language pair, not two")
}

// TestVoiceover_DoesNotDuplicateFanOutForSingleLanguage is the direct guard for
// the "TTS is 2× the scene count" report: with a single voiceover language, the
// number of provider syntheses must equal the number of scenes exactly. The
// streaming SceneTextReady coordinator synthesizes each scene once, and the
// batch voiceover phase that runs afterwards must REUSE those references
// (len(work)==0) rather than re-dispatch them. It also pins that a fully-reused
// streaming run still reports its real TTSCalls — the batch phase owns only the
// work it dispatched, so it must add to, never overwrite, the coordinator's
// count.
func TestVoiceover_DoesNotDuplicateFanOutForSingleLanguage(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	scenes := defaultTestScenes()
	runner.textGen = &eagerStreamingTextGenerator{scenes: scenes}
	counter := newCountingVoiceoverGenerator()
	runner.voiceoverGen = counter

	req := defaultTestRequest()
	req.Languages = []Language{"en"} // source language only → exactly len(scenes) syntheses
	req.Docs.Languages = []Language{"en"}
	runID := "run-tts-no-double-002"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)

	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	require.Equal(t, RunStatusCompleted, final.Status)
	require.NotNil(t, final.Result)

	total := 0
	for key, n := range counter.counts() {
		require.Equal(t, 1, n, "scene|language %q must be synthesized exactly once, got %d", key, n)
		total += n
	}
	require.Equal(t, len(scenes), total, "a single voiceover language must cost exactly one synthesis per scene, never 2×")
	require.Equal(t, len(scenes), final.Result.AudioMetrics.TTSCalls, "TTSCalls must equal the scene count for one language, not 2×")
}
