package scriptgeneration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	kernelscript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// runner_phase_audio_finalize.go owns the tail of the audio boundary: the
// canonical EditingTimelineV1 projection, closing the AUDIO_COMPILE execution
// step, and publishing the certified final audio.
//
// The finalize phase runs AFTER the overlay render because the editing
// timeline's overlay span carries the certified artifact (its SHA, Drive link
// and media contract): the timeline is a projection over frozen facts, and the
// render's certified output is one of them. Sequenced before the render it
// would publish a timeline with an empty overlay span.
//
// It is a separate measured stage from audio_compile for the same reason the
// render is: audio_compile must report only the work it owns.

// runAudioFinalizePhase projects the editing timeline from the frozen result and
// closes the execution step the compile phase opened.
func (r *Runner) runAudioFinalizePhase(ctx context.Context, runID string, exec ExecutionContext, state audioCompileState, result *GenerateResult) bool {
	if state.AudioSkipped {
		if err := r.skipExecutionStep(ctx, exec, state.Step); err != nil {
			r.failRunWithRetry(ctx, runID, StageCompilingAudio, err)
			return false
		}
		return true
	}
	if result != nil {
		// The canonical EditingTimelineV1 is built from frozen facts. It is the
		// single projection consumed by downstream editing; no component
		// maintains a second independently calculated timeline.
		if et, err := BuildEditingTimeline(result); err != nil {
			cause := fmt.Errorf("editing timeline compilation failed: %w", err)
			r.failExecutionStepBestEffort(ctx, exec, state.Step, cause)
			r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
			return false
		} else if et != nil {
			result.EditingTimeline = et
		}
		r.log.Info("audio compile complete",
			zap.String("run_id", runID),
			zap.String("audio_mode", string(result.AudioMode)),
		)
	}
	r.checkpoint(ctx, runID, result)
	if err := r.completeExecutionStep(ctx, exec, state.Step); err != nil {
		r.failExecutionStepBestEffort(ctx, exec, state.Step, err)
		r.failRunWithRetry(ctx, runID, StageCompilingAudio, err)
		return false
	}
	return true
}

// publishFinalAudioNeeded reports whether this run has any consumer of the
// Drive-published master audio. The documents catalog renders the final audio
// link into the published document (DocumentRenderOptions.FinalAudio and the
// remote assembly payload), and the final_job payload references the asset ID
// for the remote render. A run with neither consumer — overlay-only: documents
// disabled and no final job — has no reader for the uploaded m4a, so the
// measured Drive upload is pure wall loss and must be skipped. The predicate
// mirrors the documentSkipped gate in runDocumentPhase so "we would have
// skipped the docs" and "we skip the audio publish" are always the same
// decision.
func publishFinalAudioNeeded(req GenerateRequest, r *Runner) bool {
	if req.FinalJob {
		return true
	}
	docsEnabled, docsLangs, _ := req.ResolveDocsConfig()
	return r.docPublisher != nil && docsEnabled && len(docsLangs) > 0
}

func (r *Runner) publishFinalAudio(ctx context.Context, runID string, req GenerateRequest, routing kernelscript.ArtifactRoutingContext, exec ExecutionContext, result *GenerateResult) bool {
	if result == nil || result.FinalAudio == nil || r.finalAudioPublisher == nil {
		return true
	}
	if strings.TrimSpace(result.FinalAudio.AssetID) != "" && strings.TrimSpace(result.FinalAudio.DriveLink) != "" && strings.TrimSpace(result.FinalAudio.DriveFileID) != "" {
		return true
	}
	if _, err := os.Stat(result.FinalAudio.Path); errors.Is(err, os.ErrNotExist) {
		cause := fmt.Errorf("publish final audio: %w: %s", ErrFinalAudioLocalArtifactMissing, result.FinalAudio.Path)
		r.failRunWithRetry(ctx, runID, StagePublishingDocuments, cause)
		return false
	}
	lang := req.SourceLanguage
	result.FinalAudio.Filename = audioOutputFilename(req.OutputName, lang)
	var published FinalAudioPublishResult
	// The upload is measured under its own stage, not under the audio compile
	// stage: publishing is IO against Drive, and charging it to the audio stage
	// made drive.upload that stage's reported dominant operation.
	err := kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
		Stage: StageAudioPublish, Component: "drive", Operation: "upload", Provider: "drive",
	}, func(measureCtx context.Context) error {
		var err error
		published, err = r.finalAudioPublisher.PublishFinalAudio(measureCtx, runID, lang, *result.FinalAudio, routing.VoiceoverFolderID)
		return err
	})
	var uploadMS int64
	if run := kernobs.FromContext(ctx); run != nil {
		uploadMS = kernobs.SummarizeOperations(run.Report(), string(StageAudioPublish), "upload").TotalMs
	}
	if err != nil || strings.TrimSpace(published.DriveLink) == "" || strings.TrimSpace(published.AssetID) == "" || strings.TrimSpace(published.DriveFileID) == "" {
		if err == nil {
			err = fmt.Errorf("publisher returned an empty Drive file ID, Drive link, or canonical asset ID")
		}
		r.failRunWithRetry(ctx, runID, StagePublishingDocuments, fmt.Errorf("publish final audio: %w", err))
		return false
	}
	if result.AudioMetrics != nil {
		result.AudioMetrics.UploadMS = uploadMS
	}
	result.FinalAudio.AssetID = strings.TrimSpace(published.AssetID)
	result.FinalAudio.DriveFileID = strings.TrimSpace(published.DriveFileID)
	result.FinalAudio.DriveLink = strings.TrimSpace(published.DriveLink)
	// Drive correlation: the published final_audio asset is traceable to its
	// upload via (language, asset_id) = (source language, published.AssetID).
	if err := r.recordArtifactOperation(ctx, exec, ArtifactOperation{
		OperationID: artifactOperationID(exec.Attempt, OperationDriveUpload, "final_audio", string(lang)),
		Kind:        OperationDriveUpload,
		Language:    lang,
		AssetID:     strings.TrimSpace(published.AssetID),
		Status:      "COMPLETED",
	}); err != nil {
		r.failRunWithRetry(ctx, runID, StagePublishingDocuments, err)
		return false
	}
	r.checkpoint(ctx, runID, result)
	if r.log != nil {
		r.log.Info("certified final audio published before documents", zap.String("run_id", runID), zap.String("language", string(lang)))
	}
	return true
}

func audioOutputFilename(outputName string, language Language) string {
	name := strings.TrimSpace(outputName)
	if name == "" {
		name = "voiceover"
	}
	name = strings.NewReplacer("/", "-", "\\", "-", ":", "-", "\n", " ", "\r", " ").Replace(name)
	name = strings.Join(strings.Fields(name), " ")
	return fmt.Sprintf("%s [%s].m4a", name, strings.TrimSpace(string(language)))
}
