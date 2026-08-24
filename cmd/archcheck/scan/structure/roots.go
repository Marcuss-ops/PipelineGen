// Package scan — archcheck rule-family scanners.
package structure

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// ScanForbiddenDirs reports internal/<x> first-level dirs whose name
// matches a forbidden_top_level_dirs entry.
func ScanForbiddenDirs(root string, pol *policy.Policy, r *report.Report) {
	internalDir := filepath.Join(root, "internal")
	entries, err := os.ReadDir(internalDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		for _, forbidden := range pol.ForbiddenTopLevelDirs {
			if name == forbidden {
				r.Violations = append(r.Violations, report.Violation{
					Directory:   filepath.ToSlash(filepath.Join("internal", name)),
					MatchedRule: "forbidden_top_level_dirs",
					Rule:        "forbidden_dir",
					Severity:    "warn",
				})
			}
		}
	}
}

// ScanKernelSubzoneHints emits an info-level hint when a kernel subzone
// currently lives at internal/<x> rather than internal/kernel/<x>.
func ScanKernelSubzoneHints(root string, pol *policy.Policy, r *report.Report) {
	if !dirExists(filepath.Join(root, "internal", "kernel")) {
		return
	}
	internalDir := filepath.Join(root, "internal")
	entries, err := os.ReadDir(internalDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "kernel" {
			continue
		}
		name := e.Name()
		for _, k := range pol.KernelSubzones {
			if name == k {
				r.Violations = append(r.Violations, report.Violation{
					Directory:   filepath.ToSlash(filepath.Join("internal", name)),
					MatchedRule: "kernel_split_hint",
					Rule:        "kernel_split_hint",
					Severity:    "info",
					Note:        "candidate for initial move to internal/kernel/" + k,
				})
			}
		}
	}
}

// ScanUnknownInternalRoots treats a first-level internal root as governed when
// it is either declared by the layout policy or registered in
// architecture/package_hotspots.json with an owner, targets and deadline.
// Registered migration roots are accepted without an unknown-root warning;
// their migration metadata is validated by the hotspot registry loader. A
// truly unmanaged root is an error so new top-level zones cannot appear
// silently.
func ScanUnknownInternalRoots(root string, pol *policy.Policy, r *report.Report) {
	internalDir := filepath.Join(root, "internal")
	entries, err := os.ReadDir(internalDir)
	if err != nil {
		return
	}
	known := map[string]bool{}
	for _, k := range pol.LegacyInternalRoots {
		known[k] = true
	}
	for _, k := range pol.TargetInternalRoots {
		known[k] = true
	}

	registered := map[string]internalRootMigration{}
	registry, registryErr := loadPackageHotspotRegistry(root)
	if registryErr == nil {
		for _, migration := range registry.RootMigrations {
			name := strings.TrimPrefix(filepath.ToSlash(migration.Path), "internal/")
			if !strings.Contains(name, "/") {
				registered[name] = migration
			}
		}
	}

	for _, e := range entries {
		if !e.IsDir() || known[e.Name()] {
			continue
		}
		if _, ok := registered[e.Name()]; ok {
			continue
		}
		r.Violations = append(r.Violations, report.Violation{
			Directory:   filepath.ToSlash(filepath.Join("internal", e.Name())),
			MatchedRule: "unregistered_internal_root",
			Rule:        "unknown_internal_root",
			Severity:    "error",
			Note:        "first-level internal/ directory has no layout-policy entry and no owner/target/deadline in " + packageHotspotRegistryPath,
		})
	}
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
