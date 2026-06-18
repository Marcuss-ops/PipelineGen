package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	logger "github.com/Marcuss-ops/PipelineGen/internal/platform/logging"
)

// appLogger initializes the application config and logger for admin commands.
func appLogger() (*config.Config, *zap.Logger, func(), error) {
	cfg := config.Get()
	if err := cfg.Validate(); err != nil {
		return nil, nil, nil, fmt.Errorf("invalid configuration: %w", err)
	}

	logger.Init(cfg.GetLogLevel(), cfg.GetLogFormat())
	log := logger.Get()
	return cfg, log, func() { _ = logger.Sync() }, nil
}

// isFilePath checks whether a path string looks like a file path (has a media extension).
func isFilePath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".mp4", ".mkv", ".mov", ".avi", ".mp3", ".wav", ".txt", ".json", ".jpg", ".png", ".jpeg":
		return true
	}
	return false
}
