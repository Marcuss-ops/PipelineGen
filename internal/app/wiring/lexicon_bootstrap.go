package wiring

import (
	scriptwiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

func initLinguistics(cfg *config.Config, log *zap.Logger) error {
	return scriptwiring.InitLinguistics(cfg, log)
}
