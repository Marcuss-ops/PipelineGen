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
	"errors"
	"fmt"
	"strings"
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

// ── Root type resolver ──────────────────────────────────────────────────

// ── Canonical root classifiers ───────────────────────────────────────────

// ── Child classifiers ────────────────────────────────────────────────────

// ── Cache classifiers ────────────────────────────────────────────────────

// ── Queue classifiers ────────────────────────────────────────────────────

// ── Broken reference detection ───────────────────────────────────────────

// ── Duplicate detection ──────────────────────────────────────────────────

// ── Owner live/stale conditions ──────────────────────────────────────────

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

// ── Output ────────────────────────────────────────────────────────────────
