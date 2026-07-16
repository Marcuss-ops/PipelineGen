// Package scan — Check 76: input immutability (Wave 5, July 2026).
//
// scan/percheck_inputimmutability.go owns the forward-prevention gate
// for input immutability. Use-case input structs (named *Input or
// *Request) should be treated as read-only after they are passed to a
// function. Mutating an input struct in-place makes the code harder to
// reason about and breaks the caller's expectation of the input value.
//
// Dual-mode scanner:
//   - productionOnly=false (legacy): census all existing mutations as warnings
//   - productionOnly=true: only flag mutations introduced by the current git diff
//
// Allowlist:
//   - *_test.go : tests may mutate inputs to verify behavior.
//   - internal/app/** : composition-root wiring may transform inputs.
//
// Pattern anchors:
//
//	\*(req|input|request|params)\s*=              — whole-struct reassignment
//	(req|input|request|params)\.[A-Za-z_]+\s*=    — field assignment
package scan

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
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

const inputImmutabilityRule = "percheck_input_immutability"
const inputImmutabilityCensusID = "input_immutability_census"

// ScanInputImmutability walks <root>/internal/application/** and
// <root>/internal/api/** for non-test .go files and flags mutations
// of input parameters.
//
// productionOnly=false: legacy census mode — all existing mutations
// are counted and emitted as r.Warnings (informational census).
// productionOnly=true: forward-prevention mode — only mutations
// introduced by the current git diff are violations (SeverityError).
func ScanInputImmutability(root string, pol *policy.Policy, r *report.Report, productionOnly bool) {
	skipDirs := map[string]bool{
		".git": true, "vendor": true, "node_modules": true,
		"node-scraper": true, "examples": true, "scripts": true,
	}

	if !productionOnly {
		totalHits := 0
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
				totalHits += countInputMutations(path)
				return nil
			})
		}
		if totalHits > 0 {
			r.Warnings = append(r.Warnings, fmt.Sprintf(
				"%s current_legacy_mutations=%d (informational census; new mutations are errors in production-only mode)",
				inputImmutabilityCensusID,
				totalHits,
			))
		}
		return
	}

	// Production-only mode: only flag mutations introduced by git diff.
	hits, diffErr := collectAddedInputMutations(root, skipDirs)
	if diffErr != nil {
		r.Warnings = append(r.Warnings, inputImmutabilityCensusID+":diff_unavailable: "+diffErr.Error())
	}
	for _, hit := range hits {
		r.Violations = append(r.Violations, report.Violation{
			File:        hit.File,
			Line:        hit.Line,
			Rule:        inputImmutabilityRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "input_struct_mutation:new_import",
			Note:        "input parameter mutation detected: " + hit.Desc + " — treat input structs as read-only; return a new value or use a dedicated output type instead",
		})
	}
}

type inputMutationHit struct {
	File string
	Line int
	Desc string
}

func countInputMutations(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	count := 0
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		for _, p := range inputImmutabilityPatterns {
			if p.re.MatchString(line) {
				count++
				break
			}
		}
	}
	return count
}

func collectAddedInputMutations(root string, skipDirs map[string]bool) ([]inputMutationHit, error) {
	commands := [][]string{
		{"show", "--format=", "--unified=0", "--no-ext-diff", "HEAD", "--", "*.go"},
		{"diff", "--unified=0", "--no-ext-diff", "--", "*.go"},
		{"diff", "--cached", "--unified=0", "--no-ext-diff", "--", "*.go"},
	}
	seen := map[string]bool{}
	var hits []inputMutationHit
	var commandErrors []string
	for _, args := range commands {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		out, err := cmd.Output()
		if err != nil {
			commandErrors = append(commandErrors, strings.Join(args, " ")+": "+err.Error())
			continue
		}
		for _, hit := range parseAddedInputMutations(string(out)) {
			key := fmt.Sprintf("%s:%d", hit.File, hit.Line)
			if seen[key] {
				continue
			}
			seen[key] = true
			// Only count hits in scanned directories.
			if !strings.HasPrefix(hit.File, "internal/application/") && !strings.HasPrefix(hit.File, "internal/api/") {
				continue
			}
			hits = append(hits, hit)
		}
	}
	if len(commandErrors) == len(commands) {
		return hits, fmt.Errorf("git diff unavailable: %s", strings.Join(commandErrors, "; "))
	}
	return hits, nil
}

func parseAddedInputMutations(diff string) []inputMutationHit {
	var hits []inputMutationHit
	currentFile := ""
	newLine := 0
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++ b/"):
			currentFile = strings.TrimPrefix(line, "+++ b/")
		case strings.HasPrefix(line, "@@"):
			newLine = parseInputDiffNewLineStart(line)
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			text := strings.TrimPrefix(line, "+")
			trimmed := strings.TrimSpace(text)
			if currentFile != "" && !strings.HasSuffix(currentFile, "_test.go") && !strings.HasPrefix(trimmed, "//") {
				for _, p := range inputImmutabilityPatterns {
					if p.re.MatchString(text) {
						hits = append(hits, inputMutationHit{File: currentFile, Line: newLine, Desc: p.desc})
						break
					}
				}
			}
			newLine++
		case strings.HasPrefix(line, "-"):
			// Removed lines do not advance the new-file line number.
		default:
			if currentFile != "" && newLine > 0 {
				newLine++
			}
		}
	}
	return hits
}

func parseInputDiffNewLineStart(line string) int {
	plus := strings.Index(line, "+")
	if plus < 0 {
		return 0
	}
	rest := line[plus+1:]
	end := strings.IndexAny(rest, ", ")
	if end >= 0 {
		rest = rest[:end]
	}
	n, _ := strconv.Atoi(rest)
	return n
}
