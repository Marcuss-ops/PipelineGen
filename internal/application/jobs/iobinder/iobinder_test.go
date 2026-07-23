package iobinder

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	appio "github.com/Marcuss-ops/PipelineGen/internal/application/iobinder"
)

// disallowedSymbols enumerates the canonical sync-IO patterns the jobs
// hot paths MUST NOT use without a matching allowlist entry.
//
//   - os.ReadFile : synchronous full-file read.
//   - os.OpenFile : synchronous file open with custom flags/mode.
//   - os.Open     : synchronous file open.
var disallowedSymbols = []string{
	"os.ReadFile",
	"os.OpenFile",
	"os.Open",
}

// TestNoDirectSyncIOInJobsHotPaths walks internal/application/jobs/
// recursively, parses every production .go file with go/ast, and fails if
// any forbidden symbol appears at a (path, symbol) pair not listed in
// allowlist.txt. The test also fails if an allowlist entry no longer
// matches a live hit (stale exception).
func TestNoDirectSyncIOInJobsHotPaths(t *testing.T) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("findRepoRoot: %v", err)
	}
	jobsDir := filepath.Join(repoRoot, "internal", "application", "jobs")
	allowlistPath := filepath.Join(jobsDir, "iobinder", "allowlist.txt")

	allowlist, err := appio.LoadAllowlist(allowlistPath)
	if err != nil {
		t.Fatalf("LoadAllowlist: %v", err)
	}

	violations, err := appio.ScanDirectory(jobsDir, disallowedSymbols, appio.ScannerConfig{
		Root:           jobsDir,
		SkipSubstrings: []string{"iobinder/"},
	})
	if err != nil {
		t.Fatalf("ScanDirectory: %v", err)
	}

	var newViolations []string
	for _, v := range violations {
		key := v.File + ":" + v.Symbol
		entry, ok := allowlist[key]
		if !ok {
			newViolations = append(newViolations, fmt.Sprintf("%s (symbol %s at line %d)", v.File, v.Symbol, v.Line))
			continue
		}
		entry.Seen = true
	}

	var stale []string
	for key, entry := range allowlist {
		if !entry.Seen {
			stale = append(stale, fmt.Sprintf("%s (owner=%s deadline=%s rationale=%q)", key, entry.Owner, entry.Deadline, entry.Rationale))
		}
	}

	if len(newViolations) > 0 || len(stale) > 0 {
		var b strings.Builder
		b.WriteString("I/O binder gate failed for internal/application/jobs/.\n")
		b.WriteString("\n")
		if len(newViolations) > 0 {
			sort.Strings(newViolations)
			b.WriteString("NEW sync-IO bindings (refactor to a typed port, or add to allowlist.txt with owner/deadline/rationale):\n")
			for _, v := range newViolations {
				b.WriteString("  - " + v + "\n")
			}
		}
		if len(stale) > 0 {
			if len(newViolations) > 0 {
				b.WriteString("\n")
			}
			sort.Strings(stale)
			b.WriteString("STALE allowlist entries (no matching live hit; remove them):\n")
			for _, s := range stale {
				b.WriteString("  - " + s + "\n")
			}
		}
		t.Fatalf("%s", b.String())
	}
}

// findRepoRoot walks up from the test file's directory until it finds a go.mod.
func findRepoRoot() (string, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("runtime.Caller(0) failed")
	}
	dir := filepath.Dir(thisFile)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found walking up from %s", thisFile)
		}
		dir = parent
	}
}
