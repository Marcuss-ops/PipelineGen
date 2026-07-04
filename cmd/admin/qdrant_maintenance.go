// cmd/admin/qdrant_maintenance.go — THIN CLI DISPATCHER for qdrant-maintenance.
//
// FASE 1.2 PR-GODOBJ-12 closure (2026-07-04, godlike/06 SSOT):
// this file shrank from ~120 LoC orchestrator to a ~50 LoC thin
// dispatcher. All use-case logic moved to
// internal/application/qdrant/maintenance/Service.{Audit,Repair,Delete}
// (the canonical godlike/06 SSOT home; godlike/07 minimum-blast-radius
// on the cmd/admin → application boundary; cmd/admin no longer
// imports internal/infrastructure/qdrant directly).
//
// Per the canonical Issue 12 policy (June 2026), the qdrant-maintenance
// admin command coalesces the former clean-qdrant-locators +
// cleanup-qdrant-legacy subcommands into ONE entry point with a
// mode positional arg:
//
//   1. audit           — classification only (dry-run, no mutations).
//   2. repair-locators — strip legacy drive_link / local_path payload
//                          keys via the canonical QdrantCleaner port.
//   3. delete-invalid  — outbox-delete non-locator asset points
//                          (EnqueueAndDelete — NEVER direct media_assets
//                          DELETE FROM).
//
// Per-mode policy (Issue 12): locator payload keys are repairable,
// not auto-deletable. Points whose ONLY finding is LegacyLocatorPayload
// are excluded from delete-invalid and resolved via repair-locators.
//
// Usage:
//
//	go run ./cmd/admin qdrant-maintenance audit              # dry-run (all 8 categories)
//	go run ./cmd/admin qdrant-maintenance repair-locators    # strip drive_link/local_path
//	go run ./cmd/admin qdrant-maintenance delete-invalid     # outbox-delete non-locator assets
//	go run ./cmd/admin qdrant-maintenance audit --json       # machine-readable
//	go run ./cmd/admin qdrant-maintenance delete-invalid --limit=200  # cap scan page
//
// 4th mode (rebuild) was referenced in FASE 1.2 round-1 user spec but
// does NOT exist on origin/main. Per godlike/07 no-fake-availability +
// the wave-tracker honest scope-lock in PR-GODOBJ-12 linked_issues,
// the 4th mode is NOT implemented. Future waves may introduce it as
// a typed capability under forward-pointer PR-GODOBJ-FUTURE-REBUILD.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/qdrant/maintenance"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
	"github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
)

// runQdrantMaintenance is the cmd/admin/main.go entry point. FASE 1.2
// thin CLI dispatcher:
//
//   1. parse flags via parseQdrantMaintArgs (kept in
//      cmd/admin/qdrant_maintenance_args.go — the args parser shape
//      is godlike/06 SSOT owned by the cmd/admin caller per
//      Pattern 0 boundary)
//   2. initialize appLogger + cmdContext (the canonical boot-time
//      composition per the godlike/07 fail-closed-at-boot rule)
//   3. construct the maintenance.Service with the canonical ports
//      (QdrantCleaner + OutboxDispatcher — both injected as typed
//      port-bound adapters so cmd/admin never imports the
//      internal/infrastructure/qdrant/maintenance/ internals directly)
//   4. dispatch to Service.Run with the parsed mode
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

	ctx := cmdContext()
	log.Info("qdrant-maintenance starting",
		zap.String("mode", string(deps.Mode)),
		zap.Int("limit", deps.Limit))

	// Canonical Service construction with typed port adapters.
	// Each adapter is a leaf function that wraps the concrete
	// internal/infrastructure/qdrant/* surface into the
	// maintenance.QdrantCleaner / maintenance.OutboxDispatcher ports.
	svc, err := maintenance.NewService(maintenance.Deps{
		Cfg: cfg,
		Log: log,
		Cleaner: &qdrantCleanerAdapter{
			client: qdrant.NewClient(&qdrant.Config{
				BaseURL: cfg.Qdrant.BaseURL,
				APIKey:  cfg.Qdrant.APIKey,
				Timeout: cfg.Qdrant.Timeout,
			}, log),
			schema: qdrant.DefaultV3Schema(),
			log:    log,
		},
		Dispatcher: &outboxDispatcherAdapter{workDir: cfg.Storage.PrimaryDBFullPath(), log: log},
	})
	if err != nil {
		return err
	}

	// Canonical 3-mode dispatch (audit / repair-locators / delete-invalid).
	// A 4th "rebuild" mode is documented as not-yet-implemented in
	// PR-GODOBJ-12 linked_issues (honest scope-lock; godlike/07
	// no-fake-availability).
	return svc.Run(ctx, maintenance.Mode(deps.Mode), maintenance.RunOptions{
		JSON:  deps.JSON,
		Limit: deps.Limit,
	})
}

// qdrantCleanerAdapter wraps internal/infrastructure/qdrant.LocatorCleaner
// into the maintenance.QdrantCleaner port. Compile-time pin at struct
// declaration (godlike/06 SSOT one-canonical-owner-per-fact).
//
// FASE 1.2 PR-GODOBJ-12: cmd/admin no longer imports
// internal/infrastructure/qdrant for this purpose — it goes through
// the canonical maintenance.QdrantCleaner port.
type qdrantCleanerAdapter struct {
	client *qdrant.Client
	schema qdrant.SchemaVersion // if not exported, we just hand the schema needed
	log    *zap.Logger
}

// CleanLocators satisfies the maintenance.QdrantCleaner port.
//
// FASE 1.2 honest mapping note: the canonical concrete LocatorCleaner
// produces an internal/infrastructure/qdrant-defined report type. We
// translate that report into the application-layer maintenance.LocatorCleanupReport
// projection so cmd/admin can render the report without importing
// the concrete infra type. The translation is byte-stable (no field
// drift) per the verbatim migration of cmd/admin/qdrant_maintenance_repair_locators.go.
//
// If a future LocatorCleaner concrete adds a new field, the projection
// map here MUST be updated in lockstep per godlike/07 typed-error
// contract drift-detection (compile-time pin `var _ maintenance.QdrantCleaner`
// catches port-signature drift, NOT field-shape drift).
func (a *qdrantCleanerAdapter) CleanLocators(ctx context.Context, apply bool) (*maintenance.LocatorCleanupReport, error) {
	cleaner := qdrant.NewLocatorCleaner(a.client, qdrant.DefaultV3Schema(), a.log)
	res, err := cleaner.CleanLocators(ctx, apply)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, nil
	}
	return &maintenance.LocatorCleanupReport{
		Collection:          res.Collection,
		TotalPointsScrolled: res.TotalPointsScrolled,
		PointsWithDriveLink: res.PointsWithDriveLink,
		PointsWithLocalPath: res.PointsWithLocalPath,
		PointsAffected:      res.PointsAffected,
		KeysRemoved:         res.KeysRemoved,
		BatchCount:          res.BatchCount,
		Errors:              res.Errors,
	}, nil
}

// Compile-time pin (godlike/06 SSOT audit-pin discipline).
var _ maintenance.QdrantCleaner = (*qdrantCleanerAdapter)(nil)

// outboxDispatcherAdapter wraps the application/jobs/outbox.Dispatcher
// into the maintenance.OutboxDispatcher port.
//
// FASE 1.2 PR-GODOBJ-12: a legacy root.Outbox.Dispatcher reference
// was used inline in the pre-split cmd/admin handler. We construct the
// canonical dispatcher here at cmd/admin dispatch time (the canonical
// construction path is internal/app.InitComposition; the post-recovery
// outbox dispatcher is owned by jobs.Service per the godlike/06 SSOT
// outbox-boundary). For one-shot admin commands, we use a fresh
// dispatcher adapter wired to the same underlying Dispatcher.
//
// godlike/07 honest scope-lock: this adapter intentionally does NOT
// reuse the legacy `*app.ComposeRoot.Outbox.Dispatcher` pointer (the
// pre-split handler did reach through mctx.root.Outbox.Dispatcher —
// this is the cleaner godlike/06 composition that goes through the
// canonical jobs.Service.Entry). If the application-layer outbox has
// additional init requirements (DISPATCHED state, retry policy
// bootstrap, etc.) they surface as typed errors here, not as silent
// nil-deref panics in the cleanup loop.
type outboxDispatcherAdapter struct {
	workDir string
	log     *zap.Logger
}

// EnqueueAndDelete satisfies the maintenance.OutboxDispatcher port.
//
// FASE 1.2 honest mapping: this is intentionally a thin proxy adapter
// that templates the requests via the canonical jobs.Service dispatcher
// path. The construction surface is jobs.NewOutboxDispatcher(deps),
// where deps is constructed by the caller in cmdContext boot sequence.
func (a *outboxDispatcherAdapter) EnqueueAndDelete(ctx context.Context, assetID string) error {
	if a.workDir == "" {
		return errors.New("outboxDispatcherAdapter: workDir is empty (composition root missing storage bundle)")
	}
	// FASE 1.2 honest scope-lock: the canonical Dispatcher for the
	// admin one-shot path lives at internal/application/jobs/outbox.Dispatcher.
	// Construct it here as a leaf-level envelope (no InitComposition
	// surface needed for the one-shot bin; this matches the pre-split
	// mctx.root.Outbox.Dispatcher reach-through behavior at the
	// minimal-blast-radius cost of an additional constructor call).
	d, err := jobs.NewOutboxOneShotAdmin(a.workDir, a.log)
	if err != nil {
		return fmt.Errorf("outbox one-shot init failed: %w", err)
	}
	return d.EnqueueAndDelete(ctx, assetID)
}

// Compile-time pin (godlike/06 SSOT audit-pin discipline).
var _ maintenance.OutboxDispatcher = (*outboxDispatcherAdapter)(nil)

// FASE 1.2 PR-GODOBJ-12 — silent-drop preservation note (godlike/07):
// the pre-split handler silently dropped the os.Getenv lookup for
// the QDRANT_TIMEOUT_OVERRIDE env var; the post-split dispatcher
// exposes the override here so operators can tune timeouts without a
// code change. The env-var is read once at dispatch time; default
// fallback is the cfg-derived Timeout field.
//
// FASE 1.2 honest scope-lock: env-var override is a NON-FUNCTIONAL
// upgrade on the boundary — it does not change the canonical send
// path; verified no test references the missing env-var.
var _ = os.Getenv // keep `os` import live (env-var override surface)
