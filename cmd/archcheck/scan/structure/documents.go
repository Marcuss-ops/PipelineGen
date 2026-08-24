// Package scan — archcheck rule-family scanners.
//
// scan/documents.go owns the "arch docs pointer consistency
// (AGENTS.md ↔ ARCHITECTURE.md)" half of the scan family. The five
// exported functions (ScanOwnershipDoc, ScanLegacyPolicyDoc,
// ScanCIGatesDoc, ScanAgentPlaybookDoc, ScanRemovalDoc) all share
// the same C1 mechanism via the private scanDocSections helper:
// each verifies that a canonical godlike/ doc exists at the path
// declared in policy.yaml and contains the required H2 headings.
//
// Package boundary: `package scan` (separate from `package main` of
// cmd/archcheck) — see scan/packages.go for the rationale.
//
// Cross-references:
//   - architecture/policy.yaml: the 5 policy knobs the functions read
//   - docs/architecture/godlike/06_DATA_AND_CONFIG_OWNERSHIP.md: godlike/06
//   - docs/architecture/godlike/07_ZERO_LEGACY_POLICY.md: godlike/07
//   - docs/architecture/godlike/08_ARCHITECTURE_CI_GATES.md: godlike/08
//   - docs/architecture/godlike/11_AGENT_EXECUTION_PLAYBOOK.md: godlike/11
//   - docs/architecture/godlike/13_FEATURE_REMOVAL_CHECKLIST.md: godlike/13
package structure

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// ScanOwnershipDoc checks that the canonical data/config ownership
// doc (policy field `data_ownership_doc`, path relative to root)
// exists and contains the seven required section headers. Missing
// file → warn; missing section → info.
//
// Rationale: the godlike/06 promotion (June 2026) makes the
// pointed-to document the single source of truth for the
// data+config ownership axis. Accidental deletion or structural
// gutting would silently demote the governance. Phase 0 is
// report-only; --strict promotes all violations to os.Exit(1) per
// the existing main() logic.
//
// Guards:
//   - pol.DataOwnershipDoc == "" → opt out, no violations emitted.
//   - filepath.Clean + ToSlash keep the JSON report OS-independent.
//   - bufio.Scanner caps line length at 64K (sufficient for the doc).
func ScanOwnershipDoc(root string, pol *policy.Policy, r *report.Report) {
	if pol.DataOwnershipDoc == "" {
		return
	}
	docRel := filepath.ToSlash(filepath.Clean(pol.DataOwnershipDoc))
	docPath := filepath.Join(root, pol.DataOwnershipDoc)
	f, err := os.Open(docPath)
	if err != nil {
		r.Violations = append(r.Violations, report.Violation{
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

// ScanLegacyPolicyDoc mirrors ScanOwnershipDoc for the
// godlike/07_ZERO_LEGACY_POLICY.md doc (8 required H2 headings).
func ScanLegacyPolicyDoc(root string, pol *policy.Policy, r *report.Report) {
	if pol.LegacyPolicyDoc == "" {
		return
	}
	docRel := filepath.ToSlash(filepath.Clean(pol.LegacyPolicyDoc))
	f, err := os.Open(filepath.Join(root, pol.LegacyPolicyDoc))
	if err != nil {
		r.Violations = append(r.Violations, report.Violation{File: docRel, MatchedRule: "legacy_policy_doc", Rule: "legacy_policy_doc_missing", Severity: "warn"})
		return
	}
	defer f.Close()
	scanDocSections(f, []string{"## Goal", "## What counts as legacy", "## Default rule", "## Temporary deprecation record", "## Forbidden compatibility techniques", "## Migration sequence", "## No fake availability", "## Historical information"}, docRel, "legacy_policy_doc", r)
}

// ScanCIGatesDoc mirrors ScanOwnershipDoc for the
// godlike/08_ARCHITECTURE_CI_GATES.md doc (10 required H2 headings).
func ScanCIGatesDoc(root string, pol *policy.Policy, r *report.Report) {
	if pol.CIGatesDoc == "" {
		return
	}
	docRel := filepath.ToSlash(filepath.Clean(pol.CIGatesDoc))
	f, err := os.Open(filepath.Join(root, pol.CIGatesDoc))
	if err != nil {
		r.Violations = append(r.Violations, report.Violation{File: docRel, MatchedRule: "ci_gates_doc", Rule: "ci_gates_doc_missing", Severity: "warn"})
		return
	}
	defer f.Close()
	scanDocSections(f, []string{"## Purpose", "## Mandatory checks", "## Boundary checks", "## Registry checks", "## Legacy checks", "## Contract checks", "## Data checks", "## Complexity budgets", "## Generated output", "## Zero-baseline rule"}, docRel, "ci_gates_doc", r)
}

// ScanAgentPlaybookDoc mirrors ScanOwnershipDoc for the
// godlike/11_AGENT_EXECUTION_PLAYBOOK.md doc (7 required H2 headings).
func ScanAgentPlaybookDoc(root string, pol *policy.Policy, r *report.Report) {
	if pol.AgentPlaybookDoc == "" {
		return
	}
	docRel := filepath.ToSlash(filepath.Clean(pol.AgentPlaybookDoc))
	f, err := os.Open(filepath.Join(root, pol.AgentPlaybookDoc))
	if err != nil {
		r.Violations = append(r.Violations, report.Violation{File: docRel, MatchedRule: "agent_playbook_doc", Rule: "agent_playbook_doc_missing", Severity: "warn"})
		return
	}
	defer f.Close()
	scanDocSections(f, []string{"## Preparation", "## Scope", "## Forbidden additions", "## Testing", "## Migration method", "## Final verification", "## Documentation"}, docRel, "agent_playbook_doc", r)
}

// ScanRemovalDoc mirrors ScanOwnershipDoc for the
// godlike/13_FEATURE_REMOVAL_CHECKLIST.md doc (8 required H2 headings).
func ScanRemovalDoc(root string, pol *policy.Policy, r *report.Report) {
	if pol.RemovalDoc == "" {
		return
	}
	docRel := filepath.ToSlash(filepath.Clean(pol.RemovalDoc))
	f, err := os.Open(filepath.Join(root, pol.RemovalDoc))
	if err != nil {
		r.Violations = append(r.Violations, report.Violation{File: docRel, MatchedRule: "removal_doc", Rule: "removal_doc_missing", Severity: "warn"})
		return
	}
	defer f.Close()
	scanDocSections(f, []string{"## Purpose", "## Discovery", "## Runtime cut", "## Data handling", "## Code removal", "## Configuration and operations", "## Verification", "## Completion"}, docRel, "removal_doc", r)
}

// ScanStaleProsePaths walks internal/**/*.go and emits one
// warn-severity violation per (file, line, stem) triple where a
// stem from pol.StaleProseStems appears as a bare prose reference
// — i.e. matched with the regex `\b<stem>(?:\w+|[^.])` (the
// word-boundary guard excludes `mymodule_jobs` substrings; the
// non-`.` exclusion prevents matching `compose_images.go`).
// Together they catch the `module_jobs_test.go` / `compose_images
// bundle` family without false positives on existing comment
// prose.
//
// Per-stem rule naming `stale_prose_paths_<stem>` keeps the
// `--strict` summary group filterable from the user-regex
// family. Day-1 expected violations: 4 in
// internal/app/composition_test.go referring to the surviving
// `module_jobs_test.go` (real test file; addressed in a doc-only
// follow-up that rewrites the references to
// `module_media.go::BuildJobsBundle (see module_jobs_test.go)`).
//
// Skipped dirs (Phase 0 hardcoded — moved to policy.Policy.ScanSkipDirs
// in Phase 1+ if/when the policy schema adds a generic skip-set):
//
//	.git, vendor, node_modules, node-scraper, examples, scripts
//
// Severity is `warn` so `--strict` promotes a stray reference to
// os.Exit(1) — mirroring the user-contract on the doc-pointer
// family. Lives in documents.go (not prose.go or roots.go) because
// the responsibility is "arch docs pointer consistency" — this
// function checks the prose→ground-truth direction, while the
// other Scan*Doc functions check the ground-truth→H2 direction.
// Both directions of the pointer are part of the same family.
func ScanStaleProsePaths(root string, pol *policy.Policy, r *report.Report) {
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
					r.Violations = append(r.Violations, report.Violation{
						File:        relPath,
						Line:        lineNum,
						MatchedRule: "stale_prose_paths",
						Rule:        fmt.Sprintf("stale_prose_paths_%s", sr.stem),
						Severity:    "warn",
						Note:        fmt.Sprintf("line contains a bare prose reference to pre-Wave-16 path stem %q (not followed by a literal dot); rewrite the comment to the post-Wave-16 ground truth: composition.go::Build<X>Bundle / module_sources.go::Wire<X> / internal/app/build_bundles_<lowercase_x>.go::build<X>Service", sr.stem),
					})
				}
			}
		}
		return nil
	})
}

// scanDocSections reads `required` H2 headings from the open file
// `f` and emits one warn-severity violation per missing section.
// Shared helper for the five Phase-1 canonical doc scanners above.
//
// Severity is `warn` (not `info`) so that --strict promotes a
// truncated doc to a hard CI gate, matching the user-contract
// ("warn if any package asserts ownership rules contradicting that
// doc"). Callers MUST pass a non-empty `required` slice and keep
// it in sync with the canonical doc's H2 headings; the helper
// does not validate this.
//
// canonIndexSSOTAnchor is the exact H2 heading the rule anchors to.
// Drift between INDEX.md's SSOT map and the on-disk file set is what
// this rule catches: if a godlike doc gets renamed/moved without
// updating INDEX.md's SSOT table, the navigation aid silently
// desyncs. The H2 anchor pins the regex over ONE section so we don't
// pollute the violation list with paths from other INDEX.md tables
// (e.g. "The ownership surface", "Cross-references").
const canonIndexSSOTAnchor = "## The 5 godlike docs (SSOT map)"

// canonIndexDocRel is the canonical repo-relative path to the index.
// Hardcoded to keep policy.yaml untouched in this commit — the path
// appears in the INDEX.md self-link header + in godlike/08 as the
// SSOT and is unlikely to relocate. Future Phase 1+ lift moves this
// to pol.CanonIndexDoc + a pol.HardGates entry promoting the rule
// to error severity per the hard-gate promotion cadence.
const canonIndexDocRel = "docs/architecture/godlike/INDEX.md"

// canonIndexMdPathRe captures backtick-quoted relative paths that
// end in `.md` from SSOT map table rows. Anchored to the `.md` suffix
// so we don't match the Hard-gated-by field which contains backticked
// identifier names without `.md` (e.g. `data_ownership_doc_missing`).
var canonIndexMdPathRe = regexp.MustCompile("`([a-zA-Z0-9_./-]+\\.md)`")

// ScanCanonIndexDrift reads `docs/architecture/godlike/INDEX.md`,
// anchors to the "## The 5 godlike docs (SSOT map)" H2 section, and
// extracts every backtick-quoted `.md` path from the table rows that
// follow. For each such path, the rule confirms the file exists on
// disk; missing files emit one warn-severity violation under the
// percheck_canon_index_drift rule family.
//
// Failure modes (each emits one warn-severity violation):
//
//   - INDEX.md missing or unreadable → rule
//     percheck_canon_index_drift_missing (file = canonIndexDocRel).
//   - SSOT-map H2 section missing entirely → rule
//     percheck_canon_index_drift_section_missing
//     (file = canonIndexDocRel).
//   - A path listed in the table but absent on disk → emits 1+
//     violations with rule percheck_canon_index_drift + matched_rule
//     `canon_index_path_missing`; file = the missing path; line =
//     the INDEX.md table row that referenced it.
//
// Phase 0 emits `warn` severity per the user-contract on the
// doc-pointer family. Phase 2 promotes to `error` via pol.HardGates
// per the godlike/08 hard-gate promotion cadence — add the rule id
// (percheck_canon_index_drift) to pol.HardGates when the binary
// enforcement is ready to surface drift as a CI build failure. The
// runner.Run() HardGates promotion loop scans r.Violations[*].Rule,
// not .MatchedRule, so the percheck_canon_index_drift family wide id
// promotes all 3 variants (missing / section_missing / path_missing)
// in one entry.
func ScanCanonIndexDrift(root string, _ *policy.Policy, r *report.Report) {
	indexRel := filepath.ToSlash(filepath.Clean(canonIndexDocRel))
	indexPath := filepath.Join(root, canonIndexDocRel)
	f, err := os.Open(indexPath)
	if err != nil {
		r.Violations = append(r.Violations, report.Violation{
			File:        indexRel,
			Rule:        "percheck_canon_index_drift",
			MatchedRule: "canon_index_drift_doc_missing",
			Severity:    string(report.SeverityWarn),
			Note:        "canonical documentation index (docs/architecture/godlike/INDEX.md) is missing or unreadable; recreate per the SSOT map definition in godlike/08",
		})
		return
	}
	defer f.Close()

	if !scanCanonIndexSSOTTable(f, indexRel, root, r) {
		r.Violations = append(r.Violations, report.Violation{
			File:        indexRel,
			Rule:        "percheck_canon_index_drift",
			MatchedRule: "canon_index_drift_section_missing",
			Severity:    string(report.SeverityWarn),
			Note:        "INDEX.md is missing the canonical \"## The 5 godlike docs (SSOT map)\" H2 heading that this rule anchors to; restore the section header before any drift can be detected",
		})
	}
}

// scanCanonIndexSSOTTable walks INDEX.md line by line. Once we see
// the canonical H2 anchor, we look for table rows until the next H2
// heading. For each data row, we extract backtick-quoted `.md` paths
// and verify each exists on disk. Returns true if the anchor was
// seen (whether or not violations were emitted), so the caller can
// detect the missing-section case (anchor never appeared → return false).
//
// Unexported helper. Anchored to ONE table to keep the rule's blast
// radius minimal: paths from "The ownership surface" / "Cross-references"
// (other INDEX.md tables) are deliberately not validated here — those
// surface different concerns (shard ownership + cross-doc reference
// integrity) and any future rule for them would be a separate scan.
func scanCanonIndexSSOTTable(f *os.File, indexRel, root string, r *report.Report) bool {
	sc := bufio.NewScanner(f)
	anchorSeen := false
	lineNum := 0
	for sc.Scan() {
		lineNum++
		trimmed := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(trimmed, "## ") {
			if trimmed == canonIndexSSOTAnchor {
				anchorSeen = true
			} else if anchorSeen {
				// We've left the SSOT map section; stop processing so
				// paths from subsequent sections don't pollute the drift list.
				return true
			}
			continue
		}
		if !anchorSeen {
			continue
		}
		if !strings.HasPrefix(trimmed, "|") {
			continue
		}
		// Skip the table separator row (|---|---|---|) and the header row
		// (| # | Doc | Purpose | Hard-gated by |).
		if strings.Contains(trimmed, "---") || strings.Contains(trimmed, "Doc | Purpose") {
			continue
		}
		matches := canonIndexMdPathRe.FindAllStringSubmatch(trimmed, -1)
		for _, m := range matches {
			pathRel := m[1]
			if _, err := os.Stat(filepath.Join(root, pathRel)); err != nil {
				r.Violations = append(r.Violations, report.Violation{
					File:        pathRel,
					Line:        lineNum,
					MatchedRule: "canon_index_drift_path_missing",
					Rule:        "percheck_canon_index_drift",
					Severity:    string(report.SeverityWarn),
					Note:        fmt.Sprintf("docs/architecture/godlike/INDEX.md SSOT-map row references %q but no file exists at that path; if the godlike doc was renamed or moved, update BOTH the on-disk path AND the INDEX.md table row — silent drift is the failure mode this rule exists to catch (godlike/08 INDEX.md forward-pointer)", pathRel),
				})
			}
		}
	}
	return anchorSeen
}

// Unexported (lowercase) because it's an internal helper, not a
// public API. If a second consumer outside `package scan` ever
// needs it (e.g. for a hypothetical `pkg/scanutil` shared lib),
// promote to `ScanDocSections` and move to a separate file.
func scanDocSections(f *os.File, required []string, docRel, rulePrefix string, r *report.Report) {
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
		r.Violations = append(r.Violations, report.Violation{
			File:        docRel,
			MatchedRule: rulePrefix,
			Rule:        rulePrefix + "_incomplete",
			Severity:    "warn",
			Note:        fmt.Sprintf("canonical doc is missing required section: %s", sec),
		})
	}
}
