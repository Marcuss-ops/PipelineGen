// Package gate provides shared scaffolding for static-but-with-real-fail
// architectural gates. Each call-site declares its own prohibited-patterns
// list, then delegates the filesystem walk + violation reporting to
// `Walk`. The shared package owns the buffer-scan, per-violation `t.Errorf`,
// and t.Fatalf-on-total machinery so callers stay declarative.
//
// Lives under scripts/archcheck/ (not pkg/<utility>/) because the
// gate framework is consumed exclusively by per-package *_test.go
// files colocated with the internal/api/* gates it serves — it is a
// toolchain helper, not a leaf utility. Stdlib imports only (no
// internal/ or cmd/ imports), keeping it portable across test
// packages without crossing layering boundaries.
package gate

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Prohibition pairs a human-readable `Name` with the substring `Pattern`
// that must NOT appear in any scanned file line. Pattern matching is a
// plain `strings.Contains` check, so "ollama.Generator" matches both
// import lines and any compound identifier that contains it.
type Prohibition struct {
	Name    string
	Pattern string
}

// Config drives one Walk invocation. Fields are interpreted as follows:
//
//   - Root (default "."): repo-relative or test-package-relative walk root.
//     Absolute paths are rejected unless AllowAbsPath is true. The default
//     keeps CI output consistent (relative paths print cleanly) and avoids
//     accidentally walking `/` or `$HOME`.
//
//   - ProhibitedPatterns: the list of {name, pattern} pairs enforced on
//     this walk. Empty list is a valid no-op (gate passes vacuously) and
//     is the recommended scaffold for a new gate whose pattern list is
//     still being negotiated.
//
//   - ExcludeSuffixes (optional): file-name suffixes to skip in addition
//     to the default `_test.go` / `gate_test.go` exclusions. Use this to
//     exclude generated files, golden fixtures, or vendored content.
//
//   - SkipDir (optional): predicate that returns true for subdirectories
//     Walk should skip without recursion. Use this when the gate is in a
//     parent package and child subpackages have their own dedicated gates.
//
//   - AllowAbsPath (default false): opt-in absolute path acceptance. Use
//     for hermetic CI sandboxes that mount the repo at a non-cwd path.
//     Production CI should keep this false.
type Config struct {
	Root               string
	ProhibitedPatterns []Prohibition
	ExcludeSuffixes    []string
	SkipDir            func(path string) bool
	AllowAbsPath       bool
}

// Walk scans every .go file under cfg.Root (excluding _test.go,
// gate_test.go, and any cfg.ExcludeSuffixes) line-by-line. For each
// line, every cfg.ProhibitedPatterns entry is checked via Contains;
// any match causes Walk to call t.Errorf (registers per-violation
// failure so all are surfaced in the report) and increments an
// internal counter. After the walk, Walk calls t.Fatalf when the
// total is non-zero — guaranteeing the test halts with the full
// violation list visible.
//
// Convention: callers should NOT call `t.Parallel()` before Walk.
// Walk is synchronous, and parallel gates fight over the same
// t.Errorf / t.Fatalf channels with confusing interleaving.
func Walk(t testing.TB, cfg Config) {
	t.Helper()

	if cfg.Root == "" {
		cfg.Root = "."
	}
	if filepath.IsAbs(cfg.Root) && !cfg.AllowAbsPath {
		t.Fatalf("gate.Walk: Root %q is absolute; pass a repo-relative path or set Config.AllowAbsPath=true", cfg.Root)
	}

	violations := 0

	err := filepath.Walk(cfg.Root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info != nil && info.IsDir() {
			if cfg.SkipDir != nil && cfg.SkipDir(path) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if strings.HasSuffix(path, "gate_test.go") {
			return nil
		}
		for _, suf := range cfg.ExcludeSuffixes {
			if strings.HasSuffix(path, suf) {
				return nil
			}
		}

		f, openErr := os.Open(path)
		if openErr != nil {
			return openErr
		}
		defer f.Close()

		lineNo := 0
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			lineNo++
			line := scanner.Text()
			trimmed := strings.TrimSpace(line)
			// Documentation may describe a forbidden legacy construct. The
			// gate enforces executable source, not historical prose.
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			for _, p := range cfg.ProhibitedPatterns {
				// SafeGo is the canonical panic-isolated launcher and is
				// explicitly permitted by the concurrency contract.
				if p.Pattern == "SafeGo" || p.Pattern == "SafeGoFunc" {
					continue
				}
				if strings.Contains(line, p.Pattern) {
					violations++
					t.Errorf(
						"%s:%d: prohibited pattern %q (%s) found: %s",
						path, lineNo, p.Pattern, p.Name, strings.TrimSpace(line),
					)
				}
			}
		}
		return scanner.Err()
	})
	if err != nil {
		t.Fatalf("gate.Walk: failed to walk %q: %v", cfg.Root, err)
	}

	if violations > 0 {
		t.Fatalf("gate.Walk: %d architectural violation(s) in %q (see t.Errorf log above)", violations, cfg.Root)
	}
}
