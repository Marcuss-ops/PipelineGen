package main

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func TestEnforceWarningBudgetAllowsAtOrBelowCeiling(t *testing.T) {
	for _, count := range []int{0, 2, 3} {
		r := &report.Report{Warnings: make([]string, count)}
		enforceWarningBudget(&policy.Policy{MaxWarnings: 3}, r)
		if len(r.Violations) != 0 {
			t.Fatalf("warning count %d unexpectedly violated budget: %#v", count, r.Violations)
		}
	}
}

func TestEnforceWarningBudgetReportsGrowth(t *testing.T) {
	r := &report.Report{Warnings: []string{"a", "b", "c", "d"}}
	enforceWarningBudget(&policy.Policy{MaxWarnings: 3}, r)
	if len(r.Violations) != 1 {
		t.Fatalf("violations=%d, want 1", len(r.Violations))
	}
	got := r.Violations[0]
	if got.Rule != warningBudgetRule || got.MatchedRule != warningBudgetRule {
		t.Fatalf("unexpected rule identity: %#v", got)
	}
	if got.ActualCount != 4 || got.AllowedCount != 3 {
		t.Fatalf("counts=%d/%d, want 4/3", got.ActualCount, got.AllowedCount)
	}
	if got.Severity != string(report.SeverityError) {
		t.Fatalf("severity=%q, want error", got.Severity)
	}
}

func TestEnforceWarningBudgetAllowsZeroCeiling(t *testing.T) {
	r := &report.Report{Warnings: []string{"residue"}}
	enforceWarningBudget(&policy.Policy{MaxWarnings: 0}, r)
	if len(r.Violations) != 1 || r.Violations[0].AllowedCount != 0 {
		t.Fatalf("zero budget did not fail closed: %#v", r.Violations)
	}
}

func TestCanonicalPolicyWarningBudget(t *testing.T) {
	p, err := policy.Load("../../architecture/policy.yaml")
	if err != nil {
		t.Fatalf("load canonical policy: %v", err)
	}
	if p.MaxWarnings != 77 {
		t.Fatalf("MaxWarnings=%d, want committed budget 77", p.MaxWarnings)
	}
}
