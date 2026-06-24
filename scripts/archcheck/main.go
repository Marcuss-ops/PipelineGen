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
)

func main() {
	strict := flag.Bool("strict", false, "run the strict architectural gate")
	flag.Parse()

	var report Report
	if *strict {
		report = runStrictChecks()
	} else {
		report = runFocusedChecks()
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

	return Report{
		Passed:            len(violations) == 0,
		FocusedGatePassed: len(violations) == 0,
		Mode:              "focused",
		Commit:            "ci/archcheck-hard-fail",
		Checks:            checks,
		Violations:        violations,
	}
}

func runStrictChecks() Report {
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

	return Report{
		Passed:            len(violations) == 0,
		FocusedGatePassed: len(violations) == 0,
		Mode:              "strict",
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
	"internal/api/middleware/middleware_logger.go",
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
// architecture/migration.yaml carries `verified_zero: true`.
func checkMigrationYAML() (verifiedOK int, total int, violations []string) {
	const migPath = "architecture/migration.yaml"
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
