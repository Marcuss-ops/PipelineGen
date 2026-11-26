// Command cyclo-budget is the Go-native cyclomatic-complexity ratchet.
//
// It parses production Go files under internal/, cmd/ and pkg/, computes
// complexity with the same decision family as gocyclo (if, for, range, case,
// comm, &&, ||), and compares the functions above the threshold with a
// committed baseline. A new offender or a growth fails; an improvement or a
// removal is reported as a stale baseline warning and the baseline is
// regenerated with -write.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

const (
	baselineVersion = 1
	defaultRoot     = "."
	defaultBaseline = "architecture/cyclo_budget.json"
	defaultLimit    = 40
)

func main() {
	root := flag.String("root", defaultRoot, "repository root to scan")
	baselinePath := flag.String("baseline", defaultBaseline, "baseline JSON path, relative to root unless absolute")
	threshold := flag.Int("threshold", defaultLimit, "maximum allowed cyclomatic complexity")
	check := flag.Bool("check", true, "compare the scan against the baseline")
	write := flag.Bool("write", false, "regenerate the baseline from the current scan")
	list := flag.Bool("list", false, "print every function above the threshold and exit")
	flag.Parse()

	if *threshold < 1 {
		fatalf("-threshold must be >= 1")
	}
	current, err := scan(*root, *threshold)
	if err != nil {
		fatalf("scan: %v", err)
	}
	if *list {
		printFindings(current)
		return
	}
	path := resolvePath(*root, *baselinePath)
	if *write {
		if err := writeBaseline(path, baseline{Version: baselineVersion, Threshold: *threshold, Functions: complexityMap(current)}); err != nil {
			fatalf("write baseline: %v", err)
		}
		fmt.Printf("wrote %s: %d functions above %d\n", path, len(current), *threshold)
		return
	}
	if !*check {
		printFindings(current)
		return
	}

	base, err := loadBaseline(path)
	if err != nil {
		fatalf("load baseline %s: %v", path, err)
	}
	result := checkFindings(current, base, *threshold)
	for _, warning := range result.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", warning)
	}
	if len(result.Failures) > 0 {
		fmt.Fprintf(os.Stderr, "❌ cyclo budget failed: %d regression(s)\n", len(result.Failures))
		for i, failure := range result.Failures {
			if i == 20 {
				fmt.Fprintf(os.Stderr, "... and %d more\n", len(result.Failures)-i)
				break
			}
			fmt.Fprintf(os.Stderr, "  - %s\n", failure)
		}
		os.Exit(1)
	}
	fmt.Printf("✅ cyclo budget passed: %d functions above %d, %d baseline entries\n", len(current), *threshold, len(base.Functions))
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}

func resolvePath(root, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(root, path)
}
