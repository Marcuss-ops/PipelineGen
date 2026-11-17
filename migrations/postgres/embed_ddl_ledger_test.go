package migration

import "testing"

// TestMediaSelfBootstrapMigrationsMatchOrderedDDL pins the registry used by
// RunMediaMigrations to the ordered DDL list the self-bootstrap call sites
// already consume. The registry adds only ledger identity (version + filename);
// a drift between the two lists would make the ledger describe a migration set
// the runner does not actually apply.
func TestMediaSelfBootstrapMigrationsMatchOrderedDDL(t *testing.T) {
	if len(MediaSelfBootstrapMigrations) != len(MediaSelfBootstrapDDL) {
		t.Fatalf("MediaSelfBootstrapMigrations has %d entries, MediaSelfBootstrapDDL has %d",
			len(MediaSelfBootstrapMigrations), len(MediaSelfBootstrapDDL))
	}
	seen := make(map[int]bool, len(MediaSelfBootstrapMigrations))
	previous := 0
	for i, migration := range MediaSelfBootstrapMigrations {
		if migration.Version <= previous {
			t.Fatalf("MediaSelfBootstrapMigrations[%d] version %d is not strictly ascending after %d",
				i, migration.Version, previous)
		}
		previous = migration.Version
		if seen[migration.Version] {
			t.Fatalf("MediaSelfBootstrapMigrations has duplicate version %d", migration.Version)
		}
		seen[migration.Version] = true
		if migration.Filename == "" || migration.DDL == "" {
			t.Fatalf("MediaSelfBootstrapMigrations[%d] has an empty filename or DDL", i)
		}
		if migration.DDL != MediaSelfBootstrapDDL[i] {
			t.Fatalf("MediaSelfBootstrapMigrations[%d] (%s) DDL diverged from MediaSelfBootstrapDDL[%d]",
				i, migration.Filename, i)
		}
	}

	if MediaSchemaMigrationsMigration.Version != 14 {
		t.Fatalf("ledger migration version = %d, want 14", MediaSchemaMigrationsMigration.Version)
	}
	if MediaSchemaMigrationsMigration.Filename != "014_media_schema_migrations.sql" {
		t.Fatalf("ledger migration filename = %q", MediaSchemaMigrationsMigration.Filename)
	}
	if MediaSchemaMigrationsMigration.DDL != MediaSchemaMigrationsDDL {
		t.Fatal("ledger migration DDL diverged from MediaSchemaMigrationsDDL")
	}
}
