// cmd/admin/qdrant_bucket_report.go — 4-bucket semantic classification
// report (plan item #9, August 2026).
//
// Classifies every media_assets row + every active Qdrant point into the
// four semantic buckets using the SINGLE canonical eligibility boundary
// (capregistry.SearchIndexEligibilitySQL — the SSOT, see
// internal/capabilities/mediaregistry/search_index_policy.go):
//
//	A. healthy                — eligible in SQLite AND present in Qdrant
//	B. missing_in_qdrant      — eligible in SQLite but MISSING in Qdrant
//	                           (projection bug)
//	C. indexed_but_ineligible — index_state='INDEXED' but NOT eligible
//	                           (metadata/state bug)
//	D. orphan_in_qdrant       — present in Qdrant but absent/ineligible in
//	                           SQLite (stale projection)
//
// The report deliberately does NOT compare COUNT(INDEXED) vs COUNT(Qdrant):
// INDEXED is an observed projection result, while eligibility is a property
// of the canonical asset row. The correct comparison is
// SQLiteEligibleAssetIDs vs QdrantActiveAssetIDs — the same boundary the
// Qdrant asset projection (indexing.IndexWriter / asset_store) uses.
//
// Usage:
//
//	go run ./cmd/admin qdrant-bucket-report
//	go run ./cmd/admin qdrant-bucket-report --json
//	go run ./cmd/admin qdrant-bucket-report --report-path=./buckets.json
//	go run ./cmd/admin qdrant-bucket-report --collection=media_assets_v4_xxx
//	go run ./cmd/admin qdrant-bucket-report --batch-size=1000
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"go.uber.org/zap"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	qdrantschema "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
)

// qdrantBucketReportDeps holds the parsed flags for RunQdrantBucketReport.
type qdrantBucketReportDeps struct {
	JSON       bool
	ReportPath string
	Collection string
	BatchSize  int
}

// parseQdrantBucketReportArgs parses CLI args.
// Flags:
//
//	--json                  machine-readable output
//	--report-path=PATH      write JSON report to disk
//	--collection=NAME       override Qdrant collection (default: active alias target)
//	--batch-size=N          points per scroll page (default 500)
func parseQdrantBucketReportArgs(args []string) (qdrantBucketReportDeps, error) {
	deps := qdrantBucketReportDeps{BatchSize: 500}
	for _, a := range args {
		a = strings.TrimSpace(a)
		switch {
		case a == "--json":
			deps.JSON = true
		case strings.HasPrefix(a, "--report-path="):
			deps.ReportPath = strings.TrimPrefix(a, "--report-path=")
		case strings.HasPrefix(a, "--collection="):
			deps.Collection = strings.TrimPrefix(a, "--collection=")
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
	return deps, nil
}

// bucketAssetRow is the minimal SQLite-side row the report needs: the
// canonical eligibility decision + the observed index_state.
type bucketAssetRow struct {
	ID         string
	Eligible   bool
	IndexState string
}

// qdrantBucketReport is the machine-readable 4-bucket classification.
type qdrantBucketReport struct {
	Collection              string   `json:"collection"`
	GeneratedAt             string   `json:"generated_at"`
	TotalAssets             int      `json:"total_assets"`
	IndexedCount            int      `json:"indexed_count"`
	EligibleSQLite          int      `json:"eligible_sqlite"`
	QdrantPoints            int      `json:"qdrant_points"`
	Healthy                 int      `json:"healthy"`
	MissingInQdrant         int      `json:"missing_in_qdrant"`
	IndexedButIneligible    int      `json:"indexed_but_ineligible"`
	OrphanInQdrant          int      `json:"orphan_in_qdrant"`
	PointsMissingAssetID    int      `json:"points_missing_asset_id,omitempty"`
	MissingIDs              []string `json:"missing_ids,omitempty"`
	IndexedButIneligibleIDs []string `json:"indexed_but_ineligible_ids,omitempty"`
	OrphanIDs               []string `json:"orphan_ids,omitempty"`
	Errors                  []string `json:"errors,omitempty"`
}

// bucketAssetQuery reads every non-folder media_assets row with the canonical
// eligibility decision (SearchIndexEligibilitySQL — SSOT) + observed
// index_state. The eligibility predicate is embedded in a CASE WHEN so the
// report shares EXACTLY the same boundary as the Qdrant projection.
var bucketAssetQuery = `
SELECT id,
       CASE WHEN (` + capregistry.SearchIndexEligibilitySQL + `) THEN 1 ELSE 0 END,
       COALESCE(index_state, '')
FROM media_assets
WHERE COALESCE(media_type, '') != 'folder'
ORDER BY id
`

// computeQdrantBuckets is the pure 4-bucket classification. It is the
// testable core: given the SQLite rows and the active Qdrant asset_id set,
// it produces the report body (counts + per-bucket ID lists).
//
// Bucket semantics:
//   - A healthy: eligible row whose asset_id is in the Qdrant set.
//   - B missing_in_qdrant: eligible row whose asset_id is NOT in Qdrant.
//   - C indexed_but_ineligible: row with index_state='INDEXED' that does not
//     satisfy the canonical eligibility policy (INDEXED ≠ searchable).
//   - D orphan_in_qdrant: Qdrant asset_id that is not in the eligible SQLite
//     set (absent row or ineligible row — a stale projection point).
func computeQdrantBuckets(rows []bucketAssetRow, qdrantAssetIDs map[string]struct{}) qdrantBucketReport {
	r := qdrantBucketReport{}
	eligibleSet := make(map[string]struct{})
	for _, row := range rows {
		r.TotalAssets++
		if row.IndexState == "INDEXED" {
			r.IndexedCount++
		}
		if !row.Eligible {
			if row.IndexState == "INDEXED" {
				r.IndexedButIneligible++
				r.IndexedButIneligibleIDs = append(r.IndexedButIneligibleIDs, row.ID)
			}
			continue
		}
		eligibleSet[row.ID] = struct{}{}
		r.EligibleSQLite++
		if _, ok := qdrantAssetIDs[row.ID]; ok {
			r.Healthy++
		} else {
			r.MissingInQdrant++
			r.MissingIDs = append(r.MissingIDs, row.ID)
		}
	}
	for id := range qdrantAssetIDs {
		if _, ok := eligibleSet[id]; !ok {
			r.OrphanInQdrant++
			r.OrphanIDs = append(r.OrphanIDs, id)
		}
	}
	sort.Strings(r.MissingIDs)
	sort.Strings(r.IndexedButIneligibleIDs)
	sort.Strings(r.OrphanIDs)
	return r
}

// scrollQdrantAssetIDs scrolls the collection to completion and returns the
// set of payload asset_ids. Points whose payload carries no asset_id are
// counted separately (diagnostic) and reported in errs.
func scrollQdrantAssetIDs(ctx context.Context, client *transport.Client, collection string, batchSize int) (map[string]struct{}, int, []string, error) {
	assetIDs := make(map[string]struct{})
	var missing int
	var errs []string
	offset := ""
	const maxPages = 400 // safety cap (~200k points at batch=500)
	for i := 0; i < maxPages; i++ {
		res, err := client.ScrollPoints(ctx, collection, offset, batchSize, nil)
		if err != nil {
			return assetIDs, missing, errs, fmt.Errorf("scroll page %d: %w", i, err)
		}
		if res == nil || len(res.Points) == 0 {
			break
		}
		for _, p := range res.Points {
			id, ok := p.Payload["asset_id"].(string)
			if !ok || id == "" {
				missing++
				errs = append(errs, fmt.Sprintf("point %q missing payload asset_id", p.ID))
				continue
			}
			assetIDs[id] = struct{}{}
		}
		if res.NextOffset == "" {
			break
		}
		offset = res.NextOffset
		if i == maxPages-1 {
			return assetIDs, missing, errs, fmt.Errorf("scroll iteration cap %d reached with NextOffset still trailing", maxPages)
		}
	}
	return assetIDs, missing, errs, nil
}

// runQdrantBucketReport is the entry point registered in cmd/admin/main.go.
func runQdrantBucketReport(args []string) error {
	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	deps, err := parseQdrantBucketReportArgs(args)
	if err != nil {
		return err
	}

	if !cfg.Qdrant.Enabled {
		return errors.New(
			"qdrant is disabled in config (qdrant.enabled=false); " +
				"qdrant-bucket-report requires qdrant.enabled=true",
		)
	}

	ctx := cli.CmdContext()

	log.Info("qdrant-bucket-report starting",
		zap.String("report_path", deps.ReportPath),
		zap.String("collection_override", deps.Collection),
		zap.Int("batch_size", deps.BatchSize),
		zap.String("qdrant_url", cfg.Qdrant.BaseURL),
	)

	sqliteDB, err := storage.OpenSQLiteDB(cfg.Storage.PrimaryDBFullPath(), log)
	if err != nil {
		return fmt.Errorf("open media DB: %w", err)
	}
	defer sqliteDB.Close()

	schema := qdrantschema.DefaultV3Schema()
	client := transport.NewClient(&qdrantschema.Config{
		BaseURL: cfg.Qdrant.BaseURL,
		APIKey:  cfg.Qdrant.APIKey,
		Timeout: cfg.Qdrant.Timeout,
	}, log)

	// Resolve collection: explicit override > runtime alias target.
	collection := deps.Collection
	if collection == "" {
		resolved, err := client.GetAliasTarget(ctx, schema.RuntimeAlias)
		if err != nil {
			return fmt.Errorf("resolve runtime alias %q: %w", schema.RuntimeAlias, err)
		}
		if resolved == "" {
			return fmt.Errorf(
				"runtime alias %q has no target; pass --collection=NAME to report on a specific collection",
				schema.RuntimeAlias,
			)
		}
		collection = resolved
	}
	log.Info("reporting on collection", zap.String("collection", collection))

	// SQLite side: every non-folder row with the canonical eligibility
	// decision + observed index_state.
	rows, err := sqliteDB.DB.QueryContext(ctx, bucketAssetQuery)
	if err != nil {
		return fmt.Errorf("query media_assets for bucket report: %w", err)
	}
	defer rows.Close()
	var assetRows []bucketAssetRow
	for rows.Next() {
		var r bucketAssetRow
		var eligible int
		if err := rows.Scan(&r.ID, &eligible, &r.IndexState); err != nil {
			return fmt.Errorf("scan media_assets row: %w", err)
		}
		r.Eligible = eligible == 1
		assetRows = append(assetRows, r)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate media_assets rows: %w", err)
	}

	// Qdrant side: scroll the active collection for asset_ids.
	qdrantIDs, missingPoints, scrollErrs, err := scrollQdrantAssetIDs(ctx, client, collection, deps.BatchSize)
	if err != nil {
		return fmt.Errorf("scroll Qdrant collection %q: %w", collection, err)
	}

	report := computeQdrantBuckets(assetRows, qdrantIDs)
	report.Collection = collection
	report.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	report.QdrantPoints = len(qdrantIDs)
	report.PointsMissingAssetID = missingPoints
	report.Errors = append(report.Errors, scrollErrs...)

	if deps.ReportPath != "" {
		b, _ := json.MarshalIndent(report, "", "  ")
		if err := os.WriteFile(deps.ReportPath, b, 0o644); err != nil {
			return fmt.Errorf("write report %q: %w", deps.ReportPath, err)
		}
	}
	if deps.JSON {
		b, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	printQdrantBucketReport(report)
	return nil
}

func printQdrantBucketReport(r qdrantBucketReport) {
	fmt.Printf("=== Qdrant 4-bucket report: %s ===\n", r.Collection)
	fmt.Printf("  total_assets               %d\n", r.TotalAssets)
	fmt.Printf("  indexed (INDEXED)          %d\n", r.IndexedCount)
	fmt.Printf("  eligible_sqlite            %d\n", r.EligibleSQLite)
	fmt.Printf("  qdrant_points              %d\n", r.QdrantPoints)
	if r.PointsMissingAssetID > 0 {
		fmt.Printf("  points_missing_asset_id    %d\n", r.PointsMissingAssetID)
	}
	fmt.Println("  --- buckets:")
	fmt.Printf("    A healthy (eligible+Qdrant)      %d\n", r.Healthy)
	fmt.Printf("    B missing_in_qdrant (eligible)   %d\n", r.MissingInQdrant)
	fmt.Printf("    C indexed_but_ineligible         %d\n", r.IndexedButIneligible)
	fmt.Printf("    D orphan_in_qdrant               %d\n", r.OrphanInQdrant)
	if len(r.MissingIDs) > 0 {
		fmt.Printf("  --- B missing IDs (%d):\n", len(r.MissingIDs))
		for _, id := range r.MissingIDs {
			fmt.Printf("    %s\n", id)
		}
	}
	if len(r.IndexedButIneligibleIDs) > 0 {
		fmt.Printf("  --- C indexed-but-ineligible IDs (%d):\n", len(r.IndexedButIneligibleIDs))
		for _, id := range r.IndexedButIneligibleIDs {
			fmt.Printf("    %s\n", id)
		}
	}
	if len(r.OrphanIDs) > 0 {
		fmt.Printf("  --- D orphan IDs (%d):\n", len(r.OrphanIDs))
		for _, id := range r.OrphanIDs {
			fmt.Printf("    %s\n", id)
		}
	}
	if len(r.Errors) > 0 {
		fmt.Printf("  errors: %d\n", len(r.Errors))
		for i, e := range r.Errors {
			fmt.Printf("    [%d] %s\n", i, e)
		}
	}
}
