package wiring

import (
	scriptwiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/script"
	documentadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	"go.uber.org/zap"
)

type scriptGenerationPersistence = scriptwiring.GenerationPersistence

func newScriptGenerationPersistence(repo documentadapters.ScriptRepository, log *zap.Logger) *scriptGenerationPersistence {
	return scriptwiring.NewGenerationPersistence(repo, log)
}
