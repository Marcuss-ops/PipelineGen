package scan

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

const domainJobBaselineRule = "percheck_domain_job_import_baseline"

// ScanDomainJobImportBaseline prevents the productive compatibility-import
// census from ever rising above the machine-owned migration baseline. The
// existing per-check still identifies newly added lines; this aggregate gate
// also catches out-of-band drift and an incorrectly increased registry count.
func ScanDomainJobBaselineRatchet(root string, _ *policy.Policy, r *report.Report, productionOnly bool) {
	if !productionOnly {
		return
	}
	migration, err := loadDomainJobMigration(root)
	if err != nil {
		// The canonical compatibility scanner owns registry parse diagnostics.
		return
	}

	current := len(collectDomainJobImports(root, migration.CompatibilityImport))
	if current > migration.ReportedBaselineImports {
		r.Violations = append(r.Violations, report.Violation{
			File:         domainJobMigrationPath,
			Rule:         domainJobBaselineRule,
			MatchedRule:  "domain_job_import_baseline_exceeded",
			Severity:     string(report.SeverityError),
			ActualCount:  current,
			AllowedCount: migration.ReportedBaselineImports,
			Note: fmt.Sprintf(
				"productive imports of %s increased to %d above the registered baseline %d; migrate the new sites to %s instead of raising the baseline",
				migration.CompatibilityImport,
				current,
				migration.ReportedBaselineImports,
				migration.CanonicalImport,
			),
		})
	}
}
