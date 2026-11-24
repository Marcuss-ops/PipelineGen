package scriptgeneration

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/stretchr/testify/require"
)

// batchTranslationRecorder counts provider round-trips on both paths. The
// batched path must collapse N scenes of one language into ONE call, and the
// per-scene path must remain the fallback when the batch is unusable.
type batchTranslationRecorder struct {
	mu          sync.Mutex
	batchCalls  int
	batchSizes  []int
	perScene    int
	dropLastID  bool
	blockUntil  time.Duration
	seenTargets map[string]int
}

func (r *batchTranslationRecorder) Translate(ctx context.Context, in TranslationInput) (string, error) {
	r.mu.Lock()
	r.perScene++
	r.mu.Unlock()
	if r.blockUntil > 0 {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(r.blockUntil):
		}
	}
	return "ES " + in.SourceText, nil
}

func (r *batchTranslationRecorder) TranslateBatch(ctx context.Context, in TranslationBatchInput) ([]string, error) {
	r.mu.Lock()
	r.batchCalls++
	r.batchSizes = append(r.batchSizes, len(in.Items))
	if r.seenTargets == nil {
		r.seenTargets = make(map[string]int)
	}
	r.seenTargets[string(in.TargetLanguage)] += len(in.Items)
	r.mu.Unlock()
	if r.blockUntil > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(r.blockUntil):
		}
	}
	out := make([]string, 0, len(in.Items))
	for i, item := range in.Items {
		if r.dropLastID && i == len(in.Items)-1 {
			continue
		}
		out = append(out, "ES "+item.Text)
	}
	return out, nil
}

func (r *batchTranslationRecorder) counts() (batch int, perScene int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.batchCalls, r.perScene
}

// TestTranslationPhaseBatchesScenesPerLanguage proves WST-04: three scenes in
// one target language cost ONE provider call instead of three, and every scene
// still receives its own translation.
func TestTranslationPhaseBatchesScenesPerLanguage(t *testing.T) {
	runner, repo, _, _, _, _ := newTestRunner()
	recorder := &batchTranslationRecorder{}
	runner.translator = recorder
	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeNone
	req.Project = ""
	runID := "run-translation-batch"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing}))

	runner.Execute(context.Background(), runID, req)
	run := awaitCompletion(t, repo, runID, time.Second)
	require.Equal(t, RunStatusCompleted, run.Status)

	batch, perScene := recorder.counts()
	require.Equal(t, 1, batch, "one batch per language")
	require.Equal(t, 0, perScene, "no per-scene fallback on a healthy batch")
	require.Equal(t, []int{3}, recorder.batchSizes)
	require.NotNil(t, run.Result.TranslationMetrics)
	require.Equal(t, 1, run.Result.TranslationMetrics.Calls, "the metric counts provider round-trips, not scenes")
	for _, scene := range run.Result.Scenes {
		require.Equal(t, "ES "+scene.Text[req.SourceLanguage], scene.Text["es"])
	}
}

// TestTranslationPhaseFallsBackPerSceneOnMisalignedBatch proves the batch is
// never trusted: a short answer re-runs exactly that chunk through the
// per-scene path, and the run still delivers every translation.
func TestTranslationPhaseFallsBackPerSceneOnMisalignedBatch(t *testing.T) {
	runner, repo, _, _, _, _ := newTestRunner()
	recorder := &batchTranslationRecorder{dropLastID: true}
	runner.translator = recorder
	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeNone
	req.Project = ""
	runID := "run-translation-batch-misaligned"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing}))

	runner.Execute(context.Background(), runID, req)
	run := awaitCompletion(t, repo, runID, time.Second)
	require.Equal(t, RunStatusCompleted, run.Status)

	batch, perScene := recorder.counts()
	require.Equal(t, 1, batch)
	require.Equal(t, 3, perScene, "the whole chunk is retried per scene")
	for _, scene := range run.Result.Scenes {
		require.NotEmpty(t, scene.Text["es"])
	}
}

// TestTranslationPhaseAppliesDedicatedTimeout proves the translation budget is
// its own: a provider that never answers fails the run within the translation
// timeout, not within the generation client's 600 s.
func TestTranslationPhaseAppliesDedicatedTimeout(t *testing.T) {
	runner, repo, _, _, _, _ := newTestRunner()
	recorder := &batchTranslationRecorder{blockUntil: 5 * time.Second}
	runner.translator = recorder
	runner.SetTranslationTimeout(20 * time.Millisecond)
	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeNone
	req.Project = ""
	runID := "run-translation-timeout"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing}))

	started := time.Now()
	runner.Execute(context.Background(), runID, req)
	run := awaitCompletion(t, repo, runID, 2*time.Second)
	require.Equal(t, RunStatusFailed, run.Status)
	require.Less(t, time.Since(started), 2*time.Second, fmt.Sprintf("translation must fail within its own budget, took %s", time.Since(started)))
}
