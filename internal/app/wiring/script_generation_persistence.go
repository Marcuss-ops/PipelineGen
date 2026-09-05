package wiring

import (
	scriptwiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/script"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"go.uber.org/zap"
)

type scriptGenerationPersistence = scriptwiring.GenerationPersistence

func newScriptGenerationPersistence(repo scriptports.ScriptRepository, log *zap.Logger) *scriptGenerationPersistence {
	return scriptwiring.NewGenerationPersistence(repo, log)
}
