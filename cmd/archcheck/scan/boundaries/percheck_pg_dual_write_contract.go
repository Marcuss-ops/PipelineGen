// Package scan — percheck_pg_dual_write_contract.go: forward prevention for the
// migration 004 DUAL-WRITE contract on the PostgreSQL media SSOT.
//
// WHY THIS GATE EXISTS. Migration 004_media_timestamps_timestamptz.sql adds a
// TIMESTAMPTZ mirror next to every legacy TEXT timestamp on the hot-path media
// tables so the read path can later flip to typed timestamps without a second
// backfill. 004 is explicit that the WRITER owns the dual-write, not the
// migration: "DUAL-WRITE (application layer, NOT in this file)". BASELINE_PLAN
// section 5 states the same as a single-transaction dual-write.
//
// That contract was only partially implemented and silently decayed. Measured
// on a live media database before the fix (2026-09-16):
//
//	media_assets.index_state_updated_at = '2026-09-16T…'  →  …_ts = NULL
//	outbox_events.lease_expiry          = '2026-09-16T…'  →  …_ts = NULL
//	outbox_events.completed_at          = '2026-09-16T…'  →  …_ts = NULL
//	outbox_events.updated_at_ts         frozen at its insert-time value
//
// The behavioural proof that the fixed writers agree is the live-PG test
// dual_write_timestamps_test.go. This scanner is the FORWARD PREVENTION layer
// that test cannot provide: a test only covers the paths someone remembered to
// call, whereas the gate rejects the SHAPE at review time. Without it the same
// drift reappears the first time anyone adds a `completed_at = $1` without
// `completed_at_ts = NULLIF($1, ”)::timestamptz`.
//
// WHAT IT CHECKS. The pair list is taken verbatim from 004 (see
// pgDualWritePairs) — this file declares no second copy of the schema. For every
// PostgreSQL-dialect statement that mutates one of those tables it enforces, per
// pair:
//
//  1. half write        — the TEXT column is assigned but its mirror is not.
//     This is the dangerous direction: the TEXT advances and the mirror goes
//     stale or stays NULL, so the two representations of one fact diverge.
//  2. bind mismatch     — both are assigned but they do NOT derive from the same
//     source. The check is structural, not textual: the placeholder set on the
//     TEXT side must equal the placeholder set on the mirror side, so
//     `updated_at = $3, updated_at_ts = NULLIF($3, ”)::timestamptz` passes and
//     `updated_at = $3, …_ts = NULLIF($2, ”)::timestamptz` fails. When neither
//     side is parameterised the mirror must self-derive from the same TEXT
//     column (`NULLIF(updated_at, ”)`) or be the identical literal (`NULL`),
//     which is what the `ON CONFLICT … DO UPDATE SET updated_at = EXCLUDED.updated_at`
//     form and the legacy `…_ts = NULL` lease resets legitimately do.
//  3. mirror only       — the mirror is assigned from a bound value while the
//     TEXT column is untouched, which diverges just as surely in the other
//     direction. A mirror that self-derives from the table's own TEXT column is
//     allowed (that form cannot diverge).
//
// For `INSERT INTO <table> (…)` the column list is the assignment set, so
// omitting a mirror from the list while listing its TEXT sibling is a violation
// (and the reverse, which would let the TEXT take its DEFAULT while the mirror is
// written).
//
// DIALECT PRECISION. The mirror columns exist ONLY on PostgreSQL. The operational
// SQLite schema has no *_ts columns at all, so a SQLite statement legitimately
// writes the TEXT column alone and must never be reported. Every statement is
// therefore classified by the dialect of its enclosing SQL expression — the same
// `$N` discriminator percheck_sqlite_media_reader_ban uses — and `?`-bound
// statements are skipped outright.
//
// STATIC REACH AND ITS LIMIT. A SET clause composed at runtime
// (`"UPDATE media_assets SET " + strings.Join(sets, ", ") + …`) has no SQL to
// inspect; those sites are covered by the behavioural test, not here. The gate
// states that limit rather than pretending to cover it.
//
// SUNSET. Like every expand-phase scaffold this one is temporary by design: when
// the legacy TEXT columns are contracted (BASELINE_PLAN section 5.2 contract
// phase) the dual-write, the *_ts columns and this gate are deleted together.
// It must not survive as a permanent compatibility layer.
//
// matched rule_id: `percheck_pg_dual_write_contract`.
package boundaries

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

const pgDualWriteRule = "percheck_pg_dual_write_contract"

// dualWritePair is one TEXT column and the TIMESTAMPTZ mirror that must be
// written with it.
type dualWritePair struct {
	text   string
	mirror string
}

// pgDualWritePairs is the EXACT expand-column list of migration
// 004_media_timestamps_timestamptz.sql, grouped by table. It is a transcription
// of the migration, which remains the single source of truth for the schema; when
// 004 gains or loses a column this map changes in the same reviewed change.
var pgDualWritePairs = map[string][]dualWritePair{
	"media_assets": {
		{text: "created_at", mirror: "created_at_ts"},
		{text: "updated_at", mirror: "updated_at_ts"},
		{text: "index_state_updated_at", mirror: "index_state_updated_at_ts"},
		{text: "enrich_state_updated_at", mirror: "enrich_state_updated_at_ts"},
		{text: "discovered_at", mirror: "discovered_at_ts"},
	},
	"asset_locations": {
		{text: "created_at", mirror: "created_at_ts"},
		{text: "updated_at", mirror: "updated_at_ts"},
	},
	"outbox_events": {
		{text: "created_at", mirror: "created_at_ts"},
		{text: "updated_at", mirror: "updated_at_ts"},
		{text: "next_attempt_at", mirror: "next_attempt_at_ts"},
		{text: "lease_expiry", mirror: "lease_expiry_ts"},
		{text: "completed_at", mirror: "completed_at_ts"},
	},
}

// pgDualWriteScanRoots mirrors the media gate's roots: the media writer family
// lives in internal/, and the admin CLI is the historical exception surface.
var pgDualWriteScanRoots = []string{"internal", "cmd"}

var (
	// pgDualWriteUpdateRe matches an UPDATE against a dual-written table.
	pgDualWriteUpdateSetRe = regexp.MustCompile(`(?i)\bUPDATE\s+(media_assets|asset_locations|outbox_events)\s+SET\b`)
	// pgDualWriteInsertRe matches the column list of an INSERT into one of them.
	pgDualWriteInsertRe = regexp.MustCompile(`(?i)\bINSERT\s+INTO\s+(media_assets|asset_locations|outbox_events)\s*\(`)
	// pgDualWriteDoUpdateRe matches the conflict branch whose SET clause is a
	// second assignment set for the same row.
	pgDualWriteDoUpdateRe = regexp.MustCompile(`(?i)\bDO\s+UPDATE\s+SET\b`)
	// pgDualWriteWhereRe bounds a SET clause at the top level.
	pgDualWriteWhereRe = regexp.MustCompile(`(?i)\bWHERE\b`)
	// pgDualWritePlaceholderRe is the PostgreSQL positional bind discriminator.
	pgDualWritePlaceholderRe = regexp.MustCompile(`\$\d+`)
	// pgDualWriteNowRe matches a statement's own transaction timestamp.
	pgDualWriteNowRe = regexp.MustCompile(`(?i)\bnow\s*\(|\bcurrent_timestamp\b`)
)

// pgDualWriteNote is the operator-facing remediation text.
const pgDualWriteNote = "migration 004 dual-write contract violated (PostgreSQL media SSOT, September 2026): a dual-written TEXT timestamp and its TIMESTAMPTZ mirror are two representations of one fact and MUST be written together from the same source. Writing only the TEXT half makes the mirror go stale/NULL, which breaks the documented 'readers prefer *_ts' flip; writing them from different binds makes them disagree immediately. Add the missing mirror as NULLIF(<same bind>, '')::timestamptz (and add the column to an INSERT list / the ON CONFLICT update set), or drop both. See BASELINE_PLAN.md section 5 and migrations/postgres/004_media_timestamps_timestamptz.sql. Sunset: delete this gate with the legacy TEXT columns in the contract phase — it must not become permanent compatibility."

// ScanPGDualWriteContract walks internal/ and cmd/ and reports every
// PostgreSQL-dialect statement that writes one half of a migration-004 timestamp
// pair without the other.
func ScanPGDualWriteContract(root string, _ *policy.Policy, r *report.Report) {
	for _, scanRoot := range pgDualWriteScanRoots {
		absRoot := filepath.Join(root, scanRoot)
		filepath.Walk(absRoot, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if info.IsDir() {
				if policy.StandardSkipDirs[filepath.Base(path)] {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			inspectPGDualWriteFile(root, path, r)
			return nil
		})
	}
}

// inspectPGDualWriteFile scans one production Go file.
func inspectPGDualWriteFile(root, absPath string, r *report.Report) {
	relPath, err := filepath.Rel(root, absPath)
	if err != nil {
		relPath = absPath
	}
	relPath = filepath.ToSlash(relPath)

	if strings.HasPrefix(relPath, policy.ScannerSourcePrefix) {
		return
	}
	if hasAnyPathPrefix(relPath, policy.SQLMigrationPrefixes) || policy.IsTestOnlySupportFile(relPath) {
		return
	}

	source, err := os.ReadFile(absPath)
	if err != nil {
		r.Violations = append(r.Violations, report.Violation{
			File:        relPath,
			Rule:        pgDualWriteRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "file_unreadable",
			Note:        pgDualWriteNote + " | cannot open file: " + err.Error(),
		})
		return
	}

	// Comments are masked so the many doc comments that quote these statements
	// (delete_saga.go, outbox_index_fence.go, outbox.go) are never classified as
	// writes. A file that does not parse is scanned verbatim — fail-closed.
	masked := source
	fileSet := token.NewFileSet()
	parsed, parseErr := parser.ParseFile(fileSet, absPath, source, parser.ParseComments)
	var spans []sqlMediaSpan
	if parseErr == nil && parsed != nil {
		masked = maskGoComments(source, fileSet, parsed)
		if positionFile := fileSet.File(parsed.Pos()); positionFile != nil {
			spans = sqlMediaSpans(source, positionFile, parsed)
		}
	}
	text := string(masked)

	for _, match := range pgDualWriteUpdateSetRe.FindAllStringSubmatchIndex(text, -1) {
		end, ok := pgDualWriteStatementWindow(spans, text, match[0])
		if !ok {
			continue // SQLite dialect (no *_ts columns) or dynamically composed
		}
		stmt := text[match[0]:end]
		table := strings.ToLower(text[match[2]:match[3]])
		clause := pgDualWriteSetClause(stmt, match[1]-match[0])
		pgDualWriteCheckAssignments(r, relPath, text, match[0], table, "UPDATE SET", clause)
	}

	for _, match := range pgDualWriteInsertRe.FindAllStringSubmatchIndex(text, -1) {
		end, ok := pgDualWriteStatementWindow(spans, text, match[0])
		if !ok {
			continue
		}
		stmt := text[match[0]:end]
		table := strings.ToLower(text[match[2]:match[3]])
		openIdx := strings.Index(stmt, "(")
		if openIdx < 0 {
			continue
		}
		cols, closeIdx := pgDualWriteColumnList(stmt, openIdx)
		if closeIdx < 0 {
			continue
		}
		pgDualWriteCheckColumns(r, relPath, text, match[0], table, cols)
		// The ON CONFLICT ... DO UPDATE SET branch is a second assignment set.
		rest := stmt[closeIdx:]
		if loc := pgDualWriteDoUpdateRe.FindStringIndex(rest); loc != nil {
			clause := pgDualWriteSetClause(rest, loc[1])
			pgDualWriteCheckAssignments(r, relPath, text, match[0], table, "ON CONFLICT DO UPDATE SET", clause)
		}
	}
}

// pgDualWriteStatementWindow returns the end offset of the SQL expression
// containing offset, and whether that expression is PostgreSQL-dialect.
//
// A SQLite statement (no $N) is skipped: the mirror columns do not exist on the
// operational schema, so writing the TEXT half alone there is correct. A
// dynamically composed statement has no span match at all and is likewise
// skipped — see the STATIC REACH note on the package doc.
func pgDualWriteStatementWindow(spans []sqlMediaSpan, text string, offset int) (int, bool) {
	span, ok := sqlMediaSpanContaining(spans, offset)
	if !ok || !span.postgres {
		return 0, false
	}
	end := span.end
	if end > len(text) {
		end = len(text)
	}
	return end, true
}

// pgDualWriteSetClause returns the assignment list that follows a SET keyword,
// bounded by the first top-level WHERE (paren depth 0) or the end of the
// statement.
func pgDualWriteSetClause(stmt string, start int) string {
	if start < 0 || start > len(stmt) {
		return ""
	}
	depth := 0
	for i := start; i < len(stmt); {
		switch stmt[i] {
		case '(':
			depth++
			i++
			continue
		case ')':
			if depth > 0 {
				depth--
			}
			i++
			continue
		}
		if depth == 0 && (stmt[i] == 'W' || stmt[i] == 'w') {
			if loc := pgDualWriteWhereRe.FindStringIndex(stmt[i:]); loc != nil && loc[0] == 0 {
				return stmt[start:i]
			}
		}
		i++
	}
	return stmt[start:]
}

// pgDualWriteAssignments parses `col = expr, col = expr` into a column→expr map.
// Commas nested inside parentheses (jsonb_build_object, COALESCE, casts) are not
// separators.
func pgDualWriteAssignments(clause string) map[string]string {
	out := map[string]string{}
	depth := 0
	var current strings.Builder
	flush := func() {
		part := strings.TrimSpace(current.String())
		current.Reset()
		if part == "" {
			return
		}
		eq := strings.Index(part, "=")
		if eq < 0 {
			return
		}
		col := strings.Trim(strings.TrimSpace(part[:eq]), `"`)
		expr := strings.TrimSpace(part[eq+1:])
		if col == "" {
			return
		}
		out[strings.ToLower(col)] = expr
	}
	for i := 0; i < len(clause); i++ {
		switch clause[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				flush()
				continue
			}
		}
		current.WriteByte(clause[i])
	}
	flush()
	return out
}

// pgDualWriteColumnList parses the parenthesised column list of an INSERT and
// returns the decoded names plus the index of the closing paren.
func pgDualWriteColumnList(stmt string, openIdx int) ([]string, int) {
	depth := 0
	for i := openIdx; i < len(stmt); i++ {
		switch stmt[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				inner := stmt[openIdx+1 : i]
				var cols []string
				for _, part := range strings.Split(inner, ",") {
					part = strings.TrimSpace(part)
					if part == "" {
						continue
					}
					if fields := strings.Fields(part); len(fields) > 0 {
						part = fields[0]
					}
					part = strings.Trim(strings.TrimSpace(part), `"`)
					if part != "" {
						cols = append(cols, strings.ToLower(part))
					}
				}
				return cols, i
			}
		}
	}
	return nil, -1
}

// pgDualWriteCheckColumns enforces the contract on an INSERT column list.
func pgDualWriteCheckColumns(r *report.Report, relPath, text string, offset int, table string, cols []string) {
	present := map[string]bool{}
	for _, c := range cols {
		present[c] = true
	}
	for _, pair := range pgDualWritePairs[table] {
		hasText, hasMirror := present[pair.text], present[pair.mirror]
		switch {
		case hasText && hasMirror:
			// Both halves present: nothing to add, the VALUES bind is checked
			// behaviourally (an INSERT list carries no expressions to compare).
		case hasText && !hasMirror:
			pgDualWriteViolate(r, relPath, text, offset, "insert_missing_mirror",
				table, pair, "INSERT column list writes "+pair.text+" without "+pair.mirror)
		case !hasText && hasMirror:
			pgDualWriteViolate(r, relPath, text, offset, "insert_mirror_without_text",
				table, pair, "INSERT column list writes "+pair.mirror+" without "+pair.text+" (the TEXT column would take its DEFAULT and diverge from the mirror)")
		}
	}
}

// pgDualWriteCheckAssignments enforces the contract on one SET clause.
func pgDualWriteCheckAssignments(r *report.Report, relPath, text string, offset int, table, kind, clause string) {
	assignments := pgDualWriteAssignments(clause)
	for _, pair := range pgDualWritePairs[table] {
		textExpr, hasText := assignments[pair.text]
		mirrorExpr, hasMirror := assignments[pair.mirror]
		switch {
		case hasText && hasMirror:
			if !pgDualWriteSameSource(textExpr, mirrorExpr, pair.text, pair.mirror) {
				pgDualWriteViolate(r, relPath, text, offset, "dual_write_bind_mismatch", table, pair,
					kind+" assigns "+pair.text+" and "+pair.mirror+" from DIFFERENT sources ("+pair.text+" = "+strings.TrimSpace(textExpr)+"; "+pair.mirror+" = "+strings.TrimSpace(mirrorExpr)+")")
			}
		case hasText && !hasMirror:
			pgDualWriteViolate(r, relPath, text, offset, "dual_write_half_write", table, pair,
				kind+" writes "+pair.text+" without "+pair.mirror)
		case !hasText && hasMirror:
			if !pgDualWriteSelfDerived(mirrorExpr, pair.text) {
				pgDualWriteViolate(r, relPath, text, offset, "dual_write_mirror_without_text", table, pair,
					kind+" writes "+pair.mirror+" without "+pair.text+" and does not derive it from that column ("+pair.mirror+" = "+strings.TrimSpace(mirrorExpr)+")")
			}
		}
	}
}

// pgDualWriteViolate appends one violation at the statement's line.
func pgDualWriteViolate(r *report.Report, relPath, text string, offset int, matchedRule, table string, pair dualWritePair, detail string) {
	if offset > len(text) {
		offset = len(text)
	}
	line := 1 + strings.Count(text[:offset], "\n")
	r.Violations = append(r.Violations, report.Violation{
		File:        relPath,
		Line:        line,
		Rule:        pgDualWriteRule,
		Severity:    string(report.SeverityError),
		MatchedRule: matchedRule,
		Note: pgDualWriteNote +
			" | table: " + table +
			" | pair: " + pair.text + " -> " + pair.mirror +
			" | file: " + relPath +
			" | " + detail,
	})
}

// pgDualWriteSameSource reports whether two assigned expressions derive from the
// same value.
//
// Parameterised sides must reference the SAME placeholder set, which is what
// makes the recommended `TEXT = $N, TEXT_ts = NULLIF($N, ”)::timestamptz` form
// pass while a mismatched bind fails.
//
// Non-parameterised sides must be a faithful rewrite of one source. The accepted
// shapes are structural, not a keyword allowlist:
//
//   - the mirror self-derives from the TEXT COLUMN (`…_ts = NULLIF(updated_at, ”)`),
//     which cannot diverge because it reads the value it mirrors;
//   - the mirror is the same expression with the mirror column renamed back to
//     the TEXT column, which is exactly the ON CONFLICT form
//     (`updated_at = excluded.updated_at, updated_at_ts = excluded.updated_at_ts`);
//   - the mirror wraps the TEXT expression in the canonical
//     NULLIF(…, ”)::timestamptz cast, so the TEXT expression is a substring of
//     the mirror;
//   - both sides are the statement's own transaction timestamp (`now()` /
//     `CURRENT_TIMESTAMP`), which PostgreSQL evaluates once per statement, so the
//     typed and the rendered value denote the same instant.
func pgDualWriteSameSource(textExpr, mirrorExpr, textCol, mirrorCol string) bool {
	textBinds := pgDualWritePlaceholderRe.FindAllString(textExpr, -1)
	mirrorBinds := pgDualWritePlaceholderRe.FindAllString(mirrorExpr, -1)
	if len(textBinds) > 0 || len(mirrorBinds) > 0 {
		if len(textBinds) != len(mirrorBinds) {
			return false
		}
		sort.Strings(textBinds)
		sort.Strings(mirrorBinds)
		for i := range textBinds {
			if textBinds[i] != mirrorBinds[i] {
				return false
			}
		}
		return true
	}
	if pgDualWriteSelfDerived(mirrorExpr, textCol) {
		return true
	}
	if pgDualWriteNowRe.MatchString(textExpr) && pgDualWriteNowRe.MatchString(mirrorExpr) {
		return true
	}
	text := pgDualWriteNormalise(textExpr, mirrorCol, textCol)
	mirror := pgDualWriteNormalise(mirrorExpr, mirrorCol, textCol)
	if text == "" {
		return mirror == ""
	}
	return mirror == text || strings.Contains(mirror, text)
}

// pgDualWriteNormalise lowercases an expression, renames the mirror column back
// to its TEXT sibling and removes all whitespace, so the two sides of a pair can
// be compared structurally across formatting and casing.
func pgDualWriteNormalise(expr, mirrorCol, textCol string) string {
	s := strings.ToLower(expr)
	s = strings.ReplaceAll(s, strings.ToLower(mirrorCol), strings.ToLower(textCol))
	return strings.Join(strings.Fields(s), "")
}

// pgDualWriteSelfDerived reports whether expr references the named TEXT column as
// a whole identifier (so `NULLIF(updated_at, ”)::timestamptz` and
// `EXCLUDED.updated_at_ts`-style mirrors of that column are accepted, while an
// unrelated identifier that merely contains the name is not).
func pgDualWriteSelfDerived(expr, textCol string) bool {
	re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(textCol) + `\b`)
	return re.MatchString(expr)
}
