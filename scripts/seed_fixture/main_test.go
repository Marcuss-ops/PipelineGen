package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveSeedDB_DefaultsToTemporaryDatabase(t *testing.T) {
	t.Setenv("SEED_DB_PATH", "")

	projectRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "go.mod"), []byte("module fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	path, cleanup, err := resolveSeedDB(projectRoot)
	if err != nil {
		t.Fatalf("resolveSeedDB: %v", err)
	}
	defer cleanup()

	if !strings.HasPrefix(path, os.TempDir()+string(os.PathSeparator)) {
		t.Fatalf("default seed path = %q, want a temporary path under %q", path, os.TempDir())
	}
	if strings.Contains(path, filepath.Join("tests", "fixtures")) {
		t.Fatalf("default seed path unexpectedly points at fixtures: %q", path)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("temporary seed file should not exist before opening it; stat error = %v", err)
	}
}

func TestResolveSeedDB_AllowsOnlyFixtureOverride(t *testing.T) {
	projectRoot := t.TempDir()
	fixturesRoot := filepath.Join(projectRoot, "tests", "fixtures")
	if err := os.MkdirAll(fixturesRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("fixture", func(t *testing.T) {
		fixturePath := filepath.Join(fixturesRoot, "sqlite", "media.db.sqlite")
		t.Setenv("SEED_DB_PATH", fixturePath)

		got, cleanup, err := resolveSeedDB(projectRoot)
		if err != nil {
			t.Fatalf("fixture override rejected: %v", err)
		}
		cleanup()
		want, err := filepath.Abs(fixturePath)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("fixture path = %q, want %q", got, want)
		}
	})

	for _, path := range []string{
		filepath.Join(projectRoot, "data", "media", "media.db.sqlite"),
		filepath.Join(projectRoot, "data", "media.db.sqlite"),
		filepath.Join(projectRoot, "user.sqlite"),
	} {
		t.Run(path, func(t *testing.T) {
			t.Setenv("SEED_DB_PATH", path)
			if _, cleanup, err := resolveSeedDB(projectRoot); err == nil {
				cleanup()
				t.Fatalf("resolveSeedDB accepted non-fixture path %q", path)
			}
		})
	}
}
