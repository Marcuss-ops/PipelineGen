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

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
)

type stubPhraseImpactAnalyzer struct {
	result     scriptpkg.PhraseImpactResult
	err        error
	calls      int
	lastText   string
	lastLang   string
	lastScenes []string
	lastTopics []string
}

func (s *stubPhraseImpactAnalyzer) AnalyzeWithContext(_ context.Context, transcript, language string, scenes, topics []string) (scriptpkg.PhraseImpactResult, error) {
	s.calls++
	s.lastText = transcript
	s.lastLang = language
	s.lastScenes = append([]string(nil), scenes...)
	s.lastTopics = append([]string(nil), topics...)
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
		ChapterManifest: scriptpkg.ChapterManifest{SchemaVersion: "chapter_manifest.v1", Chapters: []scriptpkg.ChapterManifestEntry{{Title: "Scene topic", TitleSource: "scene_topic", StartSentence: 0, EndSentence: 1, Bullets: []scriptpkg.ChapterBullet{{StartSentence: 0, EndSentence: 1, Text: "First scene text"}}}}},
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
	require.NotNil(t, final.Result.ChapterManifest)
	assert.Equal(t, "chapter_manifest.v1", final.Result.ChapterManifest.SchemaVersion)

	// The editorial summary must never leak into the rendered narration.
	assert.Equal(t, "First scene text\n\nSecond scene text\n\nThird scene text", final.Result.Output.Text)
}

// TestRunnerPublishesPhraseImpactStageTimings pins the measurement contract:
// the worker's own stage breakdown reaches the stage histogram, so the one
// non-local stage (embedding) is visible in production instead of inferred.
func TestRunnerPublishesPhraseImpactStageTimings(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	runner.SetPhraseImpactAnalyzer(&stubPhraseImpactAnalyzer{result: scriptpkg.PhraseImpactResult{
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

// TestRunnerSurvivesPhraseImpactFailure pins the resilience contract: the
// extractive summary is an optional data product, so a broken or missing
// Rust NLP worker must not cost the caller the video.
type stubSceneIntelligenceAnalyzer struct {
	manifest *scriptpkg.SceneIntelligenceManifest
	err      error
	calls    int
	last     []scriptpkg.SISceneAnalysisInput
}

func (s *stubSceneIntelligenceAnalyzer) AnalyzeBatch(_ context.Context, scenes []scriptpkg.SISceneAnalysisInput) (*scriptpkg.SceneIntelligenceManifest, error) {
	s.calls++
	s.last = append([]scriptpkg.SISceneAnalysisInput(nil), scenes...)
	if s.err != nil {
		return nil, s.err
	}
	return s.manifest, nil
}

func TestRunnerRunsOptionalSceneIntelligenceWithGroundedPlaybackWindows(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	analyzer := &stubSceneIntelligenceAnalyzer{manifest: &scriptpkg.SceneIntelligenceManifest{
		SchemaVersion:           scriptpkg.SceneIntelligenceManifestVersion,
		AnalyzerVersion:         scriptpkg.SceneIntelligenceAnalyzerVersion,
		RelationRegistryVersion: scriptpkg.RelationRegistryVersion,
		Scenes: []scriptpkg.SISceneSignals{{
			SceneID: "scene-0", Density: scriptpkg.SIDensity{SceneID: "scene-0", Score: 0.2, SuggestedVisualBudget: "standard"},
			TemporalEvents: []scriptpkg.SITemporalEvent{{EventID: "event-0000", TextSpan: "First scene text", TemporalExpression: "scene", NarrationIndex: 0}},
		}},
	}}
	runner.SetSceneIntelligenceAnalyzer(analyzer)
	req := defaultTestRequest()
	runID := "run-scene-intelligence-grounded-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing}))
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status)
	require.NotNil(t, final.Result)
	require.NotNil(t, final.Result.SceneIntelligence)
	require.Len(t, analyzer.last, 3)
	assert.Equal(t, int64(0), analyzer.last[0].VideoStartMS)
	assert.Equal(t, int64(1000), analyzer.last[0].VideoEndMS)
	assert.Equal(t, "scene-0", analyzer.last[0].SceneID)
	require.Len(t, final.Result.SceneIntelligenceProjection, 1)
	assert.Equal(t, "scene-0", final.Result.SceneIntelligenceProjection[0].SceneID)
	assert.Equal(t, int64(0), final.Result.SceneIntelligenceProjection[0].VideoStartMS)
	assert.Equal(t, int64(1000), final.Result.SceneIntelligenceProjection[0].VideoEndMS)
	assert.False(t, final.Result.SceneIntelligenceProjection[0].TimelineRecipeEligible,
		"a temporal event without an absolute year cannot create an absolute timeline recipe")
	assert.Nil(t, final.Result.BulletPoints, "SI must not fabricate or replace legacy editorial products when phrase analysis is unwired")
}

func TestRunnerRejectsInvalidSceneIntelligenceManifestFailSoft(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	invalid := &scriptpkg.SceneIntelligenceManifest{
		SchemaVersion: "unknown.v99", AnalyzerVersion: scriptpkg.SceneIntelligenceAnalyzerVersion,
		RelationRegistryVersion: scriptpkg.RelationRegistryVersion,
	}
	analyzer := &stubSceneIntelligenceAnalyzer{manifest: invalid}
	runner.SetSceneIntelligenceAnalyzer(analyzer)
	req := defaultTestRequest()
	runID := "run-scene-intelligence-invalid-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing}))
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status)
	require.NotNil(t, final.Result)
	assert.Nil(t, final.Result.SceneIntelligence, "an invalid worker manifest is discarded rather than persisted")
	assert.Nil(t, final.Result.SceneIntelligenceProjection)
}

func TestRunnerSceneIntelligenceFailureIsFailSoft(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	runner.SetSceneIntelligenceAnalyzer(&stubSceneIntelligenceAnalyzer{err: errors.New("SI worker offline")})
	req := defaultTestRequest()
	runID := "run-scene-intelligence-offline-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing}))
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status)
	require.NotNil(t, final.Result)
	assert.Nil(t, final.Result.SceneIntelligence)
	assert.Nil(t, final.Result.SceneIntelligenceProjection)
}

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
