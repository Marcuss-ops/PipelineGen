package main

import (
	"fmt"
	"sort"
)

type checkResult struct {
	Failures []string
	Warnings []string
}

// checkFindings is pure so the ratchet rule is testable without a repository.
// Only new offenders and growth are failures; improvements and removals are
// warnings that ask for a -write in the same change.
func checkFindings(current map[string]finding, base baseline, threshold int) checkResult {
	var result checkResult
	if base.Threshold != threshold {
		result.Failures = append(result.Failures, fmt.Sprintf("baseline threshold %d does not match requested threshold %d", base.Threshold, threshold))
	}
	for key, value := range current {
		budget, ok := base.Functions[key]
		switch {
		case !ok:
			result.Failures = append(result.Failures, fmt.Sprintf("new function above budget: %s = %d at %s:%d", key, value.Complexity, value.File, value.Line))
		case value.Complexity > budget:
			result.Failures = append(result.Failures, fmt.Sprintf("complexity grew: %s %d -> %d at %s:%d", key, budget, value.Complexity, value.File, value.Line))
		case value.Complexity < budget:
			result.Warnings = append(result.Warnings, fmt.Sprintf("stale baseline entry (improved): %s %d -> %d; run -write", key, budget, value.Complexity))
		}
	}
	for key, budget := range base.Functions {
		if _, ok := current[key]; !ok {
			result.Warnings = append(result.Warnings, fmt.Sprintf("stale baseline entry (gone or below threshold): %s = %d; run -write", key, budget))
		}
	}
	sort.Strings(result.Failures)
	sort.Strings(result.Warnings)
	return result
}
