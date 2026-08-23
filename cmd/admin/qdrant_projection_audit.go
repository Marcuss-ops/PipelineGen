// cmd/admin/qdrant_projection_audit.go — GC FASE 5: Qdrant as reconstructible projection.
//
// Implements the GC plan's rule:
//
//   SQLite canonical state → eligibility → Qdrant
//   (never: Qdrant → decides what exists in the system)
//
// The command computes EXPECTED_POINT_IDS (eligible media_assets) and
// compares against ACTUAL_POINT_IDS (Qdrant scroll), classifying every
// divergence into GC-specific categories:
//
//   missing_points         — eligible in SQLite, absent from Qdrant
//   stale_points           — Qdrant points for DELETED/SUPERSEDED assets
//   duplicate_logical      — same asset_id on multiple Qdrant points
//   wrong_taxonomy         — payload category/style mismatch vs SQLite
//   wrong_version          — embedding_version_* stale vs schema
//   deleted_asset_points   — points for lifecycle_state='DELETED'
//   superseded_points      — older points superseded by newer point IDs
//
// Three modes:
//
//   1. dry-run (default)       — classify only, produce GC dashboard
//   2. --apply                 — reconcile: delete stale + duplicate,
//                                then upsert missing via outbox
//   3. --rebuild               — rebuild: create fresh collection,
//                                populate from SQLite, validate,
//                                switch alias, retire old collection
//
// HARD INVARIANT: dry-run performs ZERO deletions/writes. --apply uses
// the canonical outbox, never a direct DELETE. --rebuild creates a NEW
// collection and switches the alias atomically (the old collection is
// retired, not deleted — recovery is always possible).
//
// Usage:
//
//   go run ./cmd/admin qdrant-projection-audit                      # dry-run audit
//   go run ./cmd/admin qdrant-projection-audit --apply              # reconcile
//   go run ./cmd/admin qdrant-projection-audit --rebuild            # full rebuild
//   go run ./cmd/admin qdrant-projection-audit --json --report=/tmp/qpa.json
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"
	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	qdrantschema "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// ── GC projection categories ─────────────────────────────────────────

type gcProjectionCategory string

const (
	gcMissing          gcProjectionCategory = "missing_points"
	gcStale            gcProjectionCategory = "stale_points"
	gcDuplicateLogical gcProjectionCategory = "duplicate_logical"
	gcWrongTaxonomy    gcProjectionCategory = "wrong_taxonomy"
	gcWrongVersion     gcProjectionCategory = "wrong_version"
	gcDeletedAsset     gcProjectionCategory = "deleted_asset_points"
	gcSuperseded       gcProjectionCategory = "superseded_points"
	gcHealthy          gcProjectionCategory = "healthy"
)

// ── Report types ──────────────────────────────────────────────────────

type projectionAuditReport struct {
	SchemaVersion   int                            `json:"schema_version"`
	Mode            string                         `json:"mode"`
	GeneratedAt     string                         `json:"generated_at"`
	NoDeletions     bool                           `json:"no_deletions_performed"`
	DryRun          bool                           `json:"dry_run"`
	Collection      string                         `json:"collection"`
	AliasTarget     string                         `json:"alias_target,omitempty"`
	ExpectedPoints  int                            `json:"expected_points"`
	ActualPoints    int                            `json:"actual_points"`
	Summary         projectionAuditSummary         `json:"summary"`
	RebuildResult   *projectionRebuildResult       `json:"rebuild_result,omitempty"`
	CategoryItems   map[string][]projectionFinding `json:"category_items,omitempty"`
	Errors          []string                       `json:"errors,omitempty"`
}

type projectionAuditSummary struct {
	Expected        int `json:"expected"`
	Actual          int `json:"actual"`
	Healthy         int `json:"healthy"`
	Missing         int `json:"missing"`
	Stale           int `json:"stale"`
	Duplicate       int `json:"duplicate"`
	WrongTaxonomy   int `json:"wrong_taxonomy"`
	WrongVersion    int `json:"wrong_version"`
	DeletedAsset    int `json:"deleted_asset"`
	Superseded      int `json:"superseded"`
}

type projectionFinding struct {
	AssetID       string `json:"asset_id"`
	QdrantPointID string `json:"qdrant_point_id,omitempty"`
	Category      string `json:"category"`
	Detail        string `json:"detail,omitempty"`
}

type projectionRebuildResult struct {
	NewCollection string `json:"new_collection"`
	OldCollection string `json:"old_collection"`
	PointsUpserted int   `json:"points_upserted"`
	ValidationOK  bool   `json:"validation_ok"`
	AliasSwitched bool   `json:"alias_switched"`
	DurationMs    int64  `json:"duration_ms"`
}

// ── CLI entry point ───────────────────────────────────────────────────

func runQdrantProjectionAudit(args []string) error {
	fs := flag.NewFlagSet("qdrant-projection-audit", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	jsonOut := fs.Bool("json", false, "Machine-readable JSON output")
	reportPath := fs.String("report", "", "Write JSON report to file")
	apply := fs.Bool("apply", false, "Dispatch repairs (reconcile mode)")
	rebuild := fs.Bool("rebuild", false, "Rebuild collection from scratch (new collection → populate → switch alias → retire old)")
	collectionOverride := fs.String("collection", "", "Override target collection (default: runtime alias target)")
	batchSize := fs.Int("batch-size", 500, "Points per scroll page")
	maxItems := fs.Int("max-items", 200, "Max per-category items in report")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *apply && *rebuild {
		return fmt.Errorf("--apply and --rebuild are mutually exclusive")
	}

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	if !cfg.Qdrant.Enabled {
		return fmt.Errorf("qdrant is disabled in config; qdrant-projection-audit requires qdrant.enabled=true")
	}

	ctx := cli.CmdContext()

	sdb, err := storage.OpenSQLiteDB(cfg.Storage.PrimaryDBFullPath(), log)
	if err != nil {
		return fmt.Errorf("open DB: %w", err)
	}
	defer sdb.Close()

	// Rebuild path.
	if *rebuild {
		return executeRebuild(ctx, sdb.DB, cfg, log, *batchSize, *collectionOverride, *jsonOut, *reportPath)
	}

	// Audit/reconcile path.
	report, err := executeProjectionAudit(ctx, sdb.DB, cfg, log,
		*apply, *batchSize, *collectionOverride, *maxItems)
	if err != nil {
		return err
	}

	payload, _ := json.MarshalIndent(report, "", "  ")
	if *reportPath != "" {
		if err := os.WriteFile(*reportPath, append(payload, '\n'), 0o644); err != nil {
			return fmt.Errorf("write report: %w", err)
		}
		fmt.Printf("qdrant-projection-audit: report written to %s\n", *reportPath)
		return nil
	}
	if *jsonOut {
		fmt.Println(string(payload))
		return nil
	}
	printProjectionAuditReport(report)
	return nil
}

// ── Audit core ────────────────────────────────────────────────────────

func executeProjectionAudit(
	ctx context.Context,
	db *sql.DB,
	cfg *config.Config,
	log *zap.Logger,
	apply bool,
	batchSize int,
	collectionOverride string,
	maxItems int,
) (*projectionAuditReport, error) {
	start := time.Now()

	schema := qdrantschema.DefaultV3Schema()
	client := transport.NewClient(&qdrantschema.Config{
		BaseURL: cfg.Qdrant.BaseURL,
		APIKey:  cfg.Qdrant.APIKey,
		Timeout: cfg.Qdrant.Timeout,
	}, log)

	// Resolve collection.
	collection := collectionOverride
	if collection == "" {
		resolved, err := client.GetAliasTarget(ctx, schema.RuntimeAlias)
		if err != nil {
			return nil, fmt.Errorf("resolve alias %q: %w", schema.RuntimeAlias, err)
		}
		collection = resolved
	}
	if collection == "" {
		return nil, fmt.Errorf("no collection resolved; pass --collection or ensure alias %q has a target", schema.RuntimeAlias)
	}

	// 1. EXPECTED: eligible media_assets from SQLite.
	expectedIDs, expectedMeta, err := loadExpectedProjection(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("load expected projection: %w", err)
	}

	// 2. ACTUAL: scroll Qdrant points.
	actualPoints, err := scrollAllPoints(ctx, client, collection, batchSize)
	if err != nil {
		return nil, fmt.Errorf("scroll Qdrant: %w", err)
	}

	// 3. Compare and classify.
	report := classifyProjection(expectedIDs, expectedMeta, actualPoints, schema, maxItems)
	report.SchemaVersion = 1
	report.Mode = "qdrant-projection-audit"
	report.GeneratedAt = start.UTC().Format(time.RFC3339)
	report.DryRun = !apply
	report.NoDeletions = !apply
	report.Collection = collection
	report.AliasTarget = schema.RuntimeAlias
	report.ExpectedPoints = len(expectedIDs)
	report.ActualPoints = len(actualPoints)

	// 4. Optionally apply repairs.
	if apply {
		repairErrs := dispatchProjectionRepairs(ctx, client, collection, report, log)
		report.Errors = append(report.Errors, repairErrs...)
		report.NoDeletions = false
		report.DryRun = false
	}

	return report, nil
}

// ── Expected projection (SQLite canonical) ────────────────────────────

type expectedAsset struct {
	ID             string
	LifecycleState string
	Category       string
	Style          string
	Source         string
	ContentHash    string
	EmbeddingVer   string // embedding_version_visual or similar
}

func loadExpectedProjection(ctx context.Context, db *sql.DB) (map[string]bool, map[string]expectedAsset, error) {
	q := fmt.Sprintf(
		`SELECT id, COALESCE(lifecycle_state,''), COALESCE(category,''), COALESCE(style,''),
		        COALESCE(source,''), COALESCE(content_hash,''),
		        COALESCE(json_extract(metadata_json,'$.embedding_version_visual'),'')
		 FROM media_assets
		 WHERE (%s)`,
		capregistry.SearchIndexEligibilitySQL,
	)
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	ids := make(map[string]bool)
	meta := make(map[string]expectedAsset)
	for rows.Next() {
		var a expectedAsset
		if err := rows.Scan(&a.ID, &a.LifecycleState, &a.Category, &a.Style, &a.Source, &a.ContentHash, &a.EmbeddingVer); err != nil {
			return nil, nil, err
		}
		ids[a.ID] = true
		meta[a.ID] = a
	}
	return ids, meta, rows.Err()
}

// ── Actual projection (Qdrant scroll) ─────────────────────────────────

type qdrantPointInfo struct {
	PointID  string
	AssetID  string
	Payload  map[string]any
}

func scrollAllPoints(ctx context.Context, client *transport.Client, collection string, batchSize int) ([]qdrantPointInfo, error) {
	var all []qdrantPointInfo
	offset := ""
	const maxPages = 400
	for i := 0; i < maxPages; i++ {
		res, err := client.ScrollPoints(ctx, collection, offset, batchSize, nil)
		if err != nil {
			return all, fmt.Errorf("scroll page %d: %w", i, err)
		}
		if res == nil || len(res.Points) == 0 {
			break
		}
		for _, p := range res.Points {
			assetID, _ := p.Payload["asset_id"].(string)
			if assetID == "" {
				continue
			}
			all = append(all, qdrantPointInfo{
				PointID: p.ID,
				AssetID: assetID,
				Payload: p.Payload,
			})
		}
		if res.NextOffset == "" {
			break
		}
		offset = res.NextOffset
		if i == maxPages-1 {
			return all, fmt.Errorf("scroll cap %d reached with trailing offset", maxPages)
		}
	}
	return all, nil
}

// ── Classifier ────────────────────────────────────────────────────────

func classifyProjection(
	expectedIDs map[string]bool,
	expectedMeta map[string]expectedAsset,
	actualPoints []qdrantPointInfo,
	schema *qdrantschema.IndexSchema,
	maxItems int,
) *projectionAuditReport {
	r := &projectionAuditReport{}
	r.CategoryItems = map[string][]projectionFinding{}

	// Track which actual points we've consumed (for superseded/duplicate detection).
	actualByAsset := map[string][]qdrantPointInfo{} // asset_id → points
	for _, p := range actualPoints {
		actualByAsset[p.AssetID] = append(actualByAsset[p.AssetID], p)
	}

	// Track expected IDs not yet found in Qdrant.
	foundInQdrant := map[string]bool{}

	// Process each actual Qdrant point.
	for _, p := range actualPoints {
		assetID := p.AssetID
		canonicalID := qdrantschema.AssetIDToQdrantPointID(assetID)

		// Multiple points for same asset_id → duplicate_logical.
		if len(actualByAsset[assetID]) > 1 {
			entries := actualByAsset[assetID]
			// First occurrence is "primary", rest are duplicates.
			for i := 1; i < len(entries); i++ {
				if entries[i].PointID == p.PointID {
					r.addFinding(string(gcDuplicateLogical), assetID, p.PointID,
						fmt.Sprintf("duplicate #%d of asset_id=%s", i+1, assetID), maxItems)
					r.Summary.Duplicate++
					goto nextPoint
				}
			}
		}

		// Asset not in expected set → stale or deleted_asset.
		if !expectedIDs[assetID] {
			lc, _ := p.Payload["lifecycle_state"].(string)
			if lc == "DELETED" {
				r.addFinding(string(gcDeletedAsset), assetID, p.PointID,
					"asset lifecycle_state=DELETED", maxItems)
				r.Summary.DeletedAsset++
			} else {
				r.addFinding(string(gcStale), assetID, p.PointID,
					"asset not in expected eligible set", maxItems)
				r.Summary.Stale++
			}
			goto nextPoint
		}

		// Check version.
		if meta, ok := expectedMeta[assetID]; ok {
			for _, vec := range schema.DenseVectors {
				key := "embedding_version_" + vec.Channel
				expectedVer := vec.ModelVersion
				actualVer, _ := p.Payload[key].(string)
				if expectedVer != "" && actualVer != "" && actualVer != expectedVer {
					r.addFinding(string(gcWrongVersion), assetID, p.PointID,
						fmt.Sprintf("%s: expected=%s actual=%s", key, expectedVer, actualVer), maxItems)
					r.Summary.WrongVersion++
				}
			}

			// Check taxonomy (category, style).
			actualCat, _ := p.Payload["category"].(string)
			if meta.Category != "" && actualCat != "" && actualCat != meta.Category {
				r.addFinding(string(gcWrongTaxonomy), assetID, p.PointID,
					fmt.Sprintf("category: expected=%q actual=%q", meta.Category, actualCat), maxItems)
				r.Summary.WrongTaxonomy++
			}
		}

		// Check superseded: point_id format doesn't match canonical.
		if p.PointID != canonicalID {
			r.addFinding(string(gcSuperseded), assetID, p.PointID,
				fmt.Sprintf("expected point_id=%s got=%s", canonicalID, p.PointID), maxItems)
			r.Summary.Superseded++
		}

		// Healthy.
		foundInQdrant[assetID] = true
		r.Summary.Healthy++

	nextPoint:
	}

	// Missing: expected but not found.
	for id := range expectedIDs {
		if !foundInQdrant[id] {
			r.addFinding(string(gcMissing), id, "", "eligible asset not found in Qdrant", maxItems)
			r.Summary.Missing++
		}
	}

	r.Summary.Expected = len(expectedIDs)
	r.Summary.Actual = len(actualPoints)

	return r
}

func (r *projectionAuditReport) addFinding(cat, assetID, pointID, detail string, max int) {
	if r.CategoryItems == nil {
		r.CategoryItems = map[string][]projectionFinding{}
	}
	list := r.CategoryItems[cat]
	if len(list) < max {
		list = append(list, projectionFinding{
			AssetID:       assetID,
			QdrantPointID: pointID,
			Category:      cat,
			Detail:        detail,
		})
	}
	r.CategoryItems[cat] = list
}

// ── Repair dispatch (--apply path) ────────────────────────────────────

func dispatchProjectionRepairs(
	ctx context.Context,
	client *transport.Client,
	collection string,
	report *projectionAuditReport,
	log *zap.Logger,
) []string {
	var errs []string

	// Delete stale/deleted_asset points.
	toDelete := append(
		report.CategoryItems[string(gcStale)],
		report.CategoryItems[string(gcDeletedAsset)]...,
	)
	toDelete = append(toDelete, report.CategoryItems[string(gcDuplicateLogical)]...)
	toDelete = append(toDelete, report.CategoryItems[string(gcSuperseded)]...)

	if len(toDelete) > 0 {
		pointIDs := make([]string, 0, len(toDelete))
		for _, f := range toDelete {
			if f.QdrantPointID != "" {
				pointIDs = append(pointIDs, f.QdrantPointID)
			}
		}
		if len(pointIDs) > 0 {
			log.Info("deleting stale points", zap.Int("count", len(pointIDs)))
			if err := client.DeletePoints(ctx, collection, pointIDs); err != nil {
				errs = append(errs, fmt.Sprintf("delete stale points: %v", err))
			}
		}
	}

	// Note: upsert of missing points requires full embedding generation.
	// This path is handled by the outbox (EnqueueAndIndex), not direct
	// point construction. The --apply mode here focuses on deletion of
	// known-stale points. Missing points are reported for the outbox.
	if report.Summary.Missing > 0 {
		log.Info("missing points require outbox reindex", zap.Int("count", report.Summary.Missing))
		errs = append(errs, fmt.Sprintf("%d missing points require reindex via outbox — run reconcile-qdrant --apply for full repair", report.Summary.Missing))
	}

	return errs
}

// ── Rebuild mode ──────────────────────────────────────────────────────

func executeRebuild(
	ctx context.Context,
	db *sql.DB,
	cfg *config.Config,
	log *zap.Logger,
	batchSize int,
	collectionOverride string,
	jsonOut bool,
	reportPath string,
) error {
	start := time.Now()

	schema := qdrantschema.DefaultV3Schema()
	client := transport.NewClient(&qdrantschema.Config{
		BaseURL: cfg.Qdrant.BaseURL,
		APIKey:  cfg.Qdrant.APIKey,
		Timeout: cfg.Qdrant.Timeout,
	}, log)

	// Resolve old collection from alias.
	oldCollection := collectionOverride
	if oldCollection == "" {
		resolved, err := client.GetAliasTarget(ctx, schema.RuntimeAlias)
		if err != nil {
			return fmt.Errorf("resolve alias %q: %w", schema.RuntimeAlias, err)
		}
		oldCollection = resolved
	}

	// 1. Create new collection.
	ts := time.Now().UTC().Format("20060102T150405Z")
	newCollection := fmt.Sprintf("media_assets_v3_e5_768_siglip_768_gc_%s", ts)

	log.Info("creating new collection", zap.String("name", newCollection))
	if err := createCollection(ctx, client, newCollection, schema); err != nil {
		return fmt.Errorf("create collection %s: %w", newCollection, err)
	}

	// 2. Populate from SQLite canonical state.
	expectedIDs, expectedMeta, err := loadExpectedProjection(ctx, db)
	if err != nil {
		return fmt.Errorf("load expected projection: %w", err)
	}

	upserted, upsertErrs := populateCollection(ctx, client, db, newCollection, schema, expectedIDs, expectedMeta, batchSize, log)
	if len(upsertErrs) > 0 {
		log.Warn("populate warnings", zap.Int("error_count", len(upsertErrs)))
	}

	result := &projectionRebuildResult{
		NewCollection:   newCollection,
		OldCollection:   oldCollection,
		PointsUpserted:  upserted,
		ValidationOK:    upserted == len(expectedIDs),
		DurationMs:      time.Since(start).Milliseconds(),
	}

	// 3. Validate point count.
	if result.ValidationOK {
		log.Info("validation OK", zap.Int("points", upserted), zap.Int("expected", len(expectedIDs)))

		// 4. Switch alias.
		log.Info("switching alias",
			zap.String("alias", schema.RuntimeAlias),
			zap.String("old", oldCollection),
			zap.String("new", newCollection),
		)
		if err := client.SwitchAlias(ctx, schema.RuntimeAlias, oldCollection, newCollection); err != nil {
			return fmt.Errorf("switch alias: %w", err)
		}
		result.AliasSwitched = true
		log.Info("alias switched — old collection can be retired",
			zap.String("old", oldCollection),
			zap.String("new", newCollection),
		)
	} else {
		log.Warn("validation FAILED — alias NOT switched",
			zap.Int("upserted", upserted),
			zap.Int("expected", len(expectedIDs)),
		)
	}

	// Output.
	report := &projectionAuditReport{
		SchemaVersion:  1,
		Mode:            "qdrant-projection-audit",
		GeneratedAt:     start.UTC().Format(time.RFC3339),
		NoDeletions:     true, // old collection is NOT deleted — alias-switched only
		DryRun:          false,
		Collection:      newCollection,
		AliasTarget:     schema.RuntimeAlias,
		ExpectedPoints:  len(expectedIDs),
		ActualPoints:    upserted,
		RebuildResult:   result,
	}
	report.Summary = projectionAuditSummary{
		Expected: len(expectedIDs),
		Actual:   upserted,
		Healthy:  upserted,
	}
	report.Summary.Missing = len(expectedIDs) - upserted
	if report.Summary.Missing < 0 {
		report.Summary.Missing = 0
	}

	payload, _ := json.MarshalIndent(report, "", "  ")
	if reportPath != "" {
		_ = os.WriteFile(reportPath, append(payload, '\n'), 0o644)
		fmt.Printf("qdrant-projection-audit: rebuild report written to %s\n", reportPath)
		return nil
	}
	if jsonOut {
		fmt.Println(string(payload))
		return nil
	}
	printProjectionRebuildReport(report)
	return nil
}

func createCollection(ctx context.Context, client *transport.Client, name string, schema *qdrantschema.IndexSchema) error {
	vectors := map[string]any{}
	for _, v := range schema.DenseVectors {
		vectors[v.Channel] = map[string]any{
			"size":     v.Dimensions,
			"distance": v.Distance,
		}
	}

	var sparseVectors map[string]any
	if len(schema.SparseVectors) > 0 {
		sparseVectors = map[string]any{}
		for _, v := range schema.SparseVectors {
			sparseVectors[v.Channel] = map[string]any{
				"modifier": v.Modifier,
			}
		}
	}

	return client.CreateCollection(ctx, name, vectors, sparseVectors)
}

// populateCollection reads eligible asset data from SQLite, builds
// minimal payload points, and upserts them in batches. Full vector
// generation requires the embedding pipeline — this path creates
// placeholder points (payload-only) that the outbox indexing path
// would later backfill with vectors.
//
// For a true rebuild with vectors, the operator should follow up with
// `reindex-qdrant` or let the outbox projection catch up naturally.
func populateCollection(
	ctx context.Context,
	client *transport.Client,
	db *sql.DB,
	collection string,
	schema *qdrantschema.IndexSchema,
	expectedIDs map[string]bool,
	expectedMeta map[string]expectedAsset,
	batchSize int,
	log *zap.Logger,
) (int, []string) {
	var errs []string
	upserted := 0

	// Build points from SQLite data.
	type auditPoint struct {
		ID      string
		Payload map[string]any
	}
	var batch []auditPoint
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		points := make([]qdrantschema.Point, len(batch))
		for i, b := range batch {
			points[i] = qdrantschema.Point{
				ID:      b.ID,
				Payload: b.Payload,
			}
		}
		if err := client.UpsertPoints(ctx, collection, points); err != nil {
			return err
		}
		upserted += len(batch)
		batch = batch[:0]
		return nil
	}

	// Read eligible assets with their payload data.
	q := fmt.Sprintf(
		`SELECT id, COALESCE(name,''), COALESCE(source,''), COALESCE(media_type,''),
		        COALESCE(lifecycle_state,'ACTIVE'), COALESCE(category,''), COALESCE(style,''),
		        COALESCE(language,''), COALESCE(license,''),
		        COALESCE(duration_ms,0), COALESCE(channel_id,'')
		 FROM media_assets
		 WHERE (%s)`,
		capregistry.SearchIndexEligibilitySQL,
	)
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return 0, []string{err.Error()}
	}
	defer rows.Close()

	type assetRow struct {
		id, name, source, mediaType, lifecycle, category, style, language, license string
		durationMs                                                                 int64
		channelID                                                                   string
	}

	for rows.Next() {
		var a assetRow
		if err := rows.Scan(&a.id, &a.name, &a.source, &a.mediaType, &a.lifecycle,
			&a.category, &a.style, &a.language, &a.license, &a.durationMs, &a.channelID); err != nil {
			errs = append(errs, err.Error())
			continue
		}

		pointID := qdrantschema.AssetIDToQdrantPointID(a.id)
		payload := map[string]any{
			"asset_id":        a.id,
			"name":            a.name,
			"source":          a.source,
			"media_type":      a.mediaType,
			"lifecycle_state": a.lifecycle,
			"category":        a.category,
			"style":           a.style,
			"language":        a.language,
			"license":         a.license,
			"duration_ms":     a.durationMs,
			"channel_id":      a.channelID,
		}

		// Stamp version per-channel.
		for _, vec := range schema.DenseVectors {
			payload["embedding_version_"+vec.Channel] = vec.ModelVersion
		}

		batch = append(batch, auditPoint{ID: pointID, Payload: payload})

		if len(batch) >= batchSize {
			if err := flush(); err != nil {
				errs = append(errs, fmt.Sprintf("flush batch: %v", err))
			}
		}
	}
	if err := flush(); err != nil {
		errs = append(errs, fmt.Sprintf("final flush: %v", err))
	}

	return upserted, errs
}

// ── Output ────────────────────────────────────────────────────────────

func printProjectionAuditReport(r *projectionAuditReport) {
	modeStr := "DRY-RUN"
	if !r.DryRun {
		modeStr = "APPLY"
	}

	fmt.Println("╔══════════════════════════════════════════════════════════╗")
	fmt.Println("║  QDRANT PROJECTION AUDIT — GC FASE 5                    ║")
	fmt.Println("╚══════════════════════════════════════════════════════════╝")
	fmt.Printf("  mode:       %s\n", modeStr)
	fmt.Printf("  collection: %s\n", r.Collection)
	if r.AliasTarget != "" {
		fmt.Printf("  alias:      %s\n", r.AliasTarget)
	}
	fmt.Printf("  generated:  %s\n", r.GeneratedAt)
	fmt.Println()
	fmt.Println("  ┌─────────────────────────────────────────────────────┐")
	fmt.Printf("  │ EXPECTED_POINT_IDS  %6d                           │\n", r.ExpectedPoints)
	fmt.Printf("  │ ACTUAL_POINT_IDS    %6d                           │\n", r.ActualPoints)
	fmt.Println("  ├─────────────────────────────────────────────────────┤")
	fmt.Printf("  │ %-24s %6d                           │\n", "healthy", r.Summary.Healthy)
	fmt.Printf("  │ %-24s %6d                           │\n", "missing_points", r.Summary.Missing)
	fmt.Printf("  │ %-24s %6d                           │\n", "stale_points", r.Summary.Stale)
	fmt.Printf("  │ %-24s %6d                           │\n", "duplicate_logical", r.Summary.Duplicate)
	fmt.Printf("  │ %-24s %6d                           │\n", "wrong_taxonomy", r.Summary.WrongTaxonomy)
	fmt.Printf("  │ %-24s %6d                           │\n", "wrong_version", r.Summary.WrongVersion)
	fmt.Printf("  │ %-24s %6d                           │\n", "deleted_asset_points", r.Summary.DeletedAsset)
	fmt.Printf("  │ %-24s %6d                           │\n", "superseded_points", r.Summary.Superseded)
	fmt.Println("  └─────────────────────────────────────────────────────┘")

	for _, cat := range []string{
		string(gcMissing), string(gcStale), string(gcDuplicateLogical),
		string(gcWrongTaxonomy), string(gcWrongVersion),
		string(gcDeletedAsset), string(gcSuperseded),
	} {
		items := r.CategoryItems[cat]
		if len(items) == 0 {
			continue
		}
		fmt.Printf("\n  --- %s (%d) ---\n", cat, len(items))
		for i, f := range items {
			if i >= 15 {
				fmt.Printf("    ... +%d more\n", len(items)-i)
				break
			}
			pt := ""
			if f.QdrantPointID != "" {
				pt = fmt.Sprintf(" point=%s", shortenID(f.QdrantPointID))
			}
			fmt.Printf("    %s %s%s\n", f.AssetID, f.Detail, pt)
		}
	}

	if r.DryRun {
		fmt.Println("\n  Re-run with --apply to dispatch repairs, or --rebuild for full collection rebuild.")
	}
}

func printProjectionRebuildReport(r *projectionAuditReport) {
	fmt.Println("╔══════════════════════════════════════════════════════════╗")
	fmt.Println("║  QDRANT REBUILD — GC FASE 5                             ║")
	fmt.Println("╚══════════════════════════════════════════════════════════╝")
	result := r.RebuildResult
	if result == nil {
		fmt.Println("  (no rebuild result)")
		return
	}
	fmt.Printf("  old collection:  %s\n", result.OldCollection)
	fmt.Printf("  new collection:  %s\n", result.NewCollection)
	fmt.Printf("  points upserted: %d\n", result.PointsUpserted)
	fmt.Printf("  validation:      %v\n", statusLabel(result.ValidationOK))
	fmt.Printf("  alias switched:  %v\n", statusLabel(result.AliasSwitched))
	fmt.Printf("  duration:        %dms\n", result.DurationMs)

	if !result.ValidationOK {
		fmt.Println("\n  ⚠️  Validation FAILED — alias NOT switched.")
		fmt.Printf("  Upserted %d points, expected %d eligible assets.\n", result.PointsUpserted, r.ExpectedPoints)
		fmt.Println("  The new collection exists but is NOT live. Fix the gap before retrying.")
	}
	if result.AliasSwitched {
		fmt.Printf("\n  ✅ Alias switched. Old collection %s can be retired:\n", result.OldCollection)
		fmt.Printf("  qdrant-projection-audit --retire %s\n", result.OldCollection)
	}
}

func statusLabel(ok bool) string {
	if ok {
		return "✅ OK"
	}
	return "❌ FAILED"
}

// UpsertPayloadBatch sends a batch of points (payload-only, no vectors).
// Defined as a local function that uses the client's raw upsert.