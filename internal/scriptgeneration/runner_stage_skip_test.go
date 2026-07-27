// Package scriptgeneration — runner_stage_skip_test.go covers the
// stage-skipping contract: when an optional port is nil OR a
// toggle in the request is false, the corresponding stage MUST be
// skipped (not panic, not attempt the call) AND the rest of the
// pipeline MUST still complete.
//
// godlike/06 SSOT invariants asserted:
//
//   - VoiceoverGenerator nil → StageGeneratingVoiceovers is
//     skipped (no AudioReference on scenes), translation and
//     render still complete.
//   - Docs disabled → StagePublishingDocuments is skipped (no
//     document upserts), render still completes.
//   - RenderVideo=false → Stages BuildingRenderPayload +
//     EnqueuingRender are skipped (no RenderJob), docs still
//     complete (when explicitly enabled).
package scriptgeneration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestRunner_VoiceoverGeneratorNil_StageSkipped: when voiceoverGen
// is nil, the runner MUST NOT panic and MUST skip the
// StageGeneratingVoiceovers stage. Translation + Docs + Render
// still complete normally.
func TestRunner_VoiceoverGeneratorNil_StageSkipped(t *testing.T) {
	repo := newInMemRunRepository()
	textGen := newStubTextGenerator(defaultTestScenes())
	translator := newStubTranslator()
	docPub := newStubDocumentPublisher()
	renderEnq := newStubRenderEnqueuer()

	// No voiceover generator — should be nil-safe.
	runner := NewRunner(repo, textGen, translator, nil, docPub, renderEnq)
	runner.SetLogger(zap.NewNop())

	req := defaultTestRequest()

	runID := "run-novo-001"
	err := repo.Create(context.Background(), &GenerationRun{
		ID:           runID,
		Request:      req,
		Status:       RunStatusPending,
		CurrentStage: StageNormalizing,
	})
	require.NoError(t, err)

	runner.Execute(context.Background(), runID, req)

	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	assert.Equal(t, RunStatusCompleted, final.Status, "run should complete even without voiceover")
	assert.Equal(t, StageCompleted, final.CurrentStage)

	// Voiceover stage should be skipped — no AudioReference on scenes.
	for i, s := range final.Result.Scenes {
		assert.Empty(t, s.Voiceover, "scene %d should have no voiceover when generator is nil", i)
	}

	// Other stages should complete normally.
	assert.NotEmpty(t, final.Result.Scenes[0].Text["es"], "translation should still work")
	assert.NotNil(t, final.Result.RenderJob, "render should still be enqueued")
}

// TestRunner_DocsDisabled_StageSkipped: when Docs.Enabled is
// false, StagePublishingDocuments is skipped AND no document
// upserts happen — render still completes if RenderVideo=true.
func TestRunner_DocsDisabled_StageSkipped(t *testing.T) {
	runner, repo, _, _, _, docPub, _ := newTestRunner()
	req := defaultTestRequest()
	req.Docs = DocumentsConfig{Enabled: false} // explicitly disabled
	req.DocsEnabled = false                    // also deprecated field

	runID := "run-nodocs-001"
	err := repo.Create(context.Background(), &GenerationRun{
		ID:           runID,
		Request:      req,
		Status:       RunStatusPending,
		CurrentStage: StageNormalizing,
	})
	require.NoError(t, err)

	runner.Execute(context.Background(), runID, req)

	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	assert.Equal(t, RunStatusCompleted, final.Status)

	// No docs published.
	assert.Equal(t, 0, len(docPub.records), "no docs should be created when disabled")

	// Render still works.
	require.NotNil(t, final.Result.RenderJob)
}

// TestRunner_RenderVideoFalse_Skipped: when RenderVideo is false,
// Stages BuildingRenderPayload + EnqueuingRender are skipped and
// no RenderJob is set. Docs (when explicitly enabled) still
// complete normally.
func TestRunner_RenderVideoFalse_Skipped(t *testing.T) {
	runner, repo, _, _, _, docPub, renderEnq := newTestRunner()
	req := defaultTestRequest()
	req.RenderVideo = false
	req.Docs = DocumentsConfig{Enabled: true, Languages: []Language{"en"}}

	runID := "run-norender-001"
	err := repo.Create(context.Background(), &GenerationRun{
		ID:           runID,
		Request:      req,
		Status:       RunStatusPending,
		CurrentStage: StageNormalizing,
	})
	require.NoError(t, err)

	runner.Execute(context.Background(), runID, req)

	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	assert.Equal(t, RunStatusCompleted, final.Status)

	// Docs published (explicitly enabled).
	assert.Equal(t, 1, len(docPub.records), "one doc should be created")

	// Render NOT enqueued.
	assert.Nil(t, final.Result.RenderJob, "render job should be nil when render_video is false")
	assert.Equal(t, 0, renderEnq.callCount, "renderEnq should not be called")
}
