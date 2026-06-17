package handlers

import (
	"context"
	"strings"

	"go.uber.org/zap"
)

func (h *ScriptFlowHandler) maybeCreateGoogleDoc(ctx context.Context, title, content string, folderID string, createDoc bool) (string, string) {
	if !createDoc || h.docClient == nil {
		return "", ""
	}
	effectiveFolderID := strings.TrimSpace(folderID)
	if effectiveFolderID == "" {
		effectiveFolderID = strings.TrimSpace(h.driveFolderID)
	}
	effectiveTitle := strings.TrimSpace(title)
	if effectiveTitle == "" {
		effectiveTitle = "Generated Script"
	}
	if h.log != nil {
		h.log.Info("creating Google Doc for generated script",
			zap.String("title", effectiveTitle),
			zap.String("folder_id", effectiveFolderID),
			zap.Int("content_chars", len(content)),
		)
	}
	saveCtx, cancel := withPostWriteContext(ctx, h.log, "create Google Doc (text mode)")
	defer cancel()
	doc, docErr := h.docClient.CreateDoc(saveCtx, effectiveTitle, content, effectiveFolderID)
	if docErr != nil {
		if h.log != nil {
			h.log.Warn("failed to create Google Doc",
				zap.String("title", effectiveTitle),
				zap.String("folder_id", effectiveFolderID),
				zap.Error(docErr),
			)
		}
		return "", ""
	}
	if h.log != nil {
		h.log.Info("Google Doc created",
			zap.String("title", effectiveTitle),
			zap.String("doc_id", doc.ID),
			zap.String("url", doc.URL),
			zap.String("folder_id", effectiveFolderID),
		)
	}
	return doc.URL, doc.ID
}
