// Package boundaries contains current-tree architecture boundary checks.
package boundaries

import (
	"os"
	"path/filepath"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// ScanAPIInfrastructureImports retains the historical entry point used by
// cmd/archcheck, but now enforces the current four-root topology. The old
// API/infrastructure allowlist was tied to deleted directories and is no
// longer a valid source of policy.
func ScanAPIInfrastructureImports(root string, _ *policy.Policy, r *report.Report) {
	for _, retiredRoot := range []string{
		"internal/api",
		"internal/application",
		"internal/domain",
		"internal/infrastructure",
	} {
		path := filepath.Join(root, retiredRoot)
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			appendLegacyRootViolation(r, retiredRoot, "cannot inspect retired root: "+err.Error())
			continue
		}
		appendLegacyRootViolation(r, retiredRoot, "retired architecture root reintroduced")
	}
}

func appendLegacyRootViolation(r *report.Report, root, note string) {
	r.Violations = append(r.Violations, report.Violation{
		File:        root,
		MatchedRule: "retired_internal_root",
		Rule:        "percheck_legacy_root_ban",
		Severity:    string(report.SeverityError),
		Note:        note + "; use internal/app, internal/kernel, internal/capabilities, or internal/platform",
	})
}
