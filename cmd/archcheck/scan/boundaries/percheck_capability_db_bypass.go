package boundaries

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

const capabilityDBBypassRule = "percheck_capability_db_bypass"

var capabilityDBBypassRe = regexp.MustCompile(`\bDB\(\)\s*\*sql\.DB`)

// ScanCapabilityDBBypass bans the anonymous DB() escape hatch in the
// composition root and in capabilities. The canonical media writers own the
// engine; an app/capability that reaches for *sql.DB has bypassed the typed
// port and can drift onto a second engine. Platform adapters are allowed to
// own the handle — that is the implementation side of the port.
func ScanCapabilityDBBypass(root string, _ *policy.Policy, r *report.Report) {
	for _, scanRoot := range []string{"internal/app", "internal/capabilities"} {
		absRoot := filepath.Join(root, scanRoot)
		if err := filepath.Walk(absRoot, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if info.IsDir() {
				if policy.StandardSkipDirs[filepath.Base(path)] {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			inspectCapabilityDBBypassFile(root, path, r)
			return nil
		}); err != nil {
			r.Warnings = append(r.Warnings, "capability-db-bypass scan "+absRoot+": "+err.Error())
		}
	}
}

func inspectCapabilityDBBypassFile(root, absPath string, r *report.Report) {
	relPath, err := filepath.Rel(root, absPath)
	if err != nil {
		relPath = absPath
	}
	relPath = filepath.ToSlash(relPath)
	if strings.HasPrefix(relPath, policy.ScannerSourcePrefix) {
		return
	}
	source, err := os.ReadFile(absPath)
	if err != nil {
		r.Warnings = append(r.Warnings, "capability-db-bypass read "+relPath+": "+err.Error())
		return
	}
	masked := source
	fileSet := token.NewFileSet()
	parsed, parseErr := parser.ParseFile(fileSet, absPath, source, parser.ParseComments)
	if parseErr == nil && parsed != nil {
		masked = maskGoComments(source, fileSet, parsed)
	}
	maskedStr := string(masked)
	for _, match := range capabilityDBBypassRe.FindAllStringIndex(maskedStr, -1) {
		line := 1 + strings.Count(maskedStr[:match[0]], "\n")
		r.Violations = append(r.Violations, report.Violation{
			File:        relPath,
			Line:        line,
			Rule:        capabilityDBBypassRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "db_bypass",
			Note: "internal/app and internal/capabilities must use a typed engine port instead of DB() *sql.DB; " +
				"the media SSOT handle belongs to platform/postgres/media (see RA-14).",
		})
	}
}
