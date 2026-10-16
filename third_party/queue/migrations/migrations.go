// Package migrations embeds the SQL schema migrations for the queue database.
//
// Migration files are named NNN_description.sql and are applied in
// lexicographic order. Add a new file (never edit an applied one) to evolve
// the schema.
package migrations

import (
	"embed"
	"sort"
)

// FS embeds all *.sql migration files in this directory.
//
//go:embed *.sql
var FS embed.FS

// Names returns the migration filenames in lexicographic (apply) order.
func Names() ([]string, error) {
	entries, err := FS.ReadDir(".")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}
