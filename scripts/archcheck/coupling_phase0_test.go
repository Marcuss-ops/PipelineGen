package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanDatabaseSQLImportsUsesRealProductionImports(t *testing.T) {
	root := t.TempDir()
	writeArchcheckTestFile(t, root, "internal/platform/sqlite/repository.go", "package sqlite\nimport \"database/sql\"\n")
	writeArchcheckTestFile(t, root, "internal/capabilities/demo/service.go", "package demo\nconst text = \"database/sql\"\n")
	writeArchcheckTestFile(t, root, "internal/capabilities/demo/service_test.go", "package demo\nimport \"database/sql\"\n")
	writeArchcheckTestFile(t, root, "internal/capabilities/demo/handler.go", "package demo\nimport \"database/sql\"\n")

	got, err := scanDatabaseSQLImports(root)
	if err != nil {
		t.Fatalf("scanDatabaseSQLImports: %v", err)
	}
	want := []string{"internal/capabilities/demo/handler.go", "internal/platform/sqlite/repository.go"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("imports = %#v, want %#v", got, want)
	}
}

func TestDatabaseSQLImportAllowedOnlyInAppAndPlatform(t *testing.T) {
	for _, test := range []struct {
		path string
		want bool
	}{
		{"internal/platform/sqlite/repository.go", true},
		{"internal/app/wiring.go", true},
		{"internal/capabilities/jobs/repository.go", true},
		{"internal/capabilities/assets/persistence/committer.go", true},
		{"internal/capabilities/books/service.go", false},
		{"internal/kernel/store.go", false},
	} {
		if got := databaseSQLImportAllowed(test.path); got != test.want {
			t.Errorf("databaseSQLImportAllowed(%q) = %v, want %v", test.path, got, test.want)
		}
	}
}

func TestScanHandlerDatabaseSQLFindsCapabilityHandlerOnly(t *testing.T) {
	root := t.TempDir()
	writeArchcheckTestFile(t, root, "internal/capabilities/demo/handler.go", "package demo\nimport \"database/sql\"\ntype Handler struct { DB *sql.DB }\n")
	writeArchcheckTestFile(t, root, "internal/capabilities/demo/service.go", "package demo\n// database/sql and *sql.DB in prose must not count\ntype Service struct { DB string }\n")
	writeArchcheckTestFile(t, root, "internal/capabilities/demo/handler_test.go", "package demo\ntype Handler struct { DB *sql.DB }\n")

	got, err := scanHandlerDatabaseSQL(root)
	if err != nil {
		t.Fatalf("scanHandlerDatabaseSQL: %v", err)
	}
	want := []string{"internal/capabilities/demo/handler.go"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("handlers = %#v, want %#v", got, want)
	}
}

func TestScanDatabaseSQLImportsReturnsParseErrors(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "internal", "platform", "broken.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package broken\nimport (\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := scanDatabaseSQLImports(root); err == nil {
		t.Fatal("scanDatabaseSQLImports returned nil error for malformed Go")
	}
}
