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

const wiringFinalJobExecRule = "percheck_wiring_finaljob_process_exec"

// wiringFinalJobProcessExecForbidden is the import RA-15 removed from the
// composition root. The final-job pipeline now lives in
// internal/platform/media/finaljob; app/wiring may construct it but must never
// spawn ffmpeg/ffprobe itself again.
const wiringFinalJobProcessExecForbidden = "os/exec"

// ScanWiringFinalJobProcessExec enforces that the ROOT of internal/app/wiring
// does not import os/exec. The two remaining subpackages (lifecycle and
// rendering) are separate families with their own owners; they are deliberately
// outside this ratchet so it can stay empty instead of allowlisting the files
// this ticket just moved. Test files are excluded: they may drive ffmpeg to
// build fixtures, and test code never ships.
func ScanWiringFinalJobProcessExec(root string, _ *policy.Policy, r *report.Report) {
	wiringRoot := filepath.Join(root, "internal", "app", "wiring")
	entries, err := os.ReadDir(wiringRoot)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(wiringRoot, entry.Name())
		inspectWiringFinalJobProcessExec(root, path, r)
	}
}

func inspectWiringFinalJobProcessExec(root, absPath string, r *report.Report) {
	relPath, err := filepath.Rel(root, absPath)
	if err != nil {
		relPath = absPath
	}
	relPath = filepath.ToSlash(relPath)
	fileSet := token.NewFileSet()
	parsed, parseErr := parser.ParseFile(fileSet, absPath, nil, parser.ImportsOnly)
	if parseErr != nil {
		r.Warnings = append(r.Warnings, "wiring-finaljob-exec parse "+relPath+": "+parseErr.Error())
		return
	}
	for _, imp := range parsed.Imports {
		importPath := strings.Trim(strings.TrimSpace(imp.Path.Value), `"`)
		if importPath != wiringFinalJobProcessExecForbidden {
			continue
		}
		r.Violations = append(r.Violations, report.Violation{
			File: relPath, Line: fileSet.Position(imp.Pos()).Line,
			Rule: wiringFinalJobExecRule, MatchedRule: "wiring_finaljob_process_exec",
			Severity: string(report.SeverityError),
			Note: "internal/app/wiring must remain a composition root: the final-job ffmpeg/ffprobe lane lives in " +
				"internal/platform/media/finaljob and is injected through its port (RA-15).",
		})
	}
}
