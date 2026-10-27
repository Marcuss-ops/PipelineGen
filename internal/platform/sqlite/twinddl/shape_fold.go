package twinddl

import (
	"fmt"
	"sort"
	"strings"
)

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
	pos    int
	kind   string // createTable | addColumn | dropColumn | renameColumn | renameTable | createIndex | dropIndex
	name   string
	arg    string
	arg2   string
	unique bool
	body   string
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
