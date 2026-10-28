// Package scan — percheck_state_matrix.go: forward-prevention gate for the
// status/state vocabulary SSOT (docs/migrations/STATE_MATRIX.md).
//
// WHY THIS GATE EXISTS. The 065→066→069 flip-flop (lowercase → UPPERCASE →
// UPPERCASE-with-CHECK on jobs.status) and the 130 READY→STAGED rename proved
// that status vocabularies were being rewritten in place by migration chains,
// leaving live databases with whichever history they happened to have. The
// STATE_MATRIX freezes the vocabularies and this gate makes the freeze
// enforceable: transitions must go through views or generated columns, never
// through a rewrite migration.
//
// WHAT IT CHECKS. Two legs, both fail-closed:
//
//  1. STATIC REWRITE BAN — every UPDATE ... SET <state column> = ... in
//     migrations/sqlite/, migrations/sqlite_jobs/ and migrations/postgres/ must
//     live in docs/migrations/state-rewrite-allowlist.txt (a closed historical
//     list). A rewrite in any other file is a SeverityError. An allowlist entry
//     whose file is gone, or which no longer contains a rewrite, is stale and
//     fails too (one-directional pin).
//
//  2. POSTGRES STATIC AGREEMENT — the PostgreSQL media declarations
//     (migrations/postgres/*.sql) are validated against the matrix rows whose
//     plane includes `postgres`: default and CHECK vocabulary must match
//     exactly. This is the static twin of the live-schema test
//     TestStateMatrixMatchesLiveSchema (which covers the sqlite and sqlite_jobs
//     planes against freshly migrated DBs).
//
// The matrix itself is parsed with the shared parser
// internal/platform/sqlite/statematrix: the machine-readable table between the
// state-matrix:begin/end markers. A matrix that cannot be parsed is a
// violation, not a silent pass.
//
// matched rule_id: `percheck_state_matrix`.
package migrations

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/statematrix"
)

const (
	stateMatrixRule      = "percheck_state_matrix"
	stateMatrixDocPath   = "docs/migrations/STATE_MATRIX.md"
	stateRewriteAllowTxt = "docs/migrations/state-rewrite-allowlist.txt"
)

// stateMigrationRoots are the migration directories whose state vocabulary is
// pinned by the matrix. PostgreSQL is included: its media tables mirror the
// SQLite ones column-for-column (migrations/postgres/001 header).
var stateMigrationRoots = []string{
	filepath.Join("migrations", "sqlite"),
	filepath.Join("migrations", "sqlite_jobs"),
	filepath.Join("migrations", "postgres"),
}

var (
	// updateStmtRe finds UPDATE statements; the SET list runs until the
	// first WHERE or statement terminator, so a WHERE status = ... predicate
	// is never mistaken for an assignment.
	updateStmtRe = regexp.MustCompile(`(?is)\bUPDATE\s+"?([a-z_]+)"?\s+SET\s+(.*?)(?:\bWHERE\b|;)`)
	// stateAssignRe matches an assignment whose column name ENDS in
	// status/state (index_state_updated_at and friends do not match: the
	// name must end right before the '=').
	stateAssignRe = regexp.MustCompile(`(?i)(?:^|,)\s*"?([a-z_]*(?:_?status|_?state))"?\s*=`)
)

// ScanStateMatrix runs the two legs described in the package comment.
func ScanStateMatrix(root string, _ *policy.Policy, r *report.Report) {
	rows := loadStateMatrix(root, r)
	if rows == nil {
		return
	}
	scanStateRewrites(root, r)
	scanPostgresAgreement(root, rows, r)
}

// loadStateMatrix parses the matrix; on any failure it emits a violation and
// returns nil so the caller skips the dependent checks (fail-closed).
func loadStateMatrix(root string, r *report.Report) []statematrix.Row {
	path := filepath.Join(root, stateMatrixDocPath)
	f, err := os.Open(path)
	if err != nil {
		stateMatrixViolation(r, stateMatrixDocPath, 0, "state_matrix_unreadable",
			"cannot open "+stateMatrixDocPath+": "+err.Error())
		return nil
	}
	defer func() { _ = f.Close() }()
	rows, err := statematrix.Parse(f)
	if err != nil {
		stateMatrixViolation(r, stateMatrixDocPath, 0, "state_matrix_unparsable", err.Error())
		return nil
	}
	if len(rows) == 0 {
		stateMatrixViolation(r, stateMatrixDocPath, 0, "state_matrix_empty",
			"the machine section of "+stateMatrixDocPath+" has no rows")
		return nil
	}
	return rows
}

// scanStateRewrites enforces leg 1: no state-value rewrite migration outside
// the closed historical allowlist.
func scanStateRewrites(root string, r *report.Report) {
	allow := loadStateRewriteAllowlist(root, r)
	seenAllow := map[string]bool{}
	for _, dir := range stateMigrationRoots {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			stateMatrixViolation(r, dir, 0, "state_rewrite_dir_unreadable", err.Error())
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
				continue
			}
			rel := filepath.ToSlash(filepath.Join(dir, e.Name()))
			body, err := os.ReadFile(filepath.Join(root, rel))
			if err != nil {
				stateMatrixViolation(r, rel, 0, "state_rewrite_file_unreadable", err.Error())
				continue
			}
			rewriteLines := stateRewritesIn(string(body))
			allowed := allow[rel]
			if allowed {
				seenAllow[rel] = true
				if len(rewriteLines) == 0 {
					stateMatrixViolation(r, rel, 0, "state_rewrite_allowlist_stale",
						"allowlisted in "+stateRewriteAllowTxt+" but contains no state rewrite any more; drop the entry")
				}
				continue
			}
			for _, hit := range rewriteLines {
				stateMatrixViolation(r, rel, hit.line, "state_rewrite_migration",
					"migration rewrites a state column value ("+hit.column+"); vocabularies transition via views/generated columns only, per "+stateMatrixDocPath+" — historical rewrites live in "+stateRewriteAllowTxt)
			}
		}
	}
	for name := range allow {
		if !seenAllow[name] {
			stateMatrixViolation(r, stateRewriteAllowTxt, 0, "state_rewrite_allowlist_stale",
				"entry "+name+" has no matching migration file in the scanned roots")
		}
	}
}

type rewriteHit struct {
	line   int
	column string
}

// stripTriggerBodies blanks out the bodies of CREATE TRIGGER statements
// (newlines preserved, so reported line numbers stay truthful). The state
// vocabulary freeze exists for IN-PLACE migration rewrites (the 065→066→069
// flip-flop): a migration that re-declares an existing trigger rewrites no
// value at migration time — the body is RUNTIME logic fired per row change.
// asset_state's trigger projection is precisely this schema's generated-column
// mechanism (canonical.go: "derived from lifecycle_state + index_state by the
// triggers"), so its UPDATE text is not a "rewrite migration".
func stripTriggerBodies(sql string) string {
	out := []byte(sql)
	lower := strings.ToLower(sql)
	pos := 0
	for {
		startRel := strings.Index(lower[pos:], "create trigger")
		if startRel < 0 {
			return string(out)
		}
		start := pos + startRel
		bodyStart := wordIndex(lower, "begin", start)
		if bodyStart < 0 {
			pos = start + 1
			continue
		}
		end := triggerBodyEnd(lower, bodyStart)
		if end < 0 {
			end = len(sql)
		}
		for i := bodyStart; i < end && i < len(out); i++ {
			if out[i] != '\n' {
				out[i] = ' '
			}
		}
		pos = end
	}
}

// wordIndex finds the next whole-word occurrence of word at or after from.
func wordIndex(lower, word string, from int) int {
	for i := from; ; {
		rel := strings.Index(lower[i:], word)
		if rel < 0 {
			return -1
		}
		at := i + rel
		beforeOK := at == 0 || !isIdentByte(lower[at-1])
		afterOK := at+len(word) >= len(lower) || !isIdentByte(lower[at+len(word)])
		if beforeOK && afterOK {
			return at
		}
		i = at + 1
	}
}

func isIdentByte(b byte) bool {
	return b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// triggerBodyEnd returns the index just past the `;` that terminates the
// trigger body starting at the `begin` keyword (case-insensitive, lower-cased
// text). Nesting is counted over BEGIN blocks, CASE expressions and IF blocks:
// the body's final END closes the outermost BEGIN, after which the first `;`
// ends the statement. Returns -1 when the body is unbalanced.
func triggerBodyEnd(lower string, bodyStart int) int {
	depth, caseDepth := 0, 0
	i := bodyStart
	for i < len(lower) {
		switch {
		case wordIndex(lower, "begin", i) == i:
			depth++
			i += 5
		case wordIndex(lower, "case", i) == i:
			caseDepth++
			i += 4
		case wordIndex(lower, "end", i) == i:
			i += 3
			j := i
			for j < len(lower) && (lower[j] == ' ' || lower[j] == '\t' || lower[j] == '\n' || lower[j] == '\r') {
				j++
			}
			switch {
			case j < len(lower) && wordIndex(lower, "if", j) == j: // END IF
				i = j + 2
			case caseDepth > 0: // CASE expression's END
				caseDepth--
				i = j
			default: // block END
				depth--
				i = j
				if depth == 0 {
					semi := strings.IndexByte(lower[i:], ';')
					if semi < 0 {
						return -1
					}
					return i + semi + 1
				}
			}
		default:
			i++
		}
	}
	return -1
}

// stateRewritesIn returns the (line, column) of every state-column assignment
// inside UPDATE ... SET clauses of the given SQL text. Trigger bodies are
// excluded first (stripTriggerBodies): they are runtime logic, not a migration
// rewriting a vocabulary in place.
func stateRewritesIn(sql string) []rewriteHit {
	cleaned := stripTriggerBodies(stripSQLLineComments(sql))
	var hits []rewriteHit
	for _, m := range updateStmtRe.FindAllStringSubmatchIndex(cleaned, -1) {
		setStart, setEnd := m[4], m[5]
		if setStart < 0 || setEnd < 0 {
			continue
		}
		setClause := cleaned[setStart:setEnd]
		for _, a := range stateAssignRe.FindAllStringSubmatchIndex(setClause, -1) {
			hits = append(hits, rewriteHit{
				line:   1 + strings.Count(cleaned[:setStart+a[0]], "\n"),
				column: setClause[a[2]:a[3]],
			})
		}
	}
	return hits
}

// scanPostgresAgreement enforces leg 2: the PostgreSQL declarations must match
// the matrix rows whose plane includes postgres.
func scanPostgresAgreement(root string, rows []statematrix.Row, r *report.Report) {
	byKey := map[string]statematrix.Row{}
	for _, row := range rows {
		for _, plane := range row.Planes {
			if plane == "postgres" {
				byKey[row.Table+"."+row.Column] = row
			}
		}
	}
	dir := filepath.Join(root, "migrations", "postgres")
	entries, err := os.ReadDir(dir)
	if err != nil {
		stateMatrixViolation(r, filepath.Join("migrations", "postgres"), 0, "state_matrix_pg_dir_unreadable", err.Error())
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		rel := filepath.Join("migrations", "postgres", e.Name())
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			stateMatrixViolation(r, rel, 0, "state_matrix_pg_file_unreadable", err.Error())
			continue
		}
		for _, def := range statematrix.ExtractStateColumns(string(body)) {
			key := def.Table + "." + def.Name
			row, ok := byKey[key]
			if !ok {
				stateMatrixViolation(r, rel, 0, "state_matrix_pg_missing_row",
					"postgres state column "+key+" is missing from "+stateMatrixDocPath+" (plane postgres)")
				continue
			}
			if row.HasDefault != def.HasDefault || (row.HasDefault && row.Default != def.Default) {
				stateMatrixViolation(r, rel, 0, "state_matrix_pg_default_drift",
					key+": default drift: matrix="+strconv.Quote(row.Default)+" declaration="+strconv.Quote(def.Default))
				continue
			}
			if def.HasCheck && !sameStringSet(row.ValueList, def.ValueList) {
				stateMatrixViolation(r, rel, 0, "state_matrix_pg_values_drift",
					key+": values drift: matrix="+strings.Join(row.ValueList, ",")+" declaration="+strings.Join(def.ValueList, ","))
			}
		}
	}
}

// sameStringSet compares two string slices as sets.
func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	ma := map[string]int{}
	for _, s := range a {
		ma[s]++
	}
	for _, s := range b {
		ma[s]--
		if ma[s] < 0 {
			return false
		}
	}
	return true
}

// stripSQLLineComments removes `-- ...` tail comments.
func stripSQLLineComments(sql string) string {
	lines := strings.Split(sql, "\n")
	for i, l := range lines {
		if idx := strings.Index(l, "--"); idx >= 0 {
			lines[i] = l[:idx]
		}
	}
	return strings.Join(lines, "\n")
}

// loadStateRewriteAllowlist reads the closed historical allowlist: one
// repo-relative migration path per line (the docs/migrations allowlist
// convention pinned by percheck_governance_artifacts), `#` comments allowed.
func loadStateRewriteAllowlist(root string, r *report.Report) map[string]bool {
	path := filepath.Join(root, stateRewriteAllowTxt)
	body, err := os.ReadFile(path)
	if err != nil {
		stateMatrixViolation(r, stateRewriteAllowTxt, 0, "state_rewrite_allowlist_unreadable", err.Error())
		return map[string]bool{}
	}
	out := map[string]bool{}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out[line] = true
	}
	return out
}

// stateMatrixViolation emits one SeverityError violation under this rule.
func stateMatrixViolation(r *report.Report, file string, line int, matched, note string) {
	r.Violations = append(r.Violations, report.Violation{
		Package:     "migrations",
		File:        file,
		Line:        line,
		Rule:        stateMatrixRule,
		Severity:    string(report.SeverityError),
		MatchedRule: matched,
		Note:        note,
	})
}
