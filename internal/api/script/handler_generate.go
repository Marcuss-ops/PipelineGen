// Package script — handler_generate.go is retained as a package-doc
// sentinel. The canonical Generate method lives on HandlerGenerate
// (handler_generate_handler.go), extracted from the 22-field
// ScriptFlowHandler per AZIONE 1 (July 2026).
//
// FASE 2 (July 2026): the pre-FASE-2 package-level enqueueEnvelopeFn
// is REMOVED (handler_enqueue.go is gone). HandlerGenerate now owns
// the canonical /generate path end-to-end through the
// generationSubmitter interface (handler_deps.go) which delegates to
// opsapp.GenerationSubmissionService.
//
// This file intentionally carries ZERO declarations, ZERO logic.
package script
