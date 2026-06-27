// cmd/admin/reconcile_qdrant.go — QDRANT-005B closure (June 2026)
//
// One-shot reconciliation between SQLite media_assets and a Qdrant
// collection. Classifies 5 drift classes (MISSING/EXTRA/STALE/VERSION/
// ID_MISMATCH), reports them, and (when --apply) repairs them via the
// canonical IndexWriter (REST UPSERT/DELETE — both idempotent on
// Qdrant point IDs, no event_key dedup needed).
//
// FAIL-CLOSED contract: a partial Qdrant scroll (error or empty
// NextOffset before end-of-stream) blocks the apply phase. Status
// "scan_incomplete" is returned and no repairs are issued.
// Reconciliation against a half-scroll would manufacture false-positive
// MISSING drifts for every unseen Qdrant point.
//
// Usage:
//
//	go run ./cmd/admin reconcile-qdrant                          # dry-run on active alias target
//	go run ./cmd/admin reconcile-qdrant --apply                   # apply repairs (capped by --limit if set)
//	go run ./cmd/admin reconcile-qdrant --apply --limit=500       # cap repairs to 500
//	go run ./cmd/admin reconcile-qdrant --collection=media_assets_v3_20260620150405  # explicit collection
//	go run ./cmd/admin reconcile-qdrant --json                    # machine-readable output
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap"

	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
)

// reconcileQdrantDeps holds the parsed flags for runReconcileQdrant.
type reconcileQdrantDeps struct {
	Apply      bool
	JSON       bool
	DryRun     bool
	Limit      int    // caps # of repairs (not scans)
	Collection string // explicit Qdrant collection name (default: active alias target)
}

// parseReconcileQdrantArgs parses CLI args into reconcileQdrantDeps.
// Flags:
//
//	--apply              actually repair via IndexWriter
//	--dry-run            explicit dry-run (default, omit when --apply)
//	--json               machine-readable output
//	--limit=N            cap number of repair operations
//	--collection=X       explicit Qdrant collection name (default: active alias)
func parseReconcileQdrantArgs(args []string) (reconcileQdrantDeps, error) {
	deps := reconcileQdrantDeps{}
	for _, a := range args {
		a = strings.TrimSpace(a)
		switch {
		case a == "--apply":
			deps.Apply = true
		case a == "--dry-run":
			deps.DryRun = true
		case a == "--json":
			deps.JSON = true
		case strings.HasPrefix(a, "--limit="):
			n, err := parsePositiveFlag(a, "--limit")
			if err != nil {
				return deps, err
			}
			deps.Limit = n
		case strings.HasPrefix(a, "--collection="):
			deps.Collection = strings.TrimPrefix(a, "--collection=")
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

// runReconcileQdrant is the entry point registered in cmd/admin/main.go.
//
// Pipeline (QDRANT-005B, June 2026):
//  1. Load config and open the media DB
//  2. Build the canonical Qdrant stack: SQLiteAssetStore → PayloadMapper → Client → IndexWriter → Reconciler
//  3. Resolve the target collection (--collection override, else active alias target via CollectionManager.GetActiveCollection)
//  4. Run the reconciler
//  5. Print human or --json output
//  6. Return ErrScanIncomplete when status="scan_incomplete" (CI signal)
func runReconcileQdrant(args []string) error {
	cfg, log, cleanup, err := appLogger()
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
			"qdrant is disabled in config (qdrant.enabled=false); " +
				"reconcile-qdrant requires qdrant.enabled=true",
		)
	}

	ctx := cmdContext()

	log.Info("reconcile-qdrant starting",
		zap.Bool("apply", deps.Apply),
		zap.Bool("dry_run", deps.DryRun || !deps.Apply),
		zap.Int("repair_limit", deps.Limit),
		zap.String("collection", deps.Collection),
		zap.String("qdrant_url", cfg.Qdrant.BaseURL),
	)

	sqliteDB, err := storage.OpenSQLiteDB(cfg.Storage.PrimaryDBFullPath(), log)
	if err != nil {
		return fmt.Errorf("open media DB: %w", err)
	}
	defer sqliteDB.Close()

	// ── Build canonical Qdrant stack ──────────────────────────────
	schema := qdrant.DefaultV3Schema()
	assetStore := qdrant.NewSQLiteAssetStore(sqliteDB.DB)
	mapper := qdrant.NewPayloadMapper(assetStore, log)
	client := qdrant.NewClient(&qdrant.Config{
		BaseURL: cfg.Qdrant.BaseURL,
		Timeout: cfg.Qdrant.Timeout,
		APIKey:  cfg.Qdrant.APIKey,
	}, log)
	writer := qdrant.NewIndexWriter(client, schema, mapper, log)
	collectionMgr := qdrant.NewCollectionManager(client, schema, log)

	// ── Resolve target collection ─────────────────────────────────
	targetCollection := deps.Collection
	if targetCollection == "" {
		resolved, resolveErr := collectionMgr.GetActiveCollection(ctx)
		if resolveErr != nil {
			return fmt.Errorf("resolve active collection (no --collection override and no alias target): %w", resolveErr)
		}
		if resolved == "" {
			return errors.New("no active alias target and no --collection override")
		}
		targetCollection = resolved
		log.Info("resolved default target via runtime alias", zap.String("collection", targetCollection))
	}

	// ── Run reconciler ───────────────────────────────────────────
	reconciler := qdrant.NewReconciler(client, sqliteDB.DB, writer, schema, log)
	result, runErr := reconciler.Reconcile(ctx, qdrant.ReconcileOptions{
		Collection:  targetCollection,
		RepairLimit: deps.Limit,
		DryRun:      !deps.Apply,
	})

	// Even on non-fatal runErr we still want to print the partial result;
	// the operator can see exactly which scans went sideways.
	if deps.JSON {
		b, marshalErr := json.Marshal(result)
		if marshalErr == nil {
			fmt.Println(string(b))
		} else {
			log.Warn("failed to marshal reconcile result", zap.Error(marshalErr))
		}
	} else if result != nil {
		log.Info("reconcile-qdrant complete",
			zap.String("status", result.Status),
			zap.Int("db_scanned", result.DBScanned),
			zap.Int("qd_scanned", result.QDScanned),
			zap.Int("missing", result.DriftSummary[qdrant.DriftMissing]),
			zap.Int("extra", result.DriftSummary[qdrant.DriftExtra]),
			zap.Int("stale", result.DriftSummary[qdrant.DriftStale]),
			zap.Int("version", result.DriftSummary[qdrant.DriftVersion]),
			zap.Int("id_mismatch", result.DriftSummary[qdrant.DriftIdMismatch]),
			zap.Int("repaired_upserts", result.RepairedUpserts),
			zap.Int("repaired_deletes", result.RepairedDeletes),
			zap.Strings("errors", result.Errors),
		)

		fmt.Printf("\nReconcile summary\n")
		fmt.Printf("  Status:            %s\n", result.Status)
		fmt.Printf("  Collection:        %s\n", targetCollection)
		fmt.Printf("  DB rows scanned:   %d\n", result.DBScanned)
		fmt.Printf("  Qdrant pts scanned: %d\n", result.QDScanned)
		fmt.Printf("  Drift summary:\n")
		fmt.Printf("    MISSING:      %d\n", result.DriftSummary[qdrant.DriftMissing])
		fmt.Printf("    EXTRA:        %d\n", result.DriftSummary[qdrant.DriftExtra])
		fmt.Printf("    STALE:        %d\n", result.DriftSummary[qdrant.DriftStale])
		fmt.Printf("    VERSION:      %d\n", result.DriftSummary[qdrant.DriftVersion])
		fmt.Printf("    ID_MISMATCH:  %d\n", result.DriftSummary[qdrant.DriftIdMismatch])
		if !deps.Apply {
			fmt.Printf("  Dry-run: no repairs applied (re-run with --apply to fix)\n")
		} else {
			fmt.Printf("  Repairs applied:\n")
			fmt.Printf("    upserts:      %d\n", result.RepairedUpserts)
			fmt.Printf("    deletes:      %d\n", result.RepairedDeletes)
		}
		if len(result.Errors) > 0 {
			fmt.Printf("  Errors:\n")
			for _, e := range result.Errors {
				fmt.Printf("    - %s\n", e)
			}
		}
	}

	if runErr != nil {
		return fmt.Errorf("reconcile run failed: %w", runErr)
	}
	if result != nil && result.Status == qdrant.ReconStatusScanIncomplete {
		// Surface as an error so CI runs fail loudly when the scan didn't
		// complete (QDRANT-005B fail-closed contract).
		return fmt.Errorf("reconcile aborted: scan incomplete (collection=%s); repairs NOT applied", targetCollection)
	}
	return nil
}
