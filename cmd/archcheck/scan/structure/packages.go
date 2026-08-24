// Package scan — architecture package-size scanners.
//
// Package hotspots are governed by architecture/package_hotspots.json. A
// registered hotspot is accepted while it remains within its baseline and before
// its deadline; unmanaged, expired or growing hotspots fail closed.
package structure

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

const packageHotspotRegistryPath = "architecture/package_hotspots.json"

type packageHotspotRegistry struct {
	Version        int                     `json:"version"`
	Hotspots       []packageHotspot        `json:"hotspots"`
	RootMigrations []internalRootMigration `json:"root_migrations"`
}

type packageHotspot struct {
	Path           string   `json:"path"`
	Owner          string   `json:"owner"`
	Deadline       string   `json:"deadline"`
	BaselineFiles  int      `json:"baseline_files"`
	TargetPackages []string `json:"target_packages"`
}

type internalRootMigration struct {
	Path          string   `json:"path"`
	Owner         string   `json:"owner"`
	Deadline      string   `json:"deadline"`
	Status        string   `json:"status"`
	NewCodePolicy string   `json:"new_code_policy"`
	Targets       []string `json:"targets"`
}

// ScanPackages runs the package-size scan. Registered debt is governed by the
// same owner/baseline/deadline contract in every mode.
func ScanPackages(root string, pol *policy.Policy, r *report.Report, fileLines map[string]int) {
	ScanPackagesForMode(root, pol, r, fileLines, false)
}

// ScanPackagesForMode walks non-test Go files, records per-file line counts and
// enforces package hotspot governance. A registered hotspot is not itself a
// violation while it stays within baseline and before its deadline. Growth,
// expiry and unregistered production hotspots remain fail-closed.
func ScanPackagesForMode(root string, pol *policy.Policy, r *report.Report, fileLines map[string]int, productionOnly bool) {
	pkgCounts := map[string]int{}
	skipDirs := map[string]bool{
		".git": true, "vendor": true, "node_modules": true,
		"node-scraper": true, "examples": true, "scripts": true,
	}

	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[filepath.Base(path)] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		dir := filepath.Dir(path)
		relDir, _ := filepath.Rel(root, dir)
		pkgCounts[filepath.ToSlash(relDir)]++

		n, lineErr := countLines(path)
		if lineErr != nil {
			return nil
		}
		fileLines[path] = n
		if n > pol.MaxLinesPerFile {
			r.Violations = append(r.Violations, report.Violation{
				File:        filepath.ToSlash(filepath.Join(relDir, filepath.Base(path))),
				ActualLines: n,
				MaxLines:    pol.MaxLinesPerFile,
				MatchedRule: "max_lines_per_file",
				Rule:        "file_size",
				Severity:    "warn",
			})
		}
		return nil
	})

	registry, registryErr := loadPackageHotspotRegistry(root)
	if registryErr != nil {
		r.Violations = append(r.Violations, report.Violation{
			File:        packageHotspotRegistryPath,
			MatchedRule: "package_hotspot_registry_invalid",
			Rule:        "pkg_size",
			Severity:    "error",
			Note:        registryErr.Error(),
		})
	}
	if registryErr == nil && registry != nil && len(pol.LegacyInternalRoots) > 0 {
		if err := validateLegacyRootRegistry(registry, pol.LegacyInternalRoots); err != nil {
			r.Violations = append(r.Violations, report.Violation{
				File:        packageHotspotRegistryPath,
				MatchedRule: "legacy_root_migration_registry_invalid",
				Rule:        "pkg_size",
				Severity:    "error",
				Note:        err.Error(),
			})
		}
	}

	hotspots := map[string]packageHotspot{}
	if registry != nil {
		for _, h := range registry.Hotspots {
			hotspots[filepath.ToSlash(h.Path)] = h
		}
	}

	for pkg, count := range pkgCounts {
		if count <= pol.MaxFilesPerPackage {
			continue
		}
		appendPackageHotspotResult(r, pkg, count, pol.MaxFilesPerPackage, hotspots, productionOnly, time.Now().UTC())
	}
}

func appendPackageHotspotResult(
	r *report.Report,
	pkg string,
	count int,
	globalCap int,
	hotspots map[string]packageHotspot,
	productionOnly bool,
	now time.Time,
) {
	h, registered := hotspots[pkg]
	if !registered {
		severity := "warn"
		matchedRule := "max_files_per_package"
		note := ""
		if productionOnly {
			severity = "error"
			matchedRule = "unregistered_package_hotspot"
			note = "package exceeds the global cap but has no owner, deadline, baseline or target packages in " + packageHotspotRegistryPath
		}
		r.Violations = append(r.Violations, report.Violation{
			Package:      pkg,
			ActualCount:  count,
			AllowedCount: globalCap,
			MatchedRule:  matchedRule,
			Rule:         "pkg_size",
			Severity:     severity,
			Note:         note,
		})
		return
	}

	if count > h.BaselineFiles {
		r.Violations = append(r.Violations, report.Violation{
			Package:      pkg,
			ActualCount:  count,
			AllowedCount: h.BaselineFiles,
			MatchedRule:  "package_hotspot_growth",
			Rule:         "pkg_size",
			Severity:     "error",
			Note: fmt.Sprintf(
				"registered hotspot exceeded baseline=%d owner=%q deadline=%s targets=%s",
				h.BaselineFiles, h.Owner, h.Deadline, strings.Join(h.TargetPackages, ", "),
			),
		})
		return
	}

	if packageHotspotDeadlineExpired(h.Deadline, now) {
		severity := "warn"
		if productionOnly {
			severity = "error"
		}
		r.Violations = append(r.Violations, report.Violation{
			Package:      pkg,
			ActualCount:  count,
			AllowedCount: globalCap,
			MatchedRule:  "package_hotspot_deadline_expired",
			Rule:         "pkg_size",
			Severity:     severity,
			Note: fmt.Sprintf(
				"registered hotspot deadline expired owner=%q deadline=%s targets=%s",
				h.Owner, h.Deadline, strings.Join(h.TargetPackages, ", "),
			),
		})
	}
}

func packageHotspotDeadlineExpired(deadline string, now time.Time) bool {
	parsed, err := time.Parse("2006-01-02", deadline)
	if err != nil {
		return false // registry validation reports the malformed date separately
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return parsed.Before(today)
}

func loadPackageHotspotRegistry(root string) (*packageHotspotRegistry, error) {
	path := filepath.Join(root, filepath.FromSlash(packageHotspotRegistryPath))
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read package hotspot registry: %w", err)
	}
	var registry packageHotspotRegistry
	if err := json.Unmarshal(data, &registry); err != nil {
		return nil, fmt.Errorf("decode package hotspot registry: %w", err)
	}
	if registry.Version != 1 {
		return nil, fmt.Errorf("unsupported package hotspot registry version %d", registry.Version)
	}

	seenHotspots := map[string]bool{}
	for _, h := range registry.Hotspots {
		if err := validatePackageHotspot(h); err != nil {
			return nil, err
		}
		path := filepath.ToSlash(h.Path)
		if seenHotspots[path] {
			return nil, fmt.Errorf("duplicate package hotspot path %q", path)
		}
		seenHotspots[path] = true
	}

	seenRoots := map[string]bool{}
	for _, migration := range registry.RootMigrations {
		if err := validateInternalRootMigration(migration); err != nil {
			return nil, err
		}
		path := filepath.ToSlash(migration.Path)
		if seenRoots[path] {
			return nil, fmt.Errorf("duplicate internal root migration path %q", path)
		}
		seenRoots[path] = true
	}
	return &registry, nil
}

func validatePackageHotspot(h packageHotspot) error {
	if h.Path == "" || h.Owner == "" || h.Deadline == "" || h.BaselineFiles <= 0 || len(h.TargetPackages) == 0 {
		return fmt.Errorf("invalid package hotspot entry for %q: path, owner, deadline, positive baseline_files and target_packages are required", h.Path)
	}
	if _, err := time.Parse("2006-01-02", h.Deadline); err != nil {
		return fmt.Errorf("invalid package hotspot deadline for %q: %w", h.Path, err)
	}
	return nil
}

func validateLegacyRootRegistry(registry *packageHotspotRegistry, roots []string) error {
	seen := make(map[string]bool, len(registry.RootMigrations))
	for _, migration := range registry.RootMigrations {
		seen[filepath.ToSlash(migration.Path)] = true
	}
	for _, root := range roots {
		if !seen["internal/"+strings.Trim(root, "/")] {
			return fmt.Errorf("missing internal root migration entry for legacy root %q", root)
		}
	}
	return nil
}

func validateInternalRootMigration(m internalRootMigration) error {
	if !strings.HasPrefix(filepath.ToSlash(m.Path), "internal/") || m.Owner == "" || m.Deadline == "" || len(m.Targets) == 0 {
		return fmt.Errorf("invalid internal root migration entry for %q", m.Path)
	}
	if m.Status != "migration_only" || m.NewCodePolicy != "no_new_capabilities_no_new_public_contracts_no_new_providers_no_new_routes_no_new_files_no_new_packages" {
		return fmt.Errorf("invalid internal root migration policy for %q: status must be migration_only and new_code_policy must prohibit new capabilities, public contracts, providers, routes, files and packages", m.Path)
	}
	for _, target := range m.Targets {
		target = filepath.ToSlash(strings.TrimSuffix(target, "/"))
		if target != "internal/kernel" && target != "internal/capabilities" && target != "internal/platform" &&
			!strings.HasPrefix(target, "internal/kernel/") && !strings.HasPrefix(target, "internal/capabilities/") && !strings.HasPrefix(target, "internal/platform/") {
			return fmt.Errorf("invalid target %q for internal root migration %q: target must be under internal/kernel, internal/capabilities or internal/platform", target, m.Path)
		}
	}
	if _, err := time.Parse("2006-01-02", m.Deadline); err != nil {
		return fmt.Errorf("invalid internal root migration deadline for %q: %w", m.Path, err)
	}
	return nil
}

// ScanCommandBinaries checks that each cmd/<name>/main.go is below
// pol.CmdMainMaxLines.
func ScanCommandBinaries(root string, pol *policy.Policy, r *report.Report, fileLines map[string]int) {
	cmdDir := filepath.Join(root, "cmd")
	entries, err := os.ReadDir(cmdDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		mainPath := filepath.Join(cmdDir, e.Name(), "main.go")
		n, ok := fileLines[mainPath]
		if !ok {
			continue
		}
		if n > pol.CmdMainMaxLines {
			rel, _ := filepath.Rel(root, mainPath)
			r.Violations = append(r.Violations, report.Violation{
				File:        filepath.ToSlash(rel),
				ActualLines: n,
				MaxLines:    pol.CmdMainMaxLines,
				MatchedRule: "cmd_main_max_lines",
				Rule:        "thin_command",
				Severity:    "warn",
				Note:        "command binaries must be thin (root ctx + config + compose + mode + shutdown)",
			})
		}
	}
}

func countLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	n := 0
	for scanner.Scan() {
		n++
	}
	return n, scanner.Err()
}
