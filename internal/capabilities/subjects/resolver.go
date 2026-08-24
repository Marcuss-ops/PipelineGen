// Package subjects declares the canonical SubjectResolver port.
//
// godlike/06 SSOT: this package is the SOLE canonical owner of the
// subject-resolution logic. Application-level code (image-side,
// stock-side, future-side) routes displayName → Subject UUID
// through this interface. Adapter implementations live in
// `internal/platform/sqlite/assets/subjectsrepo`
// (the canonical SQLite adapter) and any future target adapter
// must register here too — application code MUST NOT depend on a
// concrete adapter.
//
// godlike/07 NO-FAKE-AVAILABILITY: the resolver NEVER represents
// a missing subject as a no-op success. Resolve returns either a
// concrete Subject (created on demand by LookupOrCreate, fetched by
// Lookup, or something like that) or a typed error — callers
// branch on the typed error to fail closed.
//
// P0-1 identity contract (July 2026, stock-pipeline refactor):
//
//   - Resolve("Sugar Ray Robinson")      == Resolve("SUGAR RAY ROBINSON")
//     == Resolve("  sugar ray robinson  ")
//
// The casing/whitespace/alias invariant is enforced exactly once,
// inside the resolver. Application code MUST NOT pre-normalize
// the input; passing pre-normalized text bypasses the alias-matching
// layer and silently produces a new subject row instead of finding
// the existing one.
package subjects

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// Resolver is the canonical port that resolves a free-form
// display-name string into a Subject. Two operations split the
// semantics:
//
//   - Lookup        — read-only; returns ErrSubjectNotFound when no
//     row exists. NEVER creates a new subject. Used
//     by read-only surfaces (verification, summary
//     endpoints, repair scripts UIs).
//
//   - LookupOrCreate — upsert; returns the existing Subject on a
//     display-name-collision, creates a new one on
//     a miss. Used by ingest paths that need to
//     associate a subject with new content (e.g.,
//     a stock-pipeline run ingesting clips).
//
// Both operations share the same casing/whitespace/alias
// normalization pipeline (one canonical implementation in the
// adapter).
type Resolver interface {
	// Lookup returns the Subject whose slug or normalized display
	// name equals the canonical normalization of displayName. It
	// returns ErrSubjectNotFound if no row matches.
	Lookup(ctx context.Context, displayName string) (*asset.Subject, error)

	// LookupOrCreate returns the existing Subject matching the
	// canonical normalization of displayName, or creates a new
	// Subject row (with a freshly-generated UUID v4) if none
	// matches. The "display_name", "display_name_norm", and
	// "slug" fields are populated by the resolver; the caller fills
	// out kind/origin/category/notes separately.
	LookupOrCreate(ctx context.Context, displayName string) (*asset.Subject, error)
}

// ErrSubjectNotFound is the typed error returned by Lookup when
// no Subject matches the supplied displayName. Callers branch on
// this error type via errors.Is — they MUST NOT string-match on
// the message (per godlike/07 typed-error contract).
//
// application-level code that triggers this error must decide
// between (a) creating the subject via LookupOrCreate, or (b)
// surfacing the gap to the operator (e.g., via a 404 on a REST
// endpoint). The resolver NEVER auto-promotes to creation on the
// read-only Lookup surface — that would violate godlike/07.
var ErrSubjectNotFound = errSubjectNotFound{}

type errSubjectNotFound struct{}

func (errSubjectNotFound) Error() string {
	return "subjects: not found"
}
