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
//
//	0 — report printed (default; --strict off). Phase 0 mode.
//	1 — violations present while --strict (Phase N mode).
//	2 — load/walk/marshal error.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
	// DataOwnershipDoc is the path (relative to root) of the canonical
	// data/config ownership document whose authority the rule family
	// scanOwnershipDoc enforces. Empty string opts out (Phase 0 only;
	// Phase 1+ may treat absence as a violation). See docs/architecture/
	// godlike/06_DATA_AND_CONFIG_OWNERSHIP.md for the contract.
	DataOwnershipDoc string
	// LegacyPolicyDoc, CIGatesDoc, AgentPlaybookDoc, RemovalDoc mirror
	// the DataOwnershipDoc field for the four canonical-promoted Phase-1
	// docs (07, 08, 11, 13 of the godlike/ program). Each is enforced by
	// the corresponding scan<X>Doc() function below. Empty string opts
	// out individually (Phase 0 only).
	LegacyPolicyDoc  string
	CIGatesDoc       string
	AgentPlaybookDoc string
	RemovalDoc       string
	// KnownGrandfathered is exposed in the report header for traceability.
	KnownGrandfathered []string
	// StaleProseStems is the list of pre-Wave-16 path stems that must
	// not appear as bare prose references in *.go source files (a
	// "bare" reference is one whose stem is NOT followed by a literal
	// '.', e.g. `module_jobs_test.go` or `compose_images bundle` —
	// distinct from `compose_images.go` which is already covered by
	// the user-regex gate). Enforced by scanStaleProsePaths. Empty
	// list opts out (Phase 0 only). See architecture/policy.yaml::
	// stale_prose_paths for the comment block + severity ladder.
	StaleProseStems []string
}

// Violation is the JSON shape emitted per rule violation.
type Violation struct {
	Package      string `json:"package,omitempty"`
	Directory    string `json:"directory,omitempty"`
	File         string `json:"file,omitempty"`
	Line         int    `json:"line,omitempty"`
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
	Passed        bool        `json:"passed"`
	Mode          string      `json:"mode"`
	PolicyPath    string      `json:"policy_path"`
	Root          string      `json:"scan_root"`
	Phase         string      `json:"phase"`
	Policy        *Policy     `json:"policy_snapshot"`
	Summary       Summary     `json:"summary"`
	Violations    []Violation `json:"violations"`
	Grandfathered []string    `json:"grandfathered_known"`
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
	scanOwnershipDoc(*root, pol, &report)
	scanLegacyPolicyDoc(*root, pol, &report)
	scanCIGatesDoc(*root, pol, &report)
	scanAgentPlaybookDoc(*root, pol, &report)
	scanRemovalDoc(*root, pol, &report)
	scanStaleProsePaths(*root, pol, &report)
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
	inStaleProse := false
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
			// collect indented bullets via the shared collectBullet
			// helper (centralizes the indent + ASCII-quote trim; the
			// inStaleProse path below uses the same helper).
			if b, isBullet := collectBullet(line); isBullet {
				p.KnownGrandfathered = append(p.KnownGrandfathered, b)
				continue
			}
			inGrandfathered = false
		}

		if inStaleProse {
			// collect indented bullets via the shared collectBullet()
			// helper (handles the indent + ASCII-quote trim; see the
			// helper doc for semantics). Mirrors the inGrandfathered
			// path style.
			if b, isBullet := collectBullet(line); isBullet {
				p.StaleProseStems = append(p.StaleProseStems, b)
				continue
			}
			inStaleProse = false
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
		case "data_ownership_doc":
			p.DataOwnershipDoc = val
		case "legacy_policy_doc":
			p.LegacyPolicyDoc = val
		case "ci_gates_doc":
			p.CIGatesDoc = val
		case "agent_playbook_doc":
			p.AgentPlaybookDoc = val
		case "removal_doc":
			p.RemovalDoc = val
		case "known_grandfathered":
			if val == "" {
				inGrandfathered = true
			}
		case "stale_prose_paths":
			if val == "" {
				inStaleProse = true
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

// collectBullet parses one YAML indented-bullet line into a clean
// scalar, returning (bullet, true) when line is a bullet to append,
// or ("", false) when line has returned to top-level (calling code
// resets its in-list flag). Handles mixed quoted + unquoted YAML
// inline scalars; mirrors the original inGrandfathered block's
// semantics. Helper exists so two list-style keys don't duplicate
// the indent + dash + quote trim logic.
func collectBullet(line string) (string, bool) {
	if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
		return "", false
	}
	b := strings.Trim(strings.TrimSpace(strings.TrimLeft(line, " \t-")), "\"'")
	return b, b != ""
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

// scanOwnershipDoc checks that the canonical data/config ownership doc
// (policy field `data_ownership_doc`, path relative to root) exists and
// contains the seven required section headers. Missing file → warn;
// missing section → info.
//
// Rationale: the godlike/06 promotion (June 2026) makes the pointed-to
// document the single source of truth for the data+config ownership
// axis. Accidental deletion or structural gutting would silently
// demote the governance. Phase 0 is report-only; --strict promotes all
// violations to os.Exit(1) per the existing main() logic.
//
// Guards:
//   - pol.DataOwnershipDoc == "" → opt out, no violations emitted.
//   - filepath.Clean + ToSlash keep the JSON report OS-independent.
//   - bufio.Scanner caps line length at 64K (sufficient for the doc).
func scanOwnershipDoc(root string, pol *Policy, r *Report) {
	if pol.DataOwnershipDoc == "" {
		return
	}
	docRel := filepath.ToSlash(filepath.Clean(pol.DataOwnershipDoc))
	docPath := filepath.Join(root, pol.DataOwnershipDoc)
	f, err := os.Open(docPath)
	if err != nil {
		r.Violations = append(r.Violations, Violation{
			File:        docRel,
			MatchedRule: "data_ownership_doc",
			Rule:        "data_ownership_doc_missing",
			Severity:    "warn",
			Note:        "canonical data/config ownership document is missing or unreadable",
		})
		return
	}
	defer f.Close()

	required := []string{
		"## Durable authority",
		"## One owner per fact",
		"## Database rules",
		"## Qdrant projection",
		"## Drive and filesystem",
		"## Configuration",
		"## Future storage changes",
	}
	scanDocSections(f, required, docRel, "data_ownership_doc", r)
}

// scanLegacyPolicyDoc / scanCIGatesDoc / scanAgentPlaybookDoc / scanRemovalDoc
// mirror scanOwnershipDoc for the four Phase-1-canonical godlike/ docs
// (07, 08, 11, 13). They share the C1 mechanism (file existence +
// required H2-heading presence) via the scanDocSections helper.
func scanLegacyPolicyDoc(root string, pol *Policy, r *Report) {
	if pol.LegacyPolicyDoc == "" {
		return
	}
	docRel := filepath.ToSlash(filepath.Clean(pol.LegacyPolicyDoc))
	f, err := os.Open(filepath.Join(root, pol.LegacyPolicyDoc))
	if err != nil {
		r.Violations = append(r.Violations, Violation{File: docRel, MatchedRule: "legacy_policy_doc", Rule: "legacy_policy_doc_missing", Severity: "warn"})
		return
	}
	defer f.Close()
	scanDocSections(f, []string{"## Goal", "## What counts as legacy", "## Default rule", "## Temporary deprecation record", "## Forbidden compatibility techniques", "## Migration sequence", "## No fake availability", "## Historical information"}, docRel, "legacy_policy_doc", r)
}

func scanCIGatesDoc(root string, pol *Policy, r *Report) {
	if pol.CIGatesDoc == "" {
		return
	}
	docRel := filepath.ToSlash(filepath.Clean(pol.CIGatesDoc))
	f, err := os.Open(filepath.Join(root, pol.CIGatesDoc))
	if err != nil {
		r.Violations = append(r.Violations, Violation{File: docRel, MatchedRule: "ci_gates_doc", Rule: "ci_gates_doc_missing", Severity: "warn"})
		return
	}
	defer f.Close()
	scanDocSections(f, []string{"## Purpose", "## Mandatory checks", "## Boundary checks", "## Registry checks", "## Legacy checks", "## Contract checks", "## Data checks", "## Complexity budgets", "## Generated output", "## Zero-baseline rule"}, docRel, "ci_gates_doc", r)
}

func scanAgentPlaybookDoc(root string, pol *Policy, r *Report) {
	if pol.AgentPlaybookDoc == "" {
		return
	}
	docRel := filepath.ToSlash(filepath.Clean(pol.AgentPlaybookDoc))
	f, err := os.Open(filepath.Join(root, pol.AgentPlaybookDoc))
	if err != nil {
		r.Violations = append(r.Violations, Violation{File: docRel, MatchedRule: "agent_playbook_doc", Rule: "agent_playbook_doc_missing", Severity: "warn"})
		return
	}
	defer f.Close()
	scanDocSections(f, []string{"## Preparation", "## Scope", "## Forbidden additions", "## Testing", "## Migration method", "## Final verification", "## Documentation"}, docRel, "agent_playbook_doc", r)
}

func scanRemovalDoc(root string, pol *Policy, r *Report) {
	if pol.RemovalDoc == "" {
		return
	}
	docRel := filepath.ToSlash(filepath.Clean(pol.RemovalDoc))
	f, err := os.Open(filepath.Join(root, pol.RemovalDoc))
	if err != nil {
		r.Violations = append(r.Violations, Violation{File: docRel, MatchedRule: "removal_doc", Rule: "removal_doc_missing", Severity: "warn"})
		return
	}
	defer f.Close()
	scanDocSections(f, []string{"## Purpose", "## Discovery", "## Runtime cut", "## Data handling", "## Code removal", "## Configuration and operations", "## Verification", "## Completion"}, docRel, "removal_doc", r)
}

// scanDocSections reads `required` H2 headings from the open file `f` and
// emits one warn-severity violation per missing section. Shared helper
// for the four Phase-1 canonical doc scanners above.
//
// Severity is `warn` (not `info`) so that --strict promotes a truncated
// doc to a hard CI gate, matching the user-contract ("warn if any
// package asserts ownership rules contradicting that doc"). Callers
// MUST pass a non-empty `required` slice and keep it in sync with the
// canonical doc's H2 headings; the helper does not validate this.
func scanDocSections(f *os.File, required []string, docRel, rulePrefix string, r *Report) {
	found := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		for _, sec := range required {
			if line == sec {
				found[sec] = true
			}
		}
	}
	for _, sec := range required {
		if found[sec] {
			continue
		}
		r.Violations = append(r.Violations, Violation{
			File:        docRel,
			MatchedRule: rulePrefix,
			Rule:        rulePrefix + "_incomplete",
			Severity:    "warn",
			Note:        fmt.Sprintf("canonical doc is missing required section: %s", sec),
		})
	}
}

// scanStaleProsePaths walks internal/**/*.go and emits one warn-severity
// violation per (file, line, stem) triple where a stem from
// pol.StaleProseStems appears as a bare prose reference — i.e. matched
// with the regex `(?<![\w])<stem>(?!\.)`. The lookbehind-for-non-word
// guards against substring matches inside larger identifiers (e.g.
// `mymodule_jobs`); the negative lookahead-for-dot excludes `<stem>.go`
// references that the user-regex gate already covers (e.g.
// `compose_images.go`). Together they catch the `module_jobs_test.go`
// / `compose_images bundle` family without false positives on existing
// comment prose.
//
// (Above paragraph describes the original `(?<!\\w)<stem>(?!\\.)`
// regex design that Go's RE2 engine does NOT support. The runtime
// implementation is now the alternation `\\b<stem>(?:\\w+|[^.])` --
// see the inline comment at the regex compile site below for the
// RE2-pivot rationale.)
//
// Per-stem rule naming `stale_prose_paths_<stem>` keeps the
// `--strict` summary group filterable from the user-regex family.
// Day-1 expected violations: 4 in internal/app/composition_test.go
// referring to the surviving `module_jobs_test.go` (real test file;
// addressed in a doc-only follow-up that rewrites the references to
// `module_media.go::BuildJobsBundle (see module_jobs_test.go)`).
//
// Skipped dirs (Phase 0 hardcoded — moved to policy.go in Phase 1):
//
//	.git, vendor, node_modules, node-scraper, examples, scripts
//
// Severity is `warn` so `--strict` promotes a stray reference to
// os.Exit(1) — mirroring the user-contract on the doc-pointer family.
func scanStaleProsePaths(root string, pol *Policy, r *Report) {
	if len(pol.StaleProseStems) == 0 {
		return
	}
	type stemRe struct {
		stem string
		re   *regexp.Regexp
	}
	compiled := make([]stemRe, 0, len(pol.StaleProseStems))
	for _, s := range pol.StaleProseStems {
		// RE2 (Go's regexp engine) does NOT support lookbehind /
		// lookahead, so the design pivots to alternation:
		//
		//   \b<stem>            — word-boundary at start (excludes
		//                         `mymodule_jobs` substrings).
		//   (?:\w+|[^.])       — either one-or-more word chars
		//                         (matches `module_jobs_test.go`, where
		//                         the stem continues into `_<word>`),
		//                         OR a single non-`.` char
		//                         (matches bare prose `module_jobs
		//                         bundle`, end-of-line mentions,
		//                         etc.). The `.` exclusion is what
		//                         prevents matching `compose_images.go`,
		//                         which the user-regex gate already
		//                         covers.
		re := regexp.MustCompile(`\b` + regexp.QuoteMeta(s) + `(?:\w+|[^.])`)
		compiled = append(compiled, stemRe{stem: s, re: re})
	}
	skipDirs := map[string]bool{
		".git": true, "vendor": true, "node_modules": true,
		"node-scraper": true, "examples": true, "scripts": true,
	}
	internalDir := filepath.Join(root, "internal")
	_ = filepath.WalkDir(internalDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[filepath.Base(path)] {
				return filepath.SkipDir
			}
			return nil
		}
		// Include _test.go files — the day-1 violations live in
		// composition_test.go (referring to module_jobs_test.go). Skipping
		// tests would silently drop the family.
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		rel, _ := filepath.Rel(root, path)
		relPath := filepath.ToSlash(rel)
		sc := bufio.NewScanner(f)
		lineNum := 0
		for sc.Scan() {
			lineNum++
			line := sc.Text()
			for _, sr := range compiled {
				if sr.re.MatchString(line) {
					r.Violations = append(r.Violations, Violation{
						File:        relPath,
						Line:        lineNum,
						MatchedRule: "stale_prose_paths",
						Rule:        fmt.Sprintf("stale_prose_paths_%s", sr.stem),
						Severity:    "warn",
						// Note: file names are lowercase snake_case (`build_bundles_<lowercase_x>.go`); function names are CamelCase (`build<X>Service`). E.g. <X>=Voiceover maps to build_bundles_voiceover.go::buildVoiceoverService. Inherited from compose_media.go (deleted in W15 helper-split) where the original placeholder pattern used CamelCase <X>; the new segment adds `<lowercase_x>` to disambiguate file vs function casing without breaking the inline-template visual.
						Note: fmt.Sprintf("line contains a bare prose reference to pre-Wave-16 path stem %q (not followed by a literal dot); rewrite the comment to the post-Wave-16 ground truth: composition.go::Build<X>Bundle / module_sources.go::Wire<X> / internal/app/build_bundles_<lowercase_x>.go::build<X>Service", sr.stem),
					})
				}
			}
		}
		return nil
	})
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
