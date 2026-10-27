// Package twinddl is the single embedded DDL source for the twin table
// declarations (migration-museum audit point 7; see
// docs/migrations/BASELINE_INVENTORY.md "Twin declarations").
//
// Nine table families are declared twice by hand on the two SQLite planes —
// canonical on migrations/sqlite, projection copy on
// migrations/sqlite_jobs/001_jobs_plane.sql — and outbox_events is declared a
// third time on the PostgreSQL media SSOT. Hand rewrites drift: the
// job_checkpoints disappearance repaired by 272/273 and the
// outbox_events.event_key uniqueness divergence repaired by 285 are the
// recorded instances.
//
// This package is the shared source those copies are GENERATED from:
//
//	twin_tables.sql            the one hand-edited declaration (canonical
//	                           primary-plane shape of the nine tables)
//	projection.go              renders the two plane projections from it,
//	                           applying ONLY the pinned divergence rules
//	                           (CHECK constraints and three historical
//	                           timestamp DEFAULTs are primary-only)
//	generated/*.sql            checked-in generator output
//	                           (cmd/twin-ddl-gen --generate)
//	fold.go                    folds the real migration declarations of a
//	                           plane (CREATE TABLE + ALTER TABLE + index
//	                           events, in version order) into the same shape
//
// The parity tests require both planes' folded declarations to equal the
// generated projections, so drift between the declarations of the two planes
// — or between either plane and this source — fails the build.
package twinddl

import _ "embed"

// Source is the single hand-edited DDL declaration of the nine twin tables
// (canonical primary-plane shape). Everything else in this package is
// generated from or compared against these bytes.
//
//go:embed twin_tables.sql
var Source string

// TwinTables are the table families shared by both SQLite planes, in the
// canonical declaration order of twin_tables.sql.
var TwinTables = []string{
	"artifact_stages",
	"job_checkpoints",
	"preparation_units",
	"preparation_job_units",
	"preparation_dependencies",
	"preparation_claim_snapshots",
	"preparation_attempts",
	"job_results",
	"outbox_events",
}
