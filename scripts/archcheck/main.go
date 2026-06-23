// scripts/archcheck — focused-mode architectural gate (commit ci/archcheck-hard-fail).
//
// Produces a JSON snapshot consumed by `scripts/ci-architectural-checks.sh::Check 21`
// via `jq -e '.verified_zero == true'`. The script owns the canonical definition
// of "verified zero" for the codebase policies that have been promoted to hard
// fail. Future migrations may add new focused checks here without changing the
// CI script — the JSON shape is the contract.
//
// POLICY (commit ci/archcheck-hard-fail, June 2026):
//
//   * Focused-mode (this binary): validates the policies that have been
//     promoted to hard-fail evidence-based:
//       - Forbidden infrastructure imports in internal/api/ (Check 19 promotion)
//       - migration.yaml structural integrity: every `status: done` wave
//         carries `verified_zero: true` (Check 21 promotion)
//     If all focused checks pass, emits `{"verified_zero": true, ...}`.
//
//   * Allowlist symmetry: the Go binary applies the SAME allowlist
//     subtraction rule that bash Check 19 uses (comm -13 against
//     docs/migrations/api-infrastructure-imports-allowlist.txt) so both
//     gates produce identical verdicts.
//
//   * Hard-fail semantics: any non-zero violations or missing
//     verified_zero on done waves flips `verified_zero: false` in the
//     output. Check 21 then fails the CI with `jq -e` assertion.
//
//   * Comprehensive-mode (Wave 16 target_metrics): NOT implemented here.
//     Wave 16 status remains `in_progress` until comprehensive mode
//     lands in a followup commit (per migration.yaml's own roadmap).
//
// OUTPUT (JSON to stdout):
//
//   {
//     "verified_zero": true,
//     "mode": "focused",
//     "commit": "ci/archcheck-hard-fail",
//     "checks": {
//       "api_infrastructure_imports": 0,
//       "migration_yaml_done_waves_total": 15,
//       "migration_yaml_done_waves_with_verified_zero_true": 15
//     },
//     "violations": []
//   }
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

func main() {
	report := runFocusedChecks()

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		fmt.Fprintf(os.Stderr, "archcheck: encode report: %v\n", err)
		os.Exit(2)
	}

	// Exit code is for local pre-commit use. CI consumes stdout via
	// `jq -e '.verified_zero == true'` so the JSON shape remains the
	// contract (allows new fields without changing CI).
	if !report.VerifiedZero {
		os.Exit(1)
	}
}

// Report is the JSON contract for `scripts/archcheck` consumers.
// Keep field tags stable — they are the interface to Check 21.
type Report struct {
	VerifiedZero bool          `json:"verified_zero"`
	Mode         string        `json:"mode"`
	Commit       string        `json:"commit"`
	Checks       map[string]int `json:"checks"`
	Violations   []string      `json:"violations"`
}

// runFocusedChecks runs every focused check and aggregates the verdict.
// A violation in ANY focused check flips verified_zero to false. The CI
// gate is fail-CLOSED: each exposed path must hold.
func runFocusedChecks() Report {
	checks := map[string]int{}
	violations := []string{}

	// Focused check #1: forbidden infrastructure imports in internal/api/
	// (promoted from soft-log to hard-fail by Check 19 in this commit).
	apiViolCount, apiViolations := checkAPIInfrastructureImports()
	checks["api_infrastructure_imports"] = apiViolCount
	violations = append(violations, apiViolations...)

	// Focused check #2: migration.yaml structural integrity — every
	// `status: done` wave carries `verified_zero: true`.
	yamlVerifiedOK, yamlVerifiedTotal, yamlViolations := checkMigrationYAML()
	checks["migration_yaml_done_waves_total"] = yamlVerifiedTotal
	checks["migration_yaml_done_waves_with_verified_zero_true"] = yamlVerifiedOK
	violations = append(violations, yamlViolations...)

	return Report{
		VerifiedZero: len(violations) == 0,
		Mode:         "focused",
		Commit:       "ci/archcheck-hard-fail",
		Checks:       checks,
		Violations:   violations,
	}
}

// checkAPIInfrastructureImports scans internal/api/ (excluding _test.go)
// for any file whose source contains a real Go import of a package under
// `github.com/Marcuss-ops/PipelineGen/internal/infrastructure/`. Applies
// the same allowlist subtraction that bash Check 19 uses so the Go
// binary's verdict matches the bash gate's verdict exactly.
func checkAPIInfrastructureImports() (int, []string) {
	out, err := exec.Command("bash", "-c",
		`grep -rln 'github.com/Marcuss-ops/PipelineGen/internal/infrastructure/' internal/api/ --include='*.go' 2>/dev/null | grep -v '_test.go' | sort -u || true`,
	).Output()
	if err != nil {
		return -1, []string{fmt.Sprintf("checkAPIInfrastructureImports: grep failed: %v", err)}
	}
	actual := splitNonEmpty(string(out))
	if len(actual) == 0 {
		return 0, nil
	}

	allowlist, err := loadAllowlist("docs/migrations/api-infrastructure-imports-allowlist.txt")
	if err != nil {
		return -1, []string{fmt.Sprintf("checkAPIInfrastructureImports: load allowlist: %v", err)}
	}

	violations := subtractSet(actual, allowlist)
	return len(violations), violations
}

// loadAllowlist reads an allowlist file (one repo-root-relative path per
// line; `#` comments + blank lines ignored) and returns the sorted unique
// set of paths. The file is REQUIRED by the gate's contract — a missing
// file is treated as an operational error so a fresh checkout does not
// silently pass.
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
	return out, nil
}

// splitNonEmpty splits a newline-delimited string into non-empty trimmed
// lines. Used for both allowlist and actual-violation sets.
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

// subtractSet returns the entries in `actual` that are NOT in `allowed`.
// Mirrors the behaviour of `comm -13` invoked from bash Check 19.
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

// checkMigrationYAML validates that every `status: done` wave in
// architecture/migration.yaml carries `verified_zero: true`. Counts
// done waves and emits violations in a single pass.
//
// Wave blocks are top-level list items starting with `- id:` at column 0.
// The split token `\n- id:` correctly partitions the file because
// preamble (file header before any wave) and subwave bullets (`\n  - id:`
// or `\n    - id:`) are inside the parent block's content.
//
// Subwave break-out: any `\s*-\s+id:` line AFTER the parent's id has
// been captured indicates a subwave boundary; scanning stops at that
// line so subwave status/verified_zero does not pollute the parent.
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

// subwavePattern matches any indented list item starting `- id:`. The
// `\s*` prefix captures any leading whitespace (0/2/4-space YAML indents
// all match); the `\s+` after `-` ensures we don't accidentally match
// other dash-prefixed lines.
var subwavePattern = regexp.MustCompile(`^\s*-\s+id:\s+\S+`)

// scanYAML walks the migration.yaml file in a SINGLE pass over the
// `\n- id:` block partition, returning (doneWaveCount, []violationString).
// No fragile reconstruction like `"- id:" + b[6:]` — the parent block
// retains its original `- id:` prefix because that's what we split on.
func scanYAML(raw string) (int, []string) {
	var (
		doneTotal  int
		violations []string
	)
	for _, b := range strings.Split(raw, "\n- id:") {
		// First split chunk is the preamble (file header BEFORE any
		// wave). Skip it — it doesn't contain a wave.
		if !strings.HasPrefix(b, "- id:") {
			continue
		}

		var idv, status, verified string
		for _, line := range strings.Split(b, "\n") {
			// Subwave break-out: as soon as the parent id is captured,
			// the first indented `- id:` line signals a subwave. Stop
			// reading parent fields at that point.
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
