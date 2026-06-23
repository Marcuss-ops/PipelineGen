package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
)

type Baseline struct {
	Directories []string       `json:"directories"`
	Aliases     []string       `json:"aliases"`
	Wrappers    []string       `json:"wrappers"`
	Violations  map[string]int `json:"violations"`
}

func main() {
	updateFlag := flag.Bool("update", false, "Update baseline file with current status")
	strictFlag := flag.Bool("strict", false, "Strict mode: fail if ANY violations exist (not just new ones)")
	flag.Parse()

	// Locate root path (which should be current working directory or parent of scripts)
	root, err := os.Getwd()
	if err != nil {
		fmt.Printf("Error getting current working directory: %v\n", err)
		os.Exit(1)
	}

	baselinePath := filepath.Join(root, "scripts", "archcheck", "baseline.json")

	// 1. Gather current state
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

	// Build stable representation of aliases and wrappers to avoid line-number jitter
	var stableAliases []string
	for _, a := range rawAliases {
		// e.g. "relPath:line: type alias Name" -> "relPath:type alias Name"
		parts := strings.SplitN(a, ":", 3)
		if len(parts) == 3 {
			stableAliases = append(stableAliases, parts[0]+":"+strings.TrimSpace(parts[2]))
		} else {
			stableAliases = append(stableAliases, a)
		}
	}

	var stableWrappers []string
	for _, w := range rawWrappers {
		parts := strings.SplitN(w, ":", 3)
		if len(parts) == 3 {
			stableWrappers = append(stableWrappers, parts[0]+":"+strings.TrimSpace(parts[2]))
		} else {
			stableWrappers = append(stableWrappers, w)
		}
	}

	// Count violations per rule
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

	// 2. Handle Update mode or missing baseline
	if *updateFlag || os.Getenv("UPDATE_BASELINE") == "true" {
		baseline := Baseline{
			Directories: dirs,
			Aliases:     stableAliases,
			Wrappers:    stableWrappers,
			Violations:  violationCounts,
		}
		data, err := json.MarshalIndent(baseline, "", "  ")
		if err != nil {
			fmt.Printf("Error marshaling baseline: %v\n", err)
			os.Exit(1)
		}
		err = ioutil.WriteFile(baselinePath, data, 0644)
		if err != nil {
			fmt.Printf("Error writing baseline: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Baseline updated successfully.")
		return
	}

	// 3. Load baseline
	baselineData, err := ioutil.ReadFile(baselinePath)
	if err != nil {
		if os.IsNotExist(err) {
			// Auto-initialize if it doesn't exist
			fmt.Println("Baseline file not found. Initializing...")
			baseline := Baseline{
				Directories: dirs,
				Aliases:     stableAliases,
				Wrappers:    stableWrappers,
				Violations:  violationCounts,
			}
			data, err := json.MarshalIndent(baseline, "", "  ")
			if err != nil {
				fmt.Printf("Error marshaling baseline: %v\n", err)
				os.Exit(1)
			}
			err = ioutil.WriteFile(baselinePath, data, 0644)
			if err != nil {
				fmt.Printf("Error writing baseline: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("Baseline file initialized.")
			return
		}
		fmt.Printf("Error reading baseline file: %v\n", err)
		os.Exit(1)
	}

	var baseline Baseline
	err = json.Unmarshal(baselineData, &baseline)
	if err != nil {
		fmt.Printf("Error unmarshaling baseline: %v\n", err)
		os.Exit(1)
	}

	failed := false

	// Check 1: Allowed root directories (Invalid Roots)
	if len(invalidRoots) > 0 {
		fmt.Println("Checking allowed internal roots...")
		// If they were not in baseline directories, fail
		baseDirsMap := make(map[string]bool)
		for _, d := range baseline.Directories {
			baseDirsMap[d] = true
		}
		for _, r := range invalidRoots {
			if !baseDirsMap[r] {
				fmt.Printf("FAIL: Disallowed internal root directory '%s' is not in baseline.\n", r)
				failed = true
			}
		}
	}

	// Check 2: New directories (strictly forbid any new directories)
	baseDirsMap := make(map[string]bool)
	for _, d := range baseline.Directories {
		baseDirsMap[d] = true
	}
	for _, d := range dirs {
		if !baseDirsMap[d] {
			fmt.Printf("FAIL: New directory detected that was not in baseline: %s\n", d)
			failed = true
		}
	}

	// Check 3: New aliases
	baseAliasesMap := make(map[string]bool)
	for _, a := range baseline.Aliases {
		baseAliasesMap[a] = true
	}
	for _, a := range stableAliases {
		if !baseAliasesMap[a] {
			fmt.Printf("FAIL: New alias detected that was not in baseline: %s\n", a)
			failed = true
		}
	}

	// Check 4: New wrappers
	baseWrappersMap := make(map[string]bool)
	for _, w := range baseline.Wrappers {
		baseWrappersMap[w] = true
	}
	for _, w := range stableWrappers {
		if !baseWrappersMap[w] {
			fmt.Printf("FAIL: New wrapper function detected that was not in baseline: %s\n", w)
			failed = true
		}
	}

	// Check 5: Ratchet violation counts (violations can only decrease or stay same)
	for rule, count := range violationCounts {
		baseCount := baseline.Violations[rule]
		if *strictFlag && count > 0 {
			fmt.Printf("FAIL (strict): Violations for rule '%s' must be zero but found %d\n", rule, count)
			for _, v := range violations {
				if v.Rule == rule {
					fmt.Printf("  - %s:%d: %s\n", v.File, v.Line, v.Message)
				}
			}
			failed = true
		} else if count > baseCount {
			fmt.Printf("FAIL: Violations for rule '%s' increased from %d to %d\n", rule, baseCount, count)
			// Print out the specific new violations for debugging
			for _, v := range violations {
				if v.Rule == rule {
					fmt.Printf("  - %s:%d: %s\n", v.File, v.Line, v.Message)
				}
			}
			failed = true
		} else if count < baseCount {
			fmt.Printf("INFO: Violations for rule '%s' decreased from %d to %d (Ratchet updated!)\n", rule, baseCount, count)
		}
	}

	// Strict mode: also check baseline itself is clean (zero legacy).
	// Runs BEFORE the exit gate so alias/wrapper/baseline checks
	// accumulate into the same `failed` flag and all strict-mode
	// failures are reported in a single run.
	if *strictFlag {
		if len(baseline.Aliases) > 0 {
			fmt.Printf("FAIL (strict): %d aliases still present in baseline (must be zero)\n", len(baseline.Aliases))
			for _, a := range baseline.Aliases {
				fmt.Printf("  - %s\n", a)
			}
			failed = true
		}
		if len(baseline.Wrappers) > 0 {
			fmt.Printf("FAIL (strict): %d wrappers still present in baseline (must be zero)\n", len(baseline.Wrappers))
			for _, w := range baseline.Wrappers {
				fmt.Printf("  - %s\n", w)
			}
			failed = true
		}
		for rule, baseCount := range baseline.Violations {
			if baseCount > 0 {
				fmt.Printf("FAIL (strict): Violations for rule '%s' are %d in baseline (must be zero)\n", rule, baseCount)
				failed = true
			}
		}
	}

	if failed {
		fmt.Println("Architecture checks FAILED.")
		os.Exit(1)
	}

	fmt.Println("Architecture checks PASSED.")
}
