// Package scan — Check 76: input immutability (Wave 5, July 2026).
//
// scan/percheck_inputimmutability.go owns the forward-prevention gate
// for input immutability. Use-case input structs (named *Input or
// *Request) should be treated as read-only after they are passed to a
// function. Mutating an input struct in-place makes the code harder to
// reason about and breaks the caller's expectation of the input value.
//
// This gate flags two common mutation patterns:
//   1. Reassigning the whole input struct: `*req = ...`
//   2. Assigning to a field of an parameter named req/input/request/params.
//
// Allowlist:
//   - *_test.go : tests may mutate inputs to verify behavior.
//   - internal/app/** : composition-root wiring may transform inputs.
//
// Pattern anchors:
//   \*(req|input|request|params)\s*=              — whole-struct reassignment
//   (req|input|request|params)\.[A-Za-z_]+\s*=    — field assignment
package scan

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// inputImmutabilityPatterns are the forbidden mutation patterns.
// The first matches whole-struct reassignment; the second matches
// field assignment on a parameter named req/input/request/params.
var inputImmutabilityPatterns = []struct {
	re   *regexp.Regexp
	desc string
}{
	{regexp.MustCompile(`\*(?i)\b(req|input|request|params)\b\s*=`),
		"whole input struct reassignment"},
	{regexp.MustCompile(`(?i)\b(req|input|request|params)\b\.[A-Za-z_][A-Za-z0-9_]*\s*=`),
		"input struct field assignment"},
}

// ScanInputImmutability walks <root>/internal/application/** and
// <root>/internal/api/** for non-test .go files and flags mutations
// of input parameters.
func ScanInputImmutability(root string, pol *policy.Policy, r *report.Report) {
	skipDirs := map[string]bool{
		".git": true, "vendor": true, "node_modules": true,
		"node-scraper": true, "examples": true, "scripts": true,
	}

	for _, subdir := range []string{"internal/application", "internal/api"} {
		dir := filepath.Join(root, subdir)
		_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
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
			rel, _ := filepath.Rel(root, path)
			relSlash := filepath.ToSlash(rel)
			scanInputImmutabilityFile(root, path, relSlash, r)
			return nil
		})
	}
}

func scanInputImmutabilityFile(root, path, relPath string, r *report.Report) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	lineNum := 0
	for sc.Scan() {
		lineNum++
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		for _, p := range inputImmutabilityPatterns {
			if !p.re.MatchString(line) {
				continue
			}
			r.Violations = append(r.Violations, report.Violation{
				File:        relPath,
				Line:        lineNum,
				Rule:        "percheck_input_immutability",
				Severity:    string(report.SeverityWarn),
				MatchedRule: "input_struct_mutation",
				Note:        "input parameter mutation detected: " + p.desc + " — treat input structs as read-only; return a new value or use a dedicated output type instead",
			})
		}
	}
}
