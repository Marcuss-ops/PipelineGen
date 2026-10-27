// Package statematrix is the machine-consumption layer of
// docs/migrations/STATE_MATRIX.md (the SSOT for status/state vocabularies).
//
// Two consumers share this parser so the matrix is never transcribed twice:
//
//   - internal/platform/sqlite/state_matrix_live_test.go validates the LIVE
//     migrated schema (fresh primary + jobs DBs) against the matrix;
//   - the archcheck gate percheck_state_matrix
//     (cmd/archcheck/scan/migrations) pins the static side: PostgreSQL media
//     declarations vs the matrix, plus the no-rewrite-migration rule.
//
// The matrix format is a markdown table between the state-matrix:begin/end
// markers with exactly seven columns:
//
//	| table | column | planes | default | values | casing | terminal |
//
// `-` means "not declared / not constrained"; an empty `default` cell means
// DEFAULT ” (an empty-string default IS declared).
package statematrix

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// Row is one machine-parsed matrix row.
type Row struct {
	Table      string
	Column     string
	Planes     []string // sqlite | sqlite_jobs | postgres
	Default    string   // default literal with quotes stripped
	HasDefault bool     // false when the cell is "-"
	ValueList  []string
	Casing     string
	Terminal   string
}

// IsStateColumn reports whether a column name is a state-bearing column under
// the STATE_MATRIX naming convention: status, state, or *_status / *_state.
func IsStateColumn(name string) bool {
	name = strings.ToLower(name)
	switch name {
	case "status", "state":
		return true
	}
	return strings.HasSuffix(name, "_status") || strings.HasSuffix(name, "_state")
}

// CasingFamily classifies a vocabulary literal as UPPER, lower or Mixed.
// Empty inputs are ignored; if nothing remains the family is "-".
func CasingFamily(literals ...string) string {
	fams := map[string]bool{}
	for _, s := range literals {
		if s == "" {
			continue
		}
		upper, lower := true, true
		hasLetter := false
		for _, r := range s {
			switch {
			case r >= 'A' && r <= 'Z':
				hasLetter, lower = true, false
			case r >= 'a' && r <= 'z':
				hasLetter, upper = true, false
			}
		}
		switch {
		case !hasLetter:
			// digits/punctuation only: neutral, no family
		case upper && !lower:
			fams["UPPER"] = true
		case lower && !upper:
			fams["lower"] = true
		default:
			fams["Mixed"] = true
		}
	}
	out := make([]string, 0, len(fams))
	for _, f := range []string{"UPPER", "lower", "Mixed"} {
		if fams[f] {
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		return "-"
	}
	return strings.Join(out, "/")
}

const (
	beginMarker = "<!-- state-matrix:begin -->"
	endMarker   = "<!-- state-matrix:end -->"
)

// Parse reads the STATE_MATRIX.md machine section and returns its rows.
// It fails closed on missing markers, malformed headers, or a wrong column
// count: a matrix the parser cannot read is a matrix nobody enforces.
func Parse(r io.Reader) ([]Row, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	inSection := false
	seenBegin := false
	sawHeader := false
	seen := map[string]bool{}
	var rows []Row
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == beginMarker:
			inSection, seenBegin = true, true
			continue
		case line == endMarker:
			inSection = false
			continue
		}
		if !inSection {
			continue
		}
		if line == "" || strings.HasPrefix(line, "|---") {
			continue
		}
		cells := splitRow(line)
		if len(cells) != 7 {
			return nil, fmt.Errorf("statematrix: line %d: expected 7 cells, got %d (%q)", lineNo, len(cells), line)
		}
		if cells[0] == "table" {
			sawHeader = true
			continue
		}
		row := Row{
			Table:    cells[0],
			Column:   cells[1],
			Planes:   splitList(cells[2]),
			Default:  cells[3],
			Casing:   cells[5],
			Terminal: cells[6],
		}
		for _, plane := range row.Planes {
			switch plane {
			case "sqlite", "sqlite_jobs", "postgres":
			default:
				return nil, fmt.Errorf("statematrix: line %d: unknown plane %q", lineNo, plane)
			}
			key := row.Table + "." + row.Column + "@" + plane
			if seen[key] {
				return nil, fmt.Errorf("statematrix: line %d: duplicate row for %s", lineNo, key)
			}
			seen[key] = true
		}
		if row.Default == "-" {
			row.HasDefault = false
			row.Default = ""
		} else {
			row.HasDefault = true
		}
		if cells[4] != "-" {
			row.ValueList = splitList(cells[4])
		}
		if row.Table == "" || row.Column == "" {
			return nil, fmt.Errorf("statematrix: line %d: empty table/column", lineNo)
		}
		rows = append(rows, row)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if !seenBegin {
		return nil, fmt.Errorf("statematrix: missing %s marker", beginMarker)
	}
	if !sawHeader {
		return nil, fmt.Errorf("statematrix: missing header row in machine section")
	}
	return rows, nil
}

// splitRow splits one markdown table row into its trimmed cells.
func splitRow(line string) []string {
	line = strings.Trim(line, "|")
	raw := strings.Split(line, "|")
	cells := make([]string, len(raw))
	for i, c := range raw {
		cells[i] = strings.TrimSpace(c)
	}
	return cells
}

// splitList splits a comma-separated cell into trimmed literals.
func splitList(cell string) []string {
	parts := strings.Split(cell, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ColumnDef is one state-bearing column declaration extracted from SQL text.
type ColumnDef struct {
	Table      string
	Name       string
	Default    string // literal with quotes stripped; expression defaults kept raw
	HasDefault bool
	ValueList  []string // CHECK (col IN (...)) vocabulary, if declared
	HasCheck   bool
}

var (
	createTableRe = regexp.MustCompile(`(?i)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?"?([a-z_][a-z0-9_]*)"?\s*\(`)
	alterAddRe    = regexp.MustCompile(`(?i)ALTER\s+TABLE\s+"?([a-z_][a-z0-9_]*)"?\s+ADD\s+COLUMN\s+"?([a-z_][a-z0-9_]*)"?\s+(.*)$`)
	colDefRe      = regexp.MustCompile(`(?i)^"?([a-z_][a-z0-9_]*)"?\s+(?:TEXT|VARCHAR|CHAR|TIMESTAMP(?:TZ)?|DATETIME|DATE)`)
	checkInRe     = regexp.MustCompile(`(?i)CHECK\s*\(\s*"?([a-z_][a-z0-9_]*)"?\s+IN\s*\(([^)]*)\)`)
	defaultRe     = regexp.MustCompile(`(?i)DEFAULT\s+('([^']*)'|\([^)]*\)|\S+)`)
)

// ExtractStateColumns is ExtractColumns filtered to state-bearing columns.
func ExtractStateColumns(sql string) []ColumnDef {
	var out []ColumnDef
	for _, def := range ExtractColumns(sql) {
		if IsStateColumn(def.Name) {
			out = append(out, def)
		}
	}
	return out
}

// ExtractColumns scans SQL text (CREATE TABLE bodies and ALTER TABLE
// ADD COLUMN statements, possibly many per call) and returns every
// column declaration. Line comments (`--`) are stripped first.
// A CHECK (col IN (...)) is attached to the column it names.
//
// This is the shared SQL declaration extraction layer behind the
// machine-consumed schema docs (STATE_MATRIX.md and TIMESTAMP_CONVENTION.md):
// one parser, no second transcription of the schema.
func ExtractColumns(sql string) []ColumnDef {
	var out []ColumnDef
	cleaned := stripLineComments(sql)
	// Walk CREATE TABLE bodies; commas inside parentheses never split a
	// column definition, so the CHECK/DEFAULT clauses stay with their column.
	for _, block := range createTableRe.FindAllStringSubmatchIndex(cleaned, -1) {
		table := cleaned[block[2]:block[3]]
		bodyStart := block[1]
		depth := 1
		i := bodyStart
		for ; i < len(cleaned) && depth > 0; i++ {
			switch cleaned[i] {
			case '(':
				depth++
			case ')':
				depth--
			}
		}
		body := cleaned[bodyStart : i-1]
		for _, seg := range splitTopLevel(body, ',') {
			seg = strings.TrimSpace(seg)
			m := colDefRe.FindStringSubmatch(seg)
			if m == nil {
				continue
			}
			out = append(out, newColumnDef(table, m[1], seg))
		}
	}
	for _, line := range strings.Split(cleaned, "\n") {
		m := alterAddRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		out = append(out, newColumnDef(m[1], m[2], m[3]))
	}
	return out
}

// newColumnDef builds a ColumnDef from a column-definition segment,
// attaching its CHECK vocabulary and DEFAULT literal when present.
func newColumnDef(table, name, seg string) ColumnDef {
	def := ColumnDef{Table: table, Name: name}
	if chk := checkInRe.FindStringSubmatch(seg); chk != nil && strings.EqualFold(chk[1], name) {
		def.HasCheck = true
		for _, v := range strings.Split(chk[2], ",") {
			v = strings.TrimSpace(v)
			v = strings.Trim(v, "'\"")
			if v != "" {
				def.ValueList = append(def.ValueList, v)
			}
		}
	}
	if d := defaultRe.FindStringSubmatch(seg); d != nil {
		def.HasDefault = true
		val := d[1]
		if strings.HasPrefix(val, "'") && strings.HasSuffix(val, "'") && len(val) >= 2 {
			val = val[1 : len(val)-1]
		}
		def.Default = val
	}
	return def
}

// splitTopLevel splits s on sep, ignoring separators inside parentheses or
// single-quoted strings.
func splitTopLevel(s string, sep byte) []string {
	var parts []string
	depth := 0
	inQuote := false
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\'':
			inQuote = !inQuote
		case '(':
			if !inQuote {
				depth++
			}
		case ')':
			if !inQuote {
				depth--
			}
		case sep:
			if !inQuote && depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, s[start:])
	return parts
}

// stripLineComments removes `-- ...` tail comments, leaving the code part.
func stripLineComments(sql string) string {
	lines := strings.Split(sql, "\n")
	for i, l := range lines {
		if idx := strings.Index(l, "--"); idx >= 0 {
			lines[i] = l[:idx]
		}
	}
	return strings.Join(lines, "\n")
}
