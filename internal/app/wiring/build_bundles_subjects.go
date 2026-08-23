// Package app — wiring of the canonical subjects.Resolver.
//
// P0-1 (stock-pipeline refactor, July 2026): the canonical
// SubjectResolver is registered as a top-level service on the
// composition root so any future application service (image-side,
// stock-side, future-side) routes displayName → Subject UUID
// through ONE canonical owner (godlike/06 SSOT).
//
// godlike/06 SSOT — exactly one canonical owner per fact:
// subjectsrepo.Resolver is the SOLE place subject normalization
// happens. Application code MUST NOT recompute slug/case-normalize
// on its own — bypassing the resolver would silently produce
// duplicate subject rows on casing variants (the Sugar-Ray-
// Robinson incident).
//
// Composition-tree integration:
//   - Composition.SubjectsResolver is the public accessor used by
//     all bundles.
//   - Resolver construction is deferred until the primary SQLiteDB
//     is open (InitDatabases already enforces this ordering).
//   - The resolver is stored as a `*subjectsrepo.Resolver` (concrete
//     type) for composition simplicity; application code should
//     depend on the `subjects.Resolver` port interface.
package wiring

import (
	"database/sql"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/subjects"

	subjectsrepo "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets/subjectsrepo"
)

// BuildSubjectsResolver constructs the canonical SQLite-backed
// `subjects.Resolver` from the primary media DB handle. The resolver
// is the only canonical subject-identity layer in the codebase
// (godlike/06 SSOT) and is registered on the composition root.
//
// Resolver construction never fails for the SQLite-backed adapter
// (no required pre-state). Errors surface at the first query
// (table-missing / migration-not-applied) so wiring stays simple.
//
// `db` MUST be the canonical primary media DB. Reading from the
// observability DB would put the resolver under a different
// schema_migrations ledger, breaking the 180-migration contract.
//
// Returns the typed-port interface (subjects.Resolver) — callers
// composing it onto their own bundles MUST declare the dependency
// against the interface, not the concrete SQLite type.
func BuildSubjectsResolver(db *sql.DB) subjects.Resolver {
	return subjectsrepo.NewResolver(db)
}
