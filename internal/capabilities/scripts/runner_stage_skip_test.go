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
//     audio compile still complete.
//   - Docs disabled → StagePublishingDocuments is skipped (no
//     document upserts), the run still completes.
//   - Docs enabled → StagePublishingDocuments runs and publishes
//     the configured documents, the run still completes.
package scriptgeneration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// TestRunner_VoiceoverGeneratorNil_StageSkipped: when voiceoverGen
// is nil, the runner MUST NOT panic and MUST skip the
// StageGeneratingVoiceovers stage. Translation + Docs + audio
// compile still complete normally.
func TestRunner_VoiceoverGeneratorNil_StageSkipped(t *testing.T) {
	repo := newInMemRunRepository()
	textGen := newStubTextGenerator(defaultTestScenes())
	translator := newStubTranslator()
	docPub := newStubDocumentPublisher()

	// No voiceover generator — should be nil-safe.
	runner := NewRunner(repo, textGen, translator, nil, docPub, canonicalTestDocumentRenderer{})
	runner.SetLogger(zap.NewNop())
	runner.SetScriptDocsFolderID("test-docs-folder")

	req := defaultTestRequest()
	// Audio NONE: no voiceover requested, so a nil generator is skipped
	// safely and the NONE audio branch needs no voiceover assets. A
	// CHUNKED_VOICEOVER request without a generator would fail closed at
	// audio compile (ValidateChunkedVoiceovers) by design.
	req.Audio = capabilityaudio.AudioModeNone

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
}

// TestRunner_DocsDisabled_StageSkipped: when Docs.Enabled is
// false, StagePublishingDocuments is skipped AND no document
// upserts happen — the run still completes.
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

	// The run still completes.
}

// TestRunner_DocsEnabled_PublishesDocuments: when docs are explicitly
// enabled, StagePublishingDocuments runs and publishes the configured
// document set; the run completes normally.
func TestRunner_DocsEnabled_PublishesDocuments(t *testing.T) {
	runner, repo, _, _, _, docPub, _ := newTestRunner()
	req := defaultTestRequest()
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
}
