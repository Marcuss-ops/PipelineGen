package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

func main() {
	ratchet := flag.Bool("ratchet", false, "run the ratchet architectural gate (allowlist + baseline)")
	futureRatchet := flag.Bool("future-ratchet", false, "additionally run Phase 0 PR-A baseline-on-baseline rules (grace cycle before promotion to required)")
	seedBaseline := flag.Bool("seed-baseline", false, "explicitly seed scripts/archcheck/phase0_baseline.json from current actual state and exit 0 (operator-only; intended once per minor cycle at PR-A bootstrapping)")
	flag.Parse()

	if *seedBaseline {
		// Operator-only path: write a fresh baseline from current rg
		// actuals and exit 0. Independent of --ratchet / --future-ratchet
		// because it is a snapshot op, not a check op. If rg is missing
		// in the environment the operator will see check-rule violations
		// in the seeded baseline — that's a signal to install rg locally
		// and re-run rather than commit a poisoned baseline.
		if _, err := os.Stat(phase0BaselinePath); err == nil {
			fmt.Fprintf(os.Stderr, "archcheck: seed-baseline refused: %s already exists; remove it first or use --ratchet --future-ratchet to compare\n", phase0BaselinePath)
			os.Exit(2)
		}
		_, err := phase0SeedBaseline()
		if err != nil {
			fmt.Fprintf(os.Stderr, "archcheck: seed-baseline failed: %v\n", err)
			os.Exit(2)
		}
		fmt.Fprintf(os.Stdout, "{\"seeded\":%q,\"path\":%q}\n", time.Now().UTC().Format(time.RFC3339), phase0BaselinePath)
		os.Exit(0)
	}

	var report Report
	if *ratchet {
		report = runRatchetChecks()
	} else {
		report = runFocusedChecks()
	}
	if *futureRatchet {
		phase0Stats, phase0Violations := runPhase0Checks()
		for k, v := range phase0Stats {
			report.Checks[k] = v
		}
		report.Violations = append(report.Violations, phase0Violations...)
		report.Passed = report.Passed && len(phase0Violations) == 0
		report.Mode = report.Mode + "-future-ratchet"
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		fmt.Fprintf(os.Stderr, "archcheck: encode report: %v\n", err)
		os.Exit(2)
	}

	if !report.Passed {
		os.Exit(1)
	}
}

// Report is the JSON contract for scripts/archcheck consumers.
type Report struct {
	Passed            bool           `json:"passed"`
	FocusedGatePassed bool           `json:"focused_gate_passed,omitempty"`
	Mode              string         `json:"mode"`
	Commit            string         `json:"commit"`
	LegacyBudget      int            `json:"legacy_budget,omitempty"`
	Checks            map[string]int `json:"checks"`
	Violations        []string       `json:"violations"`
}

func runFocusedChecks() Report {
	checks := map[string]int{}
	violations := []string{}

	apiStats, apiViolations := checkAPIInfrastructureImports()
	checks["api_infrastructure_imports"] = apiStats["violations"]
	checks["api_infrastructure_imports_actual"] = apiStats["actual"]
	checks["api_infrastructure_imports_allowed"] = apiStats["allowed"]
	checks["api_infrastructure_allowlist_stale"] = apiStats["stale"]
	violations = append(violations, apiViolations...)

	yamlVerifiedOK, yamlVerifiedTotal, yamlViolations := checkMigrationYAML()
	checks["migration_yaml_done_waves_total"] = yamlVerifiedTotal
	checks["migration_yaml_done_waves_with_verified_zero_true"] = yamlVerifiedOK
	violations = append(violations, yamlViolations...)

	ownershipMissing, ownershipViolations := checkOwnershipYAML()
	checks["ownership_yaml_missing_paths"] = ownershipMissing
	violations = append(violations, ownershipViolations...)

	pythonWriterViolations, pythonWriterFindings := checkPythonLegacyWriterGate()
	checks["python_legacy_writer_violations"] = pythonWriterViolations
	violations = append(violations, pythonWriterFindings...)

	// Wave 19 (PR1 — observation only): surface capability-direction
	// edge counts. NO violations emitted (would break the active
	// Wave 14-18 ratchet until the baseline is agreed). PR2+ will
	// promote the counts to hard gates via allowlist-based subtraction.
	atiStats, atiViolations := checkApplicationToInfrastructure()
	checks["application_to_infrastructure_files"] = atiStats["actual"]
	violations = append(violations, atiViolations...)

	cciStats, cciViolations := checkCrossCapabilityImport()
	checks["cross_capability_import_pairs"] = cciStats["actual"]
	violations = append(violations, cciViolations...)

	return Report{
		Passed:            len(violations) == 0,
		FocusedGatePassed: len(violations) == 0,
		Mode:              "focused",
		Commit:            "ci/archcheck-hard-fail",
		Checks:            checks,
		Violations:        violations,
	}
}

func runRatchetChecks() Report {
	checks := map[string]int{}
	violations := []string{}

	apiStats, apiViolations := checkAPIInfrastructureImports()
	checks["api_infrastructure_imports"] = apiStats["violations"]
	checks["api_infrastructure_imports_actual"] = apiStats["actual"]
	checks["api_infrastructure_imports_allowed"] = apiStats["allowed"]
	checks["api_infrastructure_allowlist_stale"] = apiStats["stale"]
	violations = append(violations, apiViolations...)

	sqlStats, sqlViolations := checkDatabaseSQLGate()
	checks["database_sql_actual"] = sqlStats["actual"]
	checks["database_sql_baseline"] = sqlStats["baseline"]
	checks["database_sql_regressions"] = sqlStats["regressions"]
	violations = append(violations, sqlViolations...)

	yamlVerifiedOK, yamlVerifiedTotal, yamlViolations := checkMigrationYAML()
	checks["migration_yaml_done_waves_total"] = yamlVerifiedTotal
	checks["migration_yaml_done_waves_with_verified_zero_true"] = yamlVerifiedOK
	violations = append(violations, yamlViolations...)

	ownershipMissing, ownershipViolations := checkOwnershipYAML()
	checks["ownership_yaml_missing_paths"] = ownershipMissing
	violations = append(violations, ownershipViolations...)

	pythonWriterViolations, pythonWriterFindings := checkPythonLegacyWriterGate()
	checks["python_legacy_writer_violations"] = pythonWriterViolations
	violations = append(violations, pythonWriterFindings...)

	// Wave 19 (PR1 — observation only). See runFocusedChecks for rationale.
	atiStats, atiViolations := checkApplicationToInfrastructure()
	checks["application_to_infrastructure_files"] = atiStats["actual"]
	violations = append(violations, atiViolations...)

	cciStats, cciViolations := checkCrossCapabilityImport()
	checks["cross_capability_import_pairs"] = cciStats["actual"]
	violations = append(violations, cciViolations...)

	return Report{
		Passed:            len(violations) == 0,
		FocusedGatePassed: len(violations) == 0,
		Mode:              "ratchet",
		Commit:            "ci/archcheck-hard-fail",
		LegacyBudget:      0,
		Checks:            checks,
		Violations:        violations,
	}
}

func checkAPIInfrastructureImports() (map[string]int, []string) {
	stats := map[string]int{
		"actual":     0,
		"allowed":    0,
		"stale":      0,
		"violations": 0,
	}
	out, err := exec.Command("rg", "-l",
		`github\.com/Marcuss-ops/PipelineGen/internal/infrastructure/`,
		"internal/api",
		"--glob", "*.go",
		"--glob", "!*_test.go",
	).Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
			actual := []string{}
			allowlist, allowErr := loadAllowlist("docs/migrations/api-infrastructure-imports-allowlist.txt")
			if allowErr != nil {
				return stats, []string{fmt.Sprintf("checkAPIInfrastructureImports: load allowlist: %v", allowErr)}
			}
			staleAllowlist := subtractSet(allowlist, actual)
			stats["allowed"] = len(allowlist)
			stats["stale"] = len(staleAllowlist)
			stats["violations"] = len(staleAllowlist)
			var violations []string
			for _, stale := range staleAllowlist {
				violations = append(violations, "stale allowlist entry with no matching API infrastructure import: "+stale)
			}
			return stats, violations
		}
		stats["violations"] = -1
		return stats, []string{fmt.Sprintf("checkAPIInfrastructureImports: rg failed: %v", err)}
	}
	actual := normalizePaths(splitNonEmpty(string(out)))

	allowlist, err := loadAllowlist("docs/migrations/api-infrastructure-imports-allowlist.txt")
	if err != nil {
		stats["violations"] = -1
		return stats, []string{fmt.Sprintf("checkAPIInfrastructureImports: load allowlist: %v", err)}
	}

	staleAllowlist := subtractSet(allowlist, actual)
	violations := subtractSet(actual, allowlist)
	for _, stale := range staleAllowlist {
		violations = append(violations, "stale allowlist entry with no matching API infrastructure import: "+stale)
	}
	sort.Strings(violations)
	stats["actual"] = len(actual)
	stats["allowed"] = len(allowlist)
	stats["stale"] = len(staleAllowlist)
	stats["violations"] = len(violations)
	return stats, violations
}

// checkApplicationToInfrastructure is the Wave 19 (June 2026) Portland
// import-edge counter for the operator-pasted "Dependency Rules" spec.
//
// Vocabulary mapping (PR1 does NOT rename directories — would invalidate
// the active Wave 14-18 ratchet):
//   - capabilities = internal/application/<cap>
//   - platform     = internal/infrastructure/<sub>
//   - kernel       = internal/domain/<x>
//   - app          = internal/app
//
// Target after Wave 15-18 hardening: 0. For the FIRST PR the function
// reports COUNTS in the `Checks` map and emits NO violations, so the
// CI stays GREEN while operators see the surface area before any
// hard-gate promotion (PR2+: violations fail ratchet unless the edge
// is allowlisted in docs/migrations/application-infrastructure-imports-allowlist.txt).
//
// The reverse direction (infrastructure -> application) is allowed
// (composition-only bridge — Wave 15 PR4d). The `cmd -> app ->
// capabilities -> kernel` direction is enforced by the existing
// rules (pkg_to_internal, domain_to_application, domain_to_infrastructure,
// application_to_api, application_to_database_sql).
func checkApplicationToInfrastructure() (map[string]int, []string) {
	stats := map[string]int{
		"actual":     0,
		"violations": 0,
	}
	out, err := exec.Command("rg", "-l",
		`"github\.com/Marcuss-ops/PipelineGen/internal/infrastructure/`,
		"internal/application",
		"--glob", "*.go",
		"--glob", "!*_test.go",
	).Output()
	if err != nil && !execErrIsNoMatch(err) {
		stats["violations"] = -1
		return stats, []string{fmt.Sprintf("checkApplicationToInfrastructure: rg failed: %v", err)}
	}
	actual := normalizePaths(splitNonEmpty(string(out)))
	stats["actual"] = len(actual)
	// PR1 observation-only: no violations emitted (would break active
	// Wave 14-18 ratchet). PR2 promotes the edge to hard-gate via
	// subtractSet(actual, allowlist) — see Wave 19 description in
	// architecture/current.yaml.
	return stats, nil
}

// checkCrossCapabilityImport is the Wave 19 (June 2026) edge counter
// for `internal/application/<capA>/` -> `internal/application/<capB>/`.
//
// Per the operator-pasted Dependency Rules spec, Section "Cross-capability
// communication": "Capability A must not import Capability B's
// transport, repository implementation, or internal service concrete."
// The preferred order is typed port > typed event > owned read model;
// direct cross-capability import is reserved for composition adapters.
//
// Target after Wave 19 hardening: 0 — except for a documented per-edge
// allowlist. PR1 reports COUNTS in the `Checks` map; no violations
// are emitted.
//
// Distinguish same-package from cross-package imports: a file at
// `internal/application/<capA>/x.go` importing
// `internal/application/<capA>/y` is a SAME-package reference (legal
// under any layout) and DOES NOT count. Only when the source file's
// capability differs from the imported capability does the edge
// contribute to the counter.
func checkCrossCapabilityImport() (map[string]int, []string) {
	stats := map[string]int{
		"actual":     0,
		"violations": 0,
	}
	out, err := exec.Command("rg", "-n",
		`github\.com/Marcuss-ops/PipelineGen/internal/application/`,
		"internal/application",
		"--type", "go",
		"--glob", "!*_test.go",
	).Output()
	if err != nil && !execErrIsNoMatch(err) {
		stats["violations"] = -1
		return stats, []string{fmt.Sprintf("checkCrossCapabilityImport: rg failed: %v", err)}
	}

	capabilities := applicationCapabilities()
	pairCount := 0
	seenPairs := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		// rg -n emits `path:linenum:matched_text`. SplitN with limit 3
		// gives us [path, linenum, content] — robust against path
		// strings that could in theory contain additional colons
		// (and a safer shape than strings.IndexByte for any future
		// rg output-format change). The "content" slice contains the
		// matched substring (the import string itself for our pattern).
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 3 {
			continue
		}
		src := parts[0]
		content := parts[2]
		srcCap := capabilityOfFile(src, capabilities)
		importCap := capabilityOfImport(content, capabilities)
		if srcCap == "" || importCap == "" {
			continue
		}
		if srcCap == importCap {
			continue
		}
		pairKey := srcCap + "->" + importCap
		if seenPairs[pairKey] {
			continue
		}
		seenPairs[pairKey] = true
		pairCount++
	}
	stats["actual"] = pairCount
	// PR1 observation-only (no violations). Operators can read the
	// pair count as a summary statistic; the full edge list (file-
	// level) is intentionally deferred to PR2 alongside the ratchet
	// baseline — see Wave 19 exit_gate in architecture/current.yaml.
	return stats, nil
}

// applicationCapabilities returns the set of well-known capability
// sub-package names under internal/application/. Used by
// checkCrossCapabilityImport to classify source files and imports.
//
// PR2 HOOK (do not forget when promoting this rule to hard gate):
// the current list is a HARDCODED SNAPSHOT of known capability
// directories. New capability directories added after this snapshot
// will be SILENTLY IGNORED (no error, no warning) until the list is
// re-rolled. PR2 must replace this map with one of:
//  1. os.ReadDir("internal/application") at archcheck startup (auto-
//     discover, immune to new capability dirs), OR
//  2. Compute the list from architecture/ownership.yaml::application_*
//     keys (single source of truth, requires ownership.yaml to have
//     one entry per capability — see ownership.yaml::application_assets
//     as the canonical shape).
//
// PR1 remains safe for now because the in_progress Wave 14-18 waves
// are the only ones adding/removing capabilities and we re-roll the
// list in lockstep with each wave.
func applicationCapabilities() map[string]bool {
	return map[string]bool{
		"assets":        true,
		"artlist":       true,
		"association":   true,
		"books":         true,
		"catalog":       true,
		"channels":      true,
		"clips":         true,
		"content":       true,
		"generation":    true,
		"images":        true,
		"ingest":        true,
		"jobs":          true,
		"lessons":       true,
		"mediasearch":   true,
		"middleware":    true,
		"monitor":       true,
		"realtime":      true,
		"scriptassets":  true,
		"scripts":       true,
		"searchqueries": true,
		"system":        true,
		"voiceover":     true,
		"youtube":       true,
	}
}

// capabilityOfFile returns the capability name for a Go file under
// `internal/application/<cap>/...`. Returns "" for files OUTSIDE that
// layout (e.g. architecture metadata, scripts directory).
func capabilityOfFile(relPath string, caps map[string]bool) string {
	const prefix = "internal/application/"
	if !strings.HasPrefix(relPath, prefix) {
		return ""
	}
	rest := relPath[len(prefix):]
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return "" // file directly in internal/application/ root
	}
	candidate := rest[:slash]
	if !caps[candidate] {
		return "" // unknown sub-package — classification is best-effort
	}
	return candidate
}

// capabilityOfImport returns the capability name for an import line
// shaped like `github.com/Marcuss-ops/PipelineGen/internal/application/<cap>/...`.
// Returns "" for non-matching imports.
func capabilityOfImport(importLine string, caps map[string]bool) string {
	const marker = "github.com/Marcuss-ops/PipelineGen/internal/application/"
	idx := strings.Index(importLine, marker)
	if idx < 0 {
		return ""
	}
	rest := importLine[idx+len(marker):]
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return "" // import of internal/application/ root (no Go file there, but defensive)
	}
	candidate := rest[:slash]
	if !caps[candidate] {
		return ""
	}
	return candidate
}

func checkDatabaseSQLGate() (map[string]int, []string) {
	stats := map[string]int{
		"actual":      0,
		"baseline":    len(databaseSQLLegacyBaseline),
		"regressions": 0,
	}
	out, err := exec.Command("rg", "-ln", `"database/sql"`,
		"internal/api",
		"internal/application",
		"internal/domain",
		"--type", "go",
	).Output()
	if err != nil && !(execErrIsNoMatch(err)) {
		stats["regressions"] = -1
		return stats, []string{fmt.Sprintf("checkDatabaseSQLGate: rg failed: %v", err)}
	}

	actual := normalizePaths(splitNonEmpty(string(out)))
	baseline := normalizePaths(databaseSQLLegacyBaseline)
	added := subtractSet(actual, baseline)
	removed := subtractSet(baseline, actual)
	stats["actual"] = len(actual)
	stats["regressions"] = len(added)

	var violations []string
	for _, path := range added {
		violations = append(violations, "new database/sql import in api/application/domain: "+path)
	}
	if len(removed) > 0 {
		stats["baseline"] = len(baseline) - len(removed)
	}
	return stats, violations
}

func execErrIsNoMatch(err error) bool {
	if err == nil {
		return false
	}
	if exit, ok := err.(*exec.ExitError); ok {
		return exit.ExitCode() == 1
	}
	return false
}

func loadAllowlist(path string) ([]string, error) {
	text, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w (allowlist file is required; see docs/migrations/api-infrastructure-imports-allowlist.txt)", path, err)
	}
	var out []string
	for _, line := range strings.Split(string(text), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		out = append(out, trimmed)
	}
	sort.Strings(out)
	return normalizePaths(out), nil
}

func splitNonEmpty(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if t != "" {
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

func normalizePaths(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	var out []string
	for _, p := range paths {
		norm := filepath.ToSlash(strings.TrimSpace(p))
		if norm == "" || seen[norm] {
			continue
		}
		seen[norm] = true
		out = append(out, norm)
	}
	sort.Strings(out)
	return out
}

func subtractSet(actual, allowed []string) []string {
	allowedSet := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		allowedSet[a] = true
	}
	var diff []string
	for _, a := range actual {
		if !allowedSet[a] {
			diff = append(diff, a)
		}
	}
	return diff
}

// databaseSQLLegacyBaseline captures the pre-gate db/sql surface that still
// exists in api/application/domain and is being shrunk incrementally.
var databaseSQLLegacyBaseline = []string{
	"internal/api/common/health_integration_test.go",
	"internal/api/middleware/middleware_auth_test.go",
	"internal/application/assets/artifacts/clips_adapter.go",
	"internal/application/assets/artifacts/finalizer_test.go",
	"internal/application/assets/artifacts/repository.go",
	"internal/application/assets/artifacts/resolvers/resolvers.go",
	"internal/application/assets/ingest/adapter_clip.go",
	"internal/application/assets/maintenance/deep_cleanup.go",
	"internal/application/assets/maintenance/run_cleanup.go",
	"internal/application/assets/maintenance/service.go",
	"internal/application/assets/monitor/channel_monitor.go",
	"internal/application/assets/providers/artlist/assetrepo_integration_test.go",
	"internal/application/assets/providers/artlist/search_cache.go",
	"internal/application/assets/providers/artlist/service.go",
	"internal/application/assets/providers/artlist/service_test.go",
	"internal/application/books/service.go",
	"internal/application/images/google_generate.go",
	"internal/application/jobs/outbox/delivery.go",
	"internal/application/jobs/outbox/metadata_export.go",
	"internal/application/jobs/outbox/registry.go",
	"internal/application/jobs/service_test.go",
	"internal/application/scripts/batch_persistence_test.go",
	"internal/application/scripts/gemmamemory/gemmamemory.go",
	"internal/application/scripts/gemmamemory/stub_test.go",
	"internal/application/voiceover/groups_resolver_test.go",
	"internal/application/voiceover/service.go",
	"internal/application/youtube/assetrepo_integration_test.go",
	"internal/domain/asset/assets.go",
	"internal/domain/asset/dedup.go",
	"internal/domain/asset/list_clips.go",
	"internal/domain/asset/locations.go",
	"internal/domain/asset/processing.go",
	"internal/domain/asset/scan.go",
	"internal/domain/asset/store_core.go",
	"internal/domain/asset/tags.go",
	"internal/domain/asset/utility.go",
	"internal/domain/asset/versions.go",
}

// checkMigrationYAML validates that every `status: done` wave in
// architecture/current.yaml carries `verified_zero: true`.
//
// architecture/current.yaml is a YAML 1.2 multi-document stream
// (June 2026 PR —YAML-SEP): doc #1 = wave sequence
// (- id: 0, 14, 15, 16, 17, 18), doc #2 = post_cascade_followups,
// doc #3 = legacy_fallback_cleanup. Documents are separated by
// `---` markers at column 0. The text scanner below intentionally
// ignores `---` (topLevelWaveBlocks only matches lines that start
// with `- id:`, which only doc #1 carries; doc #2/#3 are mappings
// without sequence-item markers). Future YAML-library consumers must
// use yaml.NewDecoder(...).Decode() in a loop to read all 3 docs —
// yaml.Unmarshal only returns doc #1 (PyYAML safe_load_all returns
// all 3).
func checkMigrationYAML() (verifiedOK int, total int, violations []string) {
	const migPath = "architecture/current.yaml"
	text, err := os.ReadFile(migPath)
	if err != nil {
		return -1, 0, []string{fmt.Sprintf("checkMigrationYAML: read %s: %v", migPath, err)}
	}
	total, violations = scanYAML(string(text))
	verifiedOK = total - len(violations)
	return verifiedOK, total, violations
}

var subwavePattern = regexp.MustCompile(`^\s*-\s+id:\s+\S+`)

func scanYAML(raw string) (int, []string) {
	var (
		doneTotal  int
		violations []string
	)
	for _, b := range topLevelWaveBlocks(raw) {
		var idv, status, verified string
		for _, line := range strings.Split(b, "\n") {
			if idv != "" && subwavePattern.MatchString(line) {
				break
			}
			tabSplit := strings.SplitN(strings.TrimRight(line, "\r"), ":", 2)
			if len(tabSplit) != 2 {
				continue
			}
			key := strings.TrimSpace(tabSplit[0])
			val := strings.TrimSpace(tabSplit[1])
			switch key {
			case "id":
				if idv == "" {
					idv = val
				}
			case "status":
				if status == "" {
					status = val
				}
			case "verified_zero":
				if verified == "" {
					verified = val
				}
			}
		}
		if status != "done" {
			continue
		}
		doneTotal++
		if verified != "true" {
			verifStr := verified
			if verifStr == "" {
				verifStr = "missing"
			}
			violations = append(violations, fmt.Sprintf("wave id=%s has status=done but verified_zero=%s", idv, verifStr))
		}
	}
	sort.Strings(violations)
	return doneTotal, violations
}

func topLevelWaveBlocks(raw string) []string {
	var blocks []string
	var current strings.Builder
	inBlock := false
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "- id:") {
			if inBlock {
				blocks = append(blocks, current.String())
				current.Reset()
			}
			inBlock = true
		}
		if inBlock {
			current.WriteString(line)
			current.WriteString("\n")
		}
	}
	if inBlock {
		blocks = append(blocks, current.String())
	}
	return blocks
}

var ownershipPathPattern = regexp.MustCompile(`(?m)^\s+(?:owner|location):\s+([^#\n]+)`)

func checkOwnershipYAML() (int, []string) {
	const path = "architecture/ownership.yaml"
	text, err := os.ReadFile(path)
	if err != nil {
		return -1, []string{fmt.Sprintf("checkOwnershipYAML: read %s: %v", path, err)}
	}
	var violations []string
	for _, match := range ownershipPathPattern.FindAllStringSubmatch(string(text), -1) {
		ref := strings.TrimSpace(match[1])
		ref = strings.Trim(ref, `"'`)
		if ref == "" || strings.HasPrefix(ref, "/") {
			continue
		}
		for _, part := range strings.Split(ref, " + ") {
			checkOwnershipRef(strings.TrimSpace(part), &violations)
		}
	}
	sort.Strings(violations)
	return len(violations), violations
}

// ─────────────────────────────────────────────────────────────────────────────
// Phase 0 (PR-A, June 2026) — `--future-ratchet` baseline-on-baseline rules.
//
// PR-A extends the archcheck tool with five new rules that enforce the
// North Star "Compiler-enforced boundaries" invariant
// (`docs/architecture/godlike/01_NORTH_STAR.md`):
//
//  1. checkInterfaceBraceGrowth — production Go code that declares
//     fields / parameters / return types of bare `interface{}` or `any`.
//     North Star: "`interface{}`, broad `any`, runtime type assertions,
//     dependency setters, pass-through wrappers, and cross-capability
//     imports are treated as architecture debt."
//
//  2. checkSetterDetector — post-construction `Set<X>` methods on
//     Service / Client / Builder / Cfg / Adapter types (the canonical
//     North Star pattern-0 violation; PR-B removed `SetReranker` and
//     `SetVLLMConfig` per this rule).
//
//  3. checkTypeAliasCrossPkg — `type X = pkg.Y` aliases that cross
//     package boundaries. North Star calls these "pass-through aliases"
//     that hide real architectural debt.
//
//  4. checkFakeRoute — handler methods whose MountGin body returns
//     `http.StatusNotImplemented` (501) — the canonical "fake route"
//     marked as architecture debt in the godlike `Block 1 — Dead HTTP
//     surfaces` (see `docs/architecture/godlike/14_INITIAL_BACKLOG.md`).
//
//  5. checkHandlerToDB — handler files in `internal/api/**/handler*.go`
//     that reach into `database/sql` directly or hold `*sql.DB` fields.
//     North Star pattern 8: "API package: thin transport only".
//
// Lifecycle: during the "minor cycle", the rules run via
// `--future-ratchet` and fail ONLY on regressions (new entries vs the
// committed baseline file `scripts/archcheck/phase0_baseline.json`).
// After the cycle, the operator promotes the rules by removing the
// flag from `scripts/ci-architectural-checks.sh` and folding them into
// `runRatchetChecks()` with the existing baseline file as the strict
// gate (zero growth tolerated).
//
// Promotion checklist (separate follow-up PR):
//   1. Delete the `--future-ratchet` flag from main.go.
//   2. Move runPhase0Checks() invocation into runRatchetChecks().
//   3. Update docs/architecture/godlike/14_INITIAL_BACKLOG.md — mark
//      "Block 1 — Dead HTTP surfaces" + the 5 Phase 0 rules as
//      verified_zero: true.
// ─────────────────────────────────────────────────────────────────────────────

// phase0BaselinePath is the committed monotone-decreasing baseline for
// the 5 Phase 0 rules. Each rule reads only its own section; the file
// is regenerated by the operator-only `--seed-baseline` flag (see main()
// dispatch). The tool does NOT auto-write the baseline: missing-file
// cases are surfaced as a hard-error violation in runPhase0Checks().
const phase0BaselinePath = "scripts/archcheck/phase0_baseline.json"

// phase0Baseline is the JSON shape of scripts/archcheck/phase0_baseline.json.
// Each section is a list of rg-shaped strings (path or path:line:text),
// normalized and sorted. The `_meta` block records when the baseline
// was last regenerated; it is informational only.
type phase0Baseline struct {
	InterfaceBraces     []string `json:"interface_braces"`
	Setters             []string `json:"setters"`
	TypeAliasesCrossPkg []string `json:"type_aliases_cross_pkg"`
	FakeRoutes          []string `json:"fake_routes"`
	HandlersToDB        []string `json:"handlers_to_db"`
	Meta                struct {
		GeneratedAtRFC3339 string `json:"generated_at"`
		Note               string `json:"note,omitempty"`
	} `json:"_meta"`
}

// runPhase0Checks runs the 5 Phase 0 rules and compares against the
// committed baseline. It returns stats and the violation list (which
// during the minor cycle contains only REGRESSIONS — new entries vs
// the committed baseline, NOT existing entries). If the baseline file
// is missing, the function returns a hard-error stack instructing the
// operator to run --seed-baseline (no auto-seed: the tool refuses to
// silently write a possibly-poisoned baseline if rg is missing or the
// environment is non-representative).
func runPhase0Checks() (map[string]int, []string) {
	checks := map[string]int{}
	violations := []string{}

	// Hard-error guard: the committed baseline file MUST exist before
	// --future-ratchet can run. PR-A deliberately does NOT auto-seed
	// because rg availability varies across environments — auto-seeding
	// a baseline that was computed against missing/broken rg would
	// silently turn future CI runs red once rg IS available and the
	// actual set pops above the poisoned baseline. The operator path
	// is explicit: `go run ./scripts/archcheck --seed-baseline` writes
	// a fresh baseline, then `git add` + commit. The hard-error below
	// is ONE multi-line string (consumer-agnostic; CI dashboards that
	// re-format, sort, or dedup violations will not lose leading
	// whitespace fragments).
	if _, err := os.Stat(phase0BaselinePath); os.IsNotExist(err) {
		return checks, []string{
			"phase0_baseline.json missing — PR-A bootstrap incomplete\n" +
				"  fix: go run ./scripts/archcheck --seed-baseline && git add scripts/archcheck/phase0_baseline.json && git commit\n" +
				"  ref: scripts/archcheck/main.go::phase0BaselinePath + scripts/ci-architectural-checks.sh header comment",
		}
	}

	// Standard ratchet compare: actual vs baseline, fail on regressions.
	baseline, err := loadPhase0Baseline()
	if err != nil {
		return checks, []string{fmt.Sprintf("phase0: load baseline: %v", err)}
	}

	ibActual, ibViolations, ibStats := checkInterfaceBraceGrowth(baseline.InterfaceBraces)
	for k, v := range ibStats {
		checks[k] = v
	}
	violations = append(violations, ibViolations...)

	sdActual, sdViolations, sdStats := checkSetterDetector(baseline.Setters)
	for k, v := range sdStats {
		checks[k] = v
	}
	violations = append(violations, sdViolations...)

	taActual, taViolations, taStats := checkTypeAliasCrossPkg(baseline.TypeAliasesCrossPkg)
	for k, v := range taStats {
		checks[k] = v
	}
	violations = append(violations, taViolations...)

	frActual, frViolations, frStats := checkFakeRoute(baseline.FakeRoutes)
	for k, v := range frStats {
		checks[k] = v
	}
	violations = append(violations, frViolations...)

	hdActual, hdViolations, hdStats := checkHandlerToDB(baseline.HandlersToDB)
	for k, v := range hdStats {
		checks[k] = v
	}
	violations = append(violations, hdViolations...)

	// Suppress unused-var warnings (the actual sets are not directly
	// needed inside the violation loop here; baseline compare already
	// surfaces both regressions AND dimension stats in `checks`).
	_, _, _, _, _ = ibActual, sdActual, taActual, frActual, hdActual
	return checks, violations
}

// ── Phase 0 rule 1: interface{} growth ────────────────────────────────────

// checkInterfaceBraceGrowth counts production-code occurrences of
// bare `interface{}` and broad `any` declared as a type on its own
// (NOT inside a generic instantiation like `List[T any]`). The regex
// is conservative: matches `\b\w+\s+(interface\{\}|any)\b` so it
// catches field declarations, parameter types, and return types, but
// ignores `any` as an english-prose token (the `\b\w+\s+` prefix
// requires a Go identifier immediately before).
func checkInterfaceBraceGrowth(baseline []string) (actual []string, violations []string, stats map[string]int) {
	stats = map[string]int{
		"phase0_interface_braces_actual":      0,
		"phase0_interface_braces_baseline":    len(baseline),
		"phase0_interface_braces_regressions": 0,
	}
	out, err := exec.Command("rg", "-n",
		`\b\w+\s+(interface\{\}|any)\b`,
		"internal", "pkg",
		"--type", "go",
		"--glob", "!*_test.go",
	).Output()
	if err != nil && !execErrIsNoMatch(err) {
		return actual, []string{fmt.Sprintf("checkInterfaceBraceGrowth: rg failed: %v", err)}, stats
	}
	actual = splitNonEmpty(strings.TrimRight(string(out), "\n"))
	stats["phase0_interface_braces_actual"] = len(actual)
	added := subtractSet(actual, baseline)
	stats["phase0_interface_braces_regressions"] = len(added)
	for _, line := range added {
		violations = append(violations, "phase0 interface{}/any growth: "+line)
	}
	sort.Strings(violations)
	return actual, violations, stats
}

// ── Phase 0 rule 2: setter detector ────────────────────────────────────────

// checkSetterDetector scans every production Go file for
// post-construction setter methods of the shape
// `func (x *Type) SetFoo(...)` or `func (x Type) SetFoo(...)`.
// The North Star invariant forbids setters on Service / Client /
// Builder / Cfg types; PR-B removed SetReranker and SetVLLMConfig as
// canonical examples. The rule is intentionally permissive (it counts
// EVERY Set<X> method, not just the typed-named ones) so the baseline
// can be audited holistically.
func checkSetterDetector(baseline []string) (actual []string, violations []string, stats map[string]int) {
	stats = map[string]int{
		"phase0_setters_actual":      0,
		"phase0_setters_baseline":    len(baseline),
		"phase0_setters_regressions": 0,
	}
	out, err := exec.Command("rg", "-n",
		`func\s+\(\w+\s+\*?\w+\)\s+Set[A-Z]\w*\(`,
		"internal", "pkg",
		"--type", "go",
		"--glob", "!*_test.go",
	).Output()
	if err != nil && !execErrIsNoMatch(err) {
		return actual, []string{fmt.Sprintf("checkSetterDetector: rg failed: %v", err)}, stats
	}
	actual = splitNonEmpty(strings.TrimRight(string(out), "\n"))
	stats["phase0_setters_actual"] = len(actual)
	added := subtractSet(actual, baseline)
	stats["phase0_setters_regressions"] = len(added)
	for _, line := range added {
		violations = append(violations, "phase0 dependency setter introduced: "+line)
	}
	sort.Strings(violations)
	return actual, violations, stats
}

// ── Phase 0 rule 3: type alias cross-package ───────────────────────────────

// checkTypeAliasCrossPkg detects `type X = pkg.Y` aliases whose
// `pkg.Y` source lives in a different Go package than the file's
// own. North Star calls these "pass-through aliases" that hide real
// architectural debt; PR-B cleaned up several (see commit d61068b3
// for the realtime / association alias removals).
func checkTypeAliasCrossPkg(baseline []string) (actual []string, violations []string, stats map[string]int) {
	stats = map[string]int{
		"phase0_type_aliases_cross_pkg_actual":      0,
		"phase0_type_aliases_cross_pkg_baseline":    len(baseline),
		"phase0_type_aliases_cross_pkg_regressions": 0,
	}
	out, err := exec.Command("rg", "-n",
		`^\s*type\s+\w+\s*=\s*[a-z][a-z0-9_]*\.[A-Z]\w*\s*$`,
		"internal", "pkg",
		"--type", "go",
	).Output()
	if err != nil && !execErrIsNoMatch(err) {
		return actual, []string{fmt.Sprintf("checkTypeAliasCrossPkg: rg failed: %v", err)}, stats
	}
	actual = splitNonEmpty(strings.TrimRight(string(out), "\n"))
	stats["phase0_type_aliases_cross_pkg_actual"] = len(actual)
	added := subtractSet(actual, baseline)
	stats["phase0_type_aliases_cross_pkg_regressions"] = len(added)
	for _, line := range added {
		violations = append(violations, "phase0 cross-package type alias: "+line)
	}
	sort.Strings(violations)
	return actual, violations, stats
}

// ── Phase 0 rule 4: fake route ────────────────────────────────────────────

// checkFakeRoute detects handler methods whose MountGin body returns
// `http.StatusNotImplemented` (501). The canonical "fake route" the
// godlike program forbids is documented in
// `docs/architecture/godlike/14_INITIAL_BACKLOG.md` (Block 1 — Dead
// HTTP surfaces). The pattern is intentionally narrow (501 only) so
// real `not implemented` errors in handlers returning 503/500 with
// full message remain unaffected; only the canonical "mount the route
// but never serve content" code shape is caught.
func checkFakeRoute(baseline []string) (actual []string, violations []string, stats map[string]int) {
	stats = map[string]int{
		"phase0_fake_routes_actual":      0,
		"phase0_fake_routes_baseline":    len(baseline),
		"phase0_fake_routes_regressions": 0,
	}
	out, err := exec.Command("rg", "-n",
		`c\.(JSON|String|AbortWithStatus|Status)\s*\(\s*http\.StatusNotImplemented`,
		"internal/api",
		"--type", "go",
	).Output()
	if err != nil && !execErrIsNoMatch(err) {
		return actual, []string{fmt.Sprintf("checkFakeRoute: rg failed: %v", err)}, stats
	}
	actual = splitNonEmpty(strings.TrimRight(string(out), "\n"))
	stats["phase0_fake_routes_actual"] = len(actual)
	added := subtractSet(actual, baseline)
	stats["phase0_fake_routes_regressions"] = len(added)
	for _, line := range added {
		violations = append(violations, "phase0 fake route (501) introduced: "+line)
	}
	sort.Strings(violations)
	return actual, violations, stats
}

// ── Phase 0 rule 5: handler-to-DB ──────────────────────────────────────────

// checkHandlerToDB scans every production Go file under `internal/api/`
// whose name matches `handler*.go` (mirror of the North Star Pattern
// 8 invariant: `internal/api/**` is thin transport only, never the
// owner of database writes). A file is flagged if its surface contains
// either a `database/sql` import OR a `*sql.DB` field-type substring.
//
// Files which are explicitly excluded from the gate: any path ending
// in `_test.go` (handled by the rg `--glob` filter) and any path whose
// filename starts with `health_integration_test.go` (test-typed files
// are already excluded, but the substring is repeated for clarity in
// the violation message).
func checkHandlerToDB(baseline []string) (actual []string, violations []string, stats map[string]int) {
	stats = map[string]int{
		"phase0_handlers_to_db_actual":      0,
		"phase0_handlers_to_db_baseline":    len(baseline),
		"phase0_handlers_to_db_regressions": 0,
	}
	out, err := exec.Command("rg", "-nl",
		`database/sql|\*sql\.DB`,
		"internal/api",
		"--type", "go",
		"--glob", "!*_test.go",
		"--glob", "!internal/api/common/*.go",
	).Output()
	if err != nil && !execErrIsNoMatch(err) {
		return actual, []string{fmt.Sprintf("checkHandlerToDB: rg failed: %v", err)}, stats
	}
	actual = normalizePaths(splitNonEmpty(string(out)))
	stats["phase0_handlers_to_db_actual"] = len(actual)
	added := subtractSet(actual, baseline)
	stats["phase0_handlers_to_db_regressions"] = len(added)
	for _, path := range added {
		violations = append(violations, "phase0 handler file reaches into database/sql: "+path)
	}
	sort.Strings(violations)
	return actual, violations, stats
}

// ── Baseline helpers ──────────────────────────────────────────────────────

// loadPhase0Baseline reads scripts/archcheck/phase0_baseline.json and
// returns the decoded struct. The baseline file MUST exist when this
// helper is called (the missing-file case is handled upstream in
// runPhase0Checks() with a hard-error rather than auto-seeding).
//
// Sanity check: any section that decodes as nil (e.g. a partial baseline
// where someone hand-edited the JSON and removed an array) is forced
// to []string{} so subtractSet() has a safe comparison side.
func loadPhase0Baseline() (phase0Baseline, error) {
	var b phase0Baseline
	text, err := os.ReadFile(phase0BaselinePath)
	if err != nil {
		return b, fmt.Errorf("read %s: %w", phase0BaselinePath, err)
	}
	if err := json.Unmarshal(text, &b); err != nil {
		return b, fmt.Errorf("decode %s: %w", phase0BaselinePath, err)
	}
	// Force any nil section to []string{} so subtractSet is safe.
	if b.InterfaceBraces == nil {
		b.InterfaceBraces = []string{}
	}
	if b.Setters == nil {
		b.Setters = []string{}
	}
	if b.TypeAliasesCrossPkg == nil {
		b.TypeAliasesCrossPkg = []string{}
	}
	if b.FakeRoutes == nil {
		b.FakeRoutes = []string{}
	}
	if b.HandlersToDB == nil {
		b.HandlersToDB = []string{}
	}
	return b, nil
}

// phase0SeedBaseline computes the current actual state of all 5 rules
// and writes a fresh scripts/archcheck/phase0_baseline.json. The
// returned struct mirrors the file content so callers can use it for
// stats accounting without re-reading the file.
func phase0SeedBaseline() (phase0Baseline, error) {
	var b phase0Baseline
	b.InterfaceBraces, _, _ = checkInterfaceBraceGrowth(nil)
	b.Setters, _, _ = checkSetterDetector(nil)
	b.TypeAliasesCrossPkg, _, _ = checkTypeAliasCrossPkg(nil)
	b.FakeRoutes, _, _ = checkFakeRoute(nil)
	b.HandlersToDB, _, _ = checkHandlerToDB(nil)
	b.Meta.GeneratedAtRFC3339 = time.Now().UTC().Format(time.RFC3339)
	b.Meta.Note = "Phase 0 (PR-A) baseline seeded by the operator via the `--seed-baseline` flag (operator-only; main() intercepts before the normal Mode dispatch). Promote-to-required follow-up PR will tighten this baseline to zero (or fold the 5 rules into runRatchetChecks() once the minor cycle ends)."
	marshalled, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return b, fmt.Errorf("encode seed baseline: %w", err)
	}
	marshalled = append(marshalled, '\n')
	if err := os.WriteFile(phase0BaselinePath, marshalled, 0644); err != nil {
		return b, fmt.Errorf("write seed baseline: %w", err)
	}
	return b, nil
}

func checkOwnershipRef(ref string, violations *[]string) {
	ref = strings.TrimSpace(strings.Trim(ref, `"'`))
	if ref == "" {
		return
	}
	if strings.Contains(ref, " ") && !strings.Contains(ref, "::") {
		return
	}
	if strings.Contains(ref, "(") || strings.Contains(ref, "{") || strings.Contains(ref, "[") {
		return
	}
	candidate := strings.SplitN(ref, "::", 2)[0]
	candidate = strings.TrimSuffix(candidate, "/")
	candidate = filepath.FromSlash(candidate)
	if candidate == "" {
		return
	}
	if strings.HasPrefix(filepath.ToSlash(candidate), "heyavatar/") {
		return
	}
	if _, err := os.Stat(candidate); err != nil {
		*violations = append(*violations, fmt.Sprintf("ownership.yaml references missing path: %s", ref))
	}
}

type pythonLegacyRule struct {
	Path     string
	Patterns []string
}

func checkPythonLegacyWriterGate() (int, []string) {
	rules := []pythonLegacyRule{
		{
			Path: "scripts/tools/sync_drive_qdrant.py",
			Patterns: []string{
				"sqlite3",
				"SentenceTransformer",
				"qdrant_client",
				"googleapiclient",
				"google.oauth2",
				"/collections/",
			},
		},
		{
			Path:     "scripts/services/embedding_server/text.py",
			Patterns: []string{"sqlite3"},
		},
		{
			Path:     "scripts/services/embedding_server/visual.py",
			Patterns: []string{"sqlite3"},
		},
		{
			Path:     "scripts/services/embedding_server/audio.py",
			Patterns: []string{"sqlite3"},
		},
	}

	var violations []string
	for _, rule := range rules {
		raw, err := os.ReadFile(rule.Path)
		if err != nil {
			violations = append(violations, fmt.Sprintf("python legacy writer gate: read %s: %v", rule.Path, err))
			continue
		}
		text := string(raw)
		for _, pattern := range rule.Patterns {
			if strings.Contains(text, pattern) {
				violations = append(violations,
					fmt.Sprintf("python legacy writer gate: %s contains prohibited pattern %q", rule.Path, pattern))
			}
		}
	}

	sort.Strings(violations)
	return len(violations), violations
}
