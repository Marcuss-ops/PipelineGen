package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanRetiredRootViolationsRejectsSourceAndImports(t *testing.T) {
	root := t.TempDir()
	writeArchcheckTestFile(t, root, "internal/application/demo/service.go", `package demo
import _ "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
`)
	writeArchcheckTestFile(t, root, "internal/capabilities/demo/service.go", `package demo
import _ "github.com/Marcuss-ops/PipelineGen/internal/application/demo"
`)

	violations, err := scanRetiredRootViolations(root, []string{"internal/application", "internal/domain"})
	if err != nil {
		t.Fatalf("scanRetiredRootViolations: %v", err)
	}
	joined := strings.Join(violations, "\n")
	if !strings.Contains(joined, "retired root source reintroduced: internal/application/demo/service.go") {
		t.Fatalf("violations = %#v, want retired source violation", violations)
	}
	if !strings.Contains(joined, "retired root import reintroduced") {
		t.Fatalf("violations = %#v, want retired import violation", violations)
	}
}

func TestCrossCapabilityImportUsesCurrentCapabilityRoot(t *testing.T) {
	root := t.TempDir()
	writeArchcheckTestFile(t, root, "internal/capabilities/source/service.go", `package source
import _ "github.com/Marcuss-ops/PipelineGen/internal/capabilities/target"
`)
	writeArchcheckTestFile(t, root, "internal/capabilities/target/service.go", `package target
import _ "github.com/Marcuss-ops/PipelineGen/internal/capabilities/target"
`)

	stats, violations := checkCrossCapabilityImportAt(root)
	if stats["actual"] != 1 || stats["violations"] != 1 {
		t.Fatalf("stats = %#v, violations = %#v; want one cross-capability pair", stats, violations)
	}
	if !strings.Contains(violations[0], "source->target") {
		t.Fatalf("violations = %#v, want source->target", violations)
	}

	imports, err := scanProductionImports(root)
	if err != nil {
		t.Fatalf("scanProductionImports: %v", err)
	}
	caps := map[string]bool{"source": true, "target": true}
	pairs := map[string]bool{}
	for _, edge := range imports {
		source := capabilityOfFile(edge.file, caps)
		target := capabilityOfImport(edge.importPath, caps)
		if source != "" && target != "" && source != target {
			pairs[source+"->"+target] = true
		}
	}
	if !pairs["source->target"] || len(pairs) != 1 {
		t.Fatalf("pairs = %#v, want only source->target", pairs)
	}
}

func TestCurrentAndRetiredRootsAreExplicit(t *testing.T) {
	if len(currentInternalRoots) != 4 {
		t.Fatalf("currentInternalRoots = %#v, want four current roots", currentInternalRoots)
	}
	for _, root := range []string{"internal/app", "internal/kernel", "internal/capabilities", "internal/platform"} {
		found := false
		for _, current := range currentInternalRoots {
			if current == root {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("currentInternalRoots missing %q", root)
		}
	}
	for _, test := range []struct {
		path string
		want string
	}{
		{"internal/app/wiring.go", "internal/app"},
		{"internal/kernel/asset/state.go", "internal/kernel"},
		{"internal/capabilities/scripts/service.go", "internal/capabilities"},
		{"internal/platform/sqlite/store.go", "internal/platform"},
		{"internal/application/legacy.go", ""},
	} {
		if got := currentRootForPath(test.path); got != test.want {
			t.Errorf("currentRootForPath(%q) = %q, want %q", test.path, got, test.want)
		}
	}
	if got := retiredRootForPath("internal/application/legacy.go"); got != "internal/application" {
		t.Fatalf("retiredRootForPath = %q, want internal/application", got)
	}
	if got := retiredRootForImport(moduleImportPrefix + "internal/infrastructure/drive"); got != "internal/infrastructure" {
		t.Fatalf("retiredRootForImport = %q, want internal/infrastructure", got)
	}
}

func TestCapabilityClassifiersUseCurrentRoot(t *testing.T) {
	caps := map[string]bool{"scripts": true, "images": true}
	if got := capabilityOfFile("internal/capabilities/scripts/adapter.go", caps); got != "scripts" {
		t.Fatalf("capabilityOfFile = %q, want scripts", got)
	}
	if got := capabilityOfFile("internal/application/scripts/adapter.go", caps); got != "" {
		t.Fatalf("legacy capabilityOfFile = %q, want empty", got)
	}
	if got := capabilityOfImport("github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/service", caps); got != "images" {
		t.Fatalf("capabilityOfImport = %q, want images", got)
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
