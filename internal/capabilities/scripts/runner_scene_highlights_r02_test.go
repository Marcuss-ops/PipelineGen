package scriptgeneration

// R02: explicit requested important_phrases survive the new per-scene
// analysis path. The analyzer may add SceneHighlights, but requested hints
// appended to the narration must remain byte-intact in the final scenes.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type stubSceneHighlightAnalyzer struct {
	stubPhraseImpactAnalyzer
	highlights []scriptpkg.SceneHighlight
}

func (s *stubSceneHighlightAnalyzer) AnalyzeScenes(_ context.Context, _ string, _ string, _ []scriptpkg.SceneAnalysisInput) (scriptpkg.PhraseImpactResult, error) {
	if s.err != nil {
		return scriptpkg.PhraseImpactResult{}, s.err
	}
	return scriptpkg.PhraseImpactResult{
		Summary:                  s.result.Summary,
		BulletPoints:             s.result.BulletPoints,
		HeavySentences:           s.result.HeavySentences,
		ChapterManifest:          s.result.ChapterManifest,
		SceneHighlights:          s.highlights,
		SceneHighlightsCertified: true,
		Timings:                  s.result.Timings,
	}, nil
}

func TestRunnerRequestedPhrasesSurviveSceneHighlights_R02(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	title := "First Scene"
	runner.SetPhraseImpactAnalyzer(&stubSceneHighlightAnalyzer{
		stubPhraseImpactAnalyzer: stubPhraseImpactAnalyzer{
			result: scriptpkg.PhraseImpactResult{Summary: "auto summary"},
		},
		highlights: []scriptpkg.SceneHighlight{{
			SceneID: "scene-0", Title: &title, TitleStatus: "resolved",
			TitleSource:     "topic_validated",
			Bullets:         []scriptpkg.SceneBulletSpan{{SentenceStart: 0, SentenceEnd: 1, Text: "Automatic bullet"}},
			GloballyIndexed: false,
		}},
	})

	req := defaultTestRequest()
	req.MediaPlan.Extraction.ImportantPhrases = []string{" locus classicus "}
	runID := "run-r02-requested-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	require.Equal(t, RunStatusCompleted, final.Status)
	require.NotNil(t, final.Result)

	// Requested hint appended to narration, byte-intact.
	found := false
	for _, scene := range final.Result.Scenes {
		if strings.Contains(scene.Text[req.SourceLanguage], "locus classicus") {
			found = true
		}
	}
	assert.True(t, found, "R02: requested phrase must remain in the final scenes")
	// Automatic product attached alongside, not instead.
	require.NotEmpty(t, final.Result.SceneHighlights, "R02: automatic highlights must be attached")
	require.NotNil(t, final.Result.Editorial, "R02: editorial manifest must be sealed")
}
