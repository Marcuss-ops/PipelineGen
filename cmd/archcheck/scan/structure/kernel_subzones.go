package structure

import (
	"os"
	"path/filepath"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

const (
	kernelSubzoneUndeclaredRule = "kernel_subzone_undeclared"
	kernelSubzoneMissingRule    = "kernel_subzone_missing"
)

// ScanKernelSubzoneIntegrity enforces the bidirectional kernel layout
// contract: every immediate directory under internal/kernel must be declared
// by policy, and every declared kernel subzone must exist on disk. Missing
// internal/kernel itself is reported as a missing required subzone surface.
func ScanKernelSubzoneIntegrity(root string, pol *policy.Policy, r *report.Report) {
	kernelDir := filepath.Join(root, "internal", "kernel")
	entries, err := os.ReadDir(kernelDir)
	if err != nil {
		for _, name := range pol.KernelSubzones {
			r.Violations = append(r.Violations, report.Violation{
				Directory:   filepath.ToSlash(filepath.Join("internal", "kernel", name)),
				MatchedRule: kernelSubzoneMissingRule,
				Rule:        kernelSubzoneMissingRule,
				Severity:    "error",
				Note:        "declared kernel subzone is missing because internal/kernel cannot be read",
			})
		}
		return
	}

	declared := make(map[string]bool, len(pol.KernelSubzones))
	for _, name := range pol.KernelSubzones {
		declared[name] = true
		if !directoryEntryExists(entries, name) {
			r.Violations = append(r.Violations, report.Violation{
				Directory:   filepath.ToSlash(filepath.Join("internal", "kernel", name)),
				MatchedRule: kernelSubzoneMissingRule,
				Rule:        kernelSubzoneMissingRule,
				Severity:    "error",
				Note:        "kernel subzone is declared by architecture/policy.yaml but absent on disk",
			})
		}
	}

	for _, entry := range entries {
		if !entry.IsDir() || declared[entry.Name()] {
			continue
		}
		r.Violations = append(r.Violations, report.Violation{
			Directory:   filepath.ToSlash(filepath.Join("internal", "kernel", entry.Name())),
			MatchedRule: kernelSubzoneUndeclaredRule,
			Rule:        kernelSubzoneUndeclaredRule,
			Severity:    "error",
			Note:        "on-disk kernel subzone is not declared by architecture/policy.yaml",
		})
	}
}

func directoryEntryExists(entries []os.DirEntry, name string) bool {
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() == name {
			return true
		}
	}
	return false
}
