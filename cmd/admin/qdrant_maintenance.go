// cmd/admin/qdrant_maintenance.go — unified Qdrant maintenance (Issue 12, June 2026).
//
// Consolidates the former clean-qdrant-locators and cleanup-qdrant-legacy
// subcommands into a single qdrant-maintenance surface with three modes:
//
//  1. audit           — classification only (dry-run, no mutations).
//     Runs legacyaudit.Classify over all 8 categories
//     and prints the full report.
//  2. repair-locators — strip legacy drive_link / local_path payload keys.
//     Uses the canonical LocatorCleaner to delete the
//     keys from the Qdrant payload without touching
//     the asset or its vectors.
//  3. delete-invalid  — delete assets whose points hit non-locator
//     categories (1–6, 8). Dispatches via the
//     canonical outbox.Dispatcher.EnqueueAndDelete
//     path — NEVER direct DELETE FROM media_assets.
//
// Per-mode files (Issue 12 split, PR-QDRANT-MAINT-PER-MODE 2026-07-04):
//
//   - qdrant_maintenance_args.go             — qdrantMaintDeps + parseQdrantMaintArgs
//   - qdrant_maintenance_audit.go            — runQdrantMaintenanceAudit
//   - qdrant_maintenance_repair_locators.go  — runQdrantMaintenanceRepairLocators
//   - qdrant_maintenance_delete_invalid.go   — runQdrantMaintenanceDeleteInvalid
//   - qdrant_maintenance_classify.go         — classifyForMaintenance (shared by audit + delete-invalid)
//   - qdrant_maintenance_scanner.go          — qdrantScannerAdapter
//
// Policy (per user spec, Issue 12): locator payload keys are repairable,
// not automatically deletable. Points whose ONLY audit finding is
// LegacyLocatorPayload are excluded from delete-invalid and should be
// resolved via repair-locators instead.
//
// Usage:
//
//	go run ./cmd/admin qdrant-maintenance audit              # dry-run (all 8 categories)
//	go run ./cmd/admin qdrant-maintenance repair-locators    # strip drive_link/local_path
//	go run ./cmd/admin qdrant-maintenance delete-invalid     # outbox-delete non-locator assets
//	go run ./cmd/admin qdrant-maintenance audit --json       # machine-readable
//	go run ./cmd/admin qdrant-maintenance delete-invalid --limit=200  # cap scan page
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// qdrantMaintenanceModes are the valid first-positional-arg values.
var qdrantMaintenanceModes = map[string]bool{
	"audit":           true,
	"repair-locators": true,
	"delete-invalid":  true,
}

// qdrantMaintContext bundles the shared init state populated by runQdrantMaintenance
// and consumed by the per-mode handlers (audit / repair-locators / delete-invalid).
//
// Field access pattern per mode (godlike/07 honest-disclosure):
//
//   - repair-locators: reads cfg + log + ctx. The heavy-init fields
//     (sqliteDB, root, client, activeCol, scanner) remain nil — the mode
//     handler does NOT touch them.
//   - audit: reads cfg + log + ctx + scanner + activeCol (via classifyForMaintenance).
//     The orchestrator-internal fields (client, sqliteDB) are NOT read by
//     this mode handler.
//   - delete-invalid: reads cfg + log + ctx + scanner + activeCol (via
//     classifyForMaintenance) + root (for outbox.Dispatcher.EnqueueAndDelete).
//     The orchestrator-internal fields (client, sqliteDB) are NOT read by
//     this mode handler.
//   - Orchestrator-internal-only: client (feeds the scanner constructor in
//     runQdrantMaintenanceHeavy) + sqliteDB (defer-closed in the orchestrator
//     frame, never read by a mode handler).
type qdrantMaintContext struct {
	cfg *config.Config
	log *zap.Logger
	ctx context.Context

	// Heavy-init fields (audit + delete-invalid only).
	sqliteDB  *sql.DB
	root      *app.Root
	client    *qdrant.Client
	activeCol string
	scanner   *qdrantScannerAdapter
}

// runQdrantMaintenance is the cmd/admin/main.go entry point.
//
// Pipeline per mode:
//
//	audit:           init lite → init heavy → classify → print report (no mutations)
//	repair-locators: init lite → LocatorCleaner.CleanLocators → strip keys
//	delete-invalid:  init lite → init heavy → classify → filter locator-only →
//	                  outbox.Dispatcher.EnqueueAndDelete for remaining assets
func runQdrantMaintenance(args []string) error {
	deps, err := parseQdrantMaintArgs(args)
	if err != nil {
		return err
	}

	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	if !cfg.Qdrant.Enabled {
		return errors.New(
			"qdrant is disabled in config (qdrant.enabled=false); " +
				"qdrant-maintenance requires qdrant.enabled=true",
		)
	}

	ctx := cmdContext()
	log.Info("qdrant-maintenance starting",
		zap.String("mode", deps.Mode),
		zap.Int("limit", deps.Limit))

	mctx := &qdrantMaintContext{cfg: cfg, log: log, ctx: ctx}

	switch deps.Mode {
	case "repair-locators":
		return runQdrantMaintenanceRepairLocators(mctx, deps)
	case "audit":
		return runQdrantMaintenanceHeavy(mctx, deps, runQdrantMaintenanceAudit)
	case "delete-invalid":
		return runQdrantMaintenanceHeavy(mctx, deps, runQdrantMaintenanceDeleteInvalid)
	}
	return nil
}

// runQdrantMaintenanceHeavy populates the heavy-init fields on mctx
// (sqliteDB, root, client, activeCol, scanner) used by audit + delete-invalid,
// then dispatches to the supplied mode handler. Deferred cleanups
// (sqliteDB.Close + rootCleanup) fire AFTER the mode handler returns
// (the defers are scoped to this function, not the closure).
func runQdrantMaintenanceHeavy(
	mctx *qdrantMaintContext,
	deps qdrantMaintDeps,
	modeHandler func(*qdrantMaintContext, qdrantMaintDeps) error,
) error {
	sqliteDB, err := storage.OpenSQLiteDB(mctx.cfg.Storage.PrimaryDBFullPath(), mctx.log)
	if err != nil {
		return fmt.Errorf("open media DB: %w", err)
	}
	defer sqliteDB.Close()

	root, _, rootCleanup, err := app.InitComposition(mctx.cfg, mctx.log)
	if err != nil {
		return fmt.Errorf("production composition root init failed: %w", err)
	}
	defer rootCleanup()

	client := qdrant.NewClient(&qdrant.Config{
		BaseURL: mctx.cfg.Qdrant.BaseURL,
		APIKey:  mctx.cfg.Qdrant.APIKey,
		Timeout: mctx.cfg.Qdrant.Timeout,
	}, mctx.log)
	schema := qdrant.DefaultV3Schema()
	active, err := client.GetAliasTarget(mctx.ctx, schema.RuntimeAlias)
	if err != nil {
		return fmt.Errorf("resolve active collection: %w", err)
	}
	if active == "" {
		return fmt.Errorf("runtime alias %q has no target; run EnsureSchema first", schema.RuntimeAlias)
	}

	mctx.sqliteDB = sqliteDB
	mctx.root = root
	mctx.client = client
	mctx.activeCol = active
	mctx.scanner = newQdrantScannerAdapter(client, active, deps.Limit)

	return modeHandler(mctx, deps)
}
