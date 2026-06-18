package main

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// writeTree creates a temp directory tree with the given files. Returned
// cleanup removes each leaf path; defer treeCleanup(t, root) to drop the
// whole tree.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	return root
}

func TestWalkDetectsTrueSelectorReference(t *testing.T) {
	root := writeTree(t, map[string]string{
		"internal/x/y.go": `package x

import "internal/media/models"

var _ models.MediaAsset
`,
		"internal/x/clean.go": `package x

var _ = "ignore me — this string mentions models.MediaAsset but as data"`,
	})

	findings, err := walk(root, allowList{}, false)
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d:\n%s", len(findings), dump(findings))
	}
	if !strings.Contains(findings[0].File, "x/y.go") {
		t.Errorf("expected finding in x/y.go, got %s", findings[0].File)
	}
	if findings[0].Kind != "selector" {
		t.Errorf("expected kind=selector, got %s", findings[0].Kind)
	}
}

func TestWalkIgnoresTestFilesByDefault(t *testing.T) {
	root := writeTree(t, map[string]string{
		"internal/x/repo.go": `package x

import "internal/media/models"

type Repo struct{ A models.MediaAsset }
`,
		"internal/x/repo_test.go": `package x

import "internal/media/models"

// tests may still reference the legacy type for migration tests
var _ models.MediaAsset
`,
	})

	// without -include-tests → only repo.go
	findings, err := walk(root, allowList{}, false)
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 (non-test) finding, got %d:\n%s", len(findings), dump(findings))
	}
	if strings.Contains(findings[0].File, "_test.go") {
		t.Errorf("unexpected _test.go finding: %s", findings[0].File)
	}

	// with -include-tests → both files
	findings, err = walk(root, allowList{}, true)
	if err != nil {
		t.Fatalf("walk with tests: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings (with tests), got %d:\n%s", len(findings), dump(findings))
	}
}

func TestAllowListSkipsFile(t *testing.T) {
	root := writeTree(t, map[string]string{
		"internal/bridge/file.go": `package bridge

import "internal/media/models"

var _ models.MediaAsset
var _2 models.MediaAsset
`,
	})

	allowed := allowList{
		"internal/bridge/file.go": "explicit bridge during migration",
	}
	findings, err := walk(root, allowed, false)
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("allowlisted file still produced findings: %s", dump(findings))
	}
}

func TestWalkIgnoresCommentsAndStrings(t *testing.T) {
	// Even if comments and strings mention "models.MediaAsset", they're
	// invisible to the AST detector — that's the entire point of the
	// tool. Confirm by using only such references.
	root := writeTree(t, map[string]string{
		"internal/fake/comment_only.go": `package fake

// TODO: refactor this comment out — it mentions models.MediaAsset
// in source comments so the rg-based detector flagged this file,
// but here we prove the AST detector doesn't.
const note = "see models.MediaAsset in legacy bridge file"
var _ = strings.Contains(note, "models.MediaAsset")
`,
	})
	// Has NO import of internal/media/models → expected: 0 findings.
	findings, err := walk(root, allowList{}, false)
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("comments-only file should be clean: %s", dump(findings))
	}
}

func TestWalkIgnoresLocalVariableNamedModels(t *testing.T) {
	// Without an actual `import "internal/media/models"`, a local
	// identifier called models should NOT be flagged — that's a key
	// improvement over rg-based detection. (If any file does that, it's
	// presumably its own thing.)
	root := writeTree(t, map[string]string{
		"internal/fake/local_var.go": `package fake

type models struct{ MediaAsset int }
var _ models.MediaAsset
`,
	})
	findings, err := walk(root, allowList{}, false)
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("local struct should not be flagged: %s", dump(findings))
	}
}

func TestWalkFiltersFileThatDoesNotImportLegacy(t *testing.T) {
	root := writeTree(t, map[string]string{
		"internal/clean/c.go": `package c

import "fmt"
var _ = fmt.Sprintf("%d", 1)
`,
	})
	findings, err := walk(root, allowList{}, false)
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("clean file produced findings: %s", dump(findings))
	}
}

func TestLoadAllowListMissingFileIsEmpty(t *testing.T) {
	al, err := loadAllowList(filepath.Join(t.TempDir(), "missing.txt"))
	if err != nil {
		t.Fatalf("loadAllowList missing: %v", err)
	}
	if len(al) != 0 {
		t.Errorf("expected empty allowlist, got %v", al)
	}
}

func TestLoadAllowListParsesCommentsAndBlankLines(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "allowlist.txt")
	body := `# comment

internal/foo/bar.go

   internal/foo/baz.go
`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	al, err := loadAllowList(p)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"internal/foo/bar.go", "internal/foo/baz.go"}
	got := make([]string, 0, len(al))
	for k := range al {
		got = append(got, k)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func dump(findings []Finding) string {
	var b strings.Builder
	for _, f := range findings {
		b.WriteString(f.File)
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(f.Line))
		b.WriteString(" (")
		b.WriteString(f.Kind)
		b.WriteString(") ")
		b.WriteString(f.Snippet)
		b.WriteByte('\n')
	}
	return b.String()
}
