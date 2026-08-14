package scriptgeneration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func TestDocumentOverlayRefMapsPublishedFieldsOnly(t *testing.T) {
	result := &GenerateResult{
		RenderJob: &RenderReference{
			JobID:  "render-123",
			Status: "COMPLETED",
			Artifact: &RenderArtifact{
				ID:           "art-1",
				Kind:         "overlay",
				StorageKey:   "/local/overlay/out.mp4",
				URL:          "https://store.example/overlay/out.mp4",
				SHA256:       "abc123",
				MimeType:     "video/mp4",
				DurationUS:   18_200_000,
				ProfileID:    "velox-copy-v1",
				CopyEligible: true,
			},
		},
	}

	ref := documentOverlayRef(result)
	require.NotNil(t, ref)
	require.Equal(t, "art-1", ref.ArtifactID)
	require.Equal(t, "render-123", ref.JobID)
	require.Equal(t, "https://store.example/overlay/out.mp4", ref.URL)
	require.Equal(t, "abc123", ref.SHA256)
	require.Equal(t, int64(18_200_000), ref.DurationUS)
	require.Equal(t, "velox-copy-v1", ref.ProfileID)
	require.True(t, ref.CopyEligible)
}

func TestDocumentOverlayRefNilWhenNoArtifact(t *testing.T) {
	require.Nil(t, documentOverlayRef(nil))
	require.Nil(t, documentOverlayRef(&GenerateResult{}))
	require.Nil(t, documentOverlayRef(&GenerateResult{RenderJob: &RenderReference{JobID: "r"}}))
}

// capturingDocumentRenderer records the DocumentRenderOptions the document
// phase passes so the overlay wiring can be asserted end-to-end.
type capturingDocumentRenderer struct {
	options DocumentRenderOptions
}

func (c *capturingDocumentRenderer) RenderDocument(_ *scriptpkg.ModelScriptOutputV1, opts DocumentRenderOptions) (string, error) {
	c.options = opts
	return "<html></html>", nil
}

func TestDocumentPhasePassesOverlayFromRenderArtifact(t *testing.T) {
	repo := newInMemRunRepository()
	textGen := newStubTextGenerator(defaultTestScenes())
	translator := newStubTranslator()
	voiceoverGen := newStubVoiceoverGenerator()
	docPub := newStubDocumentPublisher()
	renderEnq := newStubRenderEnqueuer()
	renderEnq.ref = RenderReference{
		JobID:  "render-123",
		Status: "COMPLETED",
		Artifact: &RenderArtifact{
			ID: "art-1", URL: "https://store.example/overlay/out.mp4",
			ProfileID: "velox-copy-v1", CopyEligible: true,
		},
	}

	capture := &capturingDocumentRenderer{}
	runner := NewRunner(repo, textGen, translator, voiceoverGen, docPub, renderEnq, capture)
	runner.SetLogger(zap.NewNop())

	req := defaultTestRequest()
	req.Docs = DocumentsConfig{Enabled: true, Languages: []Language{"en"}}
	runID := "run-overlay-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status)

	require.NotNil(t, capture.options.Overlay)
	require.Equal(t, "art-1", capture.options.Overlay.ArtifactID)
	require.Equal(t, "https://store.example/overlay/out.mp4", capture.options.Overlay.URL)
	require.Equal(t, "velox-copy-v1", capture.options.Overlay.ProfileID)
	require.True(t, capture.options.Overlay.CopyEligible)
}
