package wiring

import (
	scriptwiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/script"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase/gencore"
	"go.uber.org/zap"
)

// SceneTextGenerator remains a composition-root facade over the script
// subsystem implementation.
type SceneTextGenerator = scriptwiring.SceneTextGenerator

type ClipAssetResolver = scriptwiring.ClipAssetResolver
type ClipProber = scriptwiring.ClipProber

// NewSceneTextGenerator preserves the wiring package constructor while the
// implementation and its planning helpers live with the script subsystem.
func NewSceneTextGenerator(engine *gencore.Engine, log *zap.Logger) *SceneTextGenerator {
	return scriptwiring.NewSceneTextGenerator(engine, log)
}
