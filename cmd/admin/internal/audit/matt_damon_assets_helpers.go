package audit

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
)

type mattDamonUnionFind struct {
	parent map[string]string
}

func newMattDamonUnionFind() *mattDamonUnionFind {
	return &mattDamonUnionFind{parent: make(map[string]string)}
}

func (u *mattDamonUnionFind) add(value string) {
	if _, ok := u.parent[value]; !ok {
		u.parent[value] = value
	}
}

func (u *mattDamonUnionFind) find(value string) string {
	u.add(value)
	if u.parent[value] != value {
		u.parent[value] = u.find(u.parent[value])
	}
	return u.parent[value]
}

func (u *mattDamonUnionFind) union(left, right string) {
	leftRoot, rightRoot := u.find(left), u.find(right)
	if leftRoot != rightRoot {
		u.parent[rightRoot] = leftRoot
	}
}

func mattDamonTableExists(ctx context.Context, db *sql.DB, table string) bool {
	var value int
	err := db.QueryRowContext(ctx, `SELECT 1 FROM sqlite_master WHERE type='table' AND name = ?`, table).Scan(&value)
	return err == nil && value == 1
}

func mattDamonTableColumns(ctx context.Context, db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, fmt.Errorf("audit-matt-damon-assets: inspect table %q: %w", table, err)
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("audit-matt-damon-assets: scan table column %q: %w", table, err)
		}
		columns[name] = true
	}
	return columns, rows.Err()
}

func sortedStringSetDifference(left map[string]struct{}, right map[string]struct{}) []string {
	out := make([]string, 0)
	for value := range left {
		if _, ok := right[value]; !ok {
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
