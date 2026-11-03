package sqlite

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMigrations_290_EntityTypeAliasFold pins the one-time backfill for ids
// written before the entity-type fold: only the type prefix is rewritten, the
// slug is preserved verbatim, already-canonical rows are untouched, and a
// second run changes nothing (the migration can be re-applied safely).
func TestMigrations_290_EntityTypeAliasFold(t *testing.T) {
	targetDir, err := filepath.Abs(migrationsDirFrom)
	if err != nil {
		t.Fatal(err)
	}
	// Apply everything up to 289 FIRST, seed the pre-fold rows, and only then
	// apply 290: a backfill must be tested against rows that already exist, not
	// on an empty table.
	baseDir := copyMigrationSubset(t, targetDir, 289, nil)
	dir := copyMigrationSubset(t, targetDir, 290, nil)
	dbPath := filepath.Join(t.TempDir(), "entity-alias-290.sqlite")
	if err := RunMigrationsOnDB(dbPath, nil, baseDir, "primary"); err != nil {
		t.Fatalf("apply baseline: %v", err)
	}
	db := openMigrationTestDB(t, dbPath)
	defer func() { _ = db.Close() }()

	// Seed rows without a parent script: the FK is irrelevant to the fold and
	// the fixture is about canonical_entity_id only.
	if _, err := db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatalf("disable foreign_keys for seed: %v", err)
	}
	seed := []struct{ semanticID, canonical string }{
		{"s-gpe", "gpe:garlasco"},
		{"s-location", "location:garlasco"},
		{"s-city", "city:garlasco"},
		{"s-org", "organization:acme-corp"},
		{"s-brand", "brand:acme"},
		{"s-cardinal", "cardinal:42"},
		{"s-person", "person:michael-jordan"},
		{"s-place", "place:garlasco"},
		{"s-ent", "ent_6bee252f34e49fa3"},
		{"s-empty", ""},
	}
	for _, row := range seed {
		if _, err := db.Exec(`
			INSERT INTO script_semantic_items
				(script_id, semantic_id, scene_id, type, text, normalized_text, canonical_entity_id,
				 start_char, end_char, start_us, end_us, confidence, metadata_json)
			VALUES (1, ?, 'scene-1', 'ENTITY', 'x', 'x', ?, 0, 1, 0, 1, 0.9, '{}')`,
			row.semanticID, row.canonical); err != nil {
			t.Fatalf("seed %s: %v", row.semanticID, err)
		}
	}

	if err := RunMigrationsOnDB(dbPath, nil, dir, "primary"); err != nil {
		t.Fatalf("apply migration 290 over seeded rows: %v", err)
	}

	read := func(semanticID string) string {
		t.Helper()
		var canonical string
		if err := db.QueryRow(`SELECT canonical_entity_id FROM script_semantic_items WHERE semantic_id = ?`, semanticID).Scan(&canonical); err != nil {
			t.Fatalf("read %s: %v", semanticID, err)
		}
		return canonical
	}

	want := map[string]string{
		"s-gpe":      "place:garlasco",
		"s-location": "place:garlasco",
		"s-city":     "place:garlasco",
		"s-org":      "org:acme-corp",
		"s-brand":    "logo:acme",
		"s-cardinal": "number:42",
		"s-person":   "person:michael-jordan",
		"s-place":    "place:garlasco",
		"s-ent":      "ent_6bee252f34e49fa3",
		"s-empty":    "",
	}
	for semanticID, canonical := range want {
		if got := read(semanticID); got != canonical {
			t.Errorf("%s canonical_entity_id = %q, want %q", semanticID, got, canonical)
		}
	}

	// Re-applying the fold is a no-op: the WHERE clause only matches legacy
	// spellings, so a second pass must not invent a different identity.
	content, err := os.ReadFile(filepath.Join(targetDir, "290_entity_type_alias_fold.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(content)); err != nil {
		t.Fatalf("re-apply 290: %v", err)
	}
	for semanticID, canonical := range want {
		if got := read(semanticID); got != canonical {
			t.Errorf("after re-apply %s canonical_entity_id = %q, want %q", semanticID, got, canonical)
		}
	}
}
