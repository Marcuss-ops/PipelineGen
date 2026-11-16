// Package main contains archcheck orchestration.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

const (
	ExitOK          = 0
	ExitViolations  = 1
	ExitLoadOrParse = 2
)

// Run orchestrates a single archcheck invocation.
func Run(ctx context.Context, root, policyPath, phase string, strict bool, productionOnly bool) (int, error) {
	_ = ctx

	pol, err := policy.Load(policyPath)
	if err != nil {
		return ExitLoadOrParse, fmt.Errorf("load policy %q: %w", policyPath, err)
	}

	r := &report.Report{
		Mode:       "target-tree-dry-run",
		PolicyPath: policyPath,
		Root:       root,
		Phase:      phase,
		Policy:     pol,
		Summary:    report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}

	for _, check := range DefaultChecks(productionOnly) {
		check.Run(root, pol, r)
	}
	enforceWarningBudget(pol, r)

	r.Summary.TotalViolations = len(r.Violations)
	for _, v := range r.Violations {
		r.Summary.ByReason[v.Rule]++
		r.Summary.BySeverity[v.Severity]++
	}
	r.Passed = len(r.Violations) == 0

	hasHardGate := false
	if len(pol.HardGates) > 0 {
		hgSet := make(map[string]bool, len(pol.HardGates))
		for _, id := range pol.HardGates {
			hgSet[id] = true
		}
		for i := range r.Violations {
			if hgSet[r.Violations[i].Rule] {
				r.Violations[i].Severity = string(report.SeverityError)
				hasHardGate = true
			}
		}
		if hasHardGate {
			r.Summary.BySeverity = map[string]int{}
			for _, v := range r.Violations {
				r.Summary.BySeverity[v.Severity]++
			}
		}
	}
	r.HasHardGateHits = hasHardGate

	sort.SliceStable(r.Violations, func(i, j int) bool {
		a, b := r.Violations[i], r.Violations[j]
		if a.Package != b.Package {
			return a.Package < b.Package
		}
		if a.Directory != b.Directory {
			return a.Directory < b.Directory
		}
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Rule != b.Rule {
			return a.Rule < b.Rule
		}
		if a.MatchedRule != b.MatchedRule {
			return a.MatchedRule < b.MatchedRule
		}
		if a.Severity != b.Severity {
			return a.Severity < b.Severity
		}
		return a.Note < b.Note
	})
	// Warnings are appended by multiple scans; sort them to keep
	// the JSON report deterministic across runs (TestReportContract).
	sort.Strings(r.Warnings)

	out, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return ExitLoadOrParse, fmt.Errorf("marshal report: %w", err)
	}
	fmt.Println(string(out))

	if (strict && blockingViolationCount(r, pol) > 0) || hasHardGate {
		return ExitViolations, nil
	}
	return ExitOK, nil
}

// blockingViolationCount excludes advisory rule ids from the strict exit
// decision. The violations still appear in the report (and in the summary),
// so a size-cap warning stays visible; it just cannot force a split that
// harms cohesion. Correctness rules are never advisory.
func blockingViolationCount(r *report.Report, pol *policy.Policy) int {
	if r == nil {
		return 0
	}
	advisory := make(map[string]struct{}, len(pol.AdvisoryRules))
	for _, id := range pol.AdvisoryRules {
		advisory[id] = struct{}{}
	}
	count := 0
	for _, v := range r.Violations {
		if _, skip := advisory[v.Rule]; skip {
			continue
		}
		count++
	}
	return count
}
