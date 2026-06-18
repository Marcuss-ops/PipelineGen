package handlers

import (
	"context"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/media/voiceover"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"

	"go.uber.org/zap"
)

// ── Phase: Google Doc Creation ───────────────────────────────────────────────

// createBatchDoc creates a Google Doc for the batch and returns its URL and ID.
func (h *ScriptFlowHandler) createBatchDoc(ctx context.Context, docTitle string, generatedParts []generatedPart, noChapters bool, language string, folderID string) (string, string) {
	if h.docClient == nil {
		return "", ""
	}

	htmlContent := buildBatchGoogleDocHTML(docTitle, generatedParts, noChapters, language)
	doc, docErr := h.docClient.CreateDoc(ctx, docTitle, htmlContent, folderID)
	if docErr != nil {
		h.log.Warn("failed to create batch Google Doc", zap.Error(docErr), zap.String("folder_id", folderID))
		return "", ""
	}
	return doc.URL, doc.ID
}

// ── Phase: Async Voiceover ───────────────────────────────────────────────────

// spawnBatchVoiceover spawns a fire-and-forget goroutine for async voiceover generation.
// Uses context.WithoutCancel(ctx) to survive the handler's return (intentional).
func (h *ScriptFlowHandler) spawnBatchVoiceover(ctx context.Context, script, lang, docTitle, folderID, filename string) {
	destReq := &voiceover.DestinationRequest{
		FolderID:        folderID,
		Group:           "explainatory",
		SubfolderName:   docTitle,
		CreateSubfolder: true,
	}
	destCopy := *destReq
	go func(ts, lc, fn string, d *voiceover.DestinationRequest) {
		defer func() {
			if r := recover(); r != nil {
				h.log.Error("panic in async voiceover goroutine", zap.Any("recover", r), zap.String("lang", lc))
			}
		}()
		// Background voiceover generation: derive a fresh context from the
		// caller-supplied ctx (without cancellation, so it survives the HTTP
		// response) plus a 30-minute hard deadline to prevent unbounded
		// goroutines if the voiceover backend hangs.
		voCtx, voCancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Minute)
		defer voCancel()
		voRes, voErr := h.voService.GenerateWithDestination(voCtx, textutil.CleanForVoiceover(ts), lc, fn, d)
		if voErr != nil {
			h.log.Error("batch generation: async voiceover failed for language", zap.String("lang", lc), zap.Error(voErr))
		} else if voRes != nil {
			h.log.Info("batch generation: async voiceover completed for language", zap.String("lang", lc), zap.String("drive_link", voRes.DriveLink))
		}
	}(script, lang, filename, &destCopy)
}
