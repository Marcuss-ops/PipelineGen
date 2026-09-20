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
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	qdrantschema "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
	"github.com/Marcuss-ops/PipelineGen/pkg/atomicwrite"
)

// ── CLI entry point ───────────────────────────────────────────────────

// The media-engine boundary (brokenRefMediaSource, mediaOwnedAuditTables and
// the PostgreSQL media detectors) lives in broken_references_media.go.

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

	dbSet, err := cli.OpenDatabaseSet(cfg, log)
	if err != nil {
		return fmt.Errorf("open database set: %w", err)
	}
	defer dbSet.Close()
	sdb := dbSet.Primary

	// MEDIA-SSOT: the drive/local/Qdrant checks all read media_assets, so the
	// audit resolves them from the PostgreSQL media SSOT. The handle is only
	// required when at least one of those checks will actually run; an audit
	// that skips all three reads no media rows at all. A nil handle fails
	// closed (the check is recorded as a failure, never silently skipped).
	var media brokenRefMediaSource
	if !(*skipDrive && *skipLocal && *skipQdrant) {
		mediaDB, mErr := cli.OpenMediaPostgres(ctx, cfg)
		if mErr != nil {
			return fmt.Errorf("open media postgres: %w", mErr)
		}
		if mediaDB == nil {
			return fmt.Errorf("media PostgreSQL SSOT is required for broken-references")
		}
		defer mediaDB.Close()
		media = pgmedia.NewMediaReferenceAuditReader(mediaDB)
	}

	report, err := executeBrokenReferences(ctx, sdb.DB, media, cfg, log,
		*skipDrive, *skipLocal, *skipQdrant, *noOrphanDetail, *driveInvPath)
	if err != nil {
		return err
	}
	report.GeneratedAt = time.Now().UTC().Format(time.RFC3339)

	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal broken-references report: %w", err)
	}
	switch {
	case *reportPath != "":
		if err := atomicwrite.WriteFile(*reportPath, append(payload, '\n'), 0o644); err != nil {
			return fmt.Errorf("write report: %w", err)
		}
		fmt.Printf("broken-references: report written to %s\n", *reportPath)
	case *jsonOut:
		fmt.Println(string(payload))
	default:
		printBrokenRefsReport(report)
	}
	// A check that failed is not a clean bill of health: exit non-zero so
	// scripts never mistake a partial audit for a complete one.
	if len(report.Errors) > 0 {
		return fmt.Errorf("broken-references: %d check(s) failed during the audit (partial report emitted)", len(report.Errors))
	}
	return nil
}

// ── Core computation ──────────────────────────────────────────────────

func executeBrokenReferences(
	ctx context.Context,
	db *sql.DB,
	media brokenRefMediaSource,
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

	// 1. FK orphans — reuse reachability model from Fase 2. Per-relation
	// failures are recorded in the report instead of being silently skipped;
	// the remaining relations are still examined.
	fkOrphans, fkErrs := detectFKOrphans(ctx, db, noOrphanDetail)
	r.Errors = append(r.Errors, fkErrs...)
	r.FKOrphans = fkOrphans
	for _, o := range fkOrphans {
		r.Summary.FKOrphanRows += o.OrphanRows
	}
	r.Summary.FKOrphanTables = len(fkOrphans)

	// 2. Drive references (operational tables + the PostgreSQL media SSOT).
	if !skipDrive {
		broken, total, driveErrs, err := detectBrokenDriveRefsAndMedia(ctx, db, media, cfg, log, driveInvPath)
		if err != nil {
			r.Errors = append(r.Errors, fmt.Sprintf("drive refs: %v", err))
		} else {
			r.DriveBroken = broken
			r.Errors = append(r.Errors, driveErrs...)
			r.Summary.DriveRefsTotal = total
			r.Summary.DriveBroken = len(broken)
		}
	}

	// 3. Local path references (operational tables + the PostgreSQL media SSOT).
	if !skipLocal {
		broken, total, err := detectBrokenLocalPaths(ctx, db)
		if err != nil {
			r.Errors = append(r.Errors, fmt.Sprintf("local paths: %v", err))
		} else {
			mediaBroken, mediaTotal, mErr := detectBrokenMediaLocalPaths(ctx, media)
			if mErr != nil {
				r.Errors = append(r.Errors, fmt.Sprintf("local paths (media SSOT): %v", mErr))
			} else {
				broken = append(broken, mediaBroken...)
				total += mediaTotal
			}
			r.LocalBroken = broken
			r.Summary.LocalRefsTotal = total
			r.Summary.LocalBroken = len(broken)
		}
	}

	// 4. Qdrant points referenced by eligible media assets (PostgreSQL SSOT).
	if !skipQdrant && cfg.Qdrant.Enabled {
		missing, eligible, qErr := detectMissingQdrantPoints(ctx, media, cfg, log)
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

func detectFKOrphans(ctx context.Context, db *sql.DB, noDetail bool) ([]fkOrphanTable, []string) {
	var results []fkOrphanTable
	var errs []string
	for _, rel := range canonicalOwnershipModel {
		if rel.Kind != "FK" && rel.Kind != "LOGICAL" {
			continue
		}
		ok, err := hasColumn(ctx, db, rel.ChildTable, rel.ChildColumn)
		if err != nil {
			errs = append(errs, fmt.Sprintf("fk orphans %s.%s: %v", rel.ChildTable, rel.ChildColumn, err))
			continue
		}
		if !ok {
			continue
		}
		ok, err = hasColumn(ctx, db, rel.OwnerTable, rel.OwnerColumn)
		if err != nil {
			errs = append(errs, fmt.Sprintf("fk orphans %s.%s: %v", rel.OwnerTable, rel.OwnerColumn, err))
			continue
		}
		if !ok {
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
			errs = append(errs, fmt.Sprintf("fk orphans count %s: %v", rel.ChildTable, err))
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
		if !noDetail {
			// Fetch up to 20 sample IDs; failures here only degrade the
			// sample list, never the count, but must still be recorded.
			sq := fmt.Sprintf(
				`SELECT DISTINCT c.%s FROM %s c WHERE c.%s IS NOT NULL AND c.%s!='' AND NOT EXISTS (SELECT 1 FROM %s o WHERE o.%s=c.%s) LIMIT 20`,
				qt(rel.ChildColumn), qt(rel.ChildTable), qt(rel.ChildColumn), qt(rel.ChildColumn),
				qt(rel.OwnerTable), qt(rel.OwnerColumn), qt(rel.ChildColumn),
			)
			sRows, sErr := db.QueryContext(ctx, sq)
			if sErr != nil {
				errs = append(errs, fmt.Sprintf("fk orphans samples %s: %v", rel.ChildTable, sErr))
			} else {
				for sRows.Next() {
					var val string
					if err := sRows.Scan(&val); err != nil {
						errs = append(errs, fmt.Sprintf("fk orphans sample scan %s: %v", rel.ChildTable, err))
						break
					}
					entry.SampleIDs = append(entry.SampleIDs, val)
				}
				if err := sRows.Err(); err != nil {
					errs = append(errs, fmt.Sprintf("fk orphans sample rows %s: %v", rel.ChildTable, err))
				}
				sRows.Close()
			}
		}
		results = append(results, entry)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Table < results[j].Table })
	return results, errs
}

// ── Drive file cross-check ───────────────────────────────────────────

// detectBrokenDriveRefsAndMedia cross-checks BOTH engines against the SAME
// Drive inventory: the operational tables (non-media) and the PostgreSQL media
// SSOT. The known-ID set is loaded once, so a live Drive walk is never performed
// twice for a single audit run.
func detectBrokenDriveRefsAndMedia(
	ctx context.Context,
	db *sql.DB,
	media brokenRefMediaSource,
	cfg *config.Config,
	log *zap.Logger,
	inventoryPath string,
) ([]brokenDriveRef, int, []string, error) {
	knownIDs, errs, err := loadKnownDriveIDs(ctx, cfg, log, inventoryPath)
	if err != nil {
		return nil, 0, errs, err
	}

	broken, total, opErrs, err := detectBrokenDriveRefs(ctx, db, knownIDs)
	errs = append(errs, opErrs...)
	if err != nil {
		return nil, 0, errs, err
	}

	mediaBroken, mediaTotal, mediaErrs, err := detectBrokenMediaDriveRefs(ctx, media, knownIDs)
	errs = append(errs, mediaErrs...)
	if err != nil {
		return nil, 0, errs, err
	}

	return append(broken, mediaBroken...), total + mediaTotal, errs, nil
}

// detectBrokenDriveRefs sweeps the OPERATIONAL tables that carry a
// drive_file_id column. media_assets is excluded by construction: it is
// PostgreSQL-owned, so its rows are checked by detectBrokenMediaDriveRefs.
func detectBrokenDriveRefs(ctx context.Context, db *sql.DB, knownIDs map[string]bool) ([]brokenDriveRef, int, []string, error) {
	var errs []string

	tables, err := tablesWithColumn(ctx, db, "drive_file_id")
	if err != nil {
		return nil, 0, errs, err
	}

	var broken []brokenDriveRef
	total := 0

	for _, tbl := range tables {
		if mediaOwnedAuditTables[tbl] {
			// MEDIA-SSOT: see detectBrokenMediaDriveRefs — the mirror holds no
			// committed media rows, so sweeping it here would report no broken
			// references while the SSOT held the broken ones.
			continue
		}
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

	return broken, total, errs, nil
}

// The Drive-inventory loaders (loadKnownDriveIDs, loadDriveInventoryFromFile,
// walkLiveDriveIDs) live in broken_references_drive.go.

// ── Local path cross-check ───────────────────────────────────────────

// detectBrokenLocalPaths sweeps the OPERATIONAL tables that carry a local_path
// column. media_assets is excluded by construction: it is PostgreSQL-owned, so
// its rows are checked by detectBrokenMediaLocalPaths.
func detectBrokenLocalPaths(ctx context.Context, db *sql.DB) ([]brokenLocalRef, int, error) {
	tables, err := tablesWithColumn(ctx, db, "local_path")
	if err != nil {
		return nil, 0, err
	}

	var broken []brokenLocalRef
	total := 0

	for _, tbl := range tables {
		if mediaOwnedAuditTables[tbl] {
			// MEDIA-SSOT: see detectBrokenMediaLocalPaths.
			continue
		}
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

// detectMissingQdrantPoints compares the canonical eligibility set against the
// actual Qdrant projection.
//
// MEDIA-SSOT: the eligibility set is read from the PostgreSQL media SSOT
// (pgmedia.MediaReferenceAuditReader), which applies the SAME canonical
// predicate (capregistry.SearchIndexEligibilitySQL) the projection writers use.
// Reading it from the operational mirror would compare a populated projection
// against an empty eligible set and report every point as an orphan.
func detectMissingQdrantPoints(
	ctx context.Context,
	media brokenRefMediaSource,
	cfg *config.Config,
	log *zap.Logger,
) ([]string, int, error) {
	if media == nil {
		return nil, 0, fmt.Errorf("media SSOT reader is not wired")
	}
	// 1. Query eligible asset IDs from the media SSOT.
	eligibleIDs, err := media.ListSearchEligibleAssetIDs(ctx)
	if err != nil {
		return nil, 0, err
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

	qdrantIDs, _, scrollErrs, err := cli.ScrollQdrantAssetIDs(ctx, client, collection, 500)
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
