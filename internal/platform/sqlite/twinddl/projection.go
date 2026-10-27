package twinddl

import (
	"fmt"
	"sort"
	"strings"
)

// jobsDroppedDefaults pins the DEFAULT expressions the jobs-plane projection
// drops. Rationale (BASELINE_INVENTORY.md "Twin declarations"): the jobs copy
// states timestamps without defaults because the shared writers always set
// timestamps explicitly. Keyed as table → column.
var jobsDroppedDefaults = map[string]map[string]bool{
	"artifact_stages": {"created_at": true, "updated_at": true},
	"job_results":     {"created_at": true},
}

// CanonicalShapes parses twin_tables.sql and returns the canonical
// (primary-plane) shape of each twin table.
func CanonicalShapes() (map[string]TableShape, error) {
	st := NewFoldState()
	if err := FoldDeclarations(Source, st); err != nil {
		return nil, err
	}
	out := make(map[string]TableShape, len(TwinTables))
	for _, table := range TwinTables {
		shape, ok := st.Shape(table)
		if !ok {
			return nil, fmt.Errorf("twinddl: source declares no table %q", table)
		}
		out[table] = shape
	}
	return out, nil
}

// JobsPlaneShapes derives the jobs-plane projection from the canonical source.
// The projection diverges ONLY by the pinned rules:
//
//  1. CHECK constraints are dropped (the hand copy predates them; SQLite
//     cannot add a CHECK via ALTER TABLE and both planes enforce the same
//     value sets as typed state enums in the application layer).
//  2. The three pinned historical timestamp DEFAULT expressions are dropped
//     (jobsDroppedDefaults).
//
// Everything else — column sets, types, NOT NULL, primary keys, default
// expressions, UNIQUE index shapes (uniqueness is correctness: writer
// ON CONFLICT clauses bind to it) — must be identical.
func JobsPlaneShapes() (map[string]TableShape, error) {
	canonical, err := CanonicalShapes()
	if err != nil {
		return nil, err
	}
	out := make(map[string]TableShape, len(canonical))
	for table, shape := range canonical {
		proj := TableShape{Name: shape.Name}
		dropDefault := jobsDroppedDefaults[table]
		for _, col := range shape.Columns {
			if dropDefault[col.Name] {
				col.Default = ""
				col.HasDefault = false
			}
			proj.Columns = append(proj.Columns, col)
		}
		for _, u := range shape.Uniques {
			proj.Uniques = append(proj.Uniques, u)
		}
		out[table] = proj
	}
	return out, nil
}

// RenderCanonical renders one table shape as canonical DDL (stable formatting
// — the generator output is byte-deterministic). CHECK clauses that bind to a
// column are rendered inline; the multi-column primary key is rendered as a
// table constraint.
func RenderCanonical(shape TableShape) string {
	pkCols := tablePKColumns(shape)
	inlinePK := len(pkCols) == 1

	lines := make([]string, 0, len(shape.Columns)+1)
	for _, col := range shape.Columns {
		var b strings.Builder
		fmt.Fprintf(&b, "    %s %s", col.Name, col.Type)
		if col.NotNull {
			b.WriteString(" NOT NULL")
		}
		if col.PK > 0 && inlinePK {
			b.WriteString(" PRIMARY KEY")
		}
		if col.HasDefault {
			fmt.Fprintf(&b, " DEFAULT %s", renderDefault(col.Default))
		}
		for _, chk := range shape.Checks {
			if checkBindsTo(chk, col.Name) {
				b.WriteString("\n        " + chk)
			}
		}
		lines = append(lines, b.String())
	}
	if len(pkCols) > 1 {
		lines = append(lines, "    PRIMARY KEY("+strings.Join(pkCols, ", ")+")")
	}

	var out strings.Builder
	fmt.Fprintf(&out, "CREATE TABLE IF NOT EXISTS %s (\n", shape.Name)
	out.WriteString(strings.Join(lines, ",\n"))
	out.WriteString("\n);\n")
	for _, u := range shape.Uniques {
		partial := ""
		if u.Partial {
			partial = " WHERE " + u.Where
		}
		fmt.Fprintf(&out, "CREATE UNIQUE INDEX IF NOT EXISTS %s\n    ON %s(%s)%s;\n", u.Name, shape.Name, u.Cols, partial)
	}
	return out.String()
}

// tablePKColumns returns the primary-key columns ordered by key position
// (empty when the table has no primary key).
func tablePKColumns(shape TableShape) []string {
	var cols []string
	for _, col := range shape.Columns {
		if col.PK > 0 {
			cols = append(cols, col.Name)
		}
	}
	sort.Slice(cols, func(i, j int) bool {
		ci, _ := shape.Column(cols[i])
		cj, _ := shape.Column(cols[j])
		return ci.PK < cj.PK
	})
	return cols
}

// checkBindsTo reports whether a normalised CHECK clause names the column
// (used only for rendering placement).
func checkBindsTo(check, column string) bool {
	return strings.Contains(strings.ToUpper(check), strings.ToUpper(column)+" ")
}

// renderDefault re-inserts parentheses around expression defaults that are not
// literals or bare numbers, so the rendered DDL stays valid SQLite.
func renderDefault(normalized string) string {
	if normalized == "" {
		return "''"
	}
	if strings.HasPrefix(normalized, "'") || strings.HasPrefix(normalized, "-") {
		return normalized
	}
	if _, err := fmt.Sscanf(normalized, "%f", new(float64)); err == nil && !strings.ContainsAny(normalized, "() ") {
		return normalized
	}
	if strings.Contains(normalized, "(") {
		return "(" + normalized + ")"
	}
	return normalized
}

// ShapesEqual compares two shapes under the twin contract:
//   - columns as a SET (type, NOT NULL, PK position, DEFAULT);
//   - CHECK constraints as an ordered list;
//   - UNIQUE index shapes as a SET of (partial, cols, where) — names excluded.
//
// It returns a human-readable description of the first difference ("" when
// equal).
func ShapesEqual(a, b TableShape) string {
	if len(a.Columns) != len(b.Columns) {
		return fmt.Sprintf("column count: %d vs %d", len(a.Columns), len(b.Columns))
	}
	for _, ca := range a.Columns {
		cb, ok := b.Column(ca.Name)
		if !ok {
			return fmt.Sprintf("column %s missing on the right side", ca.Name)
		}
		if ca.Type != cb.Type {
			return fmt.Sprintf("column %s: type %q vs %q", ca.Name, ca.Type, cb.Type)
		}
		if ca.NotNull != cb.NotNull {
			return fmt.Sprintf("column %s: NOT NULL %v vs %v", ca.Name, ca.NotNull, cb.NotNull)
		}
		if ca.PK != cb.PK {
			return fmt.Sprintf("column %s: PK position %d vs %d", ca.Name, ca.PK, cb.PK)
		}
		if ca.HasDefault != cb.HasDefault || ca.Default != cb.Default {
			return fmt.Sprintf("column %s: DEFAULT %q (present=%v) vs %q (present=%v)",
				ca.Name, ca.Default, ca.HasDefault, cb.Default, cb.HasDefault)
		}
	}
	if len(a.Checks) != len(b.Checks) {
		return fmt.Sprintf("CHECK list: %v vs %v", a.Checks, b.Checks)
	}
	for i := range a.Checks {
		if a.Checks[i] != b.Checks[i] {
			return fmt.Sprintf("CHECK #%d: %q vs %q", i+1, a.Checks[i], b.Checks[i])
		}
	}
	ua, ub := uniqueSet(a), uniqueSet(b)
	if ua != ub {
		return fmt.Sprintf("UNIQUE index shapes: %s vs %s", ua, ub)
	}
	return ""
}

// uniqueSet renders the comparable part of a shape's unique indexes.
func uniqueSet(s TableShape) string {
	parts := make([]string, 0, len(s.Uniques))
	for _, u := range s.Uniques {
		kind := "full"
		if u.Partial {
			kind = "partial"
		}
		parts = append(parts, fmt.Sprintf("%s on (%s) where %s", kind, u.Cols, u.Where))
	}
	sort.Strings(parts)
	return strings.Join(parts, " | ")
}
