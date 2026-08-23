package scriptgeneration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// capturingDocumentRenderer records the DocumentRenderOptions passed to it,
// so the docs certification can assert which certified surfaces reach the
// document renderer.
type capturingDocumentRenderer struct {
	options DocumentRenderOptions
}

func (c *capturingDocumentRenderer) RenderDocument(_ *scriptpkg.ModelScriptOutputV1, opts DocumentRenderOptions) (string, error) {
	c.options = opts
	return "<html>captured</html>", nil
}

// TestRunner_AudioOnlyDocumentsReceiveFinalAudio certifies the document
// publication contract for an audio-only run:
//
//   - documents are published only after the final audio is certified
//   - the Google Doc receives FullAudio, FinalAudio and the canonical
//     timeline (AudioTimeline) — all projected from the certified master
//   - no render job and no overlay are required: the document phase must
//     never gate on the video path
func TestRunner_AudioOnlyDocumentsReceiveFinalAudio(t *testing.T) {
	repo := newInMemRunRepository()
	textGen := newStubTextGenerator(defaultTestScenes())
	translator := newStubTranslator()
	voiceoverGen := newStubVoiceoverGenerator()
	docPub := newStubDocumentPublisher()

	capture := &capturingDocumentRenderer{}
	runner := NewRunner(repo, textGen, translator, voiceoverGen, docPub, capture)
	runner.SetLogger(zap.NewNop())
	runner.SetScriptDocsFolderID("test-docs-folder")
	runner.SetCombinedAudioRenderer(&stubCombinedAudioRenderer{})

	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeCombinedTimeline
	req.Source.Type = SourceText
	req.Docs = DocumentsConfig{Enabled: true, Languages: []Language{"en"}}
	runID := "run-audio-only-docs-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status, "audio-only run must complete: %s", final.ErrorMessage)

	res := final.Result
	require.NotNil(t, res, "result must be present")
	require.NotNil(t, res.FinalAudio, "final audio must be certified before documents")
	require.NotNil(t, res.CanonicalTimeline, "canonical timeline must be persisted")

	// The document received the certified audio surfaces.
	require.NotNil(t, capture.options.FinalAudio, "doc must receive FinalAudio")
	require.Equal(t, res.FinalAudio.AssetID, capture.options.FinalAudio.AssetID)
	require.NotNil(t, capture.options.FullAudio, "doc must receive FullAudio")
	require.Equal(t, res.FinalAudio.AssetID, capture.options.FullAudio.AssetID)
	require.NotNil(t, capture.options.AudioTimeline, "doc must receive CanonicalTimeline")

	// No video-path artifact is required or produced.
	require.Nil(t, capture.options.Overlay, "doc overlay must be nil without a render artifact")
}
