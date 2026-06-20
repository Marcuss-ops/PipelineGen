package main

import (
	"go.uber.org/zap"

	"velox/go-master/internal/config"
)

// appLogger loads the production config and a zap production logger;
// returns (cfg, log, cleanup, err). Used by admin commands that need the
// full config (cleanup, list-drive-folder, backfill-artlist-media-type,
// reset-video-ai, verify-artlist-pipeline).
//
// The cleanup callback is safe to call multiple times — zap.Sync is
// returns-nil-on-already-flushed, so an extra defer cleanup is harmless
// during exit.
func appLogger() (*config.Config, *zap.Logger, func(), error) {
	cfg := config.Get()
	log, err := zap.NewProduction()
	if err != nil {
		return nil, nil, func() {}, err
	}
	cleanup := func() { _ = log.Sync() }
	return cfg, log, cleanup, nil
}

// productionLogger returns a zap production logger without loading the
// global config. Used by upload-t5pre, which reads config directly via
// config.Get() inside its command body (no need to thread it through
// the helper signature).
func productionLogger() (*zap.Logger, func(), error) {
	log, err := zap.NewProduction()
	if err != nil {
		return nil, func() {}, err
	}
	cleanup := func() { _ = log.Sync() }
	return log, cleanup, nil
}
