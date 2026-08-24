// cmd/admin/reconcile_qdrant.go — QDRANT-005B reconciler (June 2026)
//
// One-shot dual-store compare + repair.
//
// Scope (per docs/architecture/qdrant/QDRANT-005.md §005B):
//   - Compare real ID sets (SQLite media_assets vs. Qdrant points via
//     payload.asset_id). NOT counts.
//   - 9 classification categories (see
//     internal/application/qdrant/reconciler/types.go).
//   - Repair routes are canonical:
//   - missing / version_stale / payload_incomplete /
//     lifecycle_mismatch / workspace_mismatch /
//     non_canonical_point_id → outbox_events UPSERT event
//     (routed via an inline adapter; see outboxRepairAdapter
//     below for rationale on bypassing outbox.Dispatcher).
//   - orphan → outbox_events DELETE event.
//   - lifecycle_key_legacy / locator_legacy →
//     transport.Client.DeletePayloadKeys (canonical for legacy key
//     stripping; no outbox primitive for partial payload mutation).
//
// Usage:
//
//	go run ./cmd/admin reconcile-qdrant                              # dry-run (default)
//	go run ./cmd/admin reconcile-qdrant --apply                       # dispatch repairs
//	go run ./cmd/admin reconcile-qdrant --json                        # JSON-only output
//	go run ./cmd/admin reconcile-qdrant --apply --report-path=./out.json
//	go run ./cmd/admin reconcile-qdrant --collection=media_assets_v3
//	go run ./cmd/admin reconcile-qdrant --include-lifecycle=ACTIVE,STAGING
//	go run ./cmd/admin reconcile-qdrant --batch-size=1000
package reconcile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/outbox"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/qdrant/reconciler"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/indexing"
	qdrantschema "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	qdrantsearch "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/search"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"
)

// (database/sql is required for outboxRepairAdapter.db which is *sql.DB — the OpenSQLiteDB return type)

// reconcileQdrantDeps holds the parsed flags for RunReconcileQdrant.
type reconcileQdrantDeps struct {
	Apply            bool
	DryRun           bool
	JSON             bool
	ReportPath       string
	Collection       string
	IncludeLifecycle []string
	BatchSize        int
}

// parseReconcileQdrantArgs parses CLI args.
//
// Flags:
//
//	--apply                              actually dispatch repairs (default: dry-run)
//	--dry-run                            explicit dry-run
//	--json                               machine-readable output
//	--report-path=PATH                   write JSON report to disk
//	--collection=NAME                    override Qdrant collection
//	--include-lifecycle=ACTIVE,STAGING   restrict SQLite scan to these states
//	--batch-size=N                       points per scroll page (default 500)
func parseReconcileQdrantArgs(args []string) (reconcileQdrantDeps, error) {
	deps := reconcileQdrantDeps{BatchSize: 500}
	for _, a := range args {
		a = strings.TrimSpace(a)
		switch {
		case a == "--apply":
			deps.Apply = true
		case a == "--dry-run":
			deps.DryRun = true
		case a == "--json":
			deps.JSON = true
		case strings.HasPrefix(a, "--report-path="):
			deps.ReportPath = strings.TrimPrefix(a, "--report-path=")
		case strings.HasPrefix(a, "--collection="):
			deps.Collection = strings.TrimPrefix(a, "--collection=")
		case strings.HasPrefix(a, "--include-lifecycle="):
			deps.IncludeLifecycle = strings.Split(strings.TrimPrefix(a, "--include-lifecycle="), ",")
			for i, s := range deps.IncludeLifecycle {
				deps.IncludeLifecycle[i] = strings.TrimSpace(s)
			}
		case strings.HasPrefix(a, "--batch-size="):
			n, err := cli.ParsePositiveFlag(a, "--batch-size")
			if err != nil {
				return deps, err
			}
			deps.BatchSize = n
		default:
			if strings.HasPrefix(a, "-") {
				return deps, fmt.Errorf("unknown flag: %s", a)
			}
		}
	}
	if deps.Apply && deps.DryRun {
		return deps, fmt.Errorf("--apply and --dry-run are mutually exclusive")
	}
	return deps, nil
}

// RunReconcileQdrant is the entry point registered in cmd/admin/main.go.
//
// Pipeline:
//  1. Load config; require qdrant.enabled=true.
//  2. Open the media DB.
//  3. Build canonical stack: DefaultV3Schema, asset store, transport.Client.
//  4. Resolve target collection (override > runtime alias target).
//  5. Wire service ports from the canonical concrete adapters.
//  6. Run Service.Reconcile.
//  7. Pretty-print (or JSON-only) the resulting ReconcileReport.
func RunReconcileQdrant(args []string) error {
	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	deps, err := parseReconcileQdrantArgs(args)
	if err != nil {
		return err
	}

	if !cfg.Qdrant.Enabled {
		return errors.New(
			"qdrant is disabled in config; reconcile-qdrant requires qdrant.enabled=true",
		)
	}

	ctx := cli.CmdContext()

	log.Info("reconcile-qdrant starting",
		zap.Bool("apply", deps.Apply),
		zap.Bool("dry_run", deps.DryRun || !deps.Apply),
		zap.String("report_path", deps.ReportPath),
		zap.String("collection_override", deps.Collection),
		zap.Strings("include_lifecycle", deps.IncludeLifecycle),
		zap.Int("batch_size", deps.BatchSize),
		zap.String("qdrant_url", cfg.Qdrant.BaseURL),
	)

	// 1. Open the media DB.
	sqliteDB, err := storage.OpenSQLiteDB(cfg.Storage.PrimaryDBFullPath(), log)
	if err != nil {
		return fmt.Errorf("open media DB: %w", err)
	}
	defer sqliteDB.Close()

	// 2. Build canonical stack.
	schema := qdrantschema.DefaultV3Schema()
	assetStore := indexing.NewSQLiteAssetStore(sqliteDB.DB)
	client := transport.NewClient(&qdrantschema.Config{
		BaseURL: cfg.Qdrant.BaseURL,
		APIKey:  cfg.Qdrant.APIKey,
		Timeout: cfg.Qdrant.Timeout,
	}, log)

	// 3. Resolve collection: explicit override > runtime alias target.
	collection := deps.Collection
	if collection == "" {
		resolved, err := client.GetAliasTarget(ctx, schema.RuntimeAlias)
		if err != nil {
			return fmt.Errorf("resolve runtime alias %q: %w", schema.RuntimeAlias, err)
		}
		if resolved == "" {
			return fmt.Errorf(
				"runtime alias %q has no target; pass --collection=NAME to scrub a specific collection",
				schema.RuntimeAlias,
			)
		}
		collection = resolved
	}
	log.Info("reconciling collection", zap.String("collection", collection))

	// 4. Build port adapters.
	qdrantAdapter := &qdrantListerAdapter{client: client}
	payloadAdapter := &qdrantPayloadAdapter{client: client}
	outboxEventsRepo := outboxevents.NewRepository(sqliteDB.DB)
	outboxAdapter := outbox.NewRepairAdapter(sqliteDB.DB, outboxEventsRepo, schema.Version)
	sqliteAdapter := &reconcileReaderAdapter{store: assetStore}
	pointIDFor := qdrantschema.AssetIDToQdrantPointID

	// 5. Derive schema for the scanner.
	perChannel := map[string]string{}
	for _, spec := range schema.DenseVectors {
		perChannel[spec.Channel] = spec.ModelVersion
	}
	scannerSchema := reconciler.SchemaVersions{
		Version:           schema.Version,
		PhysicalName:      schema.PhysicalName,
		RuntimeAlias:      schema.RuntimeAlias,
		PerChannelVersion: perChannel,
		RequiredKeys:      []string{"asset_id", "name", "source", "lifecycle_state"},
	}

	// 6. Build the service via ServiceDeps (PR2 refactor — eliminated
	// positional-arg footgun). Metrics port wired to PromMetricsAdapter
	// so reconcile-qdrant emits QDRANT-005C observability on every run.
	svc := reconciler.NewServiceFromDeps(reconciler.ServiceDeps{
		Schema:       scannerSchema,
		Qdrant:       qdrantAdapter,
		SQLite:       sqliteAdapter,
		Outbox:       outboxAdapter,
		Payload:      payloadAdapter,
		PointIDFor:   pointIDFor,
		ReportWriter: nil, // default filesystem report writer
		Metrics:      qdrantsearch.PromMetricsAdapter{},
		Log:          log,
	})

	// 7. Run.
	report, err := svc.Reconcile(ctx, reconciler.ReconcileOptions{
		DryRun:                 !deps.Apply,
		BatchSize:              deps.BatchSize,
		Collection:             collection,
		ReportPath:             deps.ReportPath,
		IncludeLifecycleStates: deps.IncludeLifecycle,
	})
	if err != nil {
		if deps.ReportPath != "" && report != nil {
			b, _ := json.MarshalIndent(report, "", "  ")
			_ = os.WriteFile(deps.ReportPath, b, 0o644)
		}
		log.Error("reconcile-qdrant failed", zap.Error(err))
		return err
	}

	// 8. Print.
	if deps.JSON {
		b, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(b))
		return nil
	}

	fmt.Printf("=== QDRANT-005B reconcile: %s ===\n", modeLabel(deps))
	fmt.Printf("  Collection:   %s\n", report.Collection)
	fmt.Printf("  Schema:       %s\n", report.SchemaVersion)
	fmt.Printf("  DryRun:       %v\n", report.DryRun)
	fmt.Printf("  Applied:      %v\n", report.Applied)
	fmt.Printf("  Duration:     %dms\n", report.DurationMs)
	fmt.Printf("  SQLite rows:  %d\n", report.ScannedTotals.SQLiteAssets)
	fmt.Printf("  Qdrant pts:   %d\n", report.ScannedTotals.QdrantPoints)
	fmt.Printf("  Pairs:        %d\n", report.ScannedTotals.Pairs)
	fmt.Println("  --- Action categories:")
	rr := report.Reconciliation
	fmt.Printf("    Missing:             %d\n", rr.Missing.Count)
	fmt.Printf("    Orphans:             %d\n", rr.Orphans.Count)
	fmt.Printf("    InvalidPayloads:     %d\n", rr.InvalidPayloads.Count)
	fmt.Printf("    NonCanonicalIDs:     %d\n", rr.NonCanonicalIDs.Count)
	fmt.Printf("    MissingVectors:      %d\n", rr.MissingVectors.Count)
	fmt.Printf("    DimensionMismatches: %d\n", rr.DimensionMismatches.Count)
	fmt.Printf("    Duplicates:          %d\n", rr.Duplicates.Count)
	fmt.Println("  --- All categories (12):")
	for _, k := range reconciler.AllClassificationKinds {
		fmt.Printf("    %-26s = %d\n", k, report.Counts[k])
	}
	if report.Applied {
		fmt.Println("  --- Repairs dispatched:")
		fmt.Printf("    reindex_enqueued   = %d\n", report.RepairSummary.ReindexEnqueued)
		fmt.Printf("    delete_enqueued    = %d\n", report.RepairSummary.DeleteEnqueued)
		fmt.Printf("    payload_strips     = %d\n", report.RepairSummary.PayloadStrips)
	}
	if len(report.Errors) > 0 {
		fmt.Printf("  Errors: %d\n", len(report.Errors))
		for i, e := range report.Errors {
			fmt.Printf("    [%d] %s\n", i, e)
		}
	}
	if deps.ReportPath != "" {
		fmt.Printf("  Report path:    %s\n", deps.ReportPath)
	}
	if !deps.Apply {
		fmt.Println("\nRe-run with --apply to dispatch repairs.")
	}
	return nil
}

func modeLabel(d reconcileQdrantDeps) string {
	if d.Apply && !d.DryRun {
		return "APPLY"
	}
	return "DRY-RUN"
}

// ── Port adapters (cmd/admin glue) ────────────────────────────────────

// ── Port adapters (cmd/admin glue) ──
// qdrantListerAdapter, qdrantPayloadAdapter, outboxRepairAdapter,
// reconcileReaderAdapter — extracted to reconcile_qdrant_adapters.go
// (PR-RECONCILE-SPLIT, July 2026).
