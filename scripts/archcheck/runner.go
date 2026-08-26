// Package main — archcheck CheckList orchestration.
//
// runner.go owns the high-level gate orchestration. main() (in
// main.go) parses CLI flags, then dispatches to one of:
//
//   - runFocusedChecks: the "report-only" gate (always exits 0
//     unless the smaller focused surface breaks).
//   - runRatchetChecks: the full ratchet gate (allowlist + baseline
//   - database/sql regressions + ...).
//   - runPhase0Checks: the Wave 19 PR-A baseline-on-baseline rules,
//     run only when --future-ratchet is set. Reads the committed
//     scripts/archcheck/phase0_baseline.json (via baseline/load.go)
//     and diffs against the 5 check_*.go functions in main.go.
//
// The check_*.go functions themselves stay in main.go for PR2 —
// they migrate to checks/ in PR3+PR4. The split here is purely
// orchestration: this file knows the order, the keys, and the
// shape of the report; the check files know how to look at one
// slice of the codebase and report findings.
package main

import (
	"fmt"
	"os"

	bl "github.com/Marcuss-ops/PipelineGen/scripts/archcheck/baseline"
)

// runFocusedChecks runs the focused current-tree architecture checks.
// Legacy root policy is enforced structurally; no retired allowlist is loaded.
func runFocusedChecks() Report {
	checks := map[string]int{}
	violations := []string{}

	yamlVerifiedOK, yamlVerifiedTotal, yamlViolations := checkMigrationYAML()
	checks["migration_yaml_done_waves_total"] = yamlVerifiedTotal
	checks["migration_yaml_done_waves_with_verified_zero_true"] = yamlVerifiedOK
	violations = append(violations, yamlViolations...)

	ownershipMissing, ownershipViolations := checkOwnershipYAML()
	checks["ownership_yaml_missing_paths"] = ownershipMissing
	violations = append(violations, ownershipViolations...)

	pythonWriterViolations, pythonWriterFindings := checkPythonLegacyWriterGate()
	checks["python_legacy_writer_violations"] = pythonWriterViolations
	violations = append(violations, pythonWriterFindings...)

	depStats, depViolations := checkDeprecations()
	for k, v := range depStats {
		checks[k] = v
	}
	violations = append(violations, depViolations...)

	retiredStats, retiredViolations := checkRetiredRootImports("retired_roots", retiredInternalRoots[:])
	checks["retired_root_violations"] = retiredStats["violations"]
	violations = append(violations, retiredViolations...)

	cciStats, cciViolations := checkCrossCapabilityImport()
	checks["cross_capability_import_pairs"] = cciStats["actual"]
	violations = append(violations, cciViolations...)

	return Report{
		Passed:            len(violations) == 0,
		FocusedGatePassed: len(violations) == 0,
		Mode:              "focused",
		Commit:            "ci/archcheck-hard-fail",
		Checks:            checks,
		Violations:        violations,
	}
}

// runRatchetChecks runs the full current-tree ratchet gate, including
// database/sql ownership and retired-root reintroduction checks.
func runRatchetChecks() Report {
	checks := map[string]int{}
	violations := []string{}

	sqlStats, sqlViolations := checkDatabaseSQLGate()
	checks["database_sql_actual"] = sqlStats["actual"]
	checks["database_sql_regressions"] = sqlStats["regressions"]
	violations = append(violations, sqlViolations...)

	yamlVerifiedOK, yamlVerifiedTotal, yamlViolations := checkMigrationYAML()
	checks["migration_yaml_done_waves_total"] = yamlVerifiedTotal
	checks["migration_yaml_done_waves_with_verified_zero_true"] = yamlVerifiedOK
	violations = append(violations, yamlViolations...)

	ownershipMissing, ownershipViolations := checkOwnershipYAML()
	checks["ownership_yaml_missing_paths"] = ownershipMissing
	violations = append(violations, ownershipViolations...)

	pythonWriterViolations, pythonWriterFindings := checkPythonLegacyWriterGate()
	checks["python_legacy_writer_violations"] = pythonWriterViolations
	violations = append(violations, pythonWriterFindings...)

	depStats, depViolations := checkDeprecations()
	for k, v := range depStats {
		checks[k] = v
	}
	violations = append(violations, depViolations...)

	retiredStats, retiredViolations := checkRetiredRootImports("retired_roots", retiredInternalRoots[:])
	checks["retired_root_violations"] = retiredStats["violations"]
	violations = append(violations, retiredViolations...)

	cciStats, cciViolations := checkCrossCapabilityImport()
	checks["cross_capability_import_pairs"] = cciStats["actual"]
	violations = append(violations, cciViolations...)

	return Report{
		Passed:            len(violations) == 0,
		FocusedGatePassed: len(violations) == 0,
		Mode:              "ratchet",
		Commit:            "ci/archcheck-hard-fail",
		LegacyBudget:      0,
		Checks:            checks,
		Violations:        violations,
	}
}

// runPhase0Checks runs the 5 Phase 0 rules and compares against
// the committed baseline (loaded by baseline/load.go). It returns
// stats and the violation list (which during the minor cycle
// contains only REGRESSIONS — new entries vs the committed
// baseline, NOT existing entries). If the baseline file is
// missing, the function returns a hard-error stack instructing the
// operator to run --seed-baseline (no auto-seed: the tool refuses
// to silently write a possibly-poisoned baseline if rg is missing
// or the environment is non-representative).
//
// The "phase 0 report-only guard" mentioned in the FASE 1.B spec
// refers to this function: it is the bridge between the focused/
// ratchet gates and the Wave 19 PR-A future-ratchet extension. It
// owns the missing-baseline guard and the regression-only reporting
// semantics; it does NOT own the check logic (which stays in
// main.go for PR2 and migrates to checks/ in PR3+PR4).
func runPhase0Checks() (map[string]int, []string) {
	checks := map[string]int{}
	violations := []string{}

	// Hard-error guard: the committed baseline file MUST exist
	// before --future-ratchet can run. PR-A deliberately does NOT
	// auto-seed because rg availability varies across environments
	// — auto-seeding a baseline that was computed against missing/
	// broken rg would silently turn future CI runs red once rg IS
	// available and the actual set pops above the poisoned
	// bl. The operator path is explicit: `go run
	// ./scripts/archcheck --seed-baseline` writes a fresh baseline,
	// then `git add` + commit. The hard-error below is ONE
	// multi-line string (consumer-agnostic; CI dashboards that
	// re-format, sort, or dedup violations will not lose leading
	// whitespace fragments).
	if _, err := os.Stat(bl.File); os.IsNotExist(err) {
		return checks, []string{
			"phase0_baseline.json missing — PR-A bootstrap incomplete\n" +
				"  fix: go run ./scripts/archcheck --seed-baseline && git add scripts/archcheck/phase0_baseline.json && git commit\n" +
				"  ref: scripts/archcheck/baseline/load.go::File + scripts/ci-architectural-checks.sh header comment",
		}
	}

	// Standard ratchet compare: actual vs baseline, fail on
	// regressions.
	b, err := bl.Load()
	if err != nil {
		return checks, []string{fmt.Sprintf("phase0: load baseline: %v", err)}
	}

	_, ibViolations, ibStats := checkInterfaceBraceGrowth(b.InterfaceBraces)
	for k, v := range ibStats {
		checks[k] = v
	}
	violations = append(violations, ibViolations...)

	_, sdViolations, sdStats := checkSetterDetector(b.Setters)
	for k, v := range sdStats {
		checks[k] = v
	}
	violations = append(violations, sdViolations...)

	_, taViolations, taStats := checkTypeAliasCrossPkg(b.TypeAliasesCrossPkg)
	for k, v := range taStats {
		checks[k] = v
	}
	violations = append(violations, taViolations...)

	_, frViolations, frStats := checkFakeRoute(b.FakeRoutes)
	for k, v := range frStats {
		checks[k] = v
	}
	violations = append(violations, frViolations...)

	_, hdViolations, hdStats := checkHandlerToDB(b.HandlersToDB)
	for k, v := range hdStats {
		checks[k] = v
	}
	violations = append(violations, hdViolations...)
	return checks, violations
}
