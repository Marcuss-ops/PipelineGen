// Package app — wire_script_postprocess_document.go: retired document processor registration.
//
// Sprint 1.0 retired the inline Google-Doc creation processor; document
// generation is now produced by the downstream document.generate job.
// This stub satisfies the call site in registerScriptPostProcessors.
package app

import (
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	"go.uber.org/zap"
)

// registerDocumentProcessor registers the retired no-op DocumentProcessor
// so legacy composition wiring compiles. The processor always returns
// an empty result.
func registerDocumentProcessor(ppReg *adapters.PostProcessorRegistry, _ *ComposeRoot, _ interface{}, log *zap.Logger) error {
	if !ppReg.Register(adapters.NewDocumentProcessor(nil, log)) {
		return nil
	}
	return nil
}
