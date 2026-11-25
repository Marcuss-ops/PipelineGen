package scriptgeneration

import (
	runport "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports/run"
	portscript "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports/script"
)

// Compatibility facade: the application ports moved to scripts/ports/script.
// Root call sites keep the historical names.
type (
	TextGenerator           = portscript.TextGenerator
	SceneTextRepairer       = portscript.SceneTextRepairer
	SceneTextTraceGenerator = portscript.SceneTextTraceGenerator
	SceneTextStreamer       = portscript.SceneTextStreamer
	SceneTextTraceStreamer  = portscript.SceneTextTraceStreamer
	ScriptPersistenceInput  = portscript.ScriptPersistenceInput
	ScriptPersistence       = portscript.ScriptPersistence
	TranslationInput        = portscript.TranslationInput
	Translator              = portscript.Translator
	OverlayBackgroundAsset  = portscript.OverlayBackgroundAsset
	OverlayBackgroundSource = portscript.OverlayBackgroundSource
	VoiceoverInput          = portscript.VoiceoverInput
	VoiceoverGenerator      = portscript.VoiceoverGenerator
)

// Durable run DTOs and interfaces remain owned by the thin run port.
type (
	FailRunInput                = runport.FailRunInput
	RunRepository               = runport.RunRepository
	StaleSubmissionRun          = runport.StaleSubmissionRun
	RunReconciliationRepository = runport.RunReconciliationRepository
)

var ErrRunNotFound = runport.ErrRunNotFound
