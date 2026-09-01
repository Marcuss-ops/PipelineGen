// Package mediacert — report.go renders a Report as the human-readable
// certification block the `make verify-vidrush-semantic` target prints:
//
//	SCENE IDENTITY          PASS
//	SOURCE IMMUTABILITY     PASS
//	SEMANTIC PROFILES       5/5
//	ARTLIST RELEVANCE       5/5
//	ENTITY GROUNDING       15/15
//	IMAGE FANOUT           15/15
//	QUERY OWNERSHIP           0 errors
//	ASSET OWNERSHIP           0 errors
//	CROSS-SCENE REUSE         0
//	PROVIDER POLICY           0
//	CROSS CONTAMINATION        0
//
//	CERTIFIED=true
//
// The format is intentionally line-oriented so CI can grep it and the
// pre-push gate can fail closed on a non-`CERTIFIED=true` final line.
package mediacert

import (
	"fmt"
	"strings"
)

// HumanReport renders a Report as the certification block above. Violations
// are printed after the summary block, one per line, so the operator sees
// the root cause without a separate report file.
func HumanReport(r Report) string {
	var b strings.Builder
	for _, c := range r.Checks {
		b.WriteString(formatCheck(c))
		b.WriteByte('\n')
	}
	if fanout := aggregateFanout(r); fanout != "" {
		b.WriteString(fanout)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(fmt.Sprintf("CERTIFIED=%v\n", r.Certified))
	var violations []Violation
	for _, c := range r.Checks {
		violations = append(violations, c.Violations...)
	}
	if len(violations) > 0 {
		b.WriteString("\nVIOLATIONS\n")
		for _, v := range violations {
			b.WriteString(fmt.Sprintf("  - [%s] %s: %s\n", v.Rule, v.SegmentID, v.Detail))
		}
	}
	return b.String()
}

// formatCheck renders one check line. Boolean checks print PASS/FAIL; X/Y
// checks print the counts; error-count checks print "<n> errors" or "<n>".
func aggregateFanout(r Report) string {
	for _, c := range r.Checks {
		if c.Name == CheckImageFanout && c.TotalCount > 0 {
			return fmt.Sprintf("IMAGE FANOUT AGGREGATE      %d/%d", c.PassCount*3, c.TotalCount*3)
		}
	}
	return ""
}

func formatCheck(c CheckResult) string {
	name := padName(string(c.Name))
	if c.TotalCount == 1 {
		verdict := "FAIL"
		if c.Passed {
			verdict = "PASS"
		}
		if len(c.Violations) == 0 {
			return fmt.Sprintf("%s %s", name, verdict)
		}
		return fmt.Sprintf("%s %s (%d violations)", name, verdict, len(c.Violations))
	}
	if c.TotalCount == 0 {
		return fmt.Sprintf("%s %s", name, passFail(c.Passed))
	}
	return fmt.Sprintf("%s %d/%d", name, c.PassCount, c.TotalCount)
}

// padName left-justifies the check name in a 24-char field so the verdicts
// align vertically in the printed block.
func padName(name string) string {
	const width = 24
	if len(name) >= width {
		return name
	}
	return name + strings.Repeat(" ", width-len(name))
}

func passFail(p bool) string {
	if p {
		return "PASS"
	}
	return "FAIL"
}
