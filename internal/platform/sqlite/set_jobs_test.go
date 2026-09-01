package sqlite

import (
	"path/filepath"
	"testing"

	"go.uber.org/zap"
)

func TestOpenSetJobsSplitIsExplicitAndSideBySide(t *testing.T) {
	root := t.TempDir()
	set, err := OpenSet(StorageConfig{
		DataDir:     root,
		JobsDBPath:  filepath.Join(root, "jobs", "jobs.db.sqlite"),
		CacheDBPath: filepath.Join(root, "cache", "cache.db.sqlite"),
	}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()

	if set.Jobs == nil {
		t.Fatal("explicit JobsDBPath must open the jobs database")
	}
	if set.Primary.Path() == set.Jobs.Path() {
		t.Fatal("jobs database must not share the media database path")
	}
	if got, want := set.Jobs.Path(), filepath.Join(root, "jobs", "jobs.db.sqlite"); got != want {
		t.Fatalf("jobs path = %q, want %q", got, want)
	}
}

func TestOpenSetWithoutJobsSplitDoesNotCreateJobsHandle(t *testing.T) {
	set, err := OpenSet(StorageConfig{DataDir: t.TempDir()}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	if set.Jobs != nil {
		t.Fatal("jobs handle must remain nil unless explicitly configured")
	}
}

func TestMigrationScopeRecognizesJobs(t *testing.T) {
	scope := parseMigrationScope([]byte("-- database: jobs\nCREATE TABLE jobs_marker (id TEXT);\n"))
	if scope != "jobs" {
		t.Fatalf("scope = %q, want jobs", scope)
	}
	if !migrationAppliesToTargetDB(scope, "jobs") {
		t.Fatal("jobs-scoped migration must apply to jobs target")
	}
	if migrationAppliesToTargetDB(scope, "primary") {
		t.Fatal("jobs-scoped migration must not apply to primary target")
	}
}

func TestDatabaseSetMigrateJobsPlane(t *testing.T) {
	root := t.TempDir()
	set, err := OpenSet(StorageConfig{
		DataDir:    root,
		JobsDBPath: filepath.Join(root, "jobs", "jobs.db.sqlite"),
	}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()

	if err := set.Migrate(zap.NewNop()); err != nil {
		t.Fatalf("jobs-plane migration failed: %v", err)
	}
	var n int
	if err := set.Jobs.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='jobs'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal("jobs table missing from migrated jobs plane")
	}
	if err := set.Jobs.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='media_assets'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("media_assets must not be created in the jobs plane")
	}
}
