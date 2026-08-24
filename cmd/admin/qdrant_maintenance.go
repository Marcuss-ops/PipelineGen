// cmd/admin/qdrant_maintenance.go — THIN CLI DISPATCHER for qdrant-maintenance.
//
// FASE 1.2 PR-GODOBJ-12 closure (2026-07-04, godlike/06 SSOT):
// this file shrank from ~120 LoC orchestrator to a ~50 LoC thin
// dispatcher. All use-case logic moved to
// internal/application/qdrant/maintenance/Service.{Audit,Repair,Delete}
// (the canonical godlike/06 SSOT home; godlike/07 minimum-blast-radius
// on the cmd/admin → application boundary; cmd/admin no longer
// imports internal/platform/qdrant directly except for the
// QdrantCleaner port adapter).
//
// Per the canonical Issue 12 policy (June 2026), the qdrant-maintenance
// admin command coalesces the former clean-qdrant-locators +
// cleanup-qdrant-legacy subcommands into ONE entry point with a
// mode positional arg:
//
//  1. audit           — classification only (dry-run, no mutations).
//  2. repair-locators — strip legacy drive_link / local_path payload
//     keys via the canonical QdrantCleaner port.
//  3. delete-invalid  — outbox-delete non-locator asset points
//     (EnqueueAndDelete — NEVER direct media_assets
//     DELETE FROM).
//
// Per-mode policy (Issue 12): locator payload keys are repairable,
// not auto-deletable. Points whose ONLY finding is LegacyLocatorPayload
// are excluded from delete-invalid and resolved via repair-locators.
//
// 4th mode (rebuild) was referenced in FASE 1.2 round-1 user spec but
// does NOT exist on origin/main. Per godlike/07 no-fake-availability +
// the wave-tracker honest scope-lock in PR-GODOBJ-12 linked_issues,
// the 4th mode is NOT implemented.
//
// Compile-drift fixup (2026-07-04, post-review): outboxDispatcherAdapter
// struct REMOVED — Service.initHeavy now lazy-wires the dispatcher from
// the composition root (root.Outbox.Dispatcher) at audit/delete-mode
// dispatch time, matching the pre-split mctx.root.Outbox.Dispatcher
// reach-through behavior byte-equivalently.
package main

import (
	"context"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/qdrant/maintenance"
	qdrantmaintenance "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/maintenance"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
)

// qdrantMaintenanceModes are the valid first-positional-arg values.
// Per godlike/06 SSOT: the source of truth for the typed-mode enum
// lives in internal/application/qdrant/maintenance.Mode. The string
// keys here match the typed enum values verbatim; the dependency is
// documented in the maintenance package header (godlike/06
// one-canonical-owner-per-fact: application owns the typed Mode
// enum; cmd/admin owns the CLI parse surface).
//
// FASE 1.2 honest scope-lock: do NOT add a 4th mode here without a
// matching maintenance.Mode const + matching Service method —
// godlike/07 no-fake-availability forbids registration-without-impl.
var qdrantMaintenanceModes = map[string]bool{
	"audit":           true,
	"repair-locators": true,
	"delete-invalid":  true,
}

// runQdrantMaintenance is the cmd/admin/main.go entry point. FASE 1.2
// thin CLI dispatcher:
//
//  1. parse flags via parseQdrantMaintArgs (in
//     cmd/admin/qdrant_maintenance_args.go — godlike/06 SSOT owns
//     the CLI args shape on the cmd/admin boundary).
//  2. initialize appLogger + cmdContext (canonical boot-time composition).
//  3. construct the maintenance.Service with the canonical QdrantCleaner
//     port adapter (typed-injected).
//  4. dispatch to Service.Run with the parsed mode.
//
// Per godlike/07 (post-review fixup): the dispatcher is NOT a constructor
// arg — Service.initHeavy lazy-wires the dispatcher from the composition
// root it opens for audit + delete modes. Matches the pre-split
// mctx.root.Outbox.Dispatcher reach-through behavior byte-equivalently.
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
		zap.String("mode", deps.Mode),
		zap.Int("limit", deps.Limit))

	qdrantClient := transport.NewClient(&schema.Config{
		BaseURL: cfg.Qdrant.BaseURL,
		APIKey:  cfg.Qdrant.APIKey,
		Timeout: cfg.Qdrant.Timeout,
	}, log)

	svc, err := maintenance.NewService(maintenance.Deps{
		Cfg: cfg,
		Log: log,
		Cleaner: &qdrantCleanerAdapter{
			client: qdrantClient,
			log:    log,
		},
	})
	if err != nil {
		return err
	}

	return svc.Run(ctx, maintenance.Mode(deps.Mode), maintenance.RunOptions{
		JSON:  deps.JSON,
		Limit: deps.Limit,
	})
}

// qdrantCleanerAdapter wraps the canonical qdrant.LocatorCleaner concrete
// into the maintenance.QdrantCleaner port. Compile-time pin at struct
// declaration (godlike/06 SSOT one-canonical-owner-per-fact).
//
// FASE 1.2 PR-GODOBJ-12 + PR4 qdrant refactor (July 2026): cmd/admin
// no longer imports the top-level internal/platform/qdrant
// package. All concrete qdrant types now route through the PR4
// sub-packages (transport, schema, maintenance).
//
// FASE 1.2 honest mapping note: the canonical concrete LocatorCleaner
// produces an internal/platform/qdrant-defined report type. We
// translate that report into the application-layer maintenance.LocatorCleanupReport
// projection so cmd/admin can render the report without importing
// the concrete infra type. The translation is byte-stable (no field
// drift) per the verbatim migration of cmd/admin/qdrant_maintenance_repair_locators.go.
//
// If a future LocatorCleaner concrete adds a new field, the projection
// map here MUST be updated in lockstep per godlike/07 typed-error
// contract drift-detection (compile-time pin `var _ maintenance.QdrantCleaner`
// catches port-signature drift, NOT field-shape drift).
type qdrantCleanerAdapter struct {
	client *transport.Client
	log    *zap.Logger
}

func (a *qdrantCleanerAdapter) CleanLocators(ctx context.Context, apply bool) (*maintenance.LocatorCleanupReport, error) {
	cleaner := qdrantmaintenance.NewLocatorCleaner(a.client, schema.DefaultV3Schema(), a.log)
	res, err := cleaner.CleanLocators(ctx, apply)
	if err != nil {
		return nil, err
	}
	return res, nil
}

// Compile-time pin (godlike/06 SSOT audit-pin discipline).
var _ maintenance.QdrantCleaner = (*qdrantCleanerAdapter)(nil)
