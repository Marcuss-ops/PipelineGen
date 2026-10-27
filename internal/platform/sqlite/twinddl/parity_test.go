package twinddl

import (
	"os"
	"path/filepath"
	"testing"
)

const (
	primaryCorpus = "../../../../migrations/sqlite"
	jobsCorpus    = "../../../../migrations/sqlite_jobs"
	generatedDir  = "generated"
)

// TestTwinSource_DeclaresEveryTwinTable pins the source inventory.
func TestTwinSource_DeclaresEveryTwinTable(t *testing.T) {
	shapes, err := CanonicalShapes()
	if err != nil {
		t.Fatalf("canonical shapes: %v", err)
	}
	for _, table := range TwinTables {
		shape, ok := shapes[table]
		if !ok {
			t.Errorf("twin source declares no table %q", table)
			continue
		}
		if len(shape.Columns) == 0 {
			t.Errorf("table %q: source declares no columns", table)
		}
	}
}

// TestTwinSource_GeneratedArtifactsCurrent is the generator gate: the
// checked-in generated/*.sql artifacts must be byte-identical to what
// cmd/twin-ddl-gen --generate renders from twin_tables.sql. Run the generator
// to refresh them after editing the source.
func TestTwinSource_GeneratedArtifactsCurrent(t *testing.T) {
	want, err := GenerateArtifacts()
	if err != nil {
		t.Fatalf("generate artifacts: %v", err)
	}
	for name, content := range want {
		raw, err := os.ReadFile(filepath.Join(generatedDir, name))
		if err != nil {
			t.Fatalf("generated artifact %s missing — run: go run ./cmd/twin-ddl-gen --generate (%v)", name, err)
		}
		if string(raw) != content {
			t.Errorf("generated artifact %s is stale — run: go run ./cmd/twin-ddl-gen --generate", name)
		}
	}
}

// TestTwinSource_PrimaryPlaneDeclarationsMatchSource folds the real primary-
// plane migration declarations (CREATE TABLE + ALTER TABLE + index events in
// version order) and requires them to equal the canonical source. Any drift
// between the primary declarations and the shared source fails here.
func TestTwinSource_PrimaryPlaneDeclarationsMatchSource(t *testing.T) {
	canonical, err := CanonicalShapes()
	if err != nil {
		t.Fatalf("canonical shapes: %v", err)
	}
	folded, err := FoldCorpus(primaryCorpus, "primary")
	if err != nil {
		t.Fatalf("fold primary corpus: %v", err)
	}
	for _, table := range TwinTables {
		t.Run(table, func(t *testing.T) {
			got, ok := folded.Shape(table)
			if !ok {
				t.Fatalf("primary corpus declares no table %q", table)
			}
			if diff := ShapesEqual(got, canonical[table]); diff != "" {
				t.Errorf("primary-plane declarations of %s drifted from the shared source (%s)\n"+
					"converge twin_tables.sql AND both planes' declarations in one change, or record the divergence in projection.go + BASELINE_INVENTORY.md", table, diff)
			}
		})
	}
}

// TestTwinSource_JobsPlaneDeclarationsMatchProjection folds the jobs-plane
// migration declarations and requires them to equal the generated projection.
// Because the projection is derived from the same source by the pinned
// divergence rules, this test is the drift detector between the declarations
// of the two planes.
func TestTwinSource_JobsPlaneDeclarationsMatchProjection(t *testing.T) {
	projection, err := JobsPlaneShapes()
	if err != nil {
		t.Fatalf("jobs projection: %v", err)
	}
	folded, err := FoldCorpus(jobsCorpus, "jobs")
	if err != nil {
		t.Fatalf("fold jobs corpus: %v", err)
	}
	for _, table := range TwinTables {
		t.Run(table, func(t *testing.T) {
			got, ok := folded.Shape(table)
			if !ok {
				t.Fatalf("jobs corpus declares no table %q", table)
			}
			if diff := ShapesEqual(got, projection[table]); diff != "" {
				t.Errorf("jobs-plane declarations of %s drifted from the generated projection (%s)\n"+
					"converge migrations/sqlite_jobs + twin_tables.sql in one change, or pin the divergence in projection.go + BASELINE_INVENTORY.md", table, diff)
			}
		})
	}
}

// TestTwinSource_PinnedDivergencesAreReal is stale-pin detection: every
// pinned jobs-dropped DEFAULT must actually differ between the two
// projections today. A converged pair must leave the pin list in the same
// change, so the pin list never lies about live divergence.
func TestTwinSource_PinnedDivergencesAreReal(t *testing.T) {
	canonical, err := CanonicalShapes()
	if err != nil {
		t.Fatalf("canonical shapes: %v", err)
	}
	projection, err := JobsPlaneShapes()
	if err != nil {
		t.Fatalf("jobs projection: %v", err)
	}
	for table, cols := range jobsDroppedDefaults {
		for col := range cols {
			c, ok := canonical[table].Column(col)
			if !ok {
				t.Errorf("stale pin: %s.%s is not a column of the canonical source", table, col)
				continue
			}
			j, _ := projection[table].Column(col)
			if !c.HasDefault || j.HasDefault {
				t.Errorf("stale pin: %s.%s no longer diverges (canonical hasDefault=%v, jobs hasDefault=%v) — drop it from jobsDroppedDefaults + BASELINE_INVENTORY.md",
					table, col, c.HasDefault, j.HasDefault)
			}
		}
	}
	// CHECK constraints must exist only on the canonical side.
	for _, table := range TwinTables {
		if len(projection[table].Checks) != 0 {
			t.Errorf("jobs projection of %s must carry no CHECK constraints (pinned divergence)", table)
		}
	}
	if len(canonical["artifact_stages"].Checks) == 0 || len(canonical["preparation_units"].Checks) == 0 {
		t.Error("canonical source lost its CHECK constraints")
	}
}
