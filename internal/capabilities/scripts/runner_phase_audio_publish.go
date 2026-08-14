package scriptgeneration

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
)

func (r *Runner) publishFinalAudio(ctx context.Context, runID string, req GenerateRequest, result *GenerateResult) bool {
	if result == nil || result.FinalAudio == nil || r.finalAudioPublisher == nil {
		return true
	}
	if strings.TrimSpace(result.FinalAudio.DriveLink) != "" {
		return true
	}
	lang := req.SourceLanguage
	uploadStarted := time.Now()
	link, err := r.finalAudioPublisher.PublishFinalAudio(ctx, runID, lang, *result.FinalAudio)
	uploadMS := time.Since(uploadStarted).Milliseconds()
	if err != nil || strings.TrimSpace(link) == "" {
		if err == nil {
			err = fmt.Errorf("publisher returned an empty Drive link")
		}
		r.failRunWithRetry(ctx, runID, StagePublishingDocuments, fmt.Errorf("publish final audio: %w", err))
		return false
	}
	if result.AudioMetrics != nil {
		result.AudioMetrics.UploadMS = uploadMS
	}
	result.FinalAudio.DriveLink = strings.TrimSpace(link)
	r.checkpoint(ctx, runID, result)
	if r.log != nil {
		r.log.Info("certified final audio published before documents", zap.String("run_id", runID), zap.String("language", string(lang)))
	}
	return true
}
