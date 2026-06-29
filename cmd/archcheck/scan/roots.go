// Package scan — archcheck rule-family scanners.
//
// scan/roots.go owns the "top-level dir whitelist + banned
// helpers/utils/models" half of the scan family. The three exported
// functions here are all `internal/<x>`-dir-shape checks: they look
// at the first-level subdirs of `internal/` and emit violations
// based on policy.KernelSubzones, policy.ForbiddenTopLevelDirs, and
// policy.LegacyInternalRoots ∪ policy.TargetInternalRoots.
//
// Package boundary: `package scan` (separate from `package main` of
// cmd/archcheck) — see scan/packages.go for the rationale.
//
// Cross-references:
//   - architecture/policy.yaml: the three policy knobs the functions read
//   - docs/architecture/godlike/08_ARCHITECTURE_CI_GATES.md: canonical
//     rule definitions (forbidden_dir, kernel_split_hint, unknown_internal_root)
package scan

import (
	"os"
	"path/filepath"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// ScanForbiddenDirs reports internal/<x> first-level dirs whose name
// matches a `forbidden_top_level_dirs` entry (default:
// service, repository, models, utils, helpers). Severity: warn.
//
// The scan is opt-out if the `internal/` directory does not exist
// (os.ReadDir returns an error and the function returns silently).
// This is the intended behavior for fresh checkouts / non-Go
// repositories where the archcheck is being smoke-tested.
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

// ScanKernelSubzoneHints emits an info-level hint when a kernel
// subzone (e.g. asset) currently lives at internal/<x> rather than
// internal/kernel/<x>. The hint is informational; the goal is to
// track the Phase 5 kernel split progression. Severity: info
// (intentionally — `--strict` does NOT promote info violations to
// os.Exit(1) per the existing main() logic).
//
// The function short-circuits if `internal/kernel/` does not exist
// (so fresh checkouts don't spam every internal/ subdir as a
// "candidate for initial move" hint before the kernel migration
// has even started).
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

// ScanUnknownInternalRoots emits a warn for each first-level
// `internal/<x>` not in `legacy_internal_roots` ∪
// `target_internal_roots`. This catches half-migrated zones
// (e.g. `internal/jobs` after we move `jobs/` into
// `capabilities/jobs/`).
//
// The "known" set is the union of legacy + target roots — both are
// valid first-level internal/ subdirs, just at different migration
// stages. Phase 5+ will eventually narrow this to just
// `target_internal_roots` (the legacy roots having been fully
// emptied).
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
	for _, e := range entries {
		if !e.IsDir() || known[e.Name()] {
			continue
		}
		r.Violations = append(r.Violations, report.Violation{
			Directory:   filepath.ToSlash(filepath.Join("internal", e.Name())),
			MatchedRule: "not_in_legacy_or_target_internal_roots",
			Rule:        "unknown_internal_root",
			Severity:    "warn",
			Note:        "first-level internal/ dir is not declared in legacy or target roots",
		})
	}
}

// dirExists returns true when path is a directory in the
// filesystem. The helper exists in roots.go (not a higher-level
// util package) because the only consumer is ScanKernelSubzoneHints;
// promoting it to a shared pkg/ would expand the pkg surface area
// for one call site. Promote to pkg/fsutil if a second consumer
// appears in scan/ or elsewhere.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
