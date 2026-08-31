// Package main — architecture-aggregate (Step 5, June 2026).
//
// Concatenates architecture/ownership/{modules,jobs,services,application,
// infrastructure,packages}.yaml into architecture/ownership.generated.yaml
// (committed canonical view consumed by scripts/archcheck/main.go).
//
// Determinism: the generator is stdlib only (no gopkg.in/yaml.v3) because
// yaml.v3 round-trips drop comments + reorder keys — the SSOT for
// scripts/archcheck is the on-disk YAML file with its comments preserved
// as documentation. Concatenation is byte-stable as long as each split
// file is byte-stable, which is enforced by the per-file deterministic
// writer in the splitter (architecture/ownership/<section>.yaml).
//
// Flags:
//
//	--out path   Write the generated view to a custom path
//	             (default: architecture/ownership.generated.yaml).
//	--dry-run    Diff against the committed generated file; exit 1
//	             on mismatch. Used by make verify-architecture-aggregate
//	             (Commit C — Step 5 CI guard).
//
// Exit codes:
//
//	0 — generated view written; matches committed view (or --dry-run pass).
//	1 — --dry-run mismatch (regenerated output drifted from committed).
//	2 — load/write error.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

var ownershipSplitFiles = []string{
	"modules.yaml",
	"jobs.yaml",
	"services.yaml",
	"app.yaml",
	"kernel.yaml",
	"capabilities.yaml",
	"platform.yaml",
	"packages.yaml",
}

func main() {
	var (
		outPath = flag.String("out", "architecture/ownership.generated.yaml", "output path (default: standard canonical view)")
		dryRun  = flag.Bool("dry-run", false, "diff against committed generated view; exit 1 on mismatch")
	)
	flag.Parse()

	if *dryRun {
		if err := dryRunCheck(*outPath); err != nil {
			fmt.Fprintf(os.Stderr, "architecture-aggregate: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stdout, "architecture-aggregate: --dry-run OK (regenerated matches committed %s)\n", *outPath)
		return
	}

	gen, err := aggregateOwnership()
	if err != nil {
		fmt.Fprintf(os.Stderr, "architecture-aggregate: %v\n", err)
		os.Exit(2)
	}
	if err := os.WriteFile(*outPath, gen, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "architecture-aggregate: write %s: %v\n", *outPath, err)
		os.Exit(2)
	}
	fmt.Fprintf(os.Stdout, "architecture-aggregate: wrote %d bytes to %s\n", len(gen), *outPath)
}

// aggregateOwnership reads the 6 split source files in canonical order
// and returns the byte-stable concatenation. Each section file's
// content is trimmed to a single trailing newline; a single blank line
// separates adjacent sections so the generated document reads as one
// continuous YAML.
func aggregateOwnership() ([]byte, error) {
	var buf bytes.Buffer
	for i, name := range ownershipSplitFiles {
		path := filepath.Join("architecture", "ownership", name)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		// Trim trailing whitespace + newlines; we re-add a single '\n'.
		data = bytes.TrimRight(data, " \t\r\n")
		if i > 0 {
			buf.WriteByte('\n') // blank line between sections
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

// dryRunCheck regenerates the canonical view and byte-compares it
// against the committed file at outPath. Mismatch is a hard error so
// CI can fail closed. Diff content is written to stderr for debugging.
func dryRunCheck(outPath string) error {
	got, err := aggregateOwnership()
	if err != nil {
		return fmt.Errorf("aggregate: %w", err)
	}
	committed, err := os.ReadFile(outPath)
	if err != nil {
		return fmt.Errorf("read committed %s: %w", outPath, err)
	}
	if !bytes.Equal(got, committed) {
		return fmt.Errorf("regenerated output differs from committed %s (%d regenerated vs %d committed bytes)",
			outPath, len(got), len(committed))
	}
	return nil
}
