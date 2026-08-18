package scriptgeneration

import (
	"context"
	"fmt"
	"strings"
	"time"

	kernelscript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

func (r *Runner) publishFinalAudio(ctx context.Context, runID string, req GenerateRequest, routing kernelscript.ArtifactRoutingContext, exec ExecutionContext, result *GenerateResult) bool {
	if result == nil || result.FinalAudio == nil || r.finalAudioPublisher == nil {
		return true
	}
	if strings.TrimSpace(result.FinalAudio.DriveLink) != "" {
		return true
	}
	lang := req.SourceLanguage
	uploadStarted := time.Now()
	published, err := r.finalAudioPublisher.PublishFinalAudio(ctx, runID, lang, *result.FinalAudio, routing.VoiceoverFolderID)
	uploadMS := time.Since(uploadStarted).Milliseconds()
	if err != nil || strings.TrimSpace(published.DriveLink) == "" || strings.TrimSpace(published.AssetID) == "" {
		if err == nil {
			err = fmt.Errorf("publisher returned an empty Drive link or canonical asset ID")
		}
		r.failRunWithRetry(ctx, runID, StagePublishingDocuments, fmt.Errorf("publish final audio: %w", err))
		return false
	}
	if result.AudioMetrics != nil {
		result.AudioMetrics.UploadMS = uploadMS
	}
	r.recordAudioOperation(ctx, "upload", "drive", uploadMS)
	result.FinalAudio.AssetID = strings.TrimSpace(published.AssetID)
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
