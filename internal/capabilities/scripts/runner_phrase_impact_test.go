// Package scriptgeneration — runner_phrase_impact_test.go certifies the
// extractive editorial data products (summary, bullet points, heavy
// sentences) reach the generated video result, and that an unavailable
// phrase-impact worker degrades instead of failing the run.
package scriptgeneration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type stubPhraseImpactAnalyzer struct {
	result   scriptpkg.PhraseImpactResult
	err      error
	calls    int
	lastText string
	lastLang string
}

func (s *stubPhraseImpactAnalyzer) Analyze(_ context.Context, transcript, language string) (scriptpkg.PhraseImpactResult, error) {
	s.calls++
	s.lastText = transcript
	s.lastLang = language
	if s.err != nil {
		return scriptpkg.PhraseImpactResult{}, s.err
	}
	return s.result, nil
}

// TestRunnerPersistsExtractiveSummaryOnTheResult pins the user-visible
// outcome: a configured analyzer fills the result's summary, bullets and
// heavy sentences, and it is fed the generated narration text.
func TestRunnerPersistsExtractiveSummaryOnTheResult(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	analyzer := &stubPhraseImpactAnalyzer{result: scriptpkg.PhraseImpactResult{
		Summary:      "Riassunto automatico del video.",
		BulletPoints: []string{"Punto uno", "Punto due"},
		HeavySentences: []scriptpkg.ImportantSentence{
			{Index: 0, Text: "First scene text", Importance: 0.93},
		},
	}}
	runner.SetPhraseImpactAnalyzer(analyzer)

	req := defaultTestRequest()
	runID := "run-phrase-impact-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	require.Equal(t, RunStatusCompleted, final.Status)
	require.NotNil(t, final.Result)

	assert.Equal(t, 1, analyzer.calls, "the analyzer must run once per completed scene-text generation")
	assert.NotEmpty(t, analyzer.lastText, "the analyzer must receive the generated narration text")
	assert.Equal(t, "Riassunto automatico del video.", final.Result.Summary)
	assert.Equal(t, []string{"Punto uno", "Punto due"}, final.Result.BulletPoints)
	require.Len(t, final.Result.HeavySentences, 1)
	assert.Equal(t, "First scene text", final.Result.HeavySentences[0].Text)

	// The editorial summary must never leak into the rendered narration.
	assert.Equal(t, "First scene text\n\nSecond scene text\n\nThird scene text", final.Result.Output.Text)
}

// TestRunnerSurvivesPhraseImpactFailure pins the resilience contract: the
// extractive summary is an optional data product, so a broken or missing
// Rust NLP worker must not cost the caller the video.
func TestRunnerSurvivesPhraseImpactFailure(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	runner.SetPhraseImpactAnalyzer(&stubPhraseImpactAnalyzer{err: errors.New("phrase-impact Rust worker is not configured")})

	req := defaultTestRequest()
	runID := "run-phrase-impact-degraded-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	require.NotNil(t, final.Result)

	assert.Equal(t, RunStatusCompleted, final.Status, "an unavailable summary worker must not fail the run")
	assert.Empty(t, final.Result.Summary)
	assert.Empty(t, final.Result.BulletPoints)
	assert.Empty(t, final.Result.HeavySentences)
}
