package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadComplexityEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "baseline.txt")
	content := "cc=41 cog=61 loc=10 nest=6 ch=1 internal/foo.go:12 Work()\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := loadComplexityEntries(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].CC != 41 || entries[0].Cognitive != 61 || entries[0].Nesting != 6 {
		t.Fatalf("unexpected entries: %#v", entries)
	}
}

func TestLoadComplexityEntriesRejectsEmptyBaseline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "baseline.txt")
	if err := os.WriteFile(path, []byte("not a complexity report\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadComplexityEntries(path); err == nil {
		t.Fatal("expected parse error")
	}
}
