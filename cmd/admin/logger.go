// Package main — admin CLI for one-shot operator tasks
// (seed-channels, stock-reset, summarize-book, db, ...).
// logger.go centralises the (cfg, log, cleanup) tuple that every
// command needs up front.
//
// Phase 3 audit #4 (fix/admin-config-fail-closed): the legacy
// appLogger() — which silently fell back to defaults+env via
// config.Get() when the config file was unreadable — has been
// replaced with the fail-closed appLoggerFromArgs(args). The
// new helper routes every admin subcommand through
// LoadConfigOrExit, which exits with code 2 on any config error
// (missing --app-config flag, missing file, malformed YAML, or
// cfg.Validate() failure). Operators and CI can now grep stderr
// for the canonical "admin: error loading config" prefix and the
// exit-2 status code to detect "refused to silent-fallback".
package main

import (
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// appLoggerFromArgs loads the production config (via
// LoadConfigOrExit, which exits with code 2 on any config error)
// and a zap production logger; returns (cfg, log, cleanup, err).
//
// Used by the production-critical admin subcommands that need the
// full config (cleanup-orphans, cleanup-all-orphans,
// cleanup-artlist-empty-folders, cleanup-stock-orphans,
// delete-specific-folders, list-drive-folder, reset-video-ai,
// sync-all-drive, test-youtube, verify-artlist-pipeline).
//
// ERROR CONTRACT:
//
//   - Config error (missing flag / missing file / malformed YAML /
//     Validate failure): LoadConfigOrExit calls os.Exit(2). The
//     caller NEVER sees the error return for this path; the
//     process is already terminated with a printed "admin: <err>"
//     message on stderr.
//
//   - Logger error (zap.NewProduction failure): returns the
//     underlying err. Caller propagates to main where the existing
//     "Error running command ...: <err>" + exit 1 path takes over.
//
// The cleanup callback is safe to call multiple times — zap.Sync
// is returns-nil-on-already-flushed, so an extra defer cleanup
// during exit is harmless.
func appLoggerFromArgs(args []string) (*config.Config, *zap.Logger, func(), error) {
	cfg := LoadConfigOrExit(args)
	log, err := zap.NewProduction()
	if err != nil {
		return nil, nil, func() {}, err
	}
	cleanup := func() { _ = log.Sync() }
	return cfg, log, cleanup, nil
}

// productionLogger returns a zap production logger WITHOUT loading
// config. Used by upload-t5pre, which does the config load itself
// via LoadConfigOrExit(args) so the config-error exit path is the
// same canonical code 2 regardless of which helper was used.
func productionLogger() (*zap.Logger, func(), error) {
	log, err := zap.NewProduction()
	if err != nil {
		return nil, func() {}, err
	}
	cleanup := func() { _ = log.Sync() }
	return log, cleanup, nil
}

// NOTE: there used to be a `velox/go-master/internal/config` import
// here (pre-Upstream commit 66c646b5 the handler migrated here under
// that path). It was wrong from the start of the PipelineGen module:
// velox/go-master/internal/config is a stdlib-shaped path that Go
// resolves to $GOROOT/src/velox/go-master/internal/config, not to any
// repo package. The canonical local config lives at
// internal/platform/config, which provides `func Get() *Config` and
// `func GetFromPath(path string) (*Config, error)` — the right
// replacements for legacy fallback vs fail-loud modes respectively.
