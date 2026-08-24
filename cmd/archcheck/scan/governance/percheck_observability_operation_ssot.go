package governance

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

const observabilityOperationSSOTRule = "percheck_observability_operation_ssot"

// ScanObservabilityOperationSSOT prevents the two most dangerous forms of
// regression: reintroducing the retired measurement-recorder contract or
// writing the performance read model from a second package. It deliberately
// does not ban infrastructure/health timers; those are outside run metrics.
func ScanObservabilityOperationSSOT(root string, _ *policy.Policy, r *report.Report) {
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || strings.HasPrefix(filepath.ToSlash(rel), "cmd/archcheck/scan/") {
			return nil
		}
		canonicalStore := strings.HasPrefix(filepath.ToSlash(rel), "internal/platform/sqlite/performance/")
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		scanner := bufio.NewScanner(f)
		lineNo := 0
		for scanner.Scan() {
			lineNo++
			line := strings.TrimSpace(scanner.Text())
			if strings.HasPrefix(line, "//") {
				continue
			}
			if strings.Contains(line, "MeasuredOperationRecorder") {
				appendObservabilitySSOTViolation(r, rel, lineNo, "retired MeasuredOperationRecorder; promote to OperationReport and use OperationReportProjectionRecorder")
			}
			if !canonicalStore && strings.Contains(strings.ToLower(line), "insert into performance_operations") {
				appendObservabilitySSOTViolation(r, rel, lineNo, "performance_operations is a read model; write it only through the canonical projection store")
			}
		}
		return nil
	})
}

func appendObservabilitySSOTViolation(r *report.Report, rel string, line int, note string) {
	r.Violations = append(r.Violations, report.Violation{
		File: rel, Line: line, Rule: observabilityOperationSSOTRule,
		Severity: string(report.SeverityError), MatchedRule: "observability_operation_single_writer", Note: note,
	})
}
