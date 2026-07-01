// Package main — admin CLI for one-shot operator tasks
// (stock-reset, summarize-book, backfill-monitored-sources-to-category-channels, ...).
// logger.go centralises the (cfg, log, cleanup) tuple that every
// command needs up front.
package main

import (
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// appLogger loads the production config and a zap production logger;
// returns (cfg, log, cleanup, err). Used by admin commands that need the
// full config (cleanup, list-drive-folder,
// reset-video-ai, verify-artlist-pipeline).
//
// The cleanup callback is safe to call multiple times — zap.Sync is
// returns-nil-on-already-flushed, so an extra defer cleanup is harmless
// during exit.
//
// NOTE: there used to be a `velox/go-master/internal/config` import
// here (pre-Upstream commit 66c646b5 the handler migrated here under
// that path). It was wrong from the start of the PipelineGen module:
// velox/go-master/internal/config is a stdlib-shaped path that Go
// resolves to $GOROOT/src/velox/go-master/internal/config, not to any
// repo package. The canonical local config lives at
// internal/platform/config, which provides `func Get() *Config`
// — the right replacement.
func appLogger() (*config.Config, *zap.Logger, func(), error) {
	cfg, err := config.Get()
	if err != nil {
		return nil, nil, func() {}, err
	}
	log, err := zap.NewProduction()
	if err != nil {
		return nil, nil, func() {}, err
	}
	cleanup := func() { _ = log.Sync() }
	return cfg, log, cleanup, nil
}

// productionLogger returns a zap production logger without loading the
// global config. Kept as a helper for future admin commands that want
// to read config directly via config.Get() inside their command body
// without threading it through the helper signature (the upload-t5pre
// command that used this helper was deleted in Wave A commit 1, June
// 2026 — productionLogger remains for zero-config audit utilities
// that may be reintroduced).
func productionLogger() (*zap.Logger, func(), error) {
	log, err := zap.NewProduction()
	if err != nil {
		return nil, func() {}, err
	}
	cleanup := func() { _ = log.Sync() }
	return log, cleanup, nil
}
