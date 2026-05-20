package googleaccounting

import (
	"fmt"

	"go.uber.org/zap"
	"velox/go-master/internal/config"
	"velox/go-master/internal/logger"
)

func appLogger() (*config.Config, *zap.Logger, func(), error) {
	cfg := config.Get()
	if err := cfg.Validate(); err != nil {
		return nil, nil, nil, fmt.Errorf("invalid configuration: %w", err)
	}

	logger.Init(cfg.GetLogLevel(), cfg.GetLogFormat())
	log := logger.Get()
	return cfg, log, func() { _ = logger.Sync() }, nil
}
