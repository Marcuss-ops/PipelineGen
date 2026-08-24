// Package scan — constructor-deps scanner.
//
// scan/constructors.go owns the "8-dep/ctor cap" rule family
// (`max_constructor_deps` → `constructor_deps` rule key in the JSON
// report). It walks non-test Go source files under <root>/internal/,
// detects `func New<X>(...)` package-level constructor signatures,
// counts their parameter list, and emits a `warn`-severity violation
// when the count exceeds pol.MaxConstructorDeps.
//
// Cross-references:
//   - cmd/archcheck/main.go: the caller (now invokes scan.ScanConstructors
//     instead of the inlined scanConstructorDeps from PR2-pre)
//   - architecture/policy.yaml: `max_constructor_deps` threshold
//   - docs/architecture/godlike/08_ARCHITECTURE_CI_GATES.md §"Complexity
//     budgets": the canonical rule definition
package structure

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// ScanConstructors walks non-test Go source files under <root>/internal/,
// finds func New<X>(...) constructor signatures, counts their parameters,
// and emits a warn-severity violation when parameter count exceeds
// pol.MaxConstructorDeps.
//
// Multi-line signatures are handled by accumulating lines until parentheses
// balance. Generic type parameters `[T any]` written before the opening
// paren are detected when `[` and `]` appear on the same line (rare
// multi-line generic blocks are silently skipped — acceptable Phase 1
// gap, intentional). Package-level constructors only: methods with
// receivers (e.g. `func (r *R) NewXxx(...)`) are detected via presence of
// ')' between `func` and `New`, and skipped.
//
// Skipped dirs mirror ScanPackages (.git, vendor, node_modules,
// node-scraper, examples, scripts). When pol.MaxConstructorDeps <= 0
// the function is a no-op (the policy opts out of the rule family).
//
// Phase 1 (June 2026): implements the rule formerly at the call site
// per docs/architecture/godlike/08_ARCHITECTURE_CI_GATES.md §"Complexity
// budgets". Extracted from cmd/archcheck/main.go in FASE 1.C PR3.
func ScanConstructors(root string, pol *policy.Policy, r *report.Report) {
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
			// Method receiver detection: the regex `func New\w+`
			// matches `func New<X>` regardless of whether a receiver
			// `func (r *R) New<X>` is present — so the matched
			// substring itself does NOT contain `(`. Detect the
			// receiver by looking at the text BETWEEN `func` and the
			// constructor name: package-level ctors have exactly one
			// space there; methods with receivers have `(`. (FASE 1.C
			// PR3 — the pre-PR3 check `strings.Contains(matched, ")")`
			// was a no-op because `)` never appears inside the
			// matched substring for valid Go source.)
			betweenFuncAndName := line[loc[0]+len("func") : loc[1]]
			if strings.Contains(betweenFuncAndName, "(") {
				continue // method with receiver — skip
			}
			// Find the parameter-list '(' — scan forward past any
			// generic type parameter block `[T any]` that may appear
			// between the constructor name and the param list, e.g.
			// `func NewRepo[T any](ctx context.Context, ...)`.
			rest := line[loc[1]:] // text after `func New<X>`
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
			// onward until parentheses balance (handles multi-line
			// signatures).
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
			// +1 because N commas means N+1 parameters
			// (0 commas = 1 param). Handle empty params
			// (`func NewFoo()`) — zero commas, zero params.
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

// parenDepth returns the depth delta of '(' minus ')' in s. Positive
// means more opens than closes (unbalanced).
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

// countTopLevelCommas counts commas at nesting depth 0 (i.e. commas that
// separate parameters, not commas inside nested parens/brackets/braces).
// This is used to count constructor parameters from a balanced parameter
// list.
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
