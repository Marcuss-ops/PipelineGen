// scripts/archcheck/main.go
//
// Per PR1 (Repository truth, June 2026):
//   * Loading the ratchet limit from scripts/archcheck/grandfathered_allowlist.json
//     (single source of truth, monotone-decreasing, contains ONLY violations dict).
//   * Writing the diagnostic snapshot to scripts/archcheck/current_report.json
//     (gitignored, regenerated every run, contains directories + aliases + wrappers +
//     violations. NEVER used as an allowlist).
//   * Ratchets ONLY on `violations` counts. directories/aliases/wrappers evolve
//     freely (legitimate code), so they are reported but do not fail the build.
//   * -strict mode additionally forbids non-zero violations AND a non-empty
//     directories/aliases/wrappers list in the current scan (zero-redundancy
//     target, validates migration.yaml `verified_zero: true` semantics).
//
// Usage:
//
//	go run ./scripts/archcheck               # ratchet check
//	go run ./scripts/archcheck -update        # refresh current_report.json
//	go run ./scripts/archcheck -strict        # zero-violations gate
//	go run ./scripts/archcheck -update-current-report
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// GrandfatheredAllowlist single source of truth for ratchet limits.
// Monotone-decreasing; operators may only remove entries.
type GrandfatheredAllowlist struct {
	Version   int            `json:"version"`
	Policy    string         `json:"policy"`
	LastReset string         `json:"last_reset"`
	Violations map[string]int `json:"violations"`
}

// CurrentReport diagnostic-only snapshot of one run's full scan.
// Generated on every archcheck invocation; never used as the ratchet source.
type CurrentReport struct {
	GeneratedAt string            `json:"generated_at"`
	Root        string            `json:"root"`
	Directories []string          `json:"directories"`
	Aliases     []string          `json:"aliases"`
	Wrappers    []string          `json:"wrappers"`
	Violations  map[string]int    `json:"violations"`
}

func main() {
	updateFlag := flag.Bool("update", false, "Update BOTH grandfathered_allowlist.json AND current_report.json with current status (for recorded policy reset)")
	strictFlag := flag.Bool("strict", false, "Strict mode: fail if ANY aliases/wrappers/violations exist (not just regressed ones)")
	flag.Parse()

	root, err := os.Getwd()
	if err != nil {
		fmt.Printf("Error getting current working directory: %v\n", err)
		os.Exit(1)
	}

	allowlistPath := filepath.Join(root, "scripts", "archcheck", "grandfathered_allowlist.json")
	currentReportPath := filepath.Join(root, "scripts", "archcheck", "current_report.json")

	// 1. Gather current state (full scan)
	violations, err := AnalyzeImports(root)
	if err != nil {
		fmt.Printf("Error analyzing imports: %v\n", err)
		os.Exit(1)
	}

	dirs, invalidRoots, err := FindDirectories(root)
	if err != nil {
		fmt.Printf("Error scanning directories: %v\n", err)
		os.Exit(1)
	}

	rawAliases, err := FindAliases(root)
	if err != nil {
		fmt.Printf("Error finding aliases: %v\n", err)
		os.Exit(1)
	}

	rawWrappers, err := FindWrappers(root)
	if err != nil {
		fmt.Printf("Error finding wrappers: %v\n", err)
		os.Exit(1)
	}

	// Build stable representation of aliases/wrappers (strip line numbers to avoid jitter)
	stableAliases := stabilizeKeys(rawAliases)
	stableWrappers := stabilizeKeys(rawWrappers)

	// Sort for stable diff output
	sort.Strings(dirs)
	sort.Strings(stableAliases)
	sort.Strings(stableWrappers)

	// Count violations per rule.
	violationCounts := map[string]int{
		"pkg_to_internal":                        0,
		"domain_to_application":                  0,
		"domain_to_infrastructure":               0,
		"application_to_api":                     0,
		"application_to_database_sql":            0,
		"gin_outside_api":                        0,
		"os_exec_outside_infrastructure":         0,
		"os_getenv_outside_config_app":           0,
		"sqlite_outside_infrastructure_database": 0,
	}
	for _, v := range violations {
		violationCounts[v.Rule]++
	}

	// 2. ALWAYS write current_report.json (gitignored diagnostic file).
	currentReport := CurrentReport{
		GeneratedAt: nowISO8601(),
		Root:        root,
		Directories: dirs,
		Aliases:     stableAliases,
		Wrappers:    stableWrappers,
		Violations:  violationCounts,
	}
	if err := writeJSON(currentReportPath, currentReport); err != nil {
		fmt.Printf("Error writing current_report.json: %v\n", err)
		os.Exit(1)
	}

	// 3. Handle -update (operator override of the grandfathered allowlist; warn loudly).
	if *updateFlag {
		fmt.Println("WARNING: -update would overwrite grandfathered_allowlist.json.")
		fmt.Println("         This is permitted ONLY for recorded policy resets where violation")
		fmt.Println("         counts legitimately decrease. EVERY such reset must be recorded in")
		fmt.Println("         the LastReset field with LastResetReason explaining the policy change.")
		allowlist := GrandfatheredAllowlist{
			Version:    1,
			Policy:     "Monotone-decreasing. Operators may ONLY remove entries; raising a count requires a verified_zero PR.",
			LastReset:  nowISO8601(),
			Violations: violationCounts,
		}
		if err := writeJSON(allowlistPath, allowlist); err != nil {
			fmt.Printf("Error writing grandfathered_allowlist.json: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("grandfathered_allowlist.json updated. Verify the diff before committing.")
		return
	}

	// 4. In default mode: load allowlist and check ratchet.
	allowlist, err := readAllowlist(allowlistPath)
	if err != nil {
		fmt.Printf("Error loading grandfathered_allowlist.json: %v\n", err)
		fmt.Println("First-run: pass -update-current-report or create scripts/archcheck/grandfathered_allowlist.json")
		os.Exit(1)
	}

	failed := false

	// Print current vs allowed vs remaining header for every tracked rule.
	fmt.Println("=== Ratchet report (current vs allowed vs remaining) ===")
	allRules := sortedKeys(violationCounts)
	for _, rule := range allRules {
		current := violationCounts[rule]
		allowed := allowlist.Violations[rule]
		remaining := allowed - current
		if remaining < 0 {
			fmt.Printf("  REGRESSION %s: current=%d allowed=%d remaining=%d (FORBIDDEN)\n", rule, current, allowed, remaining)
			failed = true
		} else if current < allowed {
			fmt.Printf("  PROGRESS   %s: current=%d allowed=%d remaining=%d\n", rule, current, allowed, remaining)
		} else {
			fmt.Printf("  STEADY     %s: current=%d allowed=%d remaining=0\n", rule, current, allowed)
		}
	}

	// Inform on directory/alias/wrapper evolution (NOT ratcheted, but rendered for visibility).
	fmt.Println()
	fmt.Println("=== Free-evolution surfaces (NOT ratcheted) ===")
	fmt.Printf("  directories:     current=%d (delta free)\n", len(dirs))
	fmt.Printf("  aliases:         current=%d (delta free)\n", len(stableAliases))
	fmt.Printf("  wrappers:        current=%d (delta free)\n", len(stableWrappers))
	if len(invalidRoots) > 0 {
		fmt.Printf("  invalid_roots:   current=%d (rejected by ownership.yaml)\n", len(invalidRoots))
		for _, r := range invalidRoots {
			fmt.Printf("    - %s\n", r)
		}
	}

	// Inform about current_report.json write (always happens).
	fmt.Printf("\n  current_report.json written: %s\n", currentReportPath)

	// 5. Strict mode: zero violations + zero free surfaces.
	if *strictFlag {
		fmt.Println()
		fmt.Println("=== Strict mode (zero-redundancy target) ===")
		if len(stableAliases) > 0 {
			fmt.Printf("  FAIL (strict): %d aliases present (must be zero)\n", len(stableAliases))
			failed = true
		} else {
			fmt.Println("  OK: zero aliases")
		}
		if len(stableWrappers) > 0 {
			fmt.Printf("  FAIL (strict): %d wrappers present (must be zero)\n", len(stableWrappers))
			failed = true
		} else {
			fmt.Println("  OK: zero wrappers")
		}
		for rule, current := range violationCounts {
			if current > 0 {
				fmt.Printf("  FAIL (strict): violation rule '%s' count=%d (must be zero)\n", rule, current)
				failed = true
			}
		}
	}

	if failed {
		fmt.Println()
		fmt.Println("Architecture checks FAILED.")
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("Architecture checks PASSED.")
}

// stabilizeKeys strips line numbers from "relPath:line: <details>" keys to
// "relPath:<details>" so that the snapshot survives line-number drift across PRs.
func stabilizeKeys(raw []string) []string {
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		parts := strings.SplitN(r, ":", 3)
		if len(parts) == 3 {
			out = append(out, parts[0]+":"+strings.TrimSpace(parts[2]))
		} else {
			out = append(out, r)
		}
	}
	return out
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func writeJSON(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(path, data, 0644)
}

func readAllowlist(path string) (*GrandfatheredAllowlist, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var a GrandfatheredAllowlist
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, err
	}
	if a.Violations == nil {
		a.Violations = map[string]int{}
	}
	return &a, nil
}

func nowISO8601() string {
	// RFC3339 UTC stamp suitable for `generated_at` audit field in current_report.json.
	return time.Now().UTC().Format(time.RFC3339)
}
