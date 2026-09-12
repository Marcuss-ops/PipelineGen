// Package scan — percheck_governance_artifacts.go: forward prevention for the
// governance artifacts themselves.
//
// godlike/06 (one owner per fact) applies to the enforcement layer as much as
// to production code: an allowlist whose consumer was deleted, an allowlist
// entry whose file no longer exists, or a remediation deadline that has passed
// are all "documentation that lies" — the operator (or agent) reading them
// takes the wrong architectural decision. This gate walks the artifact ledger
// and fails closed.
//
// It is deliberately DATA-DRIVEN: the vocabulary (which allowlists exist,
// which are consumed, which entries are live) is read from the tree, never
// copied here. The historical failure mode this replaces was an allowlist
// pointing at a root deleted two weeks earlier (docs/migrations/
// admin-sql-allowlist.txt, whose enforcing script had been removed), which no
// test or gate could detect.
//
// Checks, all error-severity:
//
//  1. orphan_allowlist    — a docs/migrations/*.txt file that no live scanner
//     or policy key references. An un-consumed allowlist is dead governance.
//  2. allowlist_missing   — a scanner/policy reference to an allowlist file
//     that does not exist (the scanner would silently exempt nothing).
//  3. allowlist_entry_ghost — an allowlist entry whose target path (or, for
//     the `<dir>/<pkg>:<Type>` key form, whose directory) no longer exists.
//  4. deadline_expired    — a remediation deadline declared in
//     architecture/package_hotspots.json (hotspots) or
//     architecture/current.yaml that is in the past. Deadlines exist to be
//     consumable; a passed deadline must be re-baselined or the debt closed.
//
// matched rule_id: `percheck_governance_artifacts`.
package governance

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

const governanceArtifactsRule = "percheck_governance_artifacts"

// governanceAllowlistDir is the canonical home of the per-file/transitional
// allowlists (docs/migrations). Only *.txt files participate: the directory
// also holds prose baselines and JSON snapshots with different lifecycles.
const governanceAllowlistDir = "docs/migrations"

// governanceArtifactRefRE matches a quoted reference to an allowlist file in
// Go source. The path must be a double-quoted string literal, i.e. a value the
// scanner actually opens (comment prose with backticks does not count).
var governanceArtifactRefRE = regexp.MustCompile(`"docs/migrations/([A-Za-z0-9._-]+\.txt)"`)

// governanceRefScanRoots are the trees that may hold a scanner or policy
// consumer of an allowlist.
var governanceRefScanRoots = []string{"cmd", "internal", "scripts"}

// governanceDeadlineRE matches the ISO date prefix of a deadline value, with
// or without the trailing RFC3339 time (`2026-09-30` / `2026-09-30T00:00:00Z`).
var governanceDeadlineRE = regexp.MustCompile(`deadline:\s*"?(\d{4}-\d{2}-\d{2})`)

// governanceNote is the operator-facing remediation text shared by every
// failure mode of this gate.
const governanceNote = "governance artifact integrity (godlike/06 one owner per fact, applied to the enforcement layer): an allowlist or registry that no live scanner consumes, an entry whose file no longer exists, or a remediation deadline in the past is documentation that lies. Fix the artifact (delete it, repoint it at the live gate, or re-baseline with a new owner+deadline) instead of widening an exemption."

// ScanGovernanceArtifacts validates the governance artifact ledger: allowlist
// consumers, allowlist entries and declared remediation deadlines.
func ScanGovernanceArtifacts(root string, pol *policy.Policy, r *report.Report) {
	referenced := map[string]string{} // allowlist file name → first referencing file

	walkErr := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if policy.StandardSkipDirs[filepath.Base(path)] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		inScope := false
		for _, prefix := range governanceRefScanRoots {
			if strings.HasPrefix(relSlash, prefix+"/") {
				inScope = true
				break
			}
		}
		if !inScope {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		for _, m := range governanceArtifactRefRE.FindAllStringSubmatch(string(data), -1) {
			if _, seen := referenced[m[1]]; !seen {
				referenced[m[1]] = relSlash
			}
		}
		return nil
	})
	if walkErr != nil {
		emitGovernanceViolation(r, governanceAllowlistDir, 0, "governance_walk_failed", walkErr.Error())
		return
	}

	// The policy key is a consumer too: max_lines_strict_allowlist names a
	// file whose entries are loaded by scan.ScanFileLinesStrict.
	if pol != nil && pol.MaxLinesStrictAllowlist != "" {
		name := filepath.Base(filepath.ToSlash(pol.MaxLinesStrictAllowlist))
		if _, seen := referenced[name]; !seen {
			referenced[name] = "architecture/policy.yaml"
		}
	}

	// 1 + 2. Orphans and dangling references. A missing directory is only a
	// failure when something still references an allowlist inside it: the
	// dangling-reference check below then reports each missing file.
	onDisk := map[string]bool{}
	entries, dirErr := os.ReadDir(filepath.Join(root, governanceAllowlistDir))
	if dirErr != nil {
		if len(referenced) > 0 {
			emitGovernanceViolation(r, governanceAllowlistDir, 0, "governance_dir_missing",
				"cannot read the canonical allowlist directory: "+dirErr.Error())
		}
	} else {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt") {
				continue
			}
			onDisk[entry.Name()] = true
			if _, consumed := referenced[entry.Name()]; !consumed {
				emitGovernanceViolation(r, governanceAllowlistDir+"/"+entry.Name(), 0, "orphan_allowlist",
					"allowlist has no live consumer: no non-test Go scanner opens it and no policy key points at it. Delete it (its gate is gone) or wire it to the canonical gate.")
			}
		}
	}
	for name, consumer := range referenced {
		if onDisk[name] {
			continue
		}
		emitGovernanceViolation(r, consumer, 0, "allowlist_missing",
			"references the allowlist "+governanceAllowlistDir+"/"+name+" which does not exist on disk. A missing allowlist makes the consumer exempt nothing; restore the file or remove the stale reference.")
	}

	// 3. Ghost entries inside the allowlists that ARE consumed.
	for name := range onDisk {
		scanGovernanceAllowlistEntries(root, name, r)
	}

	// 4. Expired remediation deadlines.
	checkGovernanceDeadlines(root, r)
	checkCurrentYAMLDeadlines(root, r)

	// 5. The gate definitions themselves (percheck_governance_artifacts_scanners.go):
	// one owner per rule id, and no scanner walking a root that is not there.
	scanRuleIDOwnership(root, r)
	scanGateScanRoots(root, r)
}

// scanGovernanceAllowlistEntries validates every non-comment entry of one
// consumed allowlist. Two entry shapes are supported, matching the canonical
// conventions: a repo-relative path, and the `<dir>/<pkg>:<Type>` key used by
// the duplicate-type ledger (validated through its directory).
func scanGovernanceAllowlistEntries(root, name string, r *report.Report) {
	relPath := governanceAllowlistDir + "/" + name
	f, err := os.Open(filepath.Join(root, relPath))
	if err != nil {
		emitGovernanceViolation(r, relPath, 0, "governance_artifact_unreadable", err.Error())
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		// Strip an inline trailing annotation (` # owner=... deadline=...`).
		if i := strings.Index(line, " #"); i >= 0 {
			line = line[:i]
		}
		entry := strings.TrimSpace(line)
		if entry == "" {
			continue
		}
		target := entry
		if i := strings.Index(entry, ":"); i >= 0 {
			target = strings.TrimSpace(entry[:i])
		}
		if target == "" {
			continue
		}
		if _, statErr := os.Stat(filepath.Join(root, target)); statErr != nil {
			emitGovernanceViolation(r, relPath, lineNo, "allowlist_entry_ghost",
				fmt.Sprintf("entry %q points at %q, which does not exist on disk. The exemption can never match; remove the entry (its target was deleted or renamed).", entry, target))
		}
	}
}

// hotspotsDeadlineRegistry is the subset of architecture/package_hotspots.json
// this gate consumes.
type hotspotsDeadlineRegistry struct {
	Hotspots []struct {
		Path     string `json:"path"`
		Deadline string `json:"deadline"`
		Owner    string `json:"owner"`
	} `json:"hotspots"`
}

// checkGovernanceDeadlines fails closed on a remediation deadline that has
// already passed in the hotspot registry.
func checkGovernanceDeadlines(root string, r *report.Report) {
	const relPath = "architecture/package_hotspots.json"
	data, err := os.ReadFile(filepath.Join(root, relPath))
	if err != nil {
		emitGovernanceViolation(r, relPath, 0, "governance_artifact_unreadable", err.Error())
		return
	}
	var registry hotspotsDeadlineRegistry
	if err := json.Unmarshal(data, &registry); err != nil {
		emitGovernanceViolation(r, relPath, 0, "governance_artifact_unreadable",
			"cannot parse the hotspot registry: "+err.Error())
		return
	}
	today := time.Now().UTC()
	for _, h := range registry.Hotspots {
		if h.Deadline == "" {
			continue
		}
		deadline, err := time.Parse("2006-01-02", h.Deadline)
		if err != nil {
			emitGovernanceViolation(r, relPath, 0, "deadline_malformed",
				fmt.Sprintf("hotspot %s declares deadline %q, which is not YYYY-MM-DD", h.Path, h.Deadline))
			continue
		}
		if deadline.Before(today.Truncate(24 * time.Hour)) {
			emitGovernanceViolation(r, relPath, 0, "deadline_expired",
				fmt.Sprintf("hotspot %s (owner=%s) passed its remediation deadline %s. Close the debt or re-baseline it with a new owner+deadline; a passed deadline silently authorises permanent debt.",
					h.Path, h.Owner, h.Deadline))
		}
	}
}

// checkCurrentYAMLDeadlines fails closed on a passed deadline in the active
// work registry. current.yaml is a generated view, so the violation points the
// operator at the generator's source-of-truth entry.
func checkCurrentYAMLDeadlines(root string, r *report.Report) {
	const relPath = "architecture/current.yaml"
	data, err := os.ReadFile(filepath.Join(root, relPath))
	if err != nil {
		emitGovernanceViolation(r, relPath, 0, "governance_artifact_unreadable", err.Error())
		return
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	lines := strings.Split(string(data), "\n")
	currentID := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- id:") {
			currentID = strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "- id:")), `"'`)
			continue
		}
		m := governanceDeadlineRE.FindStringSubmatch(trimmed)
		if m == nil {
			continue
		}
		deadline, err := time.Parse("2006-01-02", m[1])
		if err != nil {
			continue
		}
		if deadline.Before(today) {
			emitGovernanceViolation(r, relPath, 0, "deadline_expired",
				fmt.Sprintf("active registry entry %s passed its deadline %s (generated from architecture/catalog.yaml; update the entry there — close the work or re-baseline the deadline).",
					currentID, m[1]))
		}
	}
}

// emitGovernanceViolation appends one violation for this gate.
func emitGovernanceViolation(r *report.Report, file string, line int, matchedRule, detail string) {
	r.Violations = append(r.Violations, report.Violation{
		Package:     pkgFromRelPath(file),
		File:        file,
		Line:        line,
		Rule:        governanceArtifactsRule,
		Severity:    string(report.SeverityError),
		MatchedRule: matchedRule,
		Note:        governanceNote + " | " + detail,
	})
}
