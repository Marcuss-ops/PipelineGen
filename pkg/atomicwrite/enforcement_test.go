package atomicwrite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// canonicalArtifactGenerators are the tool entry points that emit tracked
// repository artifacts (catalogs, docs, config, SQL baseline, checkpoints).
// A truncated write in one of them corrupts the source of truth, so each MUST
// route through pkg/atomicwrite. The positive half of the enforcement gate
// pins that they do; the negative half (below) forbids the truncating
// primitive repo-wide under cmd/.
var canonicalArtifactGenerators = []string{
	"cmd/architecture-aggregate/main.go",
	"cmd/capability-inventory-aggregate/main.go",
	"cmd/model-registry-gen/main.go",
	"cmd/gen_baseline/main.go",
	"cmd/admin/regen-current-yaml/main.go",
	"cmd/admin/internal/rendering/gen_api_docs_manifest.go",
}

// truncatingWritePrimitive is the call that replaces a file's contents in
// place, so an interrupted or failing write leaves a partial file behind. It
// is exactly the failure class pkg/atomicwrite exists to remove.
const truncatingWritePrimitive = "os.WriteFile("

// TestNoTruncatingWritesUnderCmd is the enforcement hook. Every production Go
// file under cmd/ must use pkg/atomicwrite instead of os.WriteFile; a new
// direct writer fails this test and names the offending file, which is the
// point — the rule is machine-checked rather than remembered.
func TestNoTruncatingWritesUnderCmd(t *testing.T) {
	root := repoRoot(t)
	cmdDir := filepath.Join(root, "cmd")

	var offenders []string
	err := filepath.WalkDir(cmdDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(data), truncatingWritePrimitive) {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
			offenders = append(offenders, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk cmd/: %v", err)
	}
	if len(offenders) > 0 {
		t.Fatalf("cmd/ contains direct os.WriteFile calls; route them through pkg/atomicwrite so an interrupted write cannot truncate a generated artifact:\n  - %s",
			strings.Join(offenders, "\n  - "))
	}
}

// TestCanonicalArtifactGeneratorsUseAtomicWrite is the positive half: the
// declared generators must actually import and call the utility, so deleting
// the import (or silently reverting to a plain write) fails here rather than
// being caught only by the negative scan.
func TestCanonicalArtifactGeneratorsUseAtomicWrite(t *testing.T) {
	root := repoRoot(t)
	for _, rel := range canonicalArtifactGenerators {
		path := filepath.Join(root, filepath.FromSlash(rel))
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("canonical generator %s is missing: %v", rel, err)
		}
		body := string(data)
		if !strings.Contains(body, "pkg/atomicwrite") {
			t.Errorf("%s does not import pkg/atomicwrite", rel)
		}
		if !strings.Contains(body, "atomicwrite.WriteFile(") && !strings.Contains(body, "atomicwrite.Stage(") {
			t.Errorf("%s does not call atomicwrite.WriteFile/Stage", rel)
		}
	}
}

// repoRoot walks up from the test's working directory to the module root
// (the first directory containing go.mod).
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate module root (no go.mod found walking up)")
		}
		dir = parent
	}
}
