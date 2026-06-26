// Package main — archcheck (Phase 0, target-tree governance).
//
// Reads `architecture/policy.yaml` (flat key:value, stdlib-parseable — see
// comments in that file), walks the project tree, and emits a JSON
// violation report to stdout. Phase 0 is report-only (exits 0 even when
// violations are present) so existing CI is undisturbed while the policy
// and tooling stabilise. Phase N promotes the gate by passing `--strict`.
//
// Scope: this binary is **independent** of the existing
// `scripts/archcheck/` tool. `scripts/archcheck` enforces legacy ratchets
// (allowedInternalRoots, import-pattern drift). `cmd/archcheck` enforces
// the **target-tree** rules in `architecture/policy.yaml` and reports their
// drift. Phase 1+ may consolidate the two tools.
//
// Stdlib only — no gopkg.in/yaml.v3 import. The flat policy format is
// documented in architecture/policy.yaml and intentionally simple
// (one key per line, comma-separated lists). The complexity gate is the
// policy file, not the parser.
//
// Exit codes:
//   0 — report printed (default; --strict off). Phase 0 mode.
//   1 — violations present while --strict (Phase N mode).
//   2 — load/walk/marshal error.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Policy is the parsed subset of architecture/policy.yaml used by the
// scan. Unknown keys are ignored (forward-compat). Lists are parsed from
// comma-separated values; multi-line YAML lists (e.g. known_grandfathered)
// are doc-only and not consumed at runtime.
//
// `LegacyInternalRoots` is the current (Phase 0) layout — `internal/{api,
// app, application, domain, infrastructure}`. `TargetInternalRoots` is
// the migration target — `internal/{app, kernel, capabilities,
// platform}`. The scan reports any first-level `internal/<x>` not in
// either list as an unknown-root warning, so the migration progress is
// visible in the JSON report over time.
//
// `Capabilities` and `PlatformSubzones` are targets for Phase 1+
// enforcement of "expected zones exist" rules. For Phase 0 they are
// declared in the policy so the report snapshot is forward-compatible,
// but no enforcement logic runs against them yet.
type Policy struct {
	MaxFilesPerPackage    int
	MaxLinesPerFile       int
	CmdMainMaxLines       int
	ForbiddenTopLevelDirs []string
	KernelSubzones        []string
	Capabilities          []string
	PlatformSubzones      []string
	LegacyInternalRoots   []string
	TargetInternalRoots   []string
	// KnownGrandfathered is exposed in the report header for traceability.
	KnownGrandfathered []string
}

// Violation is the JSON shape emitted per rule violation.
type Violation struct {
	Package      string `json:"package,omitempty"`
	Directory    string `json:"directory,omitempty"`
	File         string `json:"file,omitempty"`
	ActualCount  int    `json:"actual_count,omitempty"`
	AllowedCount int    `json:"allowed_count,omitempty"`
	ActualLines  int    `json:"actual_lines,omitempty"`
	MaxLines     int    `json:"max_lines,omitempty"`
	MatchedRule  string `json:"matched_rule,omitempty"`
	Rule         string `json:"rule"`
	Severity     string `json:"severity"`
	Note         string `json:"note,omitempty"`
}

// Summary groups violation counts by rule id.
type Summary struct {
	TotalViolations int            `json:"total_violations"`
	ByReason        map[string]int `json:"by_reason"`
	BySeverity      map[string]int `json:"by_severity"`
}

// Report is the JSON document printed on stdout.
type Report struct {
	Passed         bool     `json:"passed"`
	Mode           string   `json:"mode"`
	PolicyPath     string   `json:"policy_path"`
	Root           string   `json:"scan_root"`
	Phase          string   `json:"phase"`
	Policy         *Policy  `json:"policy_snapshot"`
	Summary        Summary  `json:"summary"`
	Violations     []Violation `json:"violations"`
	Grandfathered  []string `json:"grandfathered_known"`
}

func main() {
	var (
		root   = flag.String("root", ".", "Project root to scan (default: cwd)")
		policy = flag.String("policy", "architecture/policy.yaml", "Path to policy YAML")
		strict = flag.Bool("strict", false, "Phase N gate: exit 1 if any violations present")
		phase  = flag.String("phase", "0", "Phase label (printed in the report)")
	)
	flag.Parse()

	pol, err := loadPolicy(*policy)
	if err != nil {
		fmt.Fprintf(os.Stderr, "archcheck: load policy %q: %v\n", *policy, err)
		os.Exit(2)
	}

	report := Report{
		Mode:       "target-tree-dry-run",
		PolicyPath: *policy,
		Root:       *root,
		Phase:      *phase,
		Policy:     pol,
		Summary:    Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}

	// TODO(phase-1): implement `New<X>(...)` regex arg-count scan for
	// the constructor max-deps rule (policy.yaml `max_constructor_deps`,
	// currently future work — see ARCHITECTURE.md §11.5).

	scanForbiddenDirs(*root, pol, &report)
	scanKernelSubzoneHints(*root, pol, &report)
	scanUnknownInternalRoots(*root, pol, &report)
	fileLines := map[string]int{}
	scanPackages(*root, pol, &report, fileLines)
	scanCommandBinaries(*root, pol, &report, fileLines)

	report.Summary.TotalViolations = len(report.Violations)
	for _, v := range report.Violations {
		report.Summary.ByReason[v.Rule]++
		report.Summary.BySeverity[v.Severity]++
	}
	report.Passed = len(report.Violations) == 0

	out, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "archcheck: marshal report: %v\n", err)
		os.Exit(2)
	}
	fmt.Println(string(out))

	if *strict && len(report.Violations) > 0 {
		os.Exit(1)
	}
}

// loadPolicy parses the flat key:value portions of architecture/policy.yaml.
// Multi-line list values are exposed in the report header but not consumed
// for enforcement in Phase 0.
func loadPolicy(path string) (*Policy, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	p := &Policy{}
	sc := bufio.NewScanner(f)
	inGrandfathered := false
	for sc.Scan() {
		line := sc.Text()
		if idx := strings.Index(line, "#"); idx >= 0 {
			line = line[:idx]
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if inGrandfathered {
			// collect indented bullets until top-level key or EOF
			if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
				inGrandfathered = false
			} else {
				// Strip leading indent + dash, surrounding whitespace, and
				// surrounding ASCII quotes (YAML inline scalar syntax). Plain
				// bullet strings without quotes work too.
				b := strings.Trim(strings.TrimSpace(strings.TrimLeft(line, " \t-")), "\"'")
				if b != "" {
					p.KnownGrandfathered = append(p.KnownGrandfathered, b)
				}
				continue
			}
		}

		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		switch key {
		case "max_files_per_package":
			p.MaxFilesPerPackage = atoiOrDefault(val, 40)
		case "max_lines_per_file":
			p.MaxLinesPerFile = atoiOrDefault(val, 500)
		case "cmd_main_max_lines":
			p.CmdMainMaxLines = atoiOrDefault(val, 200)
		case "forbidden_top_level_dirs":
			p.ForbiddenTopLevelDirs = splitTrim(val)
		case "kernel_subzones":
			p.KernelSubzones = splitTrim(val)
		case "capabilities":
			p.Capabilities = splitTrim(val)
		case "platform_subzones":
			p.PlatformSubzones = splitTrim(val)
		case "legacy_internal_roots":
			p.LegacyInternalRoots = splitTrim(val)
		case "target_internal_roots":
			p.TargetInternalRoots = splitTrim(val)
		case "known_grandfathered":
			if val == "" {
				inGrandfathered = true
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return p, nil
}

func atoiOrDefault(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}

func splitTrim(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// scanForbiddenDirs reports internal/<x> first-level dirs whose name
// matches a `forbidden_top_level_dirs` entry. Severity: warn.
func scanForbiddenDirs(root string, pol *Policy, r *Report) {
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
				r.Violations = append(r.Violations, Violation{
					Directory:   filepath.ToSlash(filepath.Join("internal", name)),
					MatchedRule: "forbidden_top_level_dirs",
					Rule:        "forbidden_dir",
					Severity:    "warn",
				})
			}
		}
	}
}

// scanKernelSubzoneHints emits an info-level hint when a kernel subzone
// (e.g. asset) currently lives at internal/<x> rather than internal/kernel/<x>.
// The hint is informational; the goal is to track the Phase 5 kernel split
// progression.
func scanKernelSubzoneHints(root string, pol *Policy, r *Report) {
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
				r.Violations = append(r.Violations, Violation{
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

// scanPackages walks non-test Go files under the root, counts files per
// package dir, and emits a violation per package exceeding
// MaxFilesPerPackage. Also emits a violation per file exceeding
// MaxLinesPerFile. The walk returns SkipDir for vendored / generated
// directories, so descendants are excluded without per-file filtering.
//
// Skipped dirs (Phase 0 hardcoded — moved to policy.go in Phase 1):
//
//	.git, vendor, node_modules, node-scraper, examples, scripts
func scanPackages(root string, pol *Policy, r *Report, fileLines map[string]int) {
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
				r.Violations = append(r.Violations, Violation{
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

	for pkg, count := range pkgCounts {
		if count > pol.MaxFilesPerPackage {
			r.Violations = append(r.Violations, Violation{
				Package:      pkg,
				ActualCount:  count,
				AllowedCount: pol.MaxFilesPerPackage,
				MatchedRule:  "max_files_per_package",
				Rule:         "pkg_size",
				Severity:     "warn",
			})
		}
	}
}

// scanUnknownInternalRoots emits a warn for each first-level `internal/<x>`
// not in `legacy_internal_roots` ∪ `target_internal_roots`. This catches
// half-migrated zones (e.g. `internal/jobs` after we move `jobs/` into
// `capabilities/jobs/`).
func scanUnknownInternalRoots(root string, pol *Policy, r *Report) {
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
		r.Violations = append(r.Violations, Violation{
			Directory:   filepath.ToSlash(filepath.Join("internal", e.Name())),
			MatchedRule: "not_in_legacy_or_target_internal_roots",
			Rule:        "unknown_internal_root",
			Severity:    "warn",
			Note:        "first-level internal/ dir is not declared in legacy or target roots",
		})
	}
}

// scanCommandBinaries checks that each cmd/<name>/main.go is below
// CmdMainMaxLines. Phase 0 reports; the proposal says command binaries
// must be thin (root ctx + config load + compose call + mode select +
// shutdown wait).
func scanCommandBinaries(root string, pol *Policy, r *Report, fileLines map[string]int) {
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
			r.Violations = append(r.Violations, Violation{
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

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
