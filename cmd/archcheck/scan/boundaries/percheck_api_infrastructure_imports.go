// Package scan — Wave-22 forward-prevention gate for low-level
// infrastructure imports in the API layer.
//
// scan/percheck_api_infrastructure_imports.go owns the Go-side mirror
// of scripts/ci/architecture/checks/check_19_api_infrastructure_imports.sh.
// The canonical CI gate is the shell check (zero-baseline rule per
// godlike/08 §"Zero-baseline rule"); the Go scanner is the on-default
// fail-closed forward-prevention surface promoted to a Wave-22 hard gate.
//
// Per godlike/06 SSOT, internal/api/ is a transport-only package. Any
// import of github.com/Marcuss-ops/PipelineGen/internal/infrastructure/
// from internal/api/ creates a godlike/07 NO-FAKE-AVAILABILITY regression
// (transport-layer callers cannot depend on infrastructure-level
// concrete types without bypassing the canonical Publisher / Repository
// adapters). The gate bans such imports; an allowlist at
// docs/migrations/api-infrastructure-imports-allowlist.txt carries
// exceptional cases with owner + deadline (godlike/06 SSOT-marker).
package boundaries

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// apiInfraImportBanned is the substring that flags a violation.
// Anchored on the full canonical GitHub import path so shorthand
// imports like `pginfra "internal/infrastructure"` (which do not
// appear in the codebase today) are NOT caught — godlike/06 says
// only the canonical import path is the SSOT; a shorthand alias
// would be in itself a godlike/06 SSOT violation caught elsewhere.
const apiInfraImportBanned = "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/"

// apiInfraImportAllowlistFile is the on-disk SSOT for grandfathered
// imports. Path is repo-relative (resolved against the scan root).
// Each non-comment, non-blank line is ONE repo-relative Go file path
// (slash-separated) whose banned import is exceptionally permitted.
// Empty by default; operators add entries with owner + deadline.
const apiInfraImportAllowlistFile = "docs/migrations/api-infrastructure-imports-allowlist.txt"

// ScanAPIInfrastructureImports walks <root>/internal/api/** non-test
// .go files and reports any line that contains
// apiInfraImportBanned unless the file is listed in
// docs/migrations/api-infrastructure-imports-allowlist.txt.
//
// Symmetric comparison (godlike/07 zero-baseline rule): both
// non-allowlisted imports AND stale allowlist entries with no matching
// import trip the gate. A missing allowlist file emits a single
// fail-closed SeverityError violation under rule id
// `percheck_api_infrastructure_imports_allowlist_missing` (the runner
// escalation pass promotes it to a Phase-N hard gate).
//
// Mirror of scripts/ci/architecture/checks/check_19_api_infrastructure_imports.sh:
// the bash check is RETAINED as the canonical gate (godlike/08
// transitional baseline rule); the Go scanner runs in parallel and is
// promoted to the Wave-22 hard gate as the on-default forward-prevention
// surface. Both must exit 0 for CI to be green. The shell check's
// canonical error signature ("forbidden infrastructure imports detected
// in API layer") is preserved bit-for-bit for the e2e harness
// (scripts/ci-archcheck-e2e.sh).
func ScanAPIInfrastructureImports(root string, pol *policy.Policy, r *report.Report) {
	apiDir := filepath.Join(root, "internal", "api")
	if _, err := os.Stat(apiDir); err != nil {
		// internal/api missing — no surface to scan. Do NOT trip
		// the gate; this is the Day-0 baseline before the api/
		// subzone split is finished. The
		// percheck_unknown_internal_roots family covers the
		// "root shape is wrong" case; this percheck only fires
		// when there IS an api/ surface to police.
		return
	}

	allowlistPath := filepath.Join(root, apiInfraImportAllowlistFile)
	allowlist := map[string]bool{}
	if _, err := os.Stat(allowlistPath); err != nil {
		// Fail-closed: missing allowlist is a godlike/07
		// NO-FAKE-AVAILABILITY regression — operators cannot
		// audit grandfathered imports without the canonical
		// file in tree. Emit a single SeverityError violation
		// under rule `percheck_api_infrastructure_imports_allowlist_missing`
		// so the Wave-22 hard-gate promotion escalates it.
		r.Violations = append(r.Violations, report.Violation{
			File:        apiInfraImportAllowlistFile,
			MatchedRule: "api_infra_import_allowlist_missing",
			Rule:        "percheck_api_infrastructure_imports_allowlist_missing",
			Severity:    string(report.SeverityError),
			Note:        "fail-closed: api-infrastructure-imports allowlist is missing or unreadable — godlike/07 NO-FAKE-AVAILABILITY: operators cannot audit grandfathered imports without the canonical file in tree; commit an empty file or restore the canonical surfaces and rerun the percheck",
		})
		// Continue scanning the api/ tree so a missing allowlist
		// does not mask hits — both classes of failure should
		// surface in the same report run.
	} else {
		allowlist = loadAPIInfraAllowlist(allowlistPath)
	}

	type apiFile struct {
		rel      string
		bannedAt []int
	}
	found := map[string]*apiFile{}

	skipDirs := map[string]bool{
		".git": true, "vendor": true, "node_modules": true,
		"node-scraper": true, "examples": true, "scripts": true,
		"docs": true, "testdata": true,
	}
	_ = filepath.WalkDir(apiDir, func(path string, d os.DirEntry, err error) error {
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
		rel, _ := filepath.Rel(root, path)
		relSlash := filepath.ToSlash(rel)
		hits := scanAPIInfraImportFile(path, apiInfraImportBanned)
		if len(hits) > 0 {
			found[relSlash] = &apiFile{rel: relSlash, bannedAt: hits}
		}
		return nil
	})

	for rel, f := range found {
		if allowlist[rel] {
			continue
		}
		for _, line := range f.bannedAt {
			r.Violations = append(r.Violations, report.Violation{
				File:        rel,
				Line:        line,
				MatchedRule: "api_infra_import_via_internal_infra",
				Rule:        "percheck_api_infrastructure_imports",
				Severity:    string(report.SeverityWarn),
				Note:        "forbidden infrastructure import in the API transport layer (" + apiInfraImportBanned + ") — route the dependency through a port in internal/application/ and inject it at the composition root (internal/app/), or add the file path to " + apiInfraImportAllowlistFile + " with owner + deadline per godlike/06 SSOT-marker discipline",
			})
		}
	}

	// Stale allowlist entries: symptom of the zero-baseline drift
	// (godlike/08). Each entry MUST mirror exactly one current
	// infra-import in the api/ tree. Dangling entries mask future
	// regressions by hiding what the operator committed to audit.
	for rel := range allowlist {
		if _, ok := found[rel]; ok {
			continue
		}
		r.Violations = append(r.Violations, report.Violation{
			File:        apiInfraImportAllowlistFile,
			MatchedRule: "api_infra_import_allowlist_stale",
			Rule:        "percheck_api_infrastructure_imports_allowlist_stale",
			Severity:    string(report.SeverityWarn),
			Note:        "stale allowlist entry; file " + rel + " no longer imports " + apiInfraImportBanned + " — remove from " + apiInfraImportAllowlistFile + " (godlike/08 zero-baseline rule: allowlist entries must exactly mirror the codebase)",
		})
	}
}

// loadAPIInfraAllowlist reads the allowlist file into a set of
// repo-relative Go file paths. Lines starting with `#` (after trim)
// and blank lines are skipped. The reader does NOT validate that
// listed paths exist on disk; staleness is surfaced separately
// (symmetric comparison, zero-baseline rule).
func loadAPIInfraAllowlist(path string) map[string]bool {
	out := map[string]bool{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out[filepath.ToSlash(line)] = true
	}
	return out
}

// scanAPIInfraImportFile returns the line numbers of every line in
// `path` that contains the banned substring. Comment-only hits are
// returned too (residue accounting is the WARN bucket's job, not
// this scanner's); severe hits are escalated by the runner's
// hard-gate promotion when rule id is in pol.HardGates.
func scanAPIInfraImportFile(path, banned string) []int {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	hits := []int{}
	lineNum := 0
	for sc.Scan() {
		lineNum++
		if strings.Contains(sc.Text(), banned) {
			hits = append(hits, lineNum)
		}
	}
	return hits
}
