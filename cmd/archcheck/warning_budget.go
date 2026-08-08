package main

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

const warningBudgetRule = "warning_budget"

// enforceWarningBudget converts warning-budget growth into a normal
// violation. Existing warnings remain visible in Report.Warnings; only
// the excess is emitted so the report identifies the exact ratchet breach.
func enforceWarningBudget(pol *policy.Policy, r *report.Report) {
	if len(r.Warnings) <= pol.MaxWarnings {
		return
	}
	excess := len(r.Warnings) - pol.MaxWarnings
	r.Violations = append(r.Violations, report.Violation{
		Rule:         warningBudgetRule,
		MatchedRule:  warningBudgetRule,
		Severity:     string(report.SeverityError),
		ActualCount:  len(r.Warnings),
		AllowedCount: pol.MaxWarnings,
		Note:         fmt.Sprintf("warning budget exceeded by %d; reduce warning residue before increasing the committed budget", excess),
	})
}
