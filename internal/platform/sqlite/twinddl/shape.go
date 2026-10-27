package twinddl

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Column is one column declaration of a twin table.
type Column struct {
	Name       string // lowercased
	Type       string // upper, whitespace-collapsed
	NotNull    bool
	PK         int    // 0 = not part of the primary key; 1..N = key position
	Default    string // normalised expression (see normalizeDefault)
	HasDefault bool
}

// UniqueIndex is one UNIQUE index declaration. Names are contract-free (the
// two planes name their indexes independently); shape is what writer
// ON CONFLICT clauses bind to, so shape is the contract.
type UniqueIndex struct {
	Name    string
	Cols    string // normalised, direction-tagged, comma-joined
	Partial bool
	Where   string // normalised predicate (empty for full unique)
}

// TableShape is one table's contract-bearing declaration: columns (as a set),
// CHECK constraints (declaration order) and UNIQUE index shapes. Non-unique
// indexes and FOREIGN KEY targets are deliberately not part of the contract —
// per-plane access patterns and per-plane sibling tables legitimately differ.
type TableShape struct {
	Name    string
	Columns []Column
	Checks  []string
	Uniques []UniqueIndex
}

// Column returns the named column declaration.
func (s TableShape) Column(name string) (Column, bool) {
	for _, c := range s.Columns {
		if c.Name == name {
			return c, true
		}
	}
	return Column{}, false
}

// ── normalisation ──────────────────────────────────────────────────────────

// normalizeWS collapses all whitespace runs to single spaces.
func normalizeWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// normalizeExpr canonicalises an expression so formatting differences do not
// register as drift: whitespace adjacent to `(`, `)` and `,` is removed, all
// remaining whitespace runs collapse to one space (never inside string
// literals), and `!=` becomes its `<>` synonym. Case is preserved — literals
// are value-set members.
func normalizeExpr(s string) string {
	s = removeSpaceAroundParens(s)
	var b strings.Builder
	inQuote := false
	pendingSpace := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inQuote:
			b.WriteByte(c)
			if c == '\'' {
				if i+1 < len(s) && s[i+1] == '\'' {
					b.WriteByte(s[i+1])
					i++
					continue
				}
				inQuote = false
			}
		case c == '\'':
			if pendingSpace {
				b.WriteByte(' ')
				pendingSpace = false
			}
			inQuote = true
			b.WriteByte(c)
		case c == '!' && i+1 < len(s) && s[i+1] == '=':
			if pendingSpace {
				b.WriteByte(' ')
				pendingSpace = false
			}
			b.WriteString("<>")
			i++
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			pendingSpace = true
		default:
			if pendingSpace {
				b.WriteByte(' ')
				pendingSpace = false
			}
			b.WriteByte(c)
		}
	}
	return b.String()
}

// removeSpaceAroundParens deletes whitespace immediately after `(` and
// immediately before `)` or after `,`, outside string literals.
func removeSpaceAroundParens(s string) string {
	var b strings.Builder
	inQuote := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inQuote {
			b.WriteByte(c)
			if c == '\'' {
				if i+1 < len(s) && s[i+1] == '\'' {
					b.WriteByte(s[i+1])
					i++
					continue
				}
				inQuote = false
			}
			continue
		}
		switch c {
		case '\'':
			inQuote = true
			b.WriteByte(c)
		case '(', ',':
			b.WriteByte(c)
			for i+1 < len(s) && (s[i+1] == ' ' || s[i+1] == '\t' || s[i+1] == '\n' || s[i+1] == '\r') {
				i++
			}
		case ')':
			// drop pending whitespace before ')'
			str := b.String()
			trimmed := strings.TrimRight(str, " \t\n\r")
			b.Reset()
			b.WriteString(trimmed)
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// normalizeDefault canonicalises a DEFAULT expression: redundant outer
// parentheses are stripped, then the expression is normalised.
func normalizeDefault(s string) string {
	s = normalizeWS(s)
	for {
		if !strings.HasPrefix(s, "(") || !strings.HasSuffix(s, ")") {
			break
		}
		depth := 0
		balanced := true
		for i := 0; i < len(s); i++ {
			switch s[i] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 && i < len(s)-1 {
					balanced = false
				}
			}
		}
		if !balanced || depth != 0 {
			break
		}
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	return normalizeExpr(s)
}

// normalizeCheck canonicalises a full "CHECK (...)" clause. Only the leading
// CHECK keyword is case-folded away; the expression itself keeps its case so
// value-set literals ('STAGED' vs 'staged') stay case-sensitive in the
// comparison.
func normalizeCheck(expr string) string {
	trimmed := strings.TrimSpace(expr)
	if strings.HasPrefix(strings.ToLower(trimmed), "check") {
		trimmed = strings.TrimSpace(trimmed[len("check"):])
	}
	return "CHECK " + normalizeExpr(trimmed)
}

// ── SQL declaration parsing ────────────────────────────────────────────────

var (
	createTableRe    = regexp.MustCompile(`(?is)\bCREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?["` + "`" + `\[]?([A-Za-z_][A-Za-z0-9_]*)["` + "`" + `\]]?\s*\(`)
	alterAddRe       = regexp.MustCompile(`(?is)\bALTER\s+TABLE\s+["` + "`" + `\[]?([A-Za-z_][A-Za-z0-9_]*)["` + "`" + `\]]?\s+ADD\s+COLUMN\s+(.*?);`)
	alterDropRe      = regexp.MustCompile(`(?is)\bALTER\s+TABLE\s+["` + "`" + `\[]?([A-Za-z_][A-Za-z0-9_]*)["` + "`" + `\]]?\s+DROP\s+COLUMN\s+["` + "`" + `\[]?([A-Za-z_][A-Za-z0-9_]*)`)
	alterRenameToRe  = regexp.MustCompile(`(?is)\bALTER\s+TABLE\s+["` + "`" + `\[]?([A-Za-z_][A-Za-z0-9_]*)["` + "`" + `\]]?\s+RENAME\s+TO\s+["` + "`" + `\[]?([A-Za-z_][A-Za-z0-9_]*)`)
	alterRenameColRe = regexp.MustCompile(`(?is)\bALTER\s+TABLE\s+["` + "`" + `\[]?([A-Za-z_][A-Za-z0-9_]*)["` + "`" + `\]]?\s+RENAME\s+COLUMN\s+["` + "`" + `\[]?([A-Za-z_][A-Za-z0-9_]*)["` + "`" + `\]]?\s+TO\s+["` + "`" + `\[]?([A-Za-z_][A-Za-z0-9_]*)`)
	createIndexRe    = regexp.MustCompile(`(?is)\bCREATE\s+(UNIQUE\s+)?INDEX\s+(?:IF\s+NOT\s+EXISTS\s+)?["` + "`" + `\[]?([A-Za-z_][A-Za-z0-9_]*)["` + "`" + `\]]?\s+ON\s+["` + "`" + `\[]?([A-Za-z_][A-Za-z0-9_]*)["` + "`" + `\]]?\s*\(`)
	dropIndexRe      = regexp.MustCompile(`(?is)\bDROP\s+INDEX\s+(?:IF\s+EXISTS\s+)?["` + "`" + `\[]?([A-Za-z_][A-Za-z0-9_]*)`)
	colNameRe        = regexp.MustCompile(`(?s)^["` + "`" + `\[]?([A-Za-z_][A-Za-z0-9_]*)["` + "`" + `\]]?\s+(.*)$`)
	notNullRe     = regexp.MustCompile(`(?i)\bNOT\s+NULL\b`)
	primaryKeyRe  = regexp.MustCompile(`(?i)\bPRIMARY\s+KEY\b`)
	defaultWordRe = regexp.MustCompile(`(?i)\bDEFAULT\b`)
	checkClauseRe = regexp.MustCompile(`(?i)\bCHECK\s*\(`)
	// tableConstraintRe classifies table-level constraint segments. It anchors
	// on the full keyword WITH its delimiter so a column named
	// checkpoint_stage is never mistaken for a CHECK clause.
	tableConstraintRe = regexp.MustCompile(`(?is)^(PRIMARY\s+KEY\s*\(|FOREIGN\s+KEY\s*\(|CONSTRAINT\s+|UNIQUE\s*\(|CHECK\s*\()`)
	tablePKRe         = regexp.MustCompile(`(?is)^PRIMARY\s+KEY\s*\(`)
	tableCheckRe      = regexp.MustCompile(`(?is)^CHECK\s*\(`)
)

// typeEndWords terminate the column-type run in a column definition.
var typeEndWords = map[string]bool{
	"PRIMARY": true, "NOT": true, "NULL": true, "DEFAULT": true,
	"CHECK": true, "REFERENCES": true, "UNIQUE": true, "COLLATE": true,
	"CONSTRAINT": true, "GENERATED": true, "AS": true,
}

// splitTopLevel splits s on commas that are outside parentheses and outside
// single-quoted literals.
func splitTopLevel(s string) []string {
	var parts []string
	depth := 0
	inQuote := false
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\'':
			if inQuote && i+1 < len(s) && s[i+1] == '\'' {
				i++
				continue
			}
			inQuote = !inQuote
		case '(':
			if !inQuote {
				depth++
			}
		case ')':
			if !inQuote {
				depth--
			}
		case ',':
			if !inQuote && depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, s[start:])
	return parts
}

// balancedParenEnd returns the index of the ')' closing the '(' at open.
func balancedParenEnd(s string, open int) int {
	depth := 0
	inQuote := false
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '\'':
			if inQuote && i+1 < len(s) && s[i+1] == '\'' {
				i++
				continue
			}
			inQuote = !inQuote
		case '(':
			if !inQuote {
				depth++
			}
		case ')':
			if !inQuote {
				depth--
				if depth == 0 {
					return i
				}
			}
		}
	}
	return -1
}

// parseColumnDef parses one column definition segment into a Column plus its
// inline CHECK clauses (in declaration order).
func parseColumnDef(seg string) (Column, []string, error) {
	m := colNameRe.FindStringSubmatch(strings.TrimSpace(seg))
	if m == nil {
		return Column{}, nil, fmt.Errorf("twinddl: cannot parse column definition %q", normalizeWS(seg))
	}
	col := Column{Name: strings.ToLower(m[1])}
	rest := m[2]

	// Type: words until a constraint keyword.
	words := strings.Fields(rest)
	typeWords := 0
	for _, w := range words {
		clean := strings.Trim(w, "\"`[](),;")
		if typeEndWords[strings.ToUpper(clean)] {
			break
		}
		typeWords++
	}
	col.Type = normalizeWS(strings.Join(words[:typeWords], " "))
	col.NotNull = notNullRe.MatchString(rest)
	if primaryKeyRe.MatchString(rest) {
		col.PK = 1
	}

	// DEFAULT <expr>
	if dm := defaultWordRe.FindStringIndex(rest); dm != nil {
		after := strings.TrimSpace(rest[dm[1]:])
		col.HasDefault, col.Default = parseDefaultExpr(after)
	}

	// CHECK (...) clauses (may appear before or after DEFAULT).
	var checks []string
	for i := 0; i < len(rest); {
		loc := checkClauseRe.FindStringIndex(rest[i:])
		if loc == nil {
			break
		}
		open := i + loc[1] - 1
		end := balancedParenEnd(rest, open)
		if end < 0 {
			break
		}
		checks = append(checks, normalizeCheck("CHECK "+rest[open:end+1]))
		i = end + 1
	}
	return col, checks, nil
}

// parseDefaultExpr reads the leading DEFAULT expression of `after`.
func parseDefaultExpr(after string) (bool, string) {
	after = strings.TrimSpace(after)
	if after == "" {
		return false, ""
	}
	switch after[0] {
	case '\'':
		for i := 1; i < len(after); i++ {
			if after[i] == '\'' {
				if i+1 < len(after) && after[i+1] == '\'' {
					i++
					continue
				}
				return true, normalizeDefault(after[:i+1])
			}
		}
		return true, normalizeDefault(after)
	case '(':
		if end := balancedParenEnd(after, 0); end >= 0 {
			return true, normalizeDefault(after[:end+1])
		}
		return true, normalizeDefault(after)
	default:
		end := strings.IndexAny(after, " ,")
		if end < 0 {
			return true, normalizeDefault(after)
		}
		return true, normalizeDefault(after[:end])
	}
}

// parseTableStatement parses a CREATE TABLE body into a TableShape.
func parseTableStatement(name, body string) (TableShape, error) {
	shape := TableShape{Name: strings.ToLower(name)}
	var tablePK []string
	for _, seg := range splitTopLevel(body) {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		switch {
		case tableConstraintRe.MatchString(seg):
			open := strings.Index(seg, "(")
			if open < 0 {
				if tablePKRe.MatchString(seg) {
					return shape, fmt.Errorf("twinddl: malformed PRIMARY KEY in %s", name)
				}
				continue // FOREIGN KEY / CONSTRAINT / UNIQUE without parens: ignored
			}
			end := balancedParenEnd(seg, open)
			if end < 0 {
				continue
			}
			switch {
			case tablePKRe.MatchString(seg):
				for _, col := range strings.Split(seg[open+1:end], ",") {
					tablePK = append(tablePK, strings.ToLower(strings.Trim(strings.TrimSpace(col), "\"`[]")))
				}
			case tableCheckRe.MatchString(seg):
				shape.Checks = append(shape.Checks, normalizeCheck("CHECK "+seg[open:end+1]))
			}
			continue
		default:
			col, checks, err := parseColumnDef(seg)
			if err != nil {
				return shape, err
			}
			shape.Columns = append(shape.Columns, col)
			shape.Checks = append(shape.Checks, checks...)
		}
	}
	for i, pkCol := range tablePK {
		for j := range shape.Columns {
			if shape.Columns[j].Name == pkCol {
				shape.Columns[j].PK = i + 1
			}
		}
	}
	return shape, nil
}

// parseIndexTail parses the column list + optional WHERE of a CREATE UNIQUE
// INDEX statement whose header (through the opening paren) was already matched.
// headerEnd is the index of that opening paren in text.
func parseIndexTail(text string, open int) (cols, where string, partial bool) {
	end := balancedParenEnd(text, open)
	if end < 0 {
		return "", "", false
	}
	var colParts []string
	for _, c := range strings.Split(text[open+1:end], ",") {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		fields := strings.Fields(c)
		name := strings.ToLower(strings.Trim(fields[0], "\"`[]"))
		dir := ""
		if len(fields) > 1 && strings.EqualFold(fields[1], "DESC") {
			dir = " DESC"
		}
		colParts = append(colParts, name+dir)
	}
	rest := text[end+1:]
	if stop := strings.Index(rest, ";"); stop >= 0 {
		rest = rest[:stop]
	}
	if wm := regexp.MustCompile(`(?is)\bWHERE\b`).FindStringIndex(rest); wm != nil {
		where = normalizeExpr(strings.TrimSpace(rest[wm[1]:]))
		partial = where != ""
	}
	return strings.Join(colParts, ","), where, partial
}

// FoldState accumulates ordered DDL events into live-shaped table shapes.
type FoldState struct {
	tables      map[string]*TableShape
	indexes     map[string]UniqueIndex
	indexOwners map[string]string
}

// NewFoldState returns an empty fold state.
func NewFoldState() *FoldState {
	return &FoldState{
		tables:      map[string]*TableShape{},
		indexes:     map[string]UniqueIndex{},
		indexOwners: map[string]string{},
	}
}

// foldEvent is one DDL event tagged with its position in the SQL text. Events
// are applied in position order: a DROP INDEX followed by a CREATE UNIQUE INDEX
// for the same name inside one migration (the 285 repair idiom) must resolve
// newest-wins, and the regexes below match per-kind, not per-position.
type foldEvent struct {
	pos     int
	kind    string // createTable | addColumn | dropColumn | renameColumn | renameTable | createIndex | dropIndex
	name    string
	arg     string
	arg2    string
	unique  bool
	body    string
}

// FoldDeclarations folds the DDL statements of one SQL text into the fold
// state, mirroring what a live database ends up with: first CREATE TABLE wins
// (IF NOT EXISTS is a no-op on re-declaration), ALTER TABLE ADD COLUMN appends
// unless the column already exists (the runner soft-skips duplicates), and
// index CREATE/DROP events resolve newest-wins.
func FoldDeclarations(text string, st *FoldState) error {
	cleaned := stripLineComments(text)
	var events []foldEvent

	for _, m := range createTableRe.FindAllStringSubmatchIndex(cleaned, -1) {
		open := m[1] - 1
		end := balancedParenEnd(cleaned, open)
		if end < 0 {
			return fmt.Errorf("twinddl: unbalanced CREATE TABLE body for %s", cleaned[m[2]:m[3]])
		}
		events = append(events, foldEvent{
			pos: m[0], kind: "createTable",
			name: strings.ToLower(cleaned[m[2]:m[3]]),
			body: cleaned[open+1 : end],
		})
	}
	for _, m := range alterAddRe.FindAllStringSubmatchIndex(cleaned, -1) {
		events = append(events, foldEvent{
			pos: m[0], kind: "addColumn",
			name: strings.ToLower(cleaned[m[2]:m[3]]),
			body: strings.TrimSuffix(strings.TrimSpace(cleaned[m[4]:m[5]]), ";"),
		})
	}
	for _, m := range alterDropRe.FindAllStringSubmatchIndex(cleaned, -1) {
		events = append(events, foldEvent{
			pos: m[0], kind: "dropColumn",
			name: strings.ToLower(cleaned[m[2]:m[3]]),
			arg:  strings.ToLower(cleaned[m[4]:m[5]]),
		})
	}
	for _, m := range alterRenameColRe.FindAllStringSubmatchIndex(cleaned, -1) {
		events = append(events, foldEvent{
			pos: m[0], kind: "renameColumn",
			name: strings.ToLower(cleaned[m[2]:m[3]]),
			arg:  strings.ToLower(cleaned[m[4]:m[5]]),
			arg2: strings.ToLower(cleaned[m[6]:m[7]]),
		})
	}
	for _, m := range alterRenameToRe.FindAllStringSubmatchIndex(cleaned, -1) {
		events = append(events, foldEvent{
			pos: m[0], kind: "renameTable",
			name: strings.ToLower(cleaned[m[2]:m[3]]),
		})
	}
	for _, m := range createIndexRe.FindAllStringSubmatchIndex(cleaned, -1) {
		if m[2] < 0 {
			continue // non-unique indexes are not part of the contract
		}
		open := m[1] - 1
		cols, where, partial := parseIndexTail(cleaned, open)
		events = append(events, foldEvent{
			pos: m[0], kind: "createIndex",
			name:   strings.ToLower(cleaned[m[4]:m[5]]),
			arg:    strings.ToLower(cleaned[m[6]:m[7]]),
			arg2:   cols,
			unique: partial,
			body:   where,
		})
	}
	for _, m := range dropIndexRe.FindAllStringSubmatchIndex(cleaned, -1) {
		events = append(events, foldEvent{
			pos: m[0], kind: "dropIndex",
			name: strings.ToLower(cleaned[m[2]:m[3]]),
		})
	}

	sort.Slice(events, func(i, j int) bool { return events[i].pos < events[j].pos })
	for _, ev := range events {
		switch ev.kind {
		case "createTable":
			if _, exists := st.tables[ev.name]; exists {
				continue // first declaration wins (CREATE TABLE IF NOT EXISTS)
			}
			shape, err := parseTableStatement(ev.name, ev.body)
			if err != nil {
				return err
			}
			st.tables[ev.name] = &shape
		case "addColumn":
			shape, ok := st.tables[ev.name]
			if !ok {
				continue
			}
			col, checks, err := parseColumnDef(ev.body)
			if err != nil {
				return err
			}
			if _, exists := shape.Column(col.Name); exists {
				continue // runner soft-skips duplicate ADD COLUMN
			}
			shape.Columns = append(shape.Columns, col)
			shape.Checks = append(shape.Checks, checks...)
		case "dropColumn":
			shape, ok := st.tables[ev.name]
			if !ok {
				continue
			}
			kept := make([]Column, 0, len(shape.Columns))
			for _, c := range shape.Columns {
				if c.Name != ev.arg {
					kept = append(kept, c)
				}
			}
			shape.Columns = kept
		case "renameColumn":
			shape, ok := st.tables[ev.name]
			if !ok {
				continue
			}
			for i := range shape.Columns {
				if shape.Columns[i].Name == ev.arg {
					shape.Columns[i].Name = ev.arg2
				}
			}
		case "renameTable":
			delete(st.tables, ev.name)
		case "createIndex":
			st.indexes[ev.name] = UniqueIndex{Name: ev.name, Cols: ev.arg2, Partial: ev.unique, Where: ev.body}
			st.indexOwners[ev.name] = ev.arg
		case "dropIndex":
			delete(st.indexes, ev.name)
		}
	}
	return nil
}

// stripLineComments removes `-- ...` tail comments.
func stripLineComments(sql string) string {
	lines := strings.Split(sql, "\n")
	for i, l := range lines {
		if idx := strings.Index(l, "--"); idx >= 0 {
			lines[i] = l[:idx]
		}
	}
	return strings.Join(lines, "\n")
}

// Shape returns the folded shape of a table with its UNIQUE indexes attached.
func (st *FoldState) Shape(table string) (TableShape, bool) {
	shape, ok := st.tables[strings.ToLower(table)]
	if !ok {
		return TableShape{}, false
	}
	out := TableShape{
		Name:    shape.Name,
		Columns: append([]Column(nil), shape.Columns...),
		Checks:  append([]string(nil), shape.Checks...),
	}
	for name, idx := range st.indexes {
		if st.indexOwners[name] == out.Name {
			out.Uniques = append(out.Uniques, idx)
		}
	}
	sort.Slice(out.Uniques, func(i, j int) bool { return out.Uniques[i].Name < out.Uniques[j].Name })
	return out, true
}
