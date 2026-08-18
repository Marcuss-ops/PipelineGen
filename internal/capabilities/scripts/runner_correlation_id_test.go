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

// TestRunnerCorrelationID_TranslationTTSDriveUpload pins the per-(scene,
// language) correlation on the chunked-voiceover path: every target scene has
// a translation operation, every (scene, language) has a TTS operation whose
// asset_id is the produced voiceover, and every docs language has a
// drive_upload operation whose asset_id is the published document.
func TestRunnerCorrelationID_TranslationTTSDriveUpload(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	recorder := &recordingExecutionRecorder{}
	runner.SetExecutionRecorder(recorder)

	req := defaultTestRequest() // en + es, 3 scenes, CHUNKED_VOICEOVER + docs
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

	// Drive: one operation per docs language whose asset_id is the document.
	doc := findOperation(recorder.operations, func(op ArtifactOperation) bool {
		return op.Kind == OperationDriveUpload && op.SceneID == "" && op.Language == "es"
	})
	require.NotNil(t, doc, "es document drive_upload operation must be recorded")
	require.Equal(t, "doc-abc-123", doc.AssetID, "drive_upload must carry the published document id")

	// Count sanity: 3 target translations, 3×2 voiceovers, 2 documents.
	var translations, ttsCount, driveUploads int
	for _, op := range recorder.operations {
		switch op.Kind {
		case OperationTranslation:
			translations++
		case OperationTTS:
			ttsCount++
		case OperationDriveUpload:
			driveUploads++
		}
	}
	require.Equal(t, 3, translations, "3 target translations")
	require.Equal(t, 6, ttsCount, "3 scenes × 2 languages voiceovers")
	require.Equal(t, 2, driveUploads, "2 documents (en + es)")
}

// TestRunnerCorrelationID_RenderValidationFinalAudioDrive pins the run-scoped
// operations on the combined-timeline path: render and validation both carry
// the produced final_audio asset_id, and the final-audio Drive upload carries
// the published canonical asset_id — completing the render → validation →
// Drive segment of the same correlation chain.
func TestRunnerCorrelationID_RenderValidationFinalAudioDrive(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
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
	runner.SetCombinedAudioRenderer(lineageAudioRenderer{}) // AssetID "final-audio"
	runner.SetFinalAudioPublisher(&stubFinalAudioPublisher{result: FinalAudioPublishResult{
		AssetID: "final-audio-published", DriveLink: "https://drive.google.com/file/d/final-audio",
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

	drive := findOperation(recorder.operations, func(op ArtifactOperation) bool {
		return op.Kind == OperationDriveUpload && op.AssetID == "final-audio-published"
	})
	require.NotNil(t, drive, "final-audio drive_upload operation must be recorded")
	require.Equal(t, Language("en"), drive.Language, "final-audio drive_upload carries the source language")
}
