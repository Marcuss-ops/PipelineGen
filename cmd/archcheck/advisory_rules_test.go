package main

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func TestAdvisoryRulesDoNotBlockStrictExit(t *testing.T) {
	pol := &policy.Policy{AdvisoryRules: []string{
		"max_files_per_package", "max_lines_per_file", "max_lines_per_file_strict",
	}}
	r := &report.Report{Violations: []report.Violation{
		{Rule: "max_lines_per_file_strict", Severity: "warn"},
		{Rule: "max_files_per_package", Severity: "warn"},
		{Rule: "percheck_kernel_boundary", Severity: "warn"},
	}}
	if got := blockingViolationCount(r, pol); got != 1 {
		t.Fatalf("blocking violations = %d, want only the correctness rule", got)
	}

	advisoryOnly := &report.Report{Violations: []report.Violation{
		{Rule: "max_lines_per_file_strict", Severity: "warn"},
	}}
	if got := blockingViolationCount(advisoryOnly, pol); got != 0 {
		t.Fatalf("advisory-only blocking violations = %d, want 0", got)
	}
}

func TestAdvisoryRulesDoNotCoverCorrectnessGates(t *testing.T) {
	pol := &policy.Policy{AdvisoryRules: []string{"max_lines_per_file_strict"}}
	for _, rule := range []string{
		"percheck_kernel_boundary",
		"percheck_legacy_root_ban",
		"max_lines_per_file",
	} {
		r := &report.Report{Violations: []report.Violation{{Rule: rule}}}
		if got := blockingViolationCount(r, pol); got != 1 {
			t.Fatalf("rule %q was treated as advisory: blocking=%d, want 1", rule, got)
		}
	}
}
