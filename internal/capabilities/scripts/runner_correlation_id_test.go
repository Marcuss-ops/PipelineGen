// Package scriptgeneration — runner_correlation_id_test.go certifies the
// end-to-end correlation key: every artifact-producing operation records
// (job_id via ExecutionContext, scene_id, language, asset_id, operation_id)
// so a question like "why was Spanish Scene 4 not uploaded?" can be answered
// by joining translation → TTS → render → validation → Drive on one key.
package scriptgeneration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/stretchr/testify/require"
)

// findOperation returns the first recorded operation matching the predicate.
func findOperation(ops []ArtifactOperation, match func(ArtifactOperation) bool) *ArtifactOperation {
	for i := range ops {
		if match(ops[i]) {
			return &ops[i]
		}
	}
	return nil
}

// TestRunnerCorrelationID_TranslationAndTTS pins the per-(scene, language)
// correlation on the chunked-voiceover path: every target scene has a
// translation operation and every (scene, language) has a TTS operation whose
// asset_id is the produced voiceover.
func TestRunnerCorrelationID_TranslationAndTTS(t *testing.T) {
	runner, repo, _, _, _, _ := newTestRunner()
	recorder := &recordingExecutionRecorder{}
	runner.SetExecutionRecorder(recorder)

	req := defaultTestRequest() // en + es, 3 scenes, CHUNKED_VOICEOVER
	runID := "run-correlation-chunked-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	exec := ExecutionContext{RootJobID: "root-correlation", JobID: "job-correlation-chunked", CorrelationID: "corr-chunked", Attempt: 1}
	runner.ExecuteWithContext(context.Background(), runID, req, exec)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status)

	// Every recorder call must carry the run's job_id correlation envelope.
	require.NotEmpty(t, recorder.contexts, "recorder must observe execution contexts")
	for _, got := range recorder.contexts {
		require.Equal(t, exec.JobID, got.JobID, "job_id must propagate on every correlation record")
	}

	// Translation: one operation per target scene×language (es only, since en
	// is the source). It carries scene_id + language and no asset (text-only).
	trans := findOperation(recorder.operations, func(op ArtifactOperation) bool {
		return op.Kind == OperationTranslation && op.SceneID == "scene-1" && op.Language == "es"
	})
	require.NotNil(t, trans, "scene-1/es translation operation must be recorded")
	require.Equal(t, "translation:scene-1:es:attempt-1", trans.OperationID)
	require.Empty(t, trans.AssetID, "translation is text-only, no asset")
	require.Equal(t, "COMPLETED", trans.Status)

	// TTS: one operation per (scene, language) whose asset_id is the voiceover.
	tts := findOperation(recorder.operations, func(op ArtifactOperation) bool {
		return op.Kind == OperationTTS && op.SceneID == "scene-1" && op.Language == "es"
	})
	require.NotNil(t, tts, "scene-1/es TTS operation must be recorded")
	require.Equal(t, "tts:scene-1:es:attempt-1", tts.OperationID)
	require.Equal(t, "vo-scene-1-es", tts.AssetID, "TTS operation must carry the produced voiceover asset")
	require.Equal(t, "COMPLETED", tts.Status)

	// Count sanity: 3 target translations, 3×2 voiceovers. Google Docs is
	// retired, so this non-final run publishes no document.
	var translations, ttsCount int
	for _, op := range recorder.operations {
		switch op.Kind {
		case OperationTranslation:
			translations++
		case OperationTTS:
			ttsCount++
		}
	}
	require.Equal(t, 3, translations, "3 target translations")
	require.Equal(t, 6, ttsCount, "3 scenes × 2 languages voiceovers")
}

// TestRunnerCorrelationID_RenderValidationFinalAudio pins the run-scoped
// operations on the combined-timeline path: render and validation both carry
// the produced final_audio asset_id. A non-final run does not publish the
// master to Drive: Google Docs was its old consumer, so the upload would be
// pure wall loss.
func TestRunnerCorrelationID_RenderValidationFinalAudio(t *testing.T) {
	runner, repo, _, _, _, _ := newTestRunner()
	clipPath := t.TempDir() + "/clip.mp4"
	clipBytes := []byte("correlation clip")
	require.NoError(t, os.WriteFile(clipPath, clipBytes, 0o600))
	sum := sha256.Sum256(clipBytes)
	for i := range runner.textGen.(*stubTextGenerator).scenes {
		runner.textGen.(*stubTextGenerator).scenes[i].Clip = &ClipReference{
			ID: "clip-correlation", Path: clipPath, SHA256: hex.EncodeToString(sum[:]), FrameCount: 375, SourceInMS: 0, SourceOutMS: 12500,
		}
	}

	recorder := &recordingExecutionRecorder{}
	runner.SetExecutionRecorder(recorder)
	audioPath := filepath.Join(t.TempDir(), "final-audio.m4a")
	require.NoError(t, os.WriteFile(audioPath, []byte("audio"), 0o600))
	runner.SetCombinedAudioRenderer(lineageAudioRenderer{path: audioPath}) // AssetID "final-audio"
	runner.SetFinalAudioPublisher(&stubFinalAudioPublisher{result: FinalAudioPublishResult{
		AssetID: "final-audio-published", DriveFileID: "final-audio", DriveLink: "https://drive.google.com/file/d/final-audio",
	}})

	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeCombinedTimeline
	runID := "run-correlation-render-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	exec := ExecutionContext{RootJobID: "root-correlation", JobID: "job-correlation-render", CorrelationID: "corr-render", Attempt: 1}
	runner.ExecuteWithContext(context.Background(), runID, req, exec)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status)

	render := findOperation(recorder.operations, func(op ArtifactOperation) bool {
		return op.Kind == OperationRender
	})
	require.NotNil(t, render, "render operation must be recorded")
	require.Equal(t, "final-audio", render.AssetID, "render operation must carry the produced final audio")
	require.Equal(t, Language("en"), render.Language)

	validation := findOperation(recorder.operations, func(op ArtifactOperation) bool {
		return op.Kind == OperationValidation
	})
	require.NotNil(t, validation, "validation operation must be recorded")
	require.Equal(t, "final-audio", validation.AssetID, "validation must reference the same certified master")

	require.Nil(t, findOperation(recorder.operations, func(op ArtifactOperation) bool {
		return op.Kind == OperationDriveUpload
	}), "a non-final run must not publish the final audio master to Drive")
}
