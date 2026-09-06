// cmd/admin/sqlite_audit.go — GC FASE 3: SQLite table-by-table audit.
//
// Classifies every record into one of 7 canonical categories:
//
//	LIVE                 — actively used, reachable from a live owner
//	STALE_BUT_REFERENCED — reachable from owner, but owner (or record)
//	                       itself is marked deleted/superseded/stale
//	ORPHAN               — owner reference does NOT resolve (Fase 2
//	                       unreachable), or canonical root with zero
//	                       children AND deleted
//	DUPLICATE            — same logical identity appearing >1 time in a
//	                       table that should be unique
//	TERMINAL_HISTORY     — job/outbox/event in terminal state past
//	                       retention; audit/history tables count all here
//	CACHE_EXPIRED        — cache row past TTL or in expired/invalidated state
//	BROKEN_REFERENCE     — pointer to external storage (drive_file_id,
//	                       local_path) that doesn't exist or is unreachable
//
// NO DELETIONS are performed. The audit is read-only and strictly
// additive — Fase 4 (broken references) and Fase 9 (render artifacts)
// will reuse these counts.
//
// Usage:
//
//	go run ./cmd/admin sqlite-audit [--json] [--report=path] [--skip-broken-refs]
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"
)

// ── Audit report types ───────────────────────────────────────────────────

type auditRecord struct {
	SchemaVersion int             `json:"schema_version"`
	Mode          string          `json:"mode"`
	GeneratedAt   string          `json:"generated_at"`
	NoDeletions   bool            `json:"no_deletions_performed"`
	Summary       auditSummary    `json:"summary"`
	Tables        []auditTableRow `json:"tables"`
}

type auditSummary struct {
	TotalRows          int `json:"total_rows"`
	Live               int `json:"live"`
	StaleButReferenced int `json:"stale_but_referenced"`
	Orphan             int `json:"orphan"`
	Duplicate          int `json:"duplicate"`
	TerminalHistory    int `json:"terminal_history"`
	CacheExpired       int `json:"cache_expired"`
	BrokenReference    int `json:"broken_reference"`
}

type auditTableRow struct {
	Table              string `json:"table"`
	RootType           string `json:"root_type"`
	TotalRows          int    `json:"total_rows"`
	Live               int    `json:"live"`
	StaleButReferenced int    `json:"stale_but_referenced"`
	Orphan             int    `json:"orphan"`
	Duplicate          int    `json:"duplicate"`
	TerminalHistory    int    `json:"terminal_history"`
	CacheExpired       int    `json:"cache_expired"`
	BrokenReference    int    `json:"broken_reference"`
	Error              string `json:"error,omitempty"`
}

// ── CLI entry point ──────────────────────────────────────────────────────

func RunSQLiteAudit(args []string) error {
	fs := flag.NewFlagSet("sqlite-audit", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "JSON output")
	reportPath := fs.String("report", "", "Write JSON to file")
	skipBroken := fs.Bool("skip-broken-refs", false, "Skip broken-reference checks (faster)")
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

	report, err := executeAudit(ctx, sdb.DB, !*skipBroken)
	if err != nil {
		return err
	}
	report.GeneratedAt = time.Now().UTC().Format(time.RFC3339)

	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal audit report: %w", err)
	}
	switch {
	case *reportPath != "":
		if err := os.WriteFile(*reportPath, append(payload, '\n'), 0o644); err != nil {
			return fmt.Errorf("write report: %w", err)
		}
		fmt.Printf("sqlite-audit: report written to %s\n", *reportPath)
	case *jsonOut:
		fmt.Println(string(payload))
	default:
		printAuditReport(report)
	}
	// A table whose classification failed is not "zero rows": fail loudly so
	// scripts never mistake a partial audit for an authoritative clean bill.
	if failures := reportTableFailures(report); len(failures) > 0 {
		return fmt.Errorf("sqlite-audit: %d table(s) errored during audit (partial report emitted): %s",
			len(failures), strings.Join(failures, "; "))
	}
	return nil
}

// reportTableFailures lists every table whose classification recorded an
// error, in deterministic (table) order.
func reportTableFailures(report *auditRecord) []string {
	var failed []string
	for _, t := range report.Tables {
		if t.Error != "" {
			failed = append(failed, t.Table+": "+t.Error)
		}
	}
	return failed
}

// ── Audit engine ─────────────────────────────────────────────────────────

func executeAudit(ctx context.Context, db *sql.DB, checkBroken bool) (*auditRecord, error) {
	tables, err := loadRealTables(ctx, db)
	if err != nil {
		return nil, err
	}

	report := &auditRecord{SchemaVersion: 1, Mode: "sqlite-audit", NoDeletions: true}

	var allRows []auditTableRow
	for _, t := range tables {
		row := classifyTable(ctx, db, t, checkBroken)
		allRows = append(allRows, row)
		report.Summary.TotalRows += row.TotalRows
		report.Summary.Live += row.Live
		report.Summary.StaleButReferenced += row.StaleButReferenced
		report.Summary.Orphan += row.Orphan
		report.Summary.Duplicate += row.Duplicate
		report.Summary.TerminalHistory += row.TerminalHistory
		report.Summary.CacheExpired += row.CacheExpired
		report.Summary.BrokenReference += row.BrokenReference
	}

	sort.Slice(allRows, func(i, j int) bool { return allRows[i].Table < allRows[j].Table })
	report.Tables = allRows
	return report, nil
}

func classifyTable(ctx context.Context, db *sql.DB, tableName string, checkBroken bool) auditTableRow {
	row := auditTableRow{Table: tableName}

	rt := classifyTableRootType(tableName)

	row.RootType = rt

	// Total rows.
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+qt(tableName)).Scan(&row.TotalRows); err != nil {
		row.Error = err.Error()
		return row
	}
	if row.TotalRows == 0 {
		return row
	}

	switch rt {
	case "canonical_root":
		classifyCanonicalRoot(ctx, db, tableName, &row, checkBroken)
	case "child":
		classifyChild(ctx, db, tableName, &row, checkBroken)
	case "cache":
		classifyCache(ctx, db, tableName, &row, checkBroken)
	case "queue":
		classifyQueue(ctx, db, tableName, &row, checkBroken)
	case "history", "audit":
		row.TerminalHistory = row.TotalRows
	case "unclassified":
		// Conservatively treat as live until classified in the model.
		row.Live = row.TotalRows
	}

	return row
}

// ── Root type resolver ──────────────────────────────────────────────────

func classifyTableRootType(tableName string) string {
	if rel, ok := canonicalOwnershipModel[tableName]; ok && rel.Kind == "" && rel.RootType != "" {
		return rel.RootType
	}
	// Check child edges.
	for _, rel := range canonicalOwnershipModel {
		if rel.ChildTable == tableName && (rel.Kind == "FK" || rel.Kind == "LOGICAL") {
			return "child"
		}
	}
	return classifyByHeuristic(tableName)
}

// ── Canonical root classifiers ───────────────────────────────────────────

func classifyCanonicalRoot(ctx context.Context, db *sql.DB, t string, row *auditTableRow, checkBroken bool) {
	switch t {
	case "media_assets":
		classifyMediaAssets(ctx, db, row, checkBroken)
	case "jobs":
		classifyJobs(ctx, db, row, checkBroken)
	case "stock_source_cache":
		classifyCache(ctx, db, t, row, checkBroken) // stock_source_cache is a cache, not root
	default:
		// Generic root: all LIVE, check duplicates + broken refs.
		row.Live = row.TotalRows
		if checkBroken {
			broken, err := countBrokenRefs(ctx, db, t)
			if err != nil {
				row.Error = err.Error()
				return
			}
			row.BrokenReference = broken
		}
		dups, err := countRootDups(ctx, db, t)
		if err != nil {
			row.Error = err.Error()
			return
		}
		row.Duplicate = dups
	}
}

func classifyMediaAssets(ctx context.Context, db *sql.DB, row *auditTableRow, checkBroken bool) {
	// Live: not deleted.
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM media_assets WHERE lifecycle_state IN ('ACTIVE','PUBLISHED','STAGING') AND (deleted_at IS NULL OR deleted_at='')`,
	).Scan(&row.Live); err != nil {
		row.Error = err.Error()
		return
	}

	// Stale-but-referenced: DELETED (soft-deleted, may still have children).
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM media_assets WHERE lifecycle_state='DELETED' OR (deleted_at IS NOT NULL AND deleted_at!='')`,
	).Scan(&row.StaleButReferenced); err != nil {
		row.Error = err.Error()
		return
	}

	// Orphan: DELETED AND zero children (safe to purge).
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM media_assets a WHERE (a.lifecycle_state='DELETED' OR (a.deleted_at IS NOT NULL AND a.deleted_at!=''))
		 AND NOT EXISTS (SELECT 1 FROM asset_locations l WHERE l.asset_id=a.id)
		 AND NOT EXISTS (SELECT 1 FROM asset_versions v WHERE v.asset_id=a.id)
		 AND NOT EXISTS (SELECT 1 FROM clip_search_terms c WHERE c.clip_id=a.id)`,
	).Scan(&row.Orphan); err != nil {
		row.Error = err.Error()
		return
	}

	// Duplicates: same drive_file_id (>1).
	if err := db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(cnt-1),0) FROM (SELECT COUNT(*) cnt FROM media_assets WHERE drive_file_id IS NOT NULL AND drive_file_id!='' GROUP BY drive_file_id HAVING cnt>1)`,
	).Scan(&row.Duplicate); err != nil {
		row.Error = err.Error()
		return
	}

	// Broken references: drive_file_id that might be stale.
	if checkBroken {
		broken, err := countBrokenRefs(ctx, db, "media_assets")
		if err != nil {
			row.Error = err.Error()
			return
		}
		row.BrokenReference = broken
	}
}

func classifyJobs(ctx context.Context, db *sql.DB, row *auditTableRow, checkBroken bool) {
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jobs WHERE status IN ('PENDING','LEASED','RUNNING','RETRY_WAIT')`,
	).Scan(&row.Live); err != nil {
		row.Error = err.Error()
		return
	}

	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jobs WHERE status IN ('SUCCEEDED','FAILED','CANCELLED')`,
	).Scan(&row.TerminalHistory); err != nil {
		row.Error = err.Error()
		return
	}

	if checkBroken {
		broken, err := countBrokenRefs(ctx, db, "jobs")
		if err != nil {
			row.Error = err.Error()
			return
		}
		row.BrokenReference = broken
	}
}

// ── Child classifiers ────────────────────────────────────────────────────

func classifyChild(ctx context.Context, db *sql.DB, t string, row *auditTableRow, checkBroken bool) {
	// Find the first ownership edge for this table.
	var rel ownershipRelation
	found := false
	for _, r := range canonicalOwnershipModel {
		if r.ChildTable == t && (r.Kind == "FK" || r.Kind == "LOGICAL") {
			rel = r
			found = true
			break
		}
	}
	if !found {
		row.Live = row.TotalRows
		return
	}

	col := rel.ChildColumn
	owner := rel.OwnerTable
	ownerCol := rel.OwnerColumn

	// orphan: reference non-null but owner doesn't exist.
	if err := db.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT COUNT(*) FROM %s c WHERE c.%s IS NOT NULL AND c.%s!='' AND NOT EXISTS (SELECT 1 FROM %s o WHERE o.%s=c.%s)`,
			qt(t), qt(col), qt(col), qt(owner), qt(ownerCol), qt(col)),
	).Scan(&row.Orphan); err != nil {
		row.Error = err.Error()
		return
	}

	// null-owner (optional provenance, not really an orphan).
	var nullOwner int
	if err := db.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT COUNT(*) FROM %s c WHERE c.%s IS NULL OR c.%s=''`, qt(t), qt(col), qt(col)),
	).Scan(&nullOwner); err != nil {
		row.Error = err.Error()
		return
	}

	// Live: reachable, with owner being LIVE.
	ownerLiveExpr := ownerLiveCondition(owner)
	q := fmt.Sprintf(
		`SELECT COUNT(*) FROM %s c WHERE c.%s IS NOT NULL AND c.%s!='' AND EXISTS (SELECT 1 FROM %s o WHERE o.%s=c.%s AND (%s))`,
		qt(t), qt(col), qt(col), qt(owner), qt(ownerCol), qt(col), ownerLiveExpr,
	)
	if err := db.QueryRowContext(ctx, q).Scan(&row.Live); err != nil {
		row.Error = err.Error()
		return
	}

	// Stale-but-referenced: reachable but owner is STALE/DELETED.
	ownerStaleExpr := ownerStaleCondition(owner)
	q2 := fmt.Sprintf(
		`SELECT COUNT(*) FROM %s c WHERE c.%s IS NOT NULL AND c.%s!='' AND EXISTS (SELECT 1 FROM %s o WHERE o.%s=c.%s AND (%s))`,
		qt(t), qt(col), qt(col), qt(owner), qt(ownerCol), qt(col), ownerStaleExpr,
	)
	if err := db.QueryRowContext(ctx, q2).Scan(&row.StaleButReferenced); err != nil {
		row.Error = err.Error()
		return
	}

	// Terminal history: child records that are terminal (e.g. FAILED steps, completed events).
	terminal, err := countChildTerminal(ctx, db, t, col, owner, ownerCol)
	if err != nil {
		row.Error = err.Error()
		return
	}
	row.TerminalHistory = terminal

	// Remaining rows: null-owner (the non-orphan, non-stale remainder).
	remaining := row.TotalRows - row.Live - row.StaleButReferenced - row.Orphan - row.TerminalHistory - nullOwner
	if remaining > 0 {
		row.Live += remaining
	}

	if checkBroken {
		broken, err := countBrokenRefs(ctx, db, t)
		if err != nil {
			row.Error = err.Error()
			return
		}
		row.BrokenReference = broken
	}
	dups, err := countChildDups(ctx, db, t, col)
	if err != nil {
		row.Error = err.Error()
		return
	}
	row.Duplicate = dups
}

// ── Cache classifiers ────────────────────────────────────────────────────

func classifyCache(ctx context.Context, db *sql.DB, t string, row *auditTableRow, checkBroken bool) {
	// Each cache table has a different expiry column. Use table-specific checks.
	row.TerminalHistory = 0
	// scan records a per-table error and reports whether classification may
	// continue; a failed count must never masquerade as a zero count.
	scan := func(query string, dest *int) bool {
		if err := db.QueryRowContext(ctx, query).Scan(dest); err != nil {
			row.Error = err.Error()
			return false
		}
		return true
	}
	switch t {
	case "stock_source_cache":
		if !scan(`SELECT COUNT(*) FROM stock_source_cache WHERE state='active'`, &row.Live) {
			return
		}
		if !scan(`SELECT COUNT(*) FROM stock_source_cache WHERE state IN ('invalidated','expired')`, &row.CacheExpired) {
			return
		}
	case "translation_cache":
		// TTL: last_used older than 7 days.
		row.Live = row.TotalRows
		if !scan(`SELECT COUNT(*) FROM translation_cache WHERE last_used < datetime('now','-7 days')`, &row.CacheExpired) {
			return
		}
		row.Live -= row.CacheExpired
	case "research_cache":
		row.Live = row.TotalRows
		if !scan(`SELECT COUNT(*) FROM research_cache WHERE last_used < datetime('now','-7 days')`, &row.CacheExpired) {
			return
		}
		row.Live -= row.CacheExpired
	case "media_query_cache":
		if !scan(`SELECT COUNT(*) FROM media_query_cache WHERE expires_at IS NULL OR expires_at > datetime('now')`, &row.Live) {
			return
		}
		if !scan(`SELECT COUNT(*) FROM media_query_cache WHERE expires_at IS NOT NULL AND expires_at <= datetime('now')`, &row.CacheExpired) {
			return
		}
	case "vidrush_provider_cache":
		row.Live = row.TotalRows
		if !scan(`SELECT COUNT(*) FROM vidrush_provider_cache WHERE updated_at < datetime('now','-2 days')`, &row.CacheExpired) {
			return
		}
		row.Live -= row.CacheExpired
	case "artifact_cache_entries":
		if !scan(`SELECT COUNT(*) FROM artifact_cache_entries WHERE status='READY'`, &row.Live) {
			return
		}
		if !scan(`SELECT COUNT(*) FROM artifact_cache_entries WHERE status IN ('FAILED','BUILDING')`, &row.CacheExpired) {
			return
		}
	case "artlist_search_cache":
		// TTL: 24h per config.
		row.Live = row.TotalRows
		if !scan(`SELECT COUNT(*) FROM artlist_search_cache WHERE cached_at < datetime('now','-24 hours')`, &row.CacheExpired) {
			return
		}
		row.Live -= row.CacheExpired
	default:
		// Generic cache: all live, check TTL if column exists.
		row.Live = row.TotalRows
		ok, err := hasColumn(ctx, db, t, "expires_at")
		if err != nil {
			row.Error = err.Error()
			return
		}
		if ok {
			if !scan(fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE expires_at IS NOT NULL AND expires_at <= datetime('now')`, qt(t)), &row.CacheExpired) {
				return
			}
			row.Live -= row.CacheExpired
		}
		ok, err = hasColumn(ctx, db, t, "state")
		if err != nil {
			row.Error = err.Error()
			return
		}
		if ok {
			if !scan(fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE state IN ('invalidated','expired')`, qt(t)), &row.CacheExpired) {
				return
			}
			if !scan(fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE state NOT IN ('invalidated','expired')`, qt(t)), &row.Live) {
				return
			}
		}
	}

	if checkBroken {
		broken, err := countBrokenRefs(ctx, db, t)
		if err != nil {
			row.Error = err.Error()
			return
		}
		row.BrokenReference = broken
	}
}

// ── Queue classifiers ────────────────────────────────────────────────────

func classifyQueue(ctx context.Context, db *sql.DB, t string, row *auditTableRow, checkBroken bool) {
	switch t {
	case "outbox_events":
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox_events WHERE status='pending'`).Scan(&row.Live); err != nil {
			row.Error = err.Error()
			return
		}
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox_events WHERE status IN ('completed','superseded','dead_letter')`).Scan(&row.TerminalHistory); err != nil {
			row.Error = err.Error()
			return
		}
	default:
		row.Live = row.TotalRows
	}
	if checkBroken {
		broken, err := countBrokenRefs(ctx, db, t)
		if err != nil {
			row.Error = err.Error()
			return
		}
		row.BrokenReference = broken
	}
}

// ── Broken reference detection ───────────────────────────────────────────

func countBrokenRefs(ctx context.Context, db *sql.DB, t string) (int, error) {
	var total int
	// Check drive_file_id columns.
	ok, err := hasColumn(ctx, db, t, "drive_file_id")
	if err != nil {
		return 0, err
	}
	if ok {
		// Non-null drive_file_ids: all count as "could be broken" until we
		// cross-check with Drive inventory (Fase 10). For Fase 3, report
		// the count of rows with drive_file_id as potential candidates.
		var n int
		if err := db.QueryRowContext(ctx,
			fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE drive_file_id IS NOT NULL AND drive_file_id!=''`, qt(t)),
		).Scan(&n); err != nil {
			return 0, err
		}
		total += n
	}
	ok, err = hasColumn(ctx, db, t, "local_path")
	if err != nil {
		return 0, err
	}
	if ok {
		// Check if the local_path file exists on disk (very cheap: only
		// count if path is non-empty, existence check deferred to Fase 4).
		var n int
		if err := db.QueryRowContext(ctx,
			fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE local_path IS NOT NULL AND local_path!=''`, qt(t)),
		).Scan(&n); err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}

// ── Duplicate detection ──────────────────────────────────────────────────

func countRootDups(ctx context.Context, db *sql.DB, t string) (int, error) {
	// For canonical roots, duplicates are rare (PRIMARY KEY should prevent).
	// Check if there's an identity column that should be unique.
	ok, err := hasColumn(ctx, db, t, "drive_file_id")
	if err != nil {
		return 0, err
	}
	if ok {
		var n int
		if err := db.QueryRowContext(ctx,
			fmt.Sprintf(`SELECT COALESCE(SUM(cnt-1),0) FROM (SELECT COUNT(*) cnt FROM %s WHERE drive_file_id IS NOT NULL AND drive_file_id!='' GROUP BY drive_file_id HAVING cnt>1)`, qt(t)),
		).Scan(&n); err != nil {
			return 0, err
		}
		return n, nil
	}
	ok, err = hasColumn(ctx, db, t, "sha256")
	if err != nil {
		return 0, err
	}
	if ok {
		var n int
		if err := db.QueryRowContext(ctx,
			fmt.Sprintf(`SELECT COALESCE(SUM(cnt-1),0) FROM (SELECT COUNT(*) cnt FROM %s WHERE sha256 IS NOT NULL AND sha256!='' GROUP BY sha256 HAVING cnt>1)`, qt(t)),
		).Scan(&n); err != nil {
			return 0, err
		}
		return n, nil
	}
	return 0, nil
}

func countChildDups(ctx context.Context, db *sql.DB, t string, fkCol string) (int, error) {
	// For child tables, duplicates = same (FK_col, <some key>) appearing >1 time.
	// General case: check duplicates on the FK column itself.
	var n int
	if err := db.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT COALESCE(SUM(cnt-1),0) FROM (SELECT COUNT(*) cnt FROM %s WHERE %s IS NOT NULL AND %s!='' GROUP BY %s HAVING cnt>1)`,
			qt(t), qt(fkCol), qt(fkCol), qt(fkCol)),
	).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// ── Owner live/stale conditions ──────────────────────────────────────────

func ownerLiveCondition(ownerTable string) string {
	switch ownerTable {
	case "media_assets":
		return `o.lifecycle_state IN ('ACTIVE','PUBLISHED','STAGING') AND o.deleted_at=''`
	case "jobs":
		return `o.status IN ('PENDING','LEASED','RUNNING','RETRY_WAIT')`
	default:
		return "1" // generic = live
	}
}

func ownerStaleCondition(ownerTable string) string {
	switch ownerTable {
	case "media_assets":
		return `o.lifecycle_state='DELETED' OR o.deleted_at!=''`
	case "jobs":
		return `o.status IN ('SUCCEEDED','FAILED','CANCELLED')`
	default:
		return "0" // generic = never stale (conservative)
	}
}

func countChildTerminal(ctx context.Context, db *sql.DB, t, fkCol, ownerTable, ownerCol string) (int, error) {
	// For child of jobs: if the owner job is terminal AND the child has
	// status/state columns indicating completion, those are terminal history.
	if ownerTable != "jobs" {
		return 0, nil
	}
	// For job children: check if they have a status column indicating completion.
	ok, err := hasColumn(ctx, db, t, "status")
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, nil
	}
	var n int
	if err := db.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT COUNT(*) FROM %s c WHERE EXISTS (SELECT 1 FROM %s o WHERE o.%s=c.%s AND o.status IN ('SUCCEEDED','FAILED','CANCELLED'))`,
			qt(t), qt(ownerTable), qt(ownerCol), qt(fkCol)),
	).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// ── Shared helpers ───────────────────────────────────────────────────────

func hasColumn(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	var x int
	err := db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT 1 FROM pragma_table_info(%q) WHERE name=%q", table, column),
	).Scan(&x)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, err
}

func qt(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

func loadRealTables(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		"SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name NOT LIKE '%_v224' ORDER BY name",
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

// ── Output ────────────────────────────────────────────────────────────────

func printAuditReport(r *auditRecord) {
	fmt.Println("=== SQLite Audit (FASE 3) ===")
	fmt.Printf("  mode:        %s\n", r.Mode)
	fmt.Printf("  generated:   %s\n", r.GeneratedAt)
	fmt.Printf("  no deletions: %v\n", r.NoDeletions)
	fmt.Println()
	fmt.Printf("  SUMMARY: %d total rows across %d tables\n", r.Summary.TotalRows, len(r.Tables))
	fmt.Printf("    %-22s %d\n", "LIVE", r.Summary.Live)
	fmt.Printf("    %-22s %d\n", "STALE_BUT_REFERENCED", r.Summary.StaleButReferenced)
	fmt.Printf("    %-22s %d\n", "ORPHAN", r.Summary.Orphan)
	fmt.Printf("    %-22s %d\n", "DUPLICATE", r.Summary.Duplicate)
	fmt.Printf("    %-22s %d\n", "TERMINAL_HISTORY", r.Summary.TerminalHistory)
	fmt.Printf("    %-22s %d\n", "CACHE_EXPIRED", r.Summary.CacheExpired)
	fmt.Printf("    %-22s %d\n", "BROKEN_REFERENCE", r.Summary.BrokenReference)
	fmt.Println()

	// Print tables with non-zero non-LIVE rows.
	fmt.Println("  --- Non-zero classifications ---")
	printed := 0
	for _, s := range r.Tables {
		if s.Error != "" {
			fmt.Printf("    %-45s ERROR: %s\n", s.Table, s.Error)
			continue
		}
		if s.TotalRows == 0 || (s.Live == s.TotalRows && s.Duplicate == 0 && s.BrokenReference == 0) {
			continue
		}
		printed++
		parts := []string{}
		add := func(label string, n int) {
			if n > 0 {
				parts = append(parts, fmt.Sprintf("%s=%d", label, n))
			}
		}
		add("LIVE", s.Live)
		add("STALE_REF", s.StaleButReferenced)
		add("ORPHAN", s.Orphan)
		add("DUP", s.Duplicate)
		add("TERMINAL", s.TerminalHistory)
		add("EXPIRED", s.CacheExpired)
		add("BROKEN", s.BrokenReference)
		fmt.Printf("    %-45s %-6s total=%-7d %s\n", s.Table, s.RootType, s.TotalRows, strings.Join(parts, " "))
		if printed >= 40 {
			remaining := 0
			for _, s2 := range r.Tables[printed:] {
				if s2.Live != s2.TotalRows || s2.Duplicate > 0 || s2.BrokenReference > 0 {
					remaining++
				}
			}
			if remaining > 0 {
				fmt.Printf("    ... +%d more tables\n", remaining)
			}
			break
		}
	}
}
