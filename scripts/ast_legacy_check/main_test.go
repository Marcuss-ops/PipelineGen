package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", full, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	return root
}

func TestWalkDetectsCanonicalImport(t *testing.T) {
	root := writeTree(t, map[string]string{
		"internal/x/x.go": `package x
import "github.com/Marcuss-ops/PipelineGen/internal/media/models"
var _ *models.MediaAsset
`,
	})

	findings, err := walk(root, allowList{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	if findings[0].Kind != "selector" {
		t.Fatalf("expected selector finding, got %q", findings[0].Kind)
	}
}

func TestWalkDetectsAliasedImport(t *testing.T) {
	root := writeTree(t, map[string]string{
		"internal/x/x.go": `package x
import legacy "github.com/Marcuss-ops/PipelineGen/internal/media/models"
var _ legacy.MediaAsset
`,
	})

	findings, err := walk(root, allowList{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Snippet != "legacy.MediaAsset" {
		t.Fatalf("unexpected findings: %+v", findings)
	}
}

func TestWalkDetectsDotImport(t *testing.T) {
	root := writeTree(t, map[string]string{
		"internal/x/x.go": `package x
import . "github.com/Marcuss-ops/PipelineGen/internal/media/models"
var _ MediaAsset
`,
	})

	findings, err := walk(root, allowList{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Kind != "dot-import" {
		t.Fatalf("unexpected findings: %+v", findings)
	}
}

func TestWalkIgnoresCommentsStringsAndUnrelatedModels(t *testing.T) {
	root := writeTree(t, map[string]string{
		"internal/x/x.go": `package x
// models.MediaAsset is only a comment.
const note = "models.MediaAsset"
type models struct{ MediaAsset int }
var _ models.MediaAsset
`,
	})

	findings, err := walk(root, allowList{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected no findings, got %+v", findings)
	}
}

func TestWalkHonorsIncludeTests(t *testing.T) {
	root := writeTree(t, map[string]string{
		"internal/x/x_test.go": `package x
import "github.com/Marcuss-ops/PipelineGen/internal/media/models"
var _ models.MediaAsset
`,
	})

	withoutTests, err := walk(root, allowList{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(withoutTests) != 0 {
		t.Fatalf("test file should be skipped, got %+v", withoutTests)
	}

	withTests, err := walk(root, allowList{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(withTests) != 1 {
		t.Fatalf("expected test finding, got %+v", withTests)
	}
}

func TestWalkHonorsAllowlist(t *testing.T) {
	root := writeTree(t, map[string]string{
		"internal/bridge/bridge.go": `package bridge
import "github.com/Marcuss-ops/PipelineGen/internal/media/models"
var _ models.MediaAsset
`,
	})
	allowed := allowList{"internal/bridge/bridge.go": {}}

	findings, err := walk(root, allowed, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("allowlisted file produced findings: %+v", findings)
	}
}

func TestIsLegacyImport(t *testing.T) {
	cases := map[string]bool{
		"github.com/Marcuss-ops/PipelineGen/internal/media/models": true,
		"example.com/fork/internal/media/models":                  true,
		"internal/media/models":                                   true,
		"github.com/example/other/models":                          false,
	}
	for path, want := range cases {
		if got := isLegacyImport(path); got != want {
			t.Fatalf("isLegacyImport(%q)=%v, want %v", path, got, want)
		}
	}
}

func TestLoadAllowList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "allowlist.txt")
	if err := os.WriteFile(path, []byte("# comment\ninternal/a.go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	allowed, err := loadAllowList(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := allowed["internal/a.go"]; !ok {
		t.Fatalf("entry missing: %+v", allowed)
	}
}
