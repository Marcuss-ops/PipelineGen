package scriptgeneration

import (
	"context"
	"sync"
	"testing"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/stretchr/testify/require"
)

type trackingTranslationGenerator struct {
	mu       sync.Mutex
	inflight int
	max      int
}

func (g *trackingTranslationGenerator) Translate(_ context.Context, in TranslationInput) (string, error) {
	g.mu.Lock()
	g.inflight++
	if g.inflight > g.max {
		g.max = g.inflight
	}
	g.mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	g.mu.Lock()
	g.inflight--
	g.mu.Unlock()
	return "translated " + in.SourceText, nil
}

func (g *trackingTranslationGenerator) maximum() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.max
}

func TestTranslationPhaseFansOutSceneLanguageCalls(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	tracker := &trackingTranslationGenerator{}
	runner.translator = tracker
	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeNone
	req.Docs = DocumentsConfig{}
	req.Project = ""
	runID := "run-translation-pool"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing}))

	runner.Execute(context.Background(), runID, req)
	run := awaitCompletion(t, repo, runID, time.Second)
	require.Equal(t, RunStatusCompleted, run.Status)
	require.NotNil(t, run.Result.TranslationMetrics)
	require.Equal(t, 3, run.Result.TranslationMetrics.Calls)
	require.GreaterOrEqual(t, tracker.maximum(), 2, "translation calls must overlap")
	require.Equal(t, DefaultTranslationConcurrency, run.Result.TranslationMetrics.Concurrency)
}
