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
//     qdrant.Client.DeletePayloadKeys (canonical for legacy key
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
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/qdrant/reconciler"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
	qdrantschema "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
)

// (database/sql is required for outboxRepairAdapter.db which is *sql.DB — the OpenSQLiteDB return type)

// reconcileQdrantDeps holds the parsed flags for runReconcileQdrant.
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
			n, err := parsePositiveFlag(a, "--batch-size")
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

// runReconcileQdrant is the entry point registered in cmd/admin/main.go.
//
// Pipeline:
//  1. Load config; require qdrant.enabled=true.
//  2. Open the media DB.
//  3. Build canonical stack: DefaultV3Schema, asset store, qdrant.Client.
//  4. Resolve target collection (override > runtime alias target).
//  5. Wire service ports from the canonical concrete adapters.
//  6. Run Service.Reconcile.
//  7. Pretty-print (or JSON-only) the resulting ReconcileReport.
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
			"qdrant is disabled in config; reconcile-qdrant requires qdrant.enabled=true",
		)
	}

	ctx := cmdContext()

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
	schema := qdrant.DefaultV3Schema()
	assetStore := qdrant.NewSQLiteAssetStore(sqliteDB.DB)
	client := qdrant.NewClient(&qdrant.Config{
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
	outboxAdapter := &outboxRepairAdapter{
		db:            sqliteDB.DB,
		outboxRepo:    outboxEventsRepo,
		schemaVersion: schema.Version,
	}
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
		Metrics:      qdrant.PromMetricsAdapter{},
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
	fmt.Println("  --- Counts (per category):")
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

// qdrantListerAdapter wraps qdrant.Client.ScrollPoints to satisfy
// reconciler.QdrantLister. The reconciler sees only PointSnapshot (no
// leak of qdrant.ScrollPoint into the application layer).
type qdrantListerAdapter struct {
	client *qdrant.Client
}

func (a *qdrantListerAdapter) ScrollPoints(ctx context.Context, collection string, offset string, limit int) (reconciler.Points, error) {
	res, err := a.client.ScrollPoints(ctx, collection, offset, limit, nil)
	if err != nil {
		return reconciler.Points{}, err
	}
	out := reconciler.Points{
		NextOffset: res.NextOffset,
		Items:      make([]reconciler.PointSnapshot, len(res.Points)),
	}
	for i, p := range res.Points {
		out.Items[i] = reconciler.PointSnapshot{ID: p.ID, Payload: p.Payload}
	}
	return out, nil
}

// qdrantPayloadAdapter wraps qdrant.Client.DeletePayloadKeys. The
// collection is captured at construction so the reconciler call sites
// stay simple.
type qdrantPayloadAdapter struct {
	client *qdrant.Client
}

func (a *qdrantPayloadAdapter) DeletePayloadKeys(ctx context.Context, collection string, keys []string, pointIDs []string) error {
	return a.client.DeletePayloadKeys(ctx, collection, keys, pointIDs)
}

// outboxRepairAdapter satisfies reconciler.OutboxRepairEnqueuer by
// writing directly to outbox_events + lightly bumping media_assets,
// bypassing outbox.Dispatcher.
//
// Rationale (vs. going through outbox.Dispatcher):
//   - Dispatcher.EnqueueAndIndex demands a fully-populated *asset.Asset
//     and constructs an event_key derived from clipindexer package vars
//   - a content_hash supplied by the caller. Calling it from
//     reconcile-repair would require synthesising an Asset and choosing
//     a content hash that varies per reconcile run — both undesirable.
//   - Reconcile-repair does NOT need the metadata-write side-effect of
//     Dispatcher (UpdateClipTx). All reconcile-repair needs is to
//     ENQUEUE an asset.index.requested.v1 event for the worker to
//     re-run IndexClip with the canonical row's current payload.
//   - Wiring direct to outboxevents.Repository keeps the adapter thin
//     (one tx per enqueue, v1 envelope built inline from a typed
//     schema-version constant) and avoids the ClipsUpserter dependency
//     cycle (production assets.ClipsRepository is NOT visible at this
//     admin path).
//
// Idempotency:
//
//   - Delete (EnqueueDelete): event_key is deterministic
//     ("delete:<assetID>"). Re-running --apply on the same asset is
//     collapsed at the SQLite level by ON CONFLICT(event_key)
//     DO NOTHING — only the first run enqueues, subsequent runs
//     are no-ops. The event_id field is a per-call UUID for audit
//     tracing (required by IndexDeleteHandler) and is NOT used in
//     the event_key.
//
//   - Reindex (EnqueueReindex): event_key is deterministic per
//     (assetID, target_schema_version, full_content_hash) tuple,
//     built via outboxevents.BuildReindexEnvelopeV1 — the canonical
//     envelope builder. PR 11 (June 2026) replaces the prior
//     uuid-suffixed key with this deterministic shape so two
//     consecutive `reconcile-qdrant --apply` runs on the same
//     asset (no content change) collapse to a single outbox_events
//     row via ON CONFLICT (event_key) DO NOTHING. A hash change
//     produces a fresh row and the worker downstream re-evaluates
//     against the new source_version (supersede gate still owns
//     the "is the event actually still current?" question at
//     execution time).
type outboxRepairAdapter struct {
	db            *sql.DB
	outboxRepo    *outboxevents.Repository
	schemaVersion string
}

// EnqueueReindex inserts an asset.index.requested.v1 outbox event for
// the supplied assetID. The event_key is deterministic per
// (assetID, target_schema_version, full_content_hash) tuple, built via
// outboxevents.BuildReindexEnvelopeV1 (the canonical envelope
// builder) — see that function's idempotency invariants in PR 11
// (June 2026). Two consecutive reconciler --apply runs on the same
// asset (no content change) collapse to a single outbox_events row
// via ON CONFLICT (event_key) DO NOTHING.
//
// The content_hash lookup happens INSIDE the producer tx so the
// captured value is exactly the row-state at the moment we commit
// (Snapshot isolation: the row is read through the same tx that
// stamps updated_at + inserts the outbox row). Empty content_hash
// is fail-closed — without a fingerprint we cannot derive a
// deterministic event_key, so we abort rather than emit a
// silently-collapsing event that the worker could never route
// (the worker compares payload.source_version against
// metadata_json.$.content_hash; an empty source_version matches
// every empty row, which would silently no-op at execution time).
//
// The hash priority mirrors the consumer-side readSourceVersion
// priority list in application/jobs/outbox/indexing.go so the
// producer and the worker agree on what counts as "the current
// fingerprint" without a JOIN round-trip:
//
//  1. metadata_json.$.content_hash  ← dispatcher atomic write
//  2. metadata_json.$.file_hash     ← non-dispatcher ingest fallback
//  3. media_assets.file_hash        ← legacy top-level column
//
// This list is duplicated here (vs imported) so the cmd/admin
// package does not pick up the indexing.go dependency for what is
// really a 3-line COALESCE pattern; if the priority changes,
// the change propagates here + there.
//
// PR 11 (June 2026, follow-up): the content_hash is now PASSED BY
// THE CALLER, not fetched here. The canonical reconciler flow
// (internal/application/qdrant/reconciler/service.go) already
// calls assets.SourceVersionFor(...) once per asset and threads
// the value here, so duplicating the COALESCE priority chain
// (metadata_json.$.content_hash → metadata_json.$.file_hash →
// media_assets.file_hash) inside this adapter is misuse-prone.
// Callers MUST hand in a non-empty contentHash; the adapter is
// fail-closed on empty (deterministic event_key requires a
// fingerprint). See
// internal/infrastructure/database/sqlite/assets/source_version.go
// + source_version_test.go for the regression pin across all
// four priority slots (including the legacy top-level column).
func (a *outboxRepairAdapter) EnqueueReindex(ctx context.Context, assetID, contentHash string) error {
	if assetID == "" {
		return errors.New("outboxRepairAdapter.EnqueueReindex: assetID must not be empty")
	}
	if contentHash == "" {
		return errors.New("outboxRepairAdapter.EnqueueReindex: contentHash must not be empty — PR 11 contract (deterministic event_key requires a fingerprint; caller must pre-fetch via assets.SourceVersionFor before invoking)")
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // standard commit-or-rollback idiom

	// Light parity bump: refresh updated_at so monitors can see the
	// reconcile-repair touched the row. We do NOT mutate source_version
	// (the worker's supersede gate reads source_version from metadata).
	nowStr := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx,
		`UPDATE media_assets SET updated_at = ? WHERE id = ?`,
		nowStr, assetID,
	); err != nil {
		return fmt.Errorf("update updated_at %s: %w", assetID, err)
	}

	eventKey, payloadJSON, err := outboxevents.BuildReindexEnvelopeV1(assetID, a.schemaVersion, contentHash, time.Now())
	if err != nil {
		return fmt.Errorf("build reindex envelope: %w", err)
	}
	if err := a.outboxRepo.Enqueue(
		ctx, tx,
		outboxevents.EventAssetIndexRequested,
		assetID, "media_asset",
		payloadJSON, eventKey,
	); err != nil {
		return fmt.Errorf("enqueue outbox reindex event: %w", err)
	}
	return tx.Commit()
}

// EnqueueDelete inserts an asset.index.delete_requested.v1 outbox event
// for the supplied assetID. The event_key is deterministic
// ("delete:<assetID>") so re-running --apply on the same asset is
// collapsed at the SQLite level by ON CONFLICT(event_key) DO NOTHING.
// The event_id field is a per-call UUID for audit tracing (required by
// IndexDeleteHandler.Handle) and is NOT used in the event_key.
func (a *outboxRepairAdapter) EnqueueDelete(ctx context.Context, assetID string) error {
	if assetID == "" {
		return errors.New("outboxRepairAdapter.EnqueueDelete: assetID must not be empty")
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // standard commit-or-rollback idiom

	// Stamp DELETE_PENDING so dashboards show the in-flight delete
	// even if the worker crashes mid-process.
	nowStr := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx,
		`UPDATE media_assets SET lifecycle_state = 'DELETE_PENDING', deleted_at = ?, updated_at = ? WHERE id = ?`,
		nowStr, nowStr, assetID,
	); err != nil {
		return fmt.Errorf("set DELETE_PENDING %s: %w", assetID, err)
	}

	eventID := uuid.NewString()
	eventKey := "delete:" + assetID
	payload := map[string]any{
		"schema_version":  "asset.index.delete_requested.v1",
		"event_id":        eventID,
		"asset_id":        assetID,
		"requested_at":    nowStr,
		"idempotency_key": eventKey,
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal v1 delete payload: %w", err)
	}
	if err := a.outboxRepo.Enqueue(
		ctx, tx,
		outboxevents.EventAssetIndexDeleteRequested,
		assetID, "media_asset",
		string(payloadJSON), eventKey,
	); err != nil {
		return fmt.Errorf("enqueue outbox delete event: %w", err)
	}
	return tx.Commit()
}

// reconcileReaderAdapter wraps qdrant.SQLiteAssetStore.ListAssetsForReconcile.
type reconcileReaderAdapter struct {
	store *qdrant.SQLiteAssetStore
}

func (a *reconcileReaderAdapter) ListForReconcile(ctx context.Context, includeLifecycleStates []string) ([]reconciler.AssetSnapshot, error) {
	rows, err := a.store.ListAssetsForReconcile(ctx, includeLifecycleStates)
	if err != nil {
		return nil, err
	}
	out := make([]reconciler.AssetSnapshot, len(rows))
	for i, r := range rows {
		out[i] = reconciler.AssetSnapshot{
			ID:             r.ID,
			WorkspaceID:    r.WorkspaceID,
			LifecycleState: r.LifecycleState,
			ContentHash:    r.ContentHash,
		}
	}
	return out, nil
}
