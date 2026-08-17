// Package main — admin CLI for one-shot operator tasks
// (stock-reset, summarize-book, backfill-monitored-sources-to-category-channels, ...).
// logger.go centralises the (cfg, log, cleanup) tuple that every
// command needs up front.
//
// PR-PKG-SIZE-CMD-ADMIN-1 (July 2026): appLogger and productionLogger
// were extracted to cmd/admin/internal/cli (exported as AppLogger +
// ProductionLogger) so the reconcile/reindex/dr subcommands (now in
// cmd/admin/reconcile subpackage) can import them. The lowercase
// appLogger shim is retained here to delegate to the canonical
// cli.AppLogger helper — the 60 non-moved cmd/admin files do NOT need
// to change as part of this PR. (productionLogger's shim was already
// orphaned and has been removed.) Tracked follow-up:
// PR-PKG-SIZE-CMD-ADMIN-1-FOLLOWUP will migrate all call sites to
// cli.AppLogger / cli.ProductionLogger directly and delete this file.
package main

import (
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// appLogger is a thin shim around cli.AppLogger. See file header.
func appLogger() (*config.Config, *zap.Logger, func(), error) {
	return cli.AppLogger()
}
