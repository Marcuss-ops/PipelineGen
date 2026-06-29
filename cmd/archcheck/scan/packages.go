// Package scan — archcheck rule-family scanners.
//
// scan/packages.go owns the "directory walking + module identification"
// half of the scan family. The two functions here both consume a
// shared `fileLines map[string]int` that ScanPackages populates as it
// walks the tree — ScanCommandBinaries reads the per-file line counts
// off the same map to emit the cmd_main_max_lines rule (so the
// walk happens exactly once per archcheck invocation, not once per
// rule family).
//
// Package boundary: `package scan` (separate from `package main` of
// cmd/archcheck) so the scan subdirectory holds a focused concern:
// "given a project root and a loaded policy, find the rule-family
// violations and append them to the report". The boundary lets the
// snapshot test (cmd/archcheck/runner_test.go) test the rule-family
// outputs in isolation if needed (the runner_test.go today exercises
// the full pipeline; future PR-A may add focused unit tests that
// import this package directly).
//
// Cross-references:
//   - cmd/archcheck/main.go: the caller (calls scan.ScanPackages + scan.ScanCommandBinaries)
//   - architecture/policy.yaml: the policy knobs (max_files_per_package,
//     max_lines_per_file, cmd_main_max_lines) the functions read
//   - docs/architecture/godlike/08_ARCHITECTURE_CI_GATES.md: the canonical
//     rule definitions (file_size, pkg_size, thin_command)
package scan

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// ScanPackages walks non-test Go files under the root, counts files per
// package dir, and emits a violation per package exceeding
// pol.MaxFilesPerPackage. Also emits a violation per file exceeding
// pol.MaxLinesPerFile. The walk returns SkipDir for vendored /
// generated directories, so descendants are excluded without
// per-file filtering.
//
// `fileLines` is a shared out-parameter populated as a side effect
// of the walk: for every .go file (excluding _test.go) the line
// count is recorded so ScanCommandBinaries can read it without a
// second walk. Callers MUST allocate the map before the call (e.g.
// `fileLines := map[string]int{}`) and pass it by reference.
//
// Skipped dirs (Phase 0 hardcoded — moved to policy.Policy.ScanSkipDirs
// in Phase 1+ if/when the policy schema adds a generic skip-set):
//
//	.git, vendor, node_modules, node-scraper, examples, scripts
func ScanPackages(root string, pol *policy.Policy, r *report.Report, fileLines map[string]int) {
	pkgCounts := map[string]int{}
	skipDirs := map[string]bool{
		".git": true, "vendor": true, "node_modules": true,
		"node-scraper": true, "examples": true, "scripts": true,
	}

	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[filepath.Base(path)] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		dir := filepath.Dir(path)
		relDir, _ := filepath.Rel(root, dir)
		pkgCounts[filepath.ToSlash(relDir)]++

		n, lerr := countLines(path)
		if lerr == nil {
			fileLines[path] = n
			if n > pol.MaxLinesPerFile {
				r.Violations = append(r.Violations, report.Violation{
					File:        filepath.ToSlash(filepath.Join(relDir, filepath.Base(path))),
					ActualLines: n,
					MaxLines:    pol.MaxLinesPerFile,
					MatchedRule: "max_lines_per_file",
					Rule:        "file_size",
					Severity:    "warn",
				})
			}
		}
		return nil
	})

	for pkg, count := range pkgCounts {
		if count > pol.MaxFilesPerPackage {
			r.Violations = append(r.Violations, report.Violation{
				Package:      pkg,
				ActualCount:  count,
				AllowedCount: pol.MaxFilesPerPackage,
				MatchedRule:  "max_files_per_package",
				Rule:         "pkg_size",
				Severity:     "warn",
			})
		}
	}
}

// ScanCommandBinaries checks that each cmd/<name>/main.go is below
// pol.CmdMainMaxLines. Phase 0 reports; the user-contract says
// command binaries must be thin (root ctx + config load + compose
// call + mode select + shutdown wait).
//
// `fileLines` is the same map ScanPackages populated — the function
// skips the cmd/<name>/main.go file size rule (it lives in
// ScanPackages already) and reuses the line count for the
// cmd_main_max_lines check. Files NOT present in the map (e.g.
// because they were skipped by the .go filter or the dir-skip list)
// are silently ignored.
func ScanCommandBinaries(root string, pol *policy.Policy, r *report.Report, fileLines map[string]int) {
	cmdDir := filepath.Join(root, "cmd")
	entries, err := os.ReadDir(cmdDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		mainPath := filepath.Join(cmdDir, name, "main.go")
		n, ok := fileLines[mainPath]
		if !ok {
			continue
		}
		if n > pol.CmdMainMaxLines {
			rel, _ := filepath.Rel(root, mainPath)
			r.Violations = append(r.Violations, report.Violation{
				File:        filepath.ToSlash(rel),
				ActualLines: n,
				MaxLines:    pol.CmdMainMaxLines,
				MatchedRule: "cmd_main_max_lines",
				Rule:        "thin_command",
				Severity:    "warn",
				Note:        "command binaries must be thin (root ctx + config + compose + mode + shutdown)",
			})
		}
	}
}

// countLines returns the number of newline-delimited lines in path.
// Uses bufio.Scanner (max 64K line length) which is sufficient for
// any source file in the project. Returns 0 + error on open failure
// (the caller ignores the error and treats count=0 as "skip").
func countLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		n++
	}
	return n, sc.Err()
}
