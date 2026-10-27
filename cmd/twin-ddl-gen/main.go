// Command twin-ddl-gen keeps the generated twin-DDL projections in sync with
// the single embedded DDL source (internal/platform/sqlite/twinddl — the
// migration-museum audit point 7 shared declaration for artifact_stages,
// job_checkpoints, preparation_*, job_results and outbox_events).
//
// The two SQLite planes declare those nine table families by hand
// (migrations/sqlite canonical, migrations/sqlite_jobs/001_jobs_plane.sql
// projection copy) and hand rewrites drift. This command is the sole writer
// of the generated projections in
// internal/platform/sqlite/twinddl/generated/:
//
//	Default mode (fail-closed): verify the checked-in projections match the
//	source byte-for-byte AND that both planes' real migration declarations
//	fold to the same shapes; exit 1 on drift.
//	--generate: rewrite the projections from twin_tables.sql.
//
// The same checks run as ordinary tests (internal/platform/sqlite/twinddl),
// so a drift that escapes this command still fails `go test ./...`.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/twinddl"
	"github.com/Marcuss-ops/PipelineGen/pkg/atomicwrite"
)

const generatedDir = "internal/platform/sqlite/twinddl/generated"

func main() {
	generate := flag.Bool("generate", false, "rewrite the generated projections from twin_tables.sql")
	flag.Parse()

	artifacts, err := twinddl.GenerateArtifacts()
	if err != nil {
		fatal(2, fmt.Errorf("render projections: %w", err))
	}

	if *generate {
		for _, name := range []string{twinddl.ArtifactCanonical, twinddl.ArtifactJobsPlane} {
			path := filepath.Join(generatedDir, name)
			if err := atomicwrite.WriteFile(path, []byte(artifacts[name]), 0o644); err != nil {
				fatal(2, fmt.Errorf("write %s: %w", path, err))
			}
			fmt.Printf("twin-ddl-gen: regenerated %s\n", path)
		}
		return
	}

	for _, name := range []string{twinddl.ArtifactCanonical, twinddl.ArtifactJobsPlane} {
		path := filepath.Join(generatedDir, name)
		existing, err := os.ReadFile(path)
		if err != nil {
			fatal(2, err)
		}
		if string(existing) != artifacts[name] {
			fatal(1, fmt.Errorf("%s drifted from twin_tables.sql — run `go run ./cmd/twin-ddl-gen --generate`", path))
		}
	}

	if err := verifyPlane("primary", "migrations/sqlite", twinddl.ArtifactCanonical); err != nil {
		fatal(1, err)
	}
	if err := verifyPlane("jobs", "migrations/sqlite_jobs", twinddl.ArtifactJobsPlane); err != nil {
		fatal(1, err)
	}
	fmt.Println("twin-ddl-gen: projections in sync with twin_tables.sql and with both planes' declarations")
}

// verifyPlane folds the plane's real migration declarations and requires them
// to equal the projection generated from the shared source.
func verifyPlane(scope, corpusDir, artifact string) error {
	canonical, err := twinddl.CanonicalShapes()
	if err != nil {
		return err
	}
	projection, err := twinddl.JobsPlaneShapes()
	if err != nil {
		return err
	}
	want := canonical
	if scope == "jobs" {
		want = projection
	}
	folded, err := twinddl.FoldCorpus(corpusDir, scope)
	if err != nil {
		return err
	}
	for _, table := range twinddl.TwinTables {
		got, ok := folded.Shape(table)
		if !ok {
			return fmt.Errorf("twin-ddl-gen: %s declares no table %q (expected the %s projection)", corpusDir, table, artifact)
		}
		if diff := twinddl.ShapesEqual(got, want[table]); diff != "" {
			return fmt.Errorf("twin-ddl-gen: %s declarations of %s drifted from %s (%s)\n"+
				"converge the declarations and twin_tables.sql in one change, or pin the divergence in projection.go + docs/migrations/BASELINE_INVENTORY.md",
				corpusDir, table, artifact, diff)
		}
	}
	return nil
}

func fatal(code int, err error) {
	fmt.Fprintf(os.Stderr, "twin-ddl-gen: %v\n", err)
	os.Exit(code)
}
