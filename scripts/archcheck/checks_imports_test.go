package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanApplicationInfrastructureImportsUsesProductionImportEdges(t *testing.T) {
	root := t.TempDir()
	writeArchcheckTestFile(t, root, "internal/application/demo/service.go", `package demo

import (
    _ "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

const prose = "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/not-an-import"
`)
	writeArchcheckTestFile(t, root, "internal/application/demo/service_test.go", `package demo

import _ "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/test-only"
`)

	got, err := scanApplicationInfrastructureImports(root)
	if err != nil {
		t.Fatalf("scanApplicationInfrastructureImports: %v", err)
	}
	want := "internal/application/demo/service.go -> github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("edges = %#v, want [%q]", got, want)
	}
}

func TestCheckApplicationToInfrastructureRejectsNewAndStaleEdges(t *testing.T) {
	root := t.TempDir()
	writeArchcheckTestFile(t, root, "internal/application/demo/service.go", `package demo
import _ "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
`)
	allowlist := filepath.Join(root, "allowlist.txt")
	if err := os.WriteFile(allowlist, []byte(strings.Join([]string{
		"internal/application/demo/service.go -> github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive",
		"internal/application/demo/removed.go -> github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files",
	}, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	baseline := filepath.Join(root, "baseline.txt")
	if err := os.WriteFile(baseline, []byte(strings.Join([]string{
		"internal/application/demo/service.go -> github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive",
		"internal/application/demo/removed.go -> github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files",
	}, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stats, violations := checkApplicationToInfrastructureAt(root, allowlist, baseline)
	if stats["actual"] != 1 || stats["allowed"] != 2 || stats["baseline"] != 2 || stats["stale"] != 1 || stats["violations"] != 1 {
		t.Fatalf("stats = %#v, want actual=1 allowed=2 stale=1 violations=1", stats)
	}
	if len(violations) != 1 || !strings.Contains(violations[0], "stale application→infrastructure allowlist entry") {
		t.Fatalf("violations = %#v, want one stale-entry violation", violations)
	}

	if err := os.WriteFile(allowlist, []byte("\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stats, violations = checkApplicationToInfrastructureAt(root, allowlist, baseline)
	if stats["actual"] != 1 || stats["allowed"] != 0 || stats["baseline"] != 2 || stats["stale"] != 0 || stats["violations"] != 1 {
		t.Fatalf("new-edge stats = %#v, want actual=1 allowed=0 stale=0 violations=1", stats)
	}
	if len(violations) != 1 || !strings.Contains(violations[0], "unallowlisted application→infrastructure import") {
		t.Fatalf("violations = %#v, want one new-edge violation", violations)
	}
}

func TestCheckApplicationToInfrastructureRejectsAllowlistGrowth(t *testing.T) {
	root := t.TempDir()
	writeArchcheckTestFile(t, root, "internal/application/demo/service.go", `package demo
import _ "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
`)
	allowlist := filepath.Join(root, "allowlist.txt")
	baseline := filepath.Join(root, "baseline.txt")
	activeEdge := "internal/application/demo/service.go -> github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	newEdge := "internal/application/demo/service.go -> github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	if err := os.WriteFile(allowlist, []byte(activeEdge+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(baseline, []byte(newEdge+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stats, violations := checkApplicationToInfrastructureAt(root, allowlist, baseline)
	if stats["violations"] != 1 {
		t.Fatalf("stats = %#v, want one allowlist-growth violation", stats)
	}
	joined := strings.Join(violations, "\n")
	if !strings.Contains(joined, "allowlist grew beyond baseline") {
		t.Fatalf("violations = %#v, want an allowlist-growth violation", violations)
	}
}

func writeArchcheckTestFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
