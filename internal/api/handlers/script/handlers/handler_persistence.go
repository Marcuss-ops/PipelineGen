package handlers

import (
	"context"

	"velox/go-master/internal/api/handlers/script"
)

// saveScriptToDB saves the generated script to the database using the persistence service
func (h *ScriptDocsHandler) saveScriptToDB(ctx context.Context, req script.ScriptDocsRequest, document *script.ScriptDocument) {
	if h.persistSvc == nil {
		return
	}

	var modelName, baseURL string
	if h.generator != nil && h.generator.GetClient() != nil {
		modelName = h.generator.GetClient().Model()
		baseURL = h.generator.GetClient().BaseURL()
	}

	_, _ = h.persistSvc.SaveScriptToDB(ctx, req, document, modelName, baseURL)
}

// triggerBackgroundHarvest enqueues jobs for background harvesting using the persistence service
func (h *ScriptDocsHandler) triggerBackgroundHarvest(ctx context.Context, document *script.ScriptDocument) {
	if h.persistSvc == nil {
		return
	}
	h.persistSvc.TriggerBackgroundHarvest(ctx, document)
}
