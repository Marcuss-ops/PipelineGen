// cmd/admin/broken_references.go — GC FASE 4: broken foreign reference report.
//
// Cross-checks every external reference in the canonical SQLite DB against
// the actual backend and reports the broken ones. Four reference types:
//
//  1. FK orphans          — child row whose foreign key reference does NOT
//     resolve to any owner row (reuses Fase 2 model).
//  2. drive_file_id       — DB rows referencing a Drive file that no longer
//     exists (checked against live Drive listing).
//  3. local_path          — DB rows referencing a local file that does not
//     exist on disk (checked via os.Stat).
//  4. qdrant_point        — asset_ids in SQLite that SHOULD be in Qdrant
//     (eligible per canonical policy) but are missing.
//
// HARD INVARIANT: NO DELETIONS are performed. This is a read-only diagnostic
// that surfaces every broken reference so Fase 5 (Qdrant reconcile) and
// Fase 10 (Drive cleanup) can act on them with full knowledge.
//
// Usage:
//
//	go run ./cmd/admin broken-references [--json] [--report=path]
//	    [--skip-drive] [--skip-local] [--skip-qdrant] [--no-orphan-detail]
//
// Flags:
//
//	--json              machine-readable JSON output
//	--report            write JSON report to file
//	--skip-drive        skip Drive cross-check
//	--skip-local        skip local_path existence check
//	--skip-qdrant       skip Qdrant point cross-check
//	--no-orphan-detail  omit per-ID FK orphan detail (faster)
//	--drive-inventory   path to drive-inventory.json from Fase 1 snapshot
//	                    (if set, cross-check against snapshot instead of live Drive)
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"
	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	qdrantschema "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
)

// ── Report types ──────────────────────────────────────────────────────

type brokenRefsReport struct {
	SchemaVersion int               `json:"schema_version"`
	Mode          string            `json:"mode"`
	GeneratedAt   string            `json:"generated_at"`
	NoDeletions   bool              `json:"no_deletions_performed"`
	Summary       brokenRefsSummary `json:"summary"`
	FKOrphans     []fkOrphanTable   `json:"fk_orphans"`
	DriveBroken   []brokenDriveRef  `json:"drive_broken"`
	LocalBroken   []brokenLocalRef  `json:"local_broken"`
	QdrantMissing []string          `json:"qdrant_missing"`
	Errors        []string          `json:"errors,omitempty"`
}

type brokenRefsSummary struct {
	FKOrphanRows   int `json:"fk_orphan_rows"`
	FKOrphanTables int `json:"fk_orphan_tables"`
	DriveRefsTotal int `json:"drive_refs_total"`
	DriveBroken    int `json:"drive_broken"`
	LocalRefsTotal int `json:"local_refs_total"`
	LocalBroken    int `json:"local_broken"`
	EligibleAssets int `json:"eligible_assets"`
	QdrantMissing  int `json:"qdrant_missing"`
}

type fkOrphanTable struct {
	Table      string   `json:"table"`
	OwnerTable string   `json:"owner_table"`
	OrphanRows int      `json:"orphan_rows"`
	SampleIDs  []string `json:"sample_ids,omitempty"`
}

type brokenDriveRef struct {
	Table       string `json:"table"`
	Column      string `json:"column"` // drive_file_id or local_path
	RefValue    string `json:"ref_value"`
	AssetID     string `json:"asset_id,omitempty"`
	FailureKind string `json:"failure_kind"` // "drive_file_not_found" | "local_path_not_found"
	Error       string `json:"error,omitempty"`
}

type brokenLocalRef struct {
	Table       string `json:"table"`
	Column      string `json:"column"`
	LocalPath   string `json:"local_path"`
	AssetID     string `json:"asset_id,omitempty"`
	FailureKind string `json:"failure_kind"` // "file_not_found" | "stat_error"
	Error       string `json:"error,omitempty"`
}

// ── CLI entry point ───────────────────────────────────────────────────

func RunBrokenReferences(args []string) error {
	fs := flag.NewFlagSet("broken-references", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "Machine-readable JSON output")
	reportPath := fs.String("report", "", "Write JSON report to file")
	skipDrive := fs.Bool("skip-drive", false, "Skip Drive cross-check")
	skipLocal := fs.Bool("skip-local", false, "Skip local_path existence check")
	skipQdrant := fs.Bool("skip-qdrant", false, "Skip Qdrant point cross-check")
	noOrphanDetail := fs.Bool("no-orphan-detail", false, "Omit per-ID FK orphan detail (faster)")
	driveInvPath := fs.String("drive-inventory", "", "Path to drive-inventory.json (Fase 1 snapshot); if set, cross-check against snapshot instead of live Drive")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	ctx := cli.CmdContext()

	sdb, err := storage.OpenSQLiteDB(cfg.Storage.PrimaryDBFullPath(), log)
	if err != nil {
		return fmt.Errorf("open DB: %w", err)
	}
	defer sdb.Close()

	report, err := executeBrokenReferences(ctx, sdb.DB, cfg, log,
		*skipDrive, *skipLocal, *skipQdrant, *noOrphanDetail, *driveInvPath)
	if err != nil {
		return err
	}
	report.GeneratedAt = time.Now().UTC().Format(time.RFC3339)

	payload, _ := json.MarshalIndent(report, "", "  ")
	if *reportPath != "" {
		if err := os.WriteFile(*reportPath, append(payload, '\n'), 0o644); err != nil {
			return fmt.Errorf("write report: %w", err)
		}
		fmt.Printf("broken-references: report written to %s\n", *reportPath)
		return nil
	}
	if *jsonOut {
		fmt.Println(string(payload))
		return nil
	}
	printBrokenRefsReport(report)
	return nil
}

// ── Core computation ──────────────────────────────────────────────────

func executeBrokenReferences(
	ctx context.Context,
	db *sql.DB,
	cfg *config.Config,
	log *zap.Logger,
	skipDrive, skipLocal, skipQdrant bool,
	noOrphanDetail bool,
	driveInvPath string,
) (*brokenRefsReport, error) {
	r := &brokenRefsReport{
		SchemaVersion: 1,
		Mode:          "broken-references",
		NoDeletions:   true,
	}

	// 1. FK orphans — reuse reachability model from Fase 2.
	fkOrphans, err := detectFKOrphans(ctx, db, noOrphanDetail)
	if err != nil {
		r.Errors = append(r.Errors, fmt.Sprintf("fk orphans: %v", err))
	} else {
		r.FKOrphans = fkOrphans
		for _, o := range fkOrphans {
			r.Summary.FKOrphanRows += o.OrphanRows
		}
		r.Summary.FKOrphanTables = len(fkOrphans)
	}

	// 2. Drive references.
	if !skipDrive {
		broken, total, driveErrs, err := detectBrokenDriveRefs(ctx, db, cfg, log, driveInvPath)
		if err != nil {
			r.Errors = append(r.Errors, fmt.Sprintf("drive refs: %v", err))
		} else {
			r.DriveBroken = broken
			r.Errors = append(r.Errors, driveErrs...)
			r.Summary.DriveRefsTotal = total
			r.Summary.DriveBroken = len(broken)
		}
	}

	// 3. Local path references.
	if !skipLocal {
		broken, total, err := detectBrokenLocalPaths(ctx, db)
		if err != nil {
			r.Errors = append(r.Errors, fmt.Sprintf("local paths: %v", err))
		} else {
			r.LocalBroken = broken
			r.Summary.LocalRefsTotal = total
			r.Summary.LocalBroken = len(broken)
		}
	}

	// 4. Qdrant points referenced by eligible SQLite assets.
	if !skipQdrant && cfg.Qdrant.Enabled {
		missing, eligible, qErr := detectMissingQdrantPoints(ctx, db, cfg, log)
		if qErr != nil {
			r.Errors = append(r.Errors, fmt.Sprintf("qdrant: %v", qErr))
		} else {
			if missing == nil {
				missing = []string{}
			}
			r.QdrantMissing = missing
			r.Summary.EligibleAssets = eligible
			r.Summary.QdrantMissing = len(missing)
		}
	}

	// Ensure non-nil slices for clean JSON.
	if r.FKOrphans == nil {
		r.FKOrphans = []fkOrphanTable{}
	}
	if r.DriveBroken == nil {
		r.DriveBroken = []brokenDriveRef{}
	}
	if r.LocalBroken == nil {
		r.LocalBroken = []brokenLocalRef{}
	}
	if r.QdrantMissing == nil {
		r.QdrantMissing = []string{}
	}

	sort.Slice(r.DriveBroken, func(i, j int) bool {
		if r.DriveBroken[i].Table != r.DriveBroken[j].Table {
			return r.DriveBroken[i].Table < r.DriveBroken[j].Table
		}
		return r.DriveBroken[i].RefValue < r.DriveBroken[j].RefValue
	})
	sort.Slice(r.LocalBroken, func(i, j int) bool {
		if r.LocalBroken[i].Table != r.LocalBroken[j].Table {
			return r.LocalBroken[i].Table < r.LocalBroken[j].Table
		}
		return r.LocalBroken[i].LocalPath < r.LocalBroken[j].LocalPath
	})
	sort.Strings(r.QdrantMissing)
	sort.Strings(r.Errors)

	return r, nil
}

// ── FK orphan detection ──────────────────────────────────────────────

func detectFKOrphans(ctx context.Context, db *sql.DB, noDetail bool) ([]fkOrphanTable, error) {
	var results []fkOrphanTable
	for _, rel := range canonicalOwnershipModel {
		if rel.Kind != "FK" && rel.Kind != "LOGICAL" {
			continue
		}
		if !hasColumn(ctx, db, rel.ChildTable, rel.ChildColumn) {
			continue
		}
		if !hasColumn(ctx, db, rel.OwnerTable, rel.OwnerColumn) {
			continue
		}

		// Count orphan rows: non-null FK that doesn't resolve.
		var orphanCount int
		q := fmt.Sprintf(
			`SELECT COUNT(*) FROM %s c WHERE c.%s IS NOT NULL AND c.%s!='' AND NOT EXISTS (SELECT 1 FROM %s o WHERE o.%s=c.%s)`,
			qt(rel.ChildTable), qt(rel.ChildColumn), qt(rel.ChildColumn),
			qt(rel.OwnerTable), qt(rel.OwnerColumn), qt(rel.ChildColumn),
		)
		if err := db.QueryRowContext(ctx, q).Scan(&orphanCount); err != nil {
			continue
		}
		if orphanCount == 0 {
			continue
		}

		entry := fkOrphanTable{
			Table:      rel.ChildTable,
			OwnerTable: rel.OwnerTable,
			OrphanRows: orphanCount,
		}
		if !noDetail && orphanCount > 0 {
			// Fetch up to 20 sample IDs.
			sq := fmt.Sprintf(
				`SELECT DISTINCT c.%s FROM %s c WHERE c.%s IS NOT NULL AND c.%s!='' AND NOT EXISTS (SELECT 1 FROM %s o WHERE o.%s=c.%s) LIMIT 20`,
				qt(rel.ChildColumn), qt(rel.ChildTable), qt(rel.ChildColumn), qt(rel.ChildColumn),
				qt(rel.OwnerTable), qt(rel.OwnerColumn), qt(rel.ChildColumn),
			)
			sRows, sErr := db.QueryContext(ctx, sq)
			if sErr == nil {
				for sRows.Next() {
					var val string
					if err := sRows.Scan(&val); err != nil {
						break
					}
					entry.SampleIDs = append(entry.SampleIDs, val)
				}
				sRows.Close()
			}
		}
		results = append(results, entry)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Table < results[j].Table })
	return results, nil
}

// ── Drive file cross-check ───────────────────────────────────────────

func detectBrokenDriveRefs(
	ctx context.Context,
	db *sql.DB,
	cfg *config.Config,
	log *zap.Logger,
	inventoryPath string,
) ([]brokenDriveRef, int, []string, error) {
	// Load the set of known Drive file IDs.
	var knownIDs map[string]bool
	var errs []string

	if inventoryPath != "" {
		// Load from Fase 1 snapshot.
		knownIDs, errs = loadDriveInventoryFromFile(inventoryPath)
	} else {
		// Walk live Drive.
		var err error
		knownIDs, errs, err = walkLiveDriveIDs(ctx, cfg, log)
		if err != nil {
			return nil, 0, errs, err
		}
	}

	// Find all tables with a drive_file_id column.
	tables, err := tablesWithColumn(ctx, db, "drive_file_id")
	if err != nil {
		return nil, 0, errs, err
	}

	var broken []brokenDriveRef
	total := 0

	for _, tbl := range tables {
		rows, err := db.QueryContext(ctx,
			fmt.Sprintf(`SELECT %s FROM %s WHERE %s IS NOT NULL AND %s!=''`,
				qt("drive_file_id"), qt(tbl), qt("drive_file_id"), qt("drive_file_id")),
		)
		if err != nil {
			errs = append(errs, fmt.Sprintf("query %s: %v", tbl, err))
			continue
		}

		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				break
			}
			ids = append(ids, id)
		}
		rows.Close()

		total += len(ids)
		for _, id := range ids {
			if !knownIDs[id] {
				broken = append(broken, brokenDriveRef{
					Table:       tbl,
					Column:      "drive_file_id",
					RefValue:    id,
					FailureKind: "drive_file_not_found",
				})
			}
		}
	}

	// Also check asset_id + drive_file_id for media_assets specifically.
	// For media_assets, add the asset_id for better diagnostics.
	for i := range broken {
		if broken[i].Table == "media_assets" {
			var assetID string
			_ = db.QueryRowContext(ctx,
				`SELECT id FROM media_assets WHERE drive_file_id=?`, broken[i].RefValue,
			).Scan(&assetID)
			broken[i].AssetID = assetID
		}
	}

	return broken, total, errs, nil
}

func loadDriveInventoryFromFile(path string) (map[string]bool, []string) {
	var errs []string
	data, err := os.ReadFile(path)
	if err != nil {
		errs = append(errs, fmt.Sprintf("read drive inventory %s: %v", path, err))
		return nil, errs
	}
	var entries []driveInventoryEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		errs = append(errs, fmt.Sprintf("parse drive inventory %s: %v", path, err))
		return nil, errs
	}
	known := make(map[string]bool, len(entries))
	for _, e := range entries {
		known[e.ID] = true
	}
	return known, errs
}

func walkLiveDriveIDs(ctx context.Context, cfg *config.Config, log *zap.Logger) (map[string]bool, []string, error) {
	uploader, err := buildDriveAdminForCLI(ctx, cfg, log)
	if err != nil {
		return nil, nil, fmt.Errorf("init Drive: %w", err)
	}

	roots := collectDriveRoots(cfg.Drive)
	if len(roots) == 0 {
		return make(map[string]bool), []string{"no Drive roots configured"}, nil
	}

	inventory, failures := walkDriveInventory(ctx, uploader.ListFiles, roots)
	errs := make([]string, len(failures))
	copy(errs, failures)

	known := make(map[string]bool, len(inventory.entries))
	for _, e := range inventory.entries {
		known[e.ID] = true
	}
	return known, errs, nil
}

// ── Local path cross-check ───────────────────────────────────────────

func detectBrokenLocalPaths(ctx context.Context, db *sql.DB) ([]brokenLocalRef, int, error) {
	tables, err := tablesWithColumn(ctx, db, "local_path")
	if err != nil {
		return nil, 0, err
	}

	var broken []brokenLocalRef
	total := 0

	for _, tbl := range tables {
		query := fmt.Sprintf(`SELECT local_path FROM %s WHERE local_path IS NOT NULL AND local_path!=''`, qt(tbl))
		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			continue
		}
		var paths []string
		for rows.Next() {
			var p string
			if err := rows.Scan(&p); err != nil {
				break
			}
			paths = append(paths, p)
		}
		rows.Close()

		total += len(paths)
		for _, p := range paths {
			info, statErr := os.Stat(p)
			if statErr != nil {
				kind := "file_not_found"
				if os.IsNotExist(statErr) {
					kind = "file_not_found"
				} else {
					kind = "stat_error"
				}
				broken = append(broken, brokenLocalRef{
					Table:       tbl,
					Column:      "local_path",
					LocalPath:   p,
					FailureKind: kind,
					Error:       statErr.Error(),
				})
			} else if info.IsDir() {
				// local_path pointing at a directory is suspicious but not necessarily broken.
				// Skip for now — directories can be valid (e.g. artifact caches).
			}
		}
	}

	return broken, total, nil
}

// ── Qdrant point cross-check ─────────────────────────────────────────

func detectMissingQdrantPoints(
	ctx context.Context,
	db *sql.DB,
	cfg *config.Config,
	log *zap.Logger,
) ([]string, int, error) {
	// 1. Query eligible asset IDs from SQLite.
	eligibleQuery := fmt.Sprintf(
		`SELECT id FROM media_assets WHERE (%s) AND COALESCE(media_type,'')!='folder' ORDER BY id`,
		capregistry.SearchIndexEligibilitySQL,
	)
	rows, err := db.QueryContext(ctx, eligibleQuery)
	if err != nil {
		return nil, 0, fmt.Errorf("query eligible assets: %w", err)
	}
	defer rows.Close()

	var eligibleIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, 0, fmt.Errorf("scan eligible asset: %w", err)
		}
		eligibleIDs = append(eligibleIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate eligible assets: %w", err)
	}

	// 2. Scroll Qdrant for actual asset_ids.
	schema := qdrantschema.DefaultV3Schema()
	client := transport.NewClient(&qdrantschema.Config{
		BaseURL: cfg.Qdrant.BaseURL,
		APIKey:  cfg.Qdrant.APIKey,
		Timeout: cfg.Qdrant.Timeout,
	}, log)

	collection, err := client.GetAliasTarget(ctx, schema.RuntimeAlias)
	if err != nil {
		return nil, len(eligibleIDs), fmt.Errorf("resolve alias %q: %w", schema.RuntimeAlias, err)
	}
	if collection == "" {
		return nil, len(eligibleIDs), fmt.Errorf("alias %q has no target", schema.RuntimeAlias)
	}

	qdrantIDs, _, scrollErrs, err := scrollQdrantAssetIDs(ctx, client, collection, 500)
	if err != nil {
		return nil, len(eligibleIDs), fmt.Errorf("scroll Qdrant: %w", err)
	}
	_ = scrollErrs

	// 3. Find eligible IDs missing from Qdrant.
	var missing []string
	for _, id := range eligibleIDs {
		if _, ok := qdrantIDs[id]; !ok {
			missing = append(missing, id)
		}
	}

	return missing, len(eligibleIDs), nil
}

// ── Shared helpers ────────────────────────────────────────────────────

func tablesWithColumn(ctx context.Context, db *sql.DB, colName string) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		"SELECT DISTINCT m.name FROM sqlite_master m JOIN pragma_table_info(m.name) p ON 1=1 WHERE p.name=? AND m.type='table' AND m.name NOT LIKE 'sqlite_%' ORDER BY m.name",
		colName,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// ── Output ────────────────────────────────────────────────────────────

func printBrokenRefsReport(r *brokenRefsReport) {
	fmt.Println("=== Broken References Report (FASE 4) ===")
	fmt.Printf("  mode:         %s\n", r.Mode)
	fmt.Printf("  generated:    %s\n", r.GeneratedAt)
	fmt.Printf("  no deletions: %v\n", r.NoDeletions)
	fmt.Println()

	// FK orphans.
	fmt.Printf("  --- FK Orphans (%d rows across %d tables) ---\n",
		r.Summary.FKOrphanRows, r.Summary.FKOrphanTables)
	for _, o := range r.FKOrphans {
		fmt.Printf("    %-40s → %-25s  orphans=%-6d", o.Table, o.OwnerTable, o.OrphanRows)
		if len(o.SampleIDs) > 0 {
			fmt.Printf("  sample=%v", o.SampleIDs[:min(3, len(o.SampleIDs))])
		}
		fmt.Println()
	}

	// Drive broken.
	if r.Summary.DriveRefsTotal > 0 {
		fmt.Printf("\n  --- Drive References (%d total, %d broken) ---\n",
			r.Summary.DriveRefsTotal, r.Summary.DriveBroken)
		printed := 0
		for _, b := range r.DriveBroken {
			if printed >= 30 {
				fmt.Printf("    ... +%d more broken drive refs\n", len(r.DriveBroken)-printed)
				break
			}
			assetStr := ""
			if b.AssetID != "" {
				assetStr = fmt.Sprintf(" asset=%s", b.AssetID)
			}
			fmt.Printf("    %-35s %s  %s%s\n", b.Table, shortenID(b.RefValue), b.FailureKind, assetStr)
			printed++
		}
	}

	// Local broken.
	if r.Summary.LocalRefsTotal > 0 {
		fmt.Printf("\n  --- Local Path References (%d total, %d broken) ---\n",
			r.Summary.LocalRefsTotal, r.Summary.LocalBroken)
		printed := 0
		for _, b := range r.LocalBroken {
			if printed >= 30 {
				fmt.Printf("    ... +%d more broken local paths\n", len(r.LocalBroken)-printed)
				break
			}
			fmt.Printf("    %-35s %s: %s\n", b.Table, b.FailureKind, truncatePath(b.LocalPath, 70))
			printed++
		}
	}

	// Qdrant missing.
	if r.Summary.QdrantMissing > 0 {
		fmt.Printf("\n  --- Qdrant Points (%d eligible, %d missing in Qdrant) ---\n",
			r.Summary.EligibleAssets, r.Summary.QdrantMissing)
		for i, id := range r.QdrantMissing {
			if i >= 20 {
				fmt.Printf("    ... +%d more missing Qdrant points\n", len(r.QdrantMissing)-i)
				break
			}
			fmt.Printf("    missing: %s\n", id)
		}
	}

	if len(r.Errors) > 0 {
		fmt.Printf("\n  --- Errors (%d) ---\n", len(r.Errors))
		for _, e := range r.Errors {
			fmt.Printf("    %s\n", e)
		}
	}
}

func shortenID(id string) string {
	if len(id) > 24 {
		return id[:12] + "..." + id[len(id)-12:]
	}
	return id
}

func truncatePath(p string, maxLen int) string {
	if len(p) <= maxLen {
		return p
	}
	return p[:maxLen/2-2] + "..." + p[len(p)-maxLen/2+1:]
}
