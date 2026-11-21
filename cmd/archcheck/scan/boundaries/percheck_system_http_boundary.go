package boundaries

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

const systemHTTPBoundaryRule = "percheck_system_http_boundary"

// systemHTTPBoundaryForbidden is the import the capability must not own. The
// pair capabilities/system ↔ platform/httpserver was bidirectional until RA-11:
// Module.Build imported platform/httpserver for BuildContext/RuntimeModule while
// the router imported capabilities/system/health. The runtime descriptor now
// belongs to the composition root (StaticCapabilityModule), so this direction
// stays at zero and the ratchet is empty on purpose.
const systemHTTPBoundaryForbidden = "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"

// ScanSystemHTTPBoundary enforces that internal/capabilities/system (including
// its subpackages) does not import platform/httpserver. Test files are excluded:
// test doubles may exercise router behaviour, and test code never ships.
func ScanSystemHTTPBoundary(root string, _ *policy.Policy, r *report.Report) {
	systemRoot := filepath.Join(root, "internal", "capabilities", "system")
	_ = filepath.WalkDir(systemRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			if policy.StandardSkipDirs[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		inspectSystemHTTPBoundaryFile(root, path, r)
		return nil
	})
}

func inspectSystemHTTPBoundaryFile(root, absPath string, r *report.Report) {
	relPath, err := filepath.Rel(root, absPath)
	if err != nil {
		relPath = absPath
	}
	relPath = filepath.ToSlash(relPath)
	if strings.HasPrefix(relPath, policy.ScannerSourcePrefix) {
		return
	}
	fileSet := token.NewFileSet()
	parsed, parseErr := parser.ParseFile(fileSet, absPath, nil, parser.ImportsOnly)
	if parseErr != nil {
		r.Warnings = append(r.Warnings, "system-http-boundary parse "+relPath+": "+parseErr.Error())
		return
	}
	for _, imp := range parsed.Imports {
		importPath := strings.Trim(strings.TrimSpace(imp.Path.Value), `"`)
		if importPath != systemHTTPBoundaryForbidden && !strings.HasPrefix(importPath, systemHTTPBoundaryForbidden+"/") {
			continue
		}
		r.Violations = append(r.Violations, report.Violation{
			File: relPath, Line: fileSet.Position(imp.Pos()).Line,
			Rule: systemHTTPBoundaryRule, MatchedRule: "system_http_import",
			Severity: string(report.SeverityError),
			Note: "internal/capabilities/system must not import platform/httpserver: the runtime descriptor belongs to the composition root " +
				"(StaticCapabilityModule), and the reverse direction (httpserver → system/health) is the only one the architecture allows (RA-11).",
		})
	}
}
