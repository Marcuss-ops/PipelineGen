// Package main — archcheck (Phase 0, target-tree governance).
//
// Reads `architecture/policy.yaml` (flat key:value, stdlib-parseable — see
// comments in that file), walks the project tree, and emits a JSON
// violation report to stdout. Phase 0 is report-only (exits 0 even when
// violations are present) so existing CI is undisturbed while the policy
// and tooling stabilise. Phase N promotes the gate by passing `--strict`.
//
// Scope: this binary is the **target-tree half** of PipelineGen's
// architectural gating. The **legacy burndown** half lives in
// `scripts/archcheck/` — a regex-heavy (`rg`) transitional ratchet that
// enforces the monotone-decreasing baselines for legacy surface area
// (`database/sql` in api/application/domain, interface{} growth,
// dependency setters, fake 501 routes, Python legacy writers, ...).
// The two binaries are KEEP_BOTH by design (June 2026 Wave 16 PR
// "scripts/archcheck dead-code sweep" decision recorded in
// architecture/current.yaml):
//
//   - cmd/archcheck is **permanent**: it enforces the target tree
//     (kernel/capability/platform subzones, max files per package,
//     forbidden top-level dirs, legacy→target internal roots, ownership
//     rule family) indefinitely.
//   - scripts/archcheck is **transient**: each rule family expires once
//     it reaches `verified_zero: true` in current.yaml; the binary
//     lives until the last legacy family is burned down.
//
// They are NOT overlapping: same name, complementary concerns. The
// shared JSON contract (`passed`, `mode`, `checks`, `violations`) is
// kept structurally identical so downstream dashboards can consume
// BOTH outputs without per-binary plumbing.
//
// Stdlib only — no gopkg.in/yaml.v3 import. The flat policy format is
// documented in architecture/policy.yaml and intentionally simple
// (one key per line, comma-separated lists). The complexity gate is the
// policy file, not the parser.
//
// Exit codes:
//
//	0 — report printed (default; --strict off). Phase 0 mode.
//	1 — violations present while --strict (Phase N mode).
//	2 — load/walk/marshal error.
//
// FASE 1.C (June 2026) layout: main.go is the CLI dispatch + the
// scanConstructorDeps scanner (moves to scan/constructors.go in PR3).
// The data model + parser live in `policy/{model,load}.go` and
// `report/model.go` (separate Go packages). The rule-family scanners
// live in `scan/{packages,roots,documents}.go` (PR2). PR4 will
// extract the orchestration into `runner.go` and trim this file
// to ≤100 LOC.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/scan"
)

func main() {
	var (
		root   = flag.String("root", ".", "Project root to scan (default: cwd)")
		polstr = flag.String("policy", "architecture/policy.yaml", "Path to policy YAML")
		strict = flag.Bool("strict", false, "Phase N gate: exit 1 if any violations present")
		phase  = flag.String("phase", "0", "Phase label (printed in the report)")
	)
	flag.Parse()

	pol, err := policy.Load(*polstr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "archcheck: load policy %q: %v\n", *polstr, err)
		os.Exit(2)
	}

	r := &report.Report{
		Mode:       "target-tree-dry-run",
		PolicyPath: *polstr,
		Root:       *root,
		Phase:      *phase,
		Policy:     pol,
		Summary:    report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}

	// Phase 1 (June 2026): scan for New<X>(...) constructors whose
	// parameter count exceeds policy.yaml max_constructor_deps. See
	// docs/architecture/godlike/08_ARCHITECTURE_CI_GATES.md §"Complexity
	// budgets" for the canonical rule definition. Moves to
	// scan/constructors.go in PR3.
	scanConstructorDeps(*root, pol, r)

	// Rule-family scanners (PR2 split). Each scan.Scan* function appends
	// report.Violation entries to r; the orchestration order is
	// preserved from the pre-PR2 inline layout (constructor deps first
	// because they look at internal/ only; roots + documents next; file
	// size / pkg size / thin_command last because they share the fileLines
	// map populated by scan.ScanPackages).
	scan.ScanForbiddenDirs(*root, pol, r)
	scan.ScanKernelSubzoneHints(*root, pol, r)
	scan.ScanUnknownInternalRoots(*root, pol, r)
	scan.ScanOwnershipDoc(*root, pol, r)
	scan.ScanLegacyPolicyDoc(*root, pol, r)
	scan.ScanCIGatesDoc(*root, pol, r)
	scan.ScanAgentPlaybookDoc(*root, pol, r)
	scan.ScanRemovalDoc(*root, pol, r)
	scan.ScanStaleProsePaths(*root, pol, r)

	fileLines := map[string]int{}
	scan.ScanPackages(*root, pol, r, fileLines)
	scan.ScanCommandBinaries(*root, pol, r, fileLines)

	r.Summary.TotalViolations = len(r.Violations)
	for _, v := range r.Violations {
		r.Summary.ByReason[v.Rule]++
		r.Summary.BySeverity[v.Severity]++
	}
	r.Passed = len(r.Violations) == 0

	out, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "archcheck: marshal report: %v\n", err)
		os.Exit(2)
	}
	fmt.Println(string(out))

	if *strict && len(r.Violations) > 0 {
		os.Exit(1)
	}
}

// scanConstructorDeps walks non-test Go source files under internal/,
// finds func New<X>(...) constructor signatures, counts their parameters,
// and emits a warn-severity violation when parameter count exceeds
// pol.MaxConstructorDeps.
//
// Multi-line signatures are handled by accumulating lines until
// parentheses balance. Generic type parameters [T any] before the
// opening paren are detected when [ and ] are on the same line
// (rare multi-line generic blocks are silently skipped — acceptable
// Phase 1 gap). Package-level constructors only: methods with
// receivers (func (r *R) NewXxx(...)) are detected via presence of
// ')' between func and New, and skipped.
//
// Phase 1 (June 2026): implements the TODO formerly at the call site
// per docs/architecture/godlike/08_ARCHITECTURE_CI_GATES.md §"Complexity
// budgets". Skipped dirs mirror ScanPackages.
//
// FASE 1.C PR3 will move this function (and its 4 helpers below) to
// cmd/archcheck/scan/constructors.go. Kept in main.go for PR2 to keep
// this commit structural-only on the scan/* family.
func scanConstructorDeps(root string, pol *policy.Policy, r *report.Report) {
	if pol.MaxConstructorDeps <= 0 {
		return
	}
	re := regexp.MustCompile(`func New\w+`)
	skipDirs := map[string]bool{
		".git": true, "vendor": true, "node_modules": true,
		"node-scraper": true, "examples": true, "scripts": true,
	}
	internalDir := filepath.Join(root, "internal")
	_ = filepath.WalkDir(internalDir, func(path string, d os.DirEntry, err error) error {
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
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		rel, _ := filepath.Rel(root, path)
		relPath := filepath.ToSlash(rel)
		sc := bufio.NewScanner(f)
		lineNum := 0
		for sc.Scan() {
			lineNum++
			line := sc.Text()
			loc := re.FindStringIndex(line)
			if loc == nil {
				continue
			}
			// Check for method receiver: if the text between "func"
			// and the matched name contains ')', it's a method with
			// a receiver (e.g. func (s *Service) NewFoo(...)).
			// Skip these — only package-level constructors count.
			if strings.Contains(line[loc[0]:loc[1]], ")") {
				continue
			}
			// Find the parameter-list '(' — scan forward past any
			// generic type parameter block [T any] that may appear
			// between the constructor name and the param list.
			// e.g. func NewFoo[T any](ctx context.Context, ...).
			rest := line[loc[1]:] // text after "func New<X>"
			openIdx := strings.Index(rest, "(")
			// If there's a '[' before '(', it's a generic type param
			// block; skip past the matching ']' to find the real '('.
			if b := strings.IndexByte(rest, '['); b >= 0 && (openIdx < 0 || b < openIdx) {
				if closeB := matchBracket(rest, b); closeB >= 0 {
					openIdx = strings.Index(rest[closeB+1:], "(")
					if openIdx >= 0 {
						openIdx += closeB + 1
					}
				}
			}
			if openIdx < 0 {
				// No '(' found on this line — not a constructor call.
				continue
			}
			// Accumulate the full signature from the opening paren
			// onward until parentheses balance.
			sig := rest[openIdx:]
			depth := parenDepth(sig)
			for depth > 0 && sc.Scan() {
				lineNum++
				next := sc.Text()
				sig += "\n" + next
				depth = parenDepth(sig)
			}
			// Extract params between the outermost ( ... ).
			closeIdx := matchParen(sig, 0)
			if closeIdx < 0 {
				continue
			}
			params := sig[1:closeIdx]
			count := countTopLevelCommas(params)
			// +1 because N commas means N+1 parameters (0 commas = 1 param).
			// Handle empty params (func NewFoo()) — zero commas, zero params.
			paramCount := 0
			if strings.TrimSpace(params) != "" {
				paramCount = count + 1
			}
			if paramCount > pol.MaxConstructorDeps {
				r.Violations = append(r.Violations, report.Violation{
					File:         relPath,
					Line:         lineNum - (strings.Count(sig, "\n")),
					ActualCount:  paramCount,
					AllowedCount: pol.MaxConstructorDeps,
					MatchedRule:  "max_constructor_deps",
					Rule:         "constructor_deps",
					Severity:     "warn",
					Note: fmt.Sprintf(
						"func New<X>(...) has %d parameters (max %d); split into smaller constructors or use a config struct",
						paramCount, pol.MaxConstructorDeps,
					),
				})
			}
		}
		return nil
	})
}

// parenDepth returns the depth delta of '(' minus ')' in s.
// Positive means more opens than closes (unbalanced).
func parenDepth(s string) int {
	d := 0
	for _, c := range s {
		switch c {
		case '(':
			d++
		case ')':
			d--
		}
	}
	return d
}

// matchParen returns the index of the closing paren that matches the
// opening paren at openIdx. Returns -1 if unmatched.
func matchParen(s string, openIdx int) int {
	d := 0
	for i := openIdx; i < len(s); i++ {
		switch s[i] {
		case '(':
			d++
		case ')':
			d--
			if d == 0 {
				return i
			}
		}
	}
	return -1
}

// matchBracket returns the index of the closing ']' that matches the
// opening '[' at openIdx. Mirrors matchParen for bracket pairs.
func matchBracket(s string, openIdx int) int {
	d := 0
	for i := openIdx; i < len(s); i++ {
		switch s[i] {
		case '[':
			d++
		case ']':
			d--
			if d == 0 {
				return i
			}
		}
	}
	return -1
}

// countTopLevelCommas counts commas at nesting depth 0 (i.e. commas
// that separate parameters, not commas inside nested parens/brackets/
// braces). This is used to count constructor parameters from a
// balanced parameter list.
func countTopLevelCommas(s string) int {
	depth := 0
	count := 0
	for _, c := range s {
		switch c {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ',':
			if depth == 0 {
				count++
			}
		}
	}
	return count
}
