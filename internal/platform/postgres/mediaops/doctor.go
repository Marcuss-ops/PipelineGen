// Package mediaops holds the operator-facing operations on the PostgreSQL
// media database: health checks (`db doctor`), retention sweeps and the
// migration ledger. They are
// kept out of `postgres/media`, which is the domain SSOT and has a package line
// budget, because these are operational surfaces over the same schema rather
// than media domain logic.
package mediaops

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/dbhealth"
)

var fkDefRE = regexp.MustCompile(`FOREIGN KEY \((.+?)\) REFERENCES (.+?)\((.+?)\)`)

// DoctorFindings performs read-only PostgreSQL health checks for the media
// database: FK violations, invalid indexes, and dead-tuple pressure. It never
// repairs; the finding names the owning ticket.
func DoctorFindings(ctx context.Context, db *sql.DB) ([]dbhealth.Finding, error) {
	if db == nil {
		return nil, fmt.Errorf("db doctor: nil database")
	}
	var findings []dbhealth.Finding
	add := func(check string, sev dbhealth.Severity, detail, remediation string) {
		findings = append(findings, dbhealth.Finding{
			Plane: "postgres/media", Check: check, Severity: sev, Detail: detail, Remediation: remediation,
		})
	}

	// FK violations: evaluate every public foreign key. The queries are
	// read-only and bounded by the parent index.
	rows, err := db.QueryContext(ctx, `
		SELECT conrelid::regclass::text, conname, pg_get_constraintdef(oid)
		FROM pg_constraint
		WHERE contype = 'f' AND connamespace = 'public'::regnamespace
		ORDER BY conrelid::regclass::text, conname`)
	if err != nil {
		return findings, fmt.Errorf("db doctor: list foreign keys: %w", err)
	}
	type fk struct{ child, name, def string }
	var fks []fk
	for rows.Next() {
		var f fk
		if err := rows.Scan(&f.child, &f.name, &f.def); err != nil {
			_ = rows.Close()
			return findings, fmt.Errorf("db doctor: scan foreign key: %w", err)
		}
		fks = append(fks, f)
	}
	if err := rows.Close(); err != nil {
		return findings, err
	}
	if err := rows.Err(); err != nil {
		return findings, err
	}
	var fkViolations int64
	for _, f := range fks {
		query, ok := fkViolationQuery(f.child, f.def)
		if !ok {
			continue
		}
		var n int64
		if err := db.QueryRowContext(ctx, query).Scan(&n); err != nil {
			add("foreign_key", dbhealth.SeverityWarning, fmt.Sprintf("cannot check %s (%s): %v", f.name, f.child, err), "DB-08")
			continue
		}
		if n > 0 {
			fkViolations += n
			add("foreign_key", dbhealth.SeverityError, fmt.Sprintf("%s on %s has %d orphan row(s)", f.name, f.child, n), "DB-01/DB-08: repair children, then keep FK enforcement on")
		}
	}
	dbhealth.DBFKViolations.WithLabelValues("postgres/media").Set(float64(fkViolations))
	if !hasSeverity(findings, "foreign_key") {
		add("foreign_key", dbhealth.SeverityInfo, "no violations", "")
	}

	// Invalid indexes (failed CONCURRENTLY builds or corruption).
	irows, err := db.QueryContext(ctx, `
		SELECT c.relname FROM pg_index i
		JOIN pg_class c ON c.oid = i.indexrelid
		WHERE NOT i.indisvalid`)
	if err != nil {
		add("invalid_index", dbhealth.SeverityWarning, err.Error(), "DB-07/DB-08")
	} else {
		var bad []string
		for irows.Next() {
			var name string
			if err := irows.Scan(&name); err != nil {
				break
			}
			bad = append(bad, name)
		}
		_ = irows.Close()
		if len(bad) > 0 {
			add("invalid_index", dbhealth.SeverityError, "invalid indexes: "+strings.Join(bad, ", "), "DB-07: REINDEX CONCURRENTLY")
		} else {
			add("invalid_index", dbhealth.SeverityInfo, "all indexes valid", "")
		}
	}

	// Dead-tuple pressure: the DB-06 migration lowers autovacuum thresholds;
	// this check catches a table that still accumulated a large backlog.
	drows, err := db.QueryContext(ctx, `
		SELECT relname, n_dead_tup, n_live_tup, COALESCE(last_autovacuum::text, 'never')
		FROM pg_stat_user_tables
		ORDER BY n_dead_tup DESC`)
	if err != nil {
		add("dead_tuples", dbhealth.SeverityWarning, err.Error(), "DB-06/DB-08")
	} else {
		count := 0
		for drows.Next() {
			var table, last string
			var dead, live int64
			if err := drows.Scan(&table, &dead, &live, &last); err != nil {
				break
			}
			// Every table is exported, so the series is a complete picture and
			// a recovered table drops back to 0 instead of going stale.
			dbhealth.DBDeadTuples.WithLabelValues("postgres/media", table).Set(float64(dead))
			if dead <= 1000 {
				continue
			}
			count++
			add("dead_tuples", dbhealth.SeverityWarning,
				fmt.Sprintf("%s: %d dead / %d live (last_autovacuum=%s)", table, dead, live, last),
				"DB-06: VACUUM (ANALYZE) and verify autovacuum thresholds")
		}
		_ = drows.Close()
		if count == 0 {
			add("dead_tuples", dbhealth.SeverityInfo, "no table above 1000 dead tuples", "")
		}
	}

	return findings, nil
}

func hasSeverity(findings []dbhealth.Finding, check string) bool {
	for _, f := range findings {
		if f.Check == check {
			return true
		}
	}
	return false
}

// fkViolationQuery turns a pg_get_constraintdef() string into a count query.
// It returns false for shapes the parser does not recognise rather than
// emitting a wrong query.
func fkViolationQuery(child, def string) (string, bool) {
	m := fkDefRE.FindStringSubmatch(def)
	if len(m) != 4 {
		return "", false
	}
	childCols := splitIdentList(m[1])
	parent := stripSchema(m[2])
	parentCols := splitIdentList(m[3])
	if len(childCols) == 0 || len(childCols) != len(parentCols) {
		return "", false
	}
	var notNull, join []string
	for i := range childCols {
		notNull = append(notNull, fmt.Sprintf(`c.%s IS NOT NULL`, quoteIdent(childCols[i])))
		join = append(join, fmt.Sprintf(`p.%s = c.%s`, quoteIdent(parentCols[i]), quoteIdent(childCols[i])))
	}
	return fmt.Sprintf(
		`SELECT COUNT(*) FROM %s c WHERE (%s) AND NOT EXISTS (SELECT 1 FROM %s p WHERE %s)`,
		quoteIdent(child), strings.Join(notNull, " AND "), quoteIdent(parent), strings.Join(join, " AND "),
	), true
}

func splitIdentList(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.Trim(p, `"`)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func stripSchema(s string) string {
	s = strings.TrimSpace(s)
	// pg_get_constraintdef may emit public.parent or "public"."parent".
	parts := strings.Split(s, ".")
	last := parts[len(parts)-1]
	return strings.Trim(last, `"`)
}

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
