// Package main — velox-knobs renders the VELOX_* knob inventory that used to
// be hand-maintained in the example config and the runbook.
//
// The registry (internal/app/knobs/registry.go) is the single source; this
// command is the only writer of the generated artifacts:
//
//	go run ./cmd/velox-knobs -write   # regenerate both artifacts
//	go run ./cmd/velox-knobs -check   # fail when a committed artifact drifted
//	go run ./cmd/velox-knobs          # print the inventory (no writes)
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/Marcuss-ops/PipelineGen/internal/app/knobs"
)

func main() {
	var (
		root  = flag.String("root", ".", "repository root the artifacts are written under")
		write = flag.Bool("write", false, "regenerate the generated config and runbook")
		check = flag.Bool("check", false, "exit 1 when a committed artifact is stale")
	)
	flag.Parse()

	switch {
	case *check && *write:
		fmt.Fprintln(os.Stderr, "velox-knobs: -check and -write are mutually exclusive")
		os.Exit(2)
	case *write:
		if err := knobs.WriteArtifacts(*root); err != nil {
			fmt.Fprintf(os.Stderr, "velox-knobs: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("velox-knobs: wrote %s and %s\n", knobs.ConfigArtifact, knobs.RunbookArtifact)
	case *check:
		if err := knobs.CheckArtifacts(*root); err != nil {
			fmt.Fprintf(os.Stderr, "velox-knobs: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("velox-knobs: artifacts are current")
	default:
		fmt.Print(knobs.RenderRunbook())
	}
}
