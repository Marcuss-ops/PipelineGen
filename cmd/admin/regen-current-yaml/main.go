// Command regen-current-yaml validates architecture/catalog.yaml and emits
// the two active governance compatibility files.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Marcuss-ops/PipelineGen/pkg/architecturecatalog"
)

func main() {
	var (
		sourcePath = flag.String("source", "architecture/catalog.yaml", "canonical architecture catalog")
		currentOut = flag.String("current-out", "architecture/current.yaml", "generated current catalog")
		issuesOut  = flag.String("issues-out", "architecture/issues.yaml", "generated issues catalog")
		legacyOut  = flag.String("out", "", "deprecated alias for --current-out")
		check      = flag.Bool("check", false, "verify generated files are current without writing")
		dryRun     = flag.Bool("dry-run", false, "deprecated alias for --check")
	)
	flag.Parse()

	if *legacyOut != "" {
		*currentOut = *legacyOut
	}
	if *dryRun {
		*check = true
	}

	catalog, err := architecturecatalog.Load(*sourcePath)
	fatalIf(err)
	current, err := catalog.RenderCurrent()
	fatalIf(err)
	issues, err := catalog.RenderIssues()
	fatalIf(err)

	if *check {
		fatalIf(checkFile(*currentOut, current))
		fatalIf(checkFile(*issuesOut, issues))
		fmt.Printf("architecture catalogs are reproducible from %s\n", *sourcePath)
		return
	}
	fatalIf(writeFile(*currentOut, current))
	fatalIf(writeFile(*issuesOut, issues))
	fmt.Printf("generated %s and %s from %s\n", *currentOut, *issuesOut, *sourcePath)
}

func checkFile(path string, want []byte) error {
	got, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read generated target %s: %w", path, err)
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("%s is stale; run go run ./cmd/admin/regen-current-yaml", path)
	}
	return nil
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create parent for %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func fatalIf(err error) {
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, "regen-current-yaml:", err)
	os.Exit(1)
}
