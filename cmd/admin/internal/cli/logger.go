// Package cli is the non-main utility package for cmd/admin. It hosts
// the cross-cutting helpers (logger, context, flag parsers) that
// every admin subcommand needs, extracted from `package main` so that
// subpackages (cmd/admin/reconcile, future cmd/admin/commands etc.)
// can import them. Go's `internal/` visibility rule allows any
// package rooted under cmd/admin to import this package — packages
// outside cmd/admin cannot.
//
// Created in PR-PKG-SIZE-CMD-ADMIN-1 (July 2026) to satisfy the
// pkg_size archcheck rule (max_files_per_package=65) by moving
// reconcile/reindex/dr subcommands into cmd/admin/reconcile/. See
// architecture/issues.yaml PR-PKG-SIZE-CMD-ADMIN-1 for the migration
// plan + follow-up tracking.
package cli

import (
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// AppLogger loads the production config and a zap production logger;
// returns (cfg, log, cleanup, err). Used by admin commands that need
// the full config (cleanup, list-drive-folder, reset-video-ai,
// verify-artlist-pipeline).
//
// The cleanup callback is safe to call multiple times — zap.Sync is
// returns-nil-on-already-flushed, so an extra defer cleanup is
// harmless during exit.
//
// Canonical replacement for the former `appLogger()` in
// cmd/admin/logger.go (PR-PKG-SIZE-CMD-ADMIN-1 extraction). The
// lowercase symbol was renamed to AppLogger to satisfy Go's
// exported-symbol rule for cross-package import.
//
// NOTE: there used to be a `velox/go-master/internal/config` import
// here (pre-Upstream commit 66c646b5 the handler migrated here under
// that path). It was wrong from the start of the PipelineGen module:
// velox/go-master/internal/config is a stdlib-shaped path that Go
// resolves to $GOROOT/src/velox/go-master/internal/config, not to any
// repo package. The canonical local config lives at
// internal/platform/config, which provides `func Get() *Config`
// — the right replacement.
func AppLogger() (*config.Config, *zap.Logger, func(), error) {
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

// ProductionLogger returns a zap production logger without loading
// the global config. Kept as a helper for future admin commands that
// want to read config directly via config.Get() inside their command
// body without threading it through the helper signature (the
// upload-t5pre command that used this helper was deleted in Wave A
// commit 1, June 2026 — productionLogger remains for zero-config
// audit utilities that may be reintroduced).
//
// Canonical replacement for the former `productionLogger()` in
// cmd/admin/logger.go (PR-PKG-SIZE-CMD-ADMIN-1 extraction).
func ProductionLogger() (*zap.Logger, func(), error) {
	log, err := zap.NewProduction()
	if err != nil {
		return nil, func() {}, err
	}
	cleanup := func() { _ = log.Sync() }
	return log, cleanup, nil
}
