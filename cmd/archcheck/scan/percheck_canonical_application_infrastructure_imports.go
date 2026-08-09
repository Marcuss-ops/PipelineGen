// Package scan — DoD gate for canonical application boundaries.
package scan

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

const (
	canonicalApplicationInfraImportPrefix = "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/"
	canonicalApplicationInfraImportRule   = "percheck_canonical_application_infrastructure_imports"
	canonicalApplicationMissingAreaRule   = "percheck_canonical_application_infrastructure_imports_missing_area"
	canonicalApplicationParseErrorRule    = "percheck_canonical_application_infrastructure_imports_parse_error"
)

// ScanCanonicalApplicationInfrastructureImports enforces the zero-baseline
// application boundary for the paths explicitly declared in policy. It uses
// Go's parser rather than text matching, so comments and string literals cannot
// create false positives. Test files are excluded because this DoD contract
// governs production application dependencies.
func ScanCanonicalApplicationInfrastructureImports(root string, pol *policy.Policy, r *report.Report) {
	if len(pol.CanonicalApplicationAreas) == 0 && canonicalApplicationGateEnabled(pol) {
		_ = appendCanonicalAreaViolation(r, "architecture/policy.yaml", "canonical_application_areas is empty or missing while the canonical application boundary hard gate is enabled")
		return
	}
	for _, area := range pol.CanonicalApplicationAreas {
		if err := scanCanonicalApplicationArea(root, area, r); err != nil {
			// scanCanonicalApplicationArea records a fail-closed violation for
			// every actionable error. Keep this guard so a future filesystem
			// error cannot make the check silently pass.
			_ = err
		}
	}
}

func scanCanonicalApplicationArea(root, area string, r *report.Report) error {
	if !isCanonicalApplicationAreaPath(area) {
		return appendCanonicalAreaViolation(r, area, "area must be a normalized relative path under internal/application")
	}

	areaPath := filepath.Join(root, filepath.FromSlash(area))
	info, err := os.Stat(areaPath)
	if err != nil {
		return appendCanonicalAreaViolation(r, area, "declared canonical application area is missing or unreadable: "+err.Error())
	}
	if !info.IsDir() {
		return appendCanonicalAreaViolation(r, area, "declared canonical application area is not a directory")
	}

	walkErr := filepath.WalkDir(areaPath, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "vendor" || entry.Name() == "testdata" || entry.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		return scanCanonicalApplicationGoFile(root, path, r)
	})
	if walkErr != nil {
		return appendCanonicalAreaViolation(r, area, "unable to scan declared canonical application area: "+walkErr.Error())
	}
	return nil
}

func scanCanonicalApplicationGoFile(root, path string, r *report.Report) error {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
	if err != nil {
		rel, _ := filepath.Rel(root, path)
		r.Violations = append(r.Violations, report.Violation{
			File:        filepath.ToSlash(rel),
			MatchedRule: canonicalApplicationParseErrorRule,
			Rule:        canonicalApplicationParseErrorRule,
			Severity:    string(report.SeverityError),
			Note:        "fail-closed: production Go file in a canonical application area could not be parsed: " + err.Error(),
		})
		return nil
	}

	rel, _ := filepath.Rel(root, path)
	rel = filepath.ToSlash(rel)
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil || !strings.HasPrefix(importPath, canonicalApplicationInfraImportPrefix) {
			continue
		}
		position := fset.Position(spec.Pos())
		r.Violations = append(r.Violations, report.Violation{
			File:        rel,
			Line:        position.Line,
			MatchedRule: "application_infrastructure_import",
			Rule:        canonicalApplicationInfraImportRule,
			Severity:    string(report.SeverityError),
			Note:        "canonical application area imports infrastructure directly (" + importPath + "); define an application-owned port and wire the infrastructure adapter from internal/app",
		})
	}
	return nil
}

func appendCanonicalAreaViolation(r *report.Report, area, note string) error {
	r.Violations = append(r.Violations, report.Violation{
		File:        filepath.ToSlash(area),
		MatchedRule: "canonical_application_area",
		Rule:        canonicalApplicationMissingAreaRule,
		Severity:    string(report.SeverityError),
		Note:        "fail-closed: " + note,
	})
	return nil
}

func canonicalApplicationGateEnabled(pol *policy.Policy) bool {
	for _, gate := range pol.HardGates {
		if gate == canonicalApplicationInfraImportRule || gate == canonicalApplicationMissingAreaRule {
			return true
		}
	}
	return false
}

func isCanonicalApplicationAreaPath(area string) bool {
	if area == "" || filepath.IsAbs(area) || filepath.ToSlash(filepath.Clean(area)) != area {
		return false
	}
	return area == "internal/application" || strings.HasPrefix(area, "internal/application/")
}
