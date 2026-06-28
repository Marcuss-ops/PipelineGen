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
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	"github.com/Marcuss-ops/PipelineGen/internal/application/qdrant/legacyaudit"
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

// qdrantMaintDeps holds the parsed flags for runQdrantMaintenance.
type qdrantMaintDeps struct {
	Mode  string // audit | repair-locators | delete-invalid
	JSON  bool
	Limit int
}

// parseQdrantMaintArgs peels the mode (positional) and flags out of args.
func parseQdrantMaintArgs(args []string) (qdrantMaintDeps, error) {
	if len(args) == 0 {
		return qdrantMaintDeps{}, fmt.Errorf("qdrant-maintenance requires a mode: audit, repair-locators, or delete-invalid")
	}
	mode := strings.TrimSpace(args[0])
	if !qdrantMaintenanceModes[mode] {
		return qdrantMaintDeps{}, fmt.Errorf("unknown mode %q — expected audit, repair-locators, or delete-invalid", mode)
	}

	fs := flag.NewFlagSet("qdrant-maintenance", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "Machine-readable JSON output")
	limit := fs.Int("limit", 0, "Optional cap on per-page scan size (Qdrant max=1000)")
	if err := fs.Parse(args[1:]); err != nil {
		return qdrantMaintDeps{}, err
	}
	if *limit < 0 {
		return qdrantMaintDeps{}, fmt.Errorf("qdrant-maintenance: --limit must be >= 0, got %d", *limit)
	}
	return qdrantMaintDeps{Mode: mode, JSON: *jsonOut, Limit: *limit}, nil
}

// runQdrantMaintenance is the cmd/admin/main.go entry point.
//
// Pipeline per mode:
//
//	audit: classify → print report (no mutations)
//	repair-locators: LocatorCleaner.CleanLocators → strip keys
//	delete-invalid: classify → filter out locator-only points →
//	                outbox.Dispatcher.EnqueueAndDelete for remaining assets
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

	// ── repair-locators mode: fast path (no composition root needed) ──
	if deps.Mode == "repair-locators" {
		return runRepairLocators(ctx, cfg, log, deps)
	}

	// ── audit + delete-invalid: need the full composition root ──────
	sqliteDB, err := storage.OpenSQLiteDB(cfg.Storage.PrimaryDBFullPath(), log)
	if err != nil {
		return fmt.Errorf("open media DB: %w", err)
	}
	defer sqliteDB.Close()

	root, _, rootCleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		return fmt.Errorf("production composition root init failed: %w", err)
	}
	defer rootCleanup()

	// Build Qdrant client + resolve active collection.
	schema := qdrant.DefaultV3Schema()
	client := qdrant.NewClient(&qdrant.Config{
		BaseURL: cfg.Qdrant.BaseURL,
		APIKey:  cfg.Qdrant.APIKey,
		Timeout: cfg.Qdrant.Timeout,
	}, log)

	active, err := client.GetAliasTarget(ctx, schema.RuntimeAlias)
	if err != nil {
		return fmt.Errorf("resolve active collection: %w", err)
	}
	if active == "" {
		return fmt.Errorf("runtime alias %q has no target; run EnsureSchema first", schema.RuntimeAlias)
	}

	// ── Classify (shared by audit and delete-invalid) ───────────────
	scanLimit := deps.Limit
	if scanLimit == 0 {
		scanLimit = 500
	}
	if scanLimit > 1000 {
		scanLimit = 1000
	}

	scanner := newQdrantScannerAdapter(client, active, scanLimit)
	report, err := legacyaudit.Classify(ctx, scanner, active, 200)
	if err != nil {
		log.Warn("qdrant-maintenance: classify returned with errors; printing partial report", zap.Error(err))
		if deps.JSON && report != nil {
			b, _ := json.Marshal(report)
			fmt.Println(string(b))
		}
		return err
	}

	// ── audit mode: print and exit ──────────────────────────────────
	if deps.Mode == "audit" {
		if deps.JSON {
			b, _ := json.Marshal(report)
			fmt.Println(string(b))
		} else {
			fmt.Println("=== qdrant-maintenance audit ===")
			fmt.Println(legacyaudit.StringifyReport(report))
			fmt.Println("\nRe-run with 'repair-locators' to strip legacy payload keys,")
			fmt.Println("or 'delete-invalid' to dispatch canonical outbox DELETE events for non-locator findings.")
		}
		return nil
	}

	// ── delete-invalid mode: filter + apply ─────────────────────────
	if report == nil {
		return nil
	}

	// Filter: exclude points whose ONLY finding is LegacyLocatorPayload.
	// Points with locator-only hits are repairable via repair-locators.
	assetIDs := collectNonLocatorAssetIDs(report)
	if len(assetIDs) == 0 {
		if deps.JSON {
			out := map[string]any{
				"applied":    0,
				"total":      0,
				"skipped":    0,
				"audit":      report.Audit,
				"collection": report.Collection,
				"message":    "zero non-locator assets to delete — all findings are locator-only; use repair-locators",
			}
			b, _ := json.Marshal(out)
			fmt.Println(string(b))
		} else {
			fmt.Println("delete-invalid: zero non-locator assets to delete.")
			fmt.Println("(All findings are locator-only; use 'repair-locators' instead.)")
		}
		return nil
	}

	if !deps.JSON {
		fmt.Printf("=== qdrant-maintenance delete-invalid ===\n")
		fmt.Println(legacyaudit.StringifyReport(report))
		fmt.Printf("\nNon-locator assets to delete: %d\n", len(assetIDs))
		fmt.Println("\nApplying via outbox.Dispatcher.EnqueueAndDelete (the canonical deletion path; " +
			"never DELETE FROM media_assets directly)...")
	}

	if root.Outbox == nil || root.Outbox.Dispatcher == nil {
		return fmt.Errorf("apply requested but root.Outbox.Dispatcher is nil (composition root missing outbox bundle)")
	}

	if err := legacyaudit.ValidateAssetIDs(assetIDs); err != nil {
		return fmt.Errorf("apply aborted: %w", err)
	}

	applied := 0
	for _, assetID := range assetIDs {
		if strings.TrimSpace(assetID) == "" {
			continue
		}
		if err := root.Outbox.Dispatcher.EnqueueAndDelete(ctx, assetID); err != nil {
			log.Warn("outbox delete enqueue failed",
				zap.String("asset_id", assetID),
				zap.Error(err))
			continue
		}
		applied++
	}

	if deps.JSON {
		out := map[string]any{
			"applied":    applied,
			"total":      len(assetIDs),
			"skipped":    len(assetIDs) - applied,
			"audit":      report.Audit,
			"collection": report.Collection,
		}
		b, _ := json.Marshal(out)
		fmt.Println(string(b))
	} else {
		fmt.Printf("delete-invalid: dispatched %d canonical DELETE events to outbox_events.\n", applied)
		if applied < len(assetIDs) {
			fmt.Printf("  (%d skipped due to enqueue errors — check logs above)\n", len(assetIDs)-applied)
		}
		fmt.Println("Run `go run ./cmd/admin qdrant-readiness` afterwards to confirm dead_letters=0 and zero legacy audit hits.")
	}
	return nil
}

// ──────────────────────────────────────────────────────────────────────
// Mode: repair-locators
// ──────────────────────────────────────────────────────────────────────

// runRepairLocators executes the repair-locators mode using the canonical
// LocatorCleaner. This is a fast path that does NOT require the full
// composition root — only the Qdrant client.
func runRepairLocators(ctx context.Context, cfg *config.Config, log *zap.Logger, deps qdrantMaintDeps) error {
	log.Info("qdrant-maintenance repair-locators: scanning for legacy drive_link / local_path keys")

	client := qdrant.NewClient(&qdrant.Config{
		BaseURL: cfg.Qdrant.BaseURL,
		APIKey:  cfg.Qdrant.APIKey,
		Timeout: cfg.Qdrant.Timeout,
	}, log)
	schema := qdrant.DefaultV3Schema()
	cleaner := qdrant.NewLocatorCleaner(client, schema, log)

	report, err := cleaner.CleanLocators(ctx, true)
	if err != nil {
		log.Error("qdrant-maintenance repair-locators failed", zap.Error(err))
		if report != nil && deps.JSON {
			b, _ := json.Marshal(report)
			fmt.Println(string(b))
		}
		return err
	}

	if deps.JSON {
		b, _ := json.Marshal(report)
		fmt.Println(string(b))
		return nil
	}

	fmt.Println("=== qdrant-maintenance repair-locators ===")
	fmt.Printf("  Collection:       %s\n", report.Collection)
	fmt.Printf("  Points scrolled:  %d\n", report.TotalPointsScrolled)
	fmt.Printf("  With drive_link:  %d\n", report.PointsWithDriveLink)
	fmt.Printf("  With local_path:  %d\n", report.PointsWithLocalPath)
	fmt.Printf("  Affected (total): %d\n", report.PointsAffected)
	fmt.Printf("  Keys removed:     %d\n", report.KeysRemoved)
	fmt.Printf("  Batch calls:      %d\n", report.BatchCount)
	if len(report.Errors) > 0 {
		fmt.Printf("  Errors:           %d\n", len(report.Errors))
		for i, e := range report.Errors {
			fmt.Printf("    [%d] %s\n", i, e)
		}
	}
	fmt.Println("\nRun 'qdrant-maintenance audit' to confirm zero LegacyLocatorPayload hits.")
	return nil
}

// ──────────────────────────────────────────────────────────────────────
// delete-invalid: locator-excluded asset ID collection
// ──────────────────────────────────────────────────────────────────────

// collectNonLocatorAssetIDs extracts asset IDs from the audit report,
// excluding points whose ONLY finding is LegacyLocatorPayload (category 7).
// Points with locator payload AND other categories are still included —
// the policy is "locator-only points are repairable, not deletable".
func collectNonLocatorAssetIDs(r *legacyaudit.Report) []string {
	if r == nil || len(r.Points) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(r.Points))
	out := make([]string, 0, len(r.Points))
	for _, pa := range r.Points {
		if pa.AssetID == "" {
			continue
		}
		if seen[pa.AssetID] {
			continue
		}
		if isLocatorOnly(pa.Categories) {
			continue
		}
		seen[pa.AssetID] = true
		out = append(out, pa.AssetID)
	}
	return out
}

// isLocatorOnly returns true when the ONLY positive category is
// LegacyLocatorPayload — all other categories are zero. Points hitting
// locator + any other category are considered deletable.
func isLocatorOnly(c legacyaudit.Categories) bool {
	nonLocatorHits := c.NonMediaRow + c.MetadataJSON + c.HiddenTempFiles +
		c.InvalidVectors + c.WrongDimensions + c.LegacyLifecycle +
		c.NonCanonicalPointID
	return nonLocatorHits == 0 && c.LegacyLocatorPayload > 0
}

// ──────────────────────────────────────────────────────────────────────
// qdrantScannerAdapter (shared with the former cleanup_qdrant_legacy.go)
// ──────────────────────────────────────────────────────────────────────

// qdrantScannerAdapter translates internal/infrastructure/qdrant.ScrollResult
// into []legacyaudit.ScrollPoint so the application-layer audit package
// does not import the infra layer.
//
// The adapter is single-goroutine (legacyaudit.Classify drives both
// ScrollPoints and NextOffset from one goroutine), so lastNextOffset
// is a plain field — no sync/atomic needed.
type qdrantScannerAdapter struct {
	client         *qdrant.Client
	activeCol      string
	pageSize       int
	lastNextOffset string
}

// newQdrantScannerAdapter constructs the adapter wired against the
// canonical qdrant.Client. pageSize is hard-clamped to <=1000 —
// Qdrant REST cannot return more than 1000 points per scroll page.
func newQdrantScannerAdapter(client *qdrant.Client, activeCol string, pageSize int) *qdrantScannerAdapter {
	if pageSize <= 0 {
		pageSize = 500
	}
	if pageSize > 1000 {
		pageSize = 1000
	}
	return &qdrantScannerAdapter{client: client, activeCol: activeCol, pageSize: pageSize}
}

// ScrollPoints returns up to limit points from the next page and
// stashes the NextOffset in lastNextOffset so the classify loop's
// NextOffsetExtractor call can drive the cursor.
func (a *qdrantScannerAdapter) ScrollPoints(ctx context.Context, _ string, offset string, _ int) ([]legacyaudit.ScrollPoint, error) {
	if a.client == nil {
		return nil, errors.New("qdrantScannerAdapter: client is nil")
	}
	res, err := a.client.ScrollPoints(ctx, a.activeCol, offset, a.pageSize, nil)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, nil
	}
	out := make([]legacyaudit.ScrollPoint, 0, len(res.Points))
	for _, p := range res.Points {
		out = append(out, legacyaudit.ScrollPoint{
			ID:      p.ID,
			Payload: p.Payload,
		})
	}
	a.lastNextOffset = res.NextOffset
	return out, nil
}

// NextOffset implements legacyaudit.NextOffsetExtractor.
func (a *qdrantScannerAdapter) NextOffset(_ []legacyaudit.ScrollPoint) string {
	return a.lastNextOffset
}

// Compile-time guards.
var (
	_ legacyaudit.QdrantScanner       = (*qdrantScannerAdapter)(nil)
	_ legacyaudit.NextOffsetExtractor = (*qdrantScannerAdapter)(nil)
	// Keep the database/sql import live in case future expansion of the
	// apply loop needs typed errors from SQLite query failures.
	_ = sql.ErrNoRows
)
