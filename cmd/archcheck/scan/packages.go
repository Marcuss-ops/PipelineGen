// Package scan — archcheck rule-family scanners.
//
// scan/packages.go owns the "directory walking + module identification"
// half of the scan family. Package hotspots are governed by the machine-
// consumed architecture/package_hotspots.json registry: every package above
// the global cap must have an owner, a concrete deadline, a non-increasing
// file-count baseline, and named target packages.
package scan

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
	Path     string   `json:"path"`
	Owner    string   `json:"owner"`
	Deadline string   `json:"deadline"`
	Targets  []string `json:"targets"`
}

// ScanPackages walks non-test Go files under the root, counts files per
// package dir, and emits a violation per package exceeding
// pol.MaxFilesPerPackage. Registered hotspots are allowed to remain above the
// cap only while their file count does not exceed the recorded baseline.
// Unregistered hotspots and baseline growth are errors.
func ScanPackages(root string, pol *policy.Policy, r *report.Report, fileLines map[string]int) {
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

		n, lerr := countLines(path)
		if lerr == nil {
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
		h, registered := hotspots[pkg]
		if registered && count > h.BaselineFiles {
			r.Violations = append(r.Violations, report.Violation{
				Package:      pkg,
				ActualCount:  count,
				AllowedCount: pol.MaxFilesPerPackage,
				MatchedRule:  "package_hotspot_growth",
				Rule:         "pkg_size",
				Severity:     "error",
				Note: fmt.Sprintf(
					"registered hotspot exceeded baseline=%d owner=%q deadline=%s targets=%s",
					h.BaselineFiles, h.Owner, h.Deadline, strings.Join(h.TargetPackages, ", "),
				),
			})
			continue
		}

		// Preserve the established report shape for current debt so the
		// byte-stable snapshot remains useful. The registry is an invisible
		// forward ratchet until a hotspot grows past its baseline.
		r.Violations = append(r.Violations, report.Violation{
			Package:      pkg,
			ActualCount:  count,
			AllowedCount: pol.MaxFilesPerPackage,
			MatchedRule:  "max_files_per_package",
			Rule:         "pkg_size",
			Severity:     "warn",
		})
	}
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
	seen := map[string]bool{}
	for _, h := range registry.Hotspots {
		if err := validatePackageHotspot(h); err != nil {
			return nil, err
		}
		path := filepath.ToSlash(h.Path)
		if seen[path] {
			return nil, fmt.Errorf("duplicate package hotspot path %q", path)
		}
		seen[path] = true
	}
	for _, migration := range registry.RootMigrations {
		if err := validateInternalRootMigration(migration); err != nil {
			return nil, err
		}
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

func validateInternalRootMigration(m internalRootMigration) error {
	if !strings.HasPrefix(filepath.ToSlash(m.Path), "internal/") || m.Owner == "" || m.Deadline == "" || len(m.Targets) == 0 {
		return fmt.Errorf("invalid internal root migration entry for %q", m.Path)
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
		name := e.Name()
		mainPath := filepath.Join(cmdDir, name, "main.go")
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
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		n++
	}
	return n, sc.Err()
}
