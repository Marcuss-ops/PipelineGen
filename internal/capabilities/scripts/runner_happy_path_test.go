// Package scriptgeneration — runner_happy_path_test.go covers the
// canonical end-to-end success path: all stages (Normalize →
// SceneText → Translate → Voiceover → AudioCompile → PublishDocs)
// MUST complete cleanly, the run MUST reach RunStatusCompleted +
// StageCompleted, and every materialized artifact (scenes.text,
// scenes.voiceover, documents, final audio) MUST be present.
//
// godlike/06 SSOT invariants asserted:
//
//   - RUN_STATUS_COMPLETED is reached when all stages complete
//   - StageCompleted is the final CurrentStage
//   - Scene-level translation (ES) and voiceover (EN, ES) are
//     both present after a single happy-path run
//   - DocumentPublisher UpsertDocument called exactly len(Docs.Languages)
//     times (here 2: EN + ES)
//   - Output.Text and WordCount are projected from the ordered scenes
package scriptgeneration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunner_HappyPath_AllStagesComplete is the canonical entry-point
// for the durable generation workflow. Pre-Fase 5 it lived in
// runner_test.go as the longest single test in the package.
func TestRunner_HappyPath_AllStagesComplete(t *testing.T) {
	runner, repo, _, _, _, docPub, _ := newTestRunner()
	req := defaultTestRequest()
	// Compile the canonical audio timeline alongside the chunked voiceovers so
	// the happy path exercises the timeline projection (audio/timeline-only).
	req.GenerateTimeline = true

	// Execute the run.
	runID := "run-happy-001"
	err := repo.Create(context.Background(), &GenerationRun{
		ID:           runID,
		Request:      req,
		Status:       RunStatusPending,
		CurrentStage: StageNormalizing,
	})
	require.NoError(t, err, "Create should succeed")

	runner.Execute(context.Background(), runID, req)

	// Wait for completion.
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final, "final run should not be nil")

	// Assert terminal status.
	assert.Equal(t, RunStatusCompleted, final.Status, "run should complete")
	assert.Equal(t, StageCompleted, final.CurrentStage, "final stage should be COMPLETED")

	// Assert result has all scenes.
	require.NotNil(t, final.Result, "result should not be nil")
	assert.Len(t, final.Result.Scenes, 3, "should have 3 scenes")

	// Assert EN text preserved.
	assert.Equal(t, "First scene text", final.Result.Scenes[0].Text["en"])

	// Assert ES translation present (scene-level checkpoint).
	for i, s := range final.Result.Scenes {
		assert.NotEmpty(t, s.Text["es"], "scene %d should have ES text", i)
	}

	// Assert voiceovers generated for EN and ES.
	for i, s := range final.Result.Scenes {
		assert.NotEmpty(t, s.Voiceover["en"].ID, "scene %d should have EN voiceover", i)
		assert.NotEmpty(t, s.Voiceover["es"].ID, "scene %d should have ES voiceover", i)
	}

	// Assert documents published.
	require.NotNil(t, final.Result.Documents, "documents should be published")
	assert.Equal(t, 2, len(docPub.records), "should have 2 doc upsert calls (EN + ES)")
	for _, record := range docPub.records {
		assert.Contains(t, record.Content, "<h2>Scene 1</h2>")
		assert.Contains(t, record.Content, "<h2>SpecScene JSON</h2>")
		assert.Contains(t, record.Content, "<pre><code>")
		if record.Language == "en" {
			assert.Contains(t, record.Content, "First scene text")
			assert.NotContains(t, record.Content, "[TRANSLATED]")
		}
		if record.Language == "es" {
			assert.Contains(t, record.Content, "[TRANSLATED] First scene text")
			assert.NotContains(t, record.Content, "Voiceover: https://drive.google.com/VOICE-EN")
		}
	}

	// Assert the canonical timeline is persisted (audio/timeline-only).
	require.NotNil(t, final.Result.CanonicalTimeline, "canonical timeline should be persisted")

	assert.Equal(t, "First scene text\n\nSecond scene text\n\nThird scene text", final.Result.Output.Text)
	assert.Equal(t, 9, final.Result.WordCount, "word count should be projected from generated text")
	assert.Equal(t, final.Result.WordCount, final.Result.Output.WordCount)
}
