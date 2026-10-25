// Package scriptgeneration — runner_phrase_impact_test.go certifies the
// extractive editorial data products (summary, bullet points, heavy
// sentences) reach the generated video result from the unified semantic
// batch, and that an unavailable batch degrades instead of failing the run.
package scriptgeneration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
)

type stubCombinedSemanticAnalyzer struct {
	calls  int
	result scriptpkg.PhraseImpactResult
	err    error
}

func (s *stubCombinedSemanticAnalyzer) SemanticCandidates(context.Context, string, string, int) (*SemanticCandidateBundle, error) {
	return &SemanticCandidateBundle{}, nil
}

func (s *stubCombinedSemanticAnalyzer) AnalyzeSemanticEditorial(_ context.Context, _, _ string, _ []scriptpkg.SceneAnalysisInput) (map[string]*SemanticCandidateBundle, scriptpkg.PhraseImpactResult, error) {
	s.calls++
	if s.err != nil {
		return nil, scriptpkg.PhraseImpactResult{}, s.err
	}
	return map[string]*SemanticCandidateBundle{}, s.result, nil
}

func TestRunnerConsumesEditorialProductsFromTheUnifiedSemanticBatch(t *testing.T) {
	runner, repo, _, _, _, _ := newTestRunner()
	combined := &stubCombinedSemanticAnalyzer{result: scriptpkg.PhraseImpactResult{
		Summary: "one batch summary", BulletPoints: []string{"one grounded point"},
		ChapterManifest: scriptpkg.ChapterManifest{SchemaVersion: "chapter_manifest.v1"},
	}}
	runner.SetSceneSemanticAnalyzer(combined)

	req := defaultTestRequest()
	runID := "run-unified-semantic-editorial-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	require.Equal(t, RunStatusCompleted, final.Status)
	require.NotNil(t, final.Result)
	assert.Equal(t, 1, combined.calls, "the combined manifest should be requested once per script")
	assert.Equal(t, "one batch summary", final.Result.Summary)
	assert.Equal(t, []string{"one grounded point"}, final.Result.BulletPoints)
}

// TestRunnerUnifiedEditorialFailureLeavesProductsEmpty pins the resilience
// contract after the legacy fallback was retired: a failed unified batch
// keeps the run and the video, leaving only the optional editorial fields
// empty, and never launches a second analysis path.
func TestRunnerUnifiedEditorialFailureLeavesProductsEmpty(t *testing.T) {
	runner, repo, _, _, _, _ := newTestRunner()
	combined := &stubCombinedSemanticAnalyzer{err: errors.New("semantic worker unavailable")}
	runner.SetSceneSemanticAnalyzer(combined)

	req := defaultTestRequest()
	runID := "run-unified-semantic-failure-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	require.Equal(t, RunStatusCompleted, final.Status)
	assert.Equal(t, 1, combined.calls)
	assert.Empty(t, final.Result.Summary)
	assert.Empty(t, final.Result.BulletPoints)
}

// TestRunnerPublishesPhraseImpactStageTimings pins the measurement contract:
// the batch's own stage breakdown reaches the stage histogram, so the one
// non-local stage (embedding) is visible in production instead of inferred.
func TestRunnerPublishesPhraseImpactStageTimings(t *testing.T) {
	runner, repo, _, _, _, _ := newTestRunner()
	runner.SetSceneSemanticAnalyzer(&stubCombinedSemanticAnalyzer{result: scriptpkg.PhraseImpactResult{
		Summary: "s", BulletPoints: []string{"b"},
		Timings: scriptpkg.PhraseImpactTimings{EmbeddingMS: 12.5, TotalMS: 17.2},
	}})

	embedding := observability.ScriptPhraseImpactStageSeconds.WithLabelValues("embedding")
	before := histogramSampleCount(t, embedding)

	req := defaultTestRequest()
	runID := "run-phrase-impact-timings-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	require.Equal(t, RunStatusCompleted, final.Status)

	if got := histogramSampleCount(t, embedding); got != before+1 {
		t.Fatalf("embedding stage observations = %d, want %d (one per successful analysis)", got, before+1)
	}
}

func histogramSampleCount(t *testing.T, observer prometheus.Observer) uint64 {
	t.Helper()
	metric, ok := observer.(prometheus.Metric)
	if !ok {
		t.Fatalf("observer is not a prometheus.Metric: %T", observer)
	}
	var value dto.Metric
	if err := metric.Write(&value); err != nil {
		t.Fatalf("write histogram metric: %v", err)
	}
	return value.GetHistogram().GetSampleCount()
}
