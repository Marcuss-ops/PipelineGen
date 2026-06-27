// cmd/admin/cleanup_qdrant_legacy.go — unified legacy cleanup (PR 14, June 2026).
//
// The single canonical cleanup surface for legacy Qdrant content.
// Aggregates all 8 classification buckets per the package
// internal/application/qdrant/legacyaudit (NON-MEDIA, METADATA_JSON,
// HIDDEN_TEMP, INVALID_VECTORS, WRONG_DIM, LEGACY_LIFECYCLE,
// LEGACY_LOCATOR, NON_CANONICAL_POINT_ID) and emits a single
// dry-run-first report.
//
// Usage:
//
//	go run ./cmd/admin cleanup-qdrant-legacy             # dry-run (default — no mutations)
//	go run ./cmd/admin cleanup-qdrant-legacy --apply     # apply via outbox Dispatcher
//	go run ./cmd/admin cleanup-qdrant-legacy --json     # machine-readable dry-run
//	go run ./cmd/admin cleanup-qdrant-legacy --limit=200 # cap scan page size
//
// --apply invokes the canonical outbox Dispatcher.EnqueueAndDelete
// path. NEVER issues direct DELETE FROM media_assets — that would
// bypass the outbox events pipeline and orphan Qdrant vectors. The
// user spec is explicit on this point.
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
)

// cleanLegacyDeps is the parsed flag bag for runCleanupQdrantLegacy.
type cleanLegacyDeps struct {
	Apply bool
	JSON  bool
	Limit int
}

// parseCleanLegacyArgs peels --apply / --json / --limit=N out of args.
// Unknown flags produce an error so the operator gets a fail-loud
// signal instead of a silently-mis-flagged invocation.
func parseCleanLegacyArgs(args []string) (cleanLegacyDeps, error) {
	fs := flag.NewFlagSet("cleanup-qdrant-legacy", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apply := fs.Bool("apply", false, "Apply via outbox Dispatcher (default: dry-run only)")
	jsonOut := fs.Bool("json", false, "Machine-readable JSON output")
	limit := fs.Int("limit", 0, "Optional cap on per-page scan size (Qdrant max=1000)")
	if err := fs.Parse(args); err != nil {
		return cleanLegacyDeps{}, err
	}
	if *limit < 0 {
		return cleanLegacyDeps{}, fmt.Errorf("cleanup-qdrant-legacy: --limit must be >= 0, got %d", *limit)
	}
	return cleanLegacyDeps{Apply: *apply, JSON: *jsonOut, Limit: *limit}, nil
}

// runCleanupQdrantLegacy is the cmd/admin/main.go entry point.
//
// Pipeline:
//  1. Load + validate flag bag.
//  2. If qdrant is disabled in config → error (the cleaning target
//     is always Qdrant — without qdrant, the command has nothing to
//     do).
//  3. Open SQLite media DB.
//  4. Build the production composition root via app.InitComposition
//     (this satisfies the "production constructor" pre-condition
//     while exposing root.Outbox.Dispatcher for the apply step).
//  5. Resolve the active Qdrant collection via qdrant.Client +
//     DefaultV3Schema's runtime alias (the canonical single point of
//     truth — see godlike/06 §Configuration).
//  6. Wrap client.ScrollPoints behind a legacyaudit.QdrantScanner
//     port adapter that translates infra ScrollPoint → legacyaudit
//     ScrollPoint with NextOffset preservation.
//  7. Dry-run: legacyaudit.Classify walks the collection, builds the
//     Report, prints human-readable string OR JSON.
//  8. Apply (only with --apply): for each asset whose point hit any
//     bucket, dispatch a single outbox.Dispatcher.EnqueueAndDelete —
//     the canonical deletion path. NO direct media_assets mutation.
//
// Failure handling: infra errors (Qdrant unreachable, sqlite open
// failure, etc.) exit non-zero. In --json mode the partial report
// is still emitted so CI can scrape class-level counters.
func runCleanupQdrantLegacy(args []string) error {
	deps, err := parseCleanLegacyArgs(args)
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
				"cleanup-qdrant-legacy requires qdrant.enabled=true",
		)
	}

	ctx := cmdContext()
	log.Info("cleanup-qdrant-legacy starting",
		zap.Bool("apply", deps.Apply),
		zap.Int("limit", deps.Limit))

	// ── Open SQLite media DB (apply path needs the txmgr) ─────────
	sqliteDB, err := storage.OpenSQLiteDB(cfg.Storage.PrimaryDBFullPath(), log)
	if err != nil {
		return fmt.Errorf("open media DB: %w", err)
	}
	defer sqliteDB.Close()

	// ── Build production composition root (provides the
	//    outbox.Dispatcher that the apply step needs to enqueue the
	//    canonical delete events). Cleanup ctx scoped to the
	//    cmdContext() to bound the lifetime as documented in AGENTS.md
	//    §7 for CLI composition roots.
	root, _, rootCleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		return fmt.Errorf("production composition root init failed: %w", err)
	}
	defer rootCleanup()

	// ── Build canonical qdrant schema + client ─────────────────────
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

	// ── Classify ───────────────────────────────────────────────────
	scanLimit := deps.Limit
	if scanLimit == 0 {
		scanLimit = 500
	}
	if scanLimit > 1000 {
		scanLimit = 1000 // Qdrant REST hard cap
	}

	scanner := newQdrantScannerAdapter(client, active, scanLimit)
	report, err := legacyaudit.Classify(ctx, scanner, active, 200)
	if err != nil {
		log.Warn("cleanup-qdrant-legacy: classify returned with errors; printing partial report", zap.Error(err))
		if deps.JSON && report != nil {
			b, _ := json.Marshal(report)
			fmt.Println(string(b))
		}
		return err
	}

	// ── Render dry-run output ──────────────────────────────────────
	if deps.JSON {
		b, _ := json.Marshal(report)
		fmt.Println(string(b))
	} else {
		mode := "DRY-RUN"
		if deps.Apply {
			mode = "APPLY"
		}
		fmt.Printf("=== cleanup-qdrant-legacy: %s ===\n", mode)
		fmt.Println(legacyaudit.StringifyReport(report))
		if !deps.Apply {
			fmt.Println("\nRe-run with --apply to dispatch the canonical outbox DELETE events.")
		}
	}

	// ── Decide whether apply has anything to do ────────────────────
	if !deps.Apply {
		return nil
	}
	if report == nil {
		return nil
	}
	if totalAuditFindings(report) == 0 {
		fmt.Println("Apply: zero legacy findings, no outbox deletion events enqueued.")
		return nil
	}

	// ── Apply (only with --apply) ───────────────────────────────────
	if !deps.JSON {
		fmt.Println("\nApplying via outbox.Dispatcher.EnqueueAndDelete (the canonical deletion path; " +
			"never DELETE FROM media_assets directly)...")
	}

	if root.Outbox == nil || root.Outbox.Dispatcher == nil {
		return fmt.Errorf("apply requested but root.Outbox.Dispatcher is nil (composition root missing outbox bundle)")
	}

	assetIDs := collectAffectedAssetIDs(report)
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

	if !deps.JSON {
		fmt.Printf("Apply: dispatched %d canonical DELETE events to outbox_events.\n", applied)
		fmt.Println("Run `go run ./cmd/admin qdrant-readiness` afterwards to confirm dead_letters=0 and zero legacy audit hits.")
	}
	return nil
}

// totalAuditFindings sums all 8 category buckets so the apply step has
// a single-source-of-truth "anything to do" check. Each bucket counts
// the number of points hitting that category (so a single point can
// contribute >1 to the sum if it fits multiple categories).
func totalAuditFindings(r *legacyaudit.Report) int {
	if r == nil {
		return 0
	}
	a := r.Audit
	return a.NonMediaRow + a.MetadataJSON + a.HiddenTempFiles + a.InvalidVectors +
		a.WrongDimensions + a.LegacyLifecycle + a.LegacyLocatorPayload +
		a.NonCanonicalPointID
}

// collectAffectedAssetIDs extracts the unique asset_id set from the
// per-point audit list. Apply only deletes points whose payload
// identifies a media_assets row (the canonical asset_id); points
// with empty asset_id are surfaced but NOT deleted (the operator can
// inspect manually).
func collectAffectedAssetIDs(r *legacyaudit.Report) []string {
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
		seen[pa.AssetID] = true
		out = append(out, pa.AssetID)
	}
	return out
}

// ── qdrantScannerAdapter: port-fulfilling wrapper around
//    infra qdrant.Client.ScrollPoints for the legacyaudit port.
// ────────────────────────────────────────────────────────────────

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
// NextOffsetExtractor call can drive the cursor. The `limit` arg is
// ignored — pageSize governs the page even if the loop requests more.
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

// NextOffset implements legacyaudit.NextOffsetExtractor. Returns the
// NextOffset returned by the most recent ScrollPoints call. Empty
// value signals end-of-collection to the classify loop.
func (a *qdrantScannerAdapter) NextOffset(_ []legacyaudit.ScrollPoint) string {
	return a.lastNextOffset
}

// Compile-time guards — drift in either interface silhouette breaks
// this assignment at build time (not at first runtime call).
// Mirrors the AGENTS.md Pattern 0 / compile-time assertion rule.
var (
	_ legacyaudit.QdrantScanner        = (*qdrantScannerAdapter)(nil)
	_ legacyaudit.NextOffsetExtractor = (*qdrantScannerAdapter)(nil)
	// _ = sql.ErrNoRows keeps the database/sql import live in case
	// future expansion of the apply loop needs typed errors from
	// SQLite query failures.
	_ = sql.ErrNoRows
)
