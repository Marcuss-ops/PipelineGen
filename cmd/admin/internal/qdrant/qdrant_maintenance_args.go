// cmd/admin/qdrant_maintenance_args.go — args parser for qdrant-maintenance.
//
// Owns the qdrantMaintDeps struct (parsed-flag bundle) and the parseQdrantMaintArgs
// entry point that peels the mode positional + --json + --limit flags out of
// the raw argv. Co-located with the deps struct per godlike/06 SSOT (one
// canonical owner per fact: deps-shape lives with the parser that produces it).
package qdrant

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

// qdrantMaintDeps holds the parsed flags for runQdrantMaintenance.
type qdrantMaintDeps struct {
	Mode  string // audit | repair-locators | delete-invalid
	JSON  bool
	Limit int
}

// parseQdrantMaintArgs peels the mode (positional) and flags out of args.
func parseQdrantMaintArgs(args []string) (qdrantMaintDeps, error) {
	if len(args) == 0 {
		return qdrantMaintDeps{}, fmt.Errorf("qdrant-maintenance requires a mode: audit, repair-locators, or delete-invalid")
	}
	mode := strings.TrimSpace(args[0])
	if !qdrantMaintenanceModes[mode] {
		return qdrantMaintDeps{}, fmt.Errorf("unknown mode %q — expected audit, repair-locators, or delete-invalid", mode)
	}

	fs := flag.NewFlagSet("qdrant-maintenance", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "Machine-readable JSON output")
	limit := fs.Int("limit", 0, "Optional cap on per-page scan size (Qdrant max=1000)")
	if err := fs.Parse(args[1:]); err != nil {
		return qdrantMaintDeps{}, err
	}
	if *limit < 0 {
		return qdrantMaintDeps{}, fmt.Errorf("qdrant-maintenance: --limit must be >= 0, got %d", *limit)
	}
	return qdrantMaintDeps{Mode: mode, JSON: *jsonOut, Limit: *limit}, nil
}
