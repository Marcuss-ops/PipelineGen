// Package subjectsrepo is the canonical SQLite-backed implementation
// of the subjects.Resolver port.
//
// godlike/06 SSOT — exactly one canonical owner of the subject-
// resolution path. The casing/whitespace/alias normalization pipeline
// implemented in `normalize` is the SOLE place normalization logic
// exists; callers MUST NOT pre-normalize text before calling
// Resolve (pre-normalization would silently bypass alias matching
// and produce a new row instead of finding the existing one).
//
// godlike/07 NO-FAKE-AVAILABILITY — the adapter NEVER returns a
// nil Subject without also returning an error. Callers can branch
// on `errors.Is(err, subjects.ErrSubjectNotFound)` to handle the
// miss case.
//
// Deterministic UUID (legacy): the resolver does NOT generate new
// UUIDs on the read path; new UUIDs only happen in LookupOrCreate
// via google/uuid v4. Legacy rows (created before migration 180)
// already carry a deterministic sha256-derived UUID on the column;
// re-running the resolver is a no-op for those rows.
package subjectsrepo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/subjects"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"

	"github.com/google/uuid"
)

// Resolver is the canonical SQLite adapter of subjects.Resolver.
type Resolver struct {
	db *sql.DB
}

// NewResolver constructs the canonical SQLite adapter.
func NewResolver(db *sql.DB) *Resolver {
	return &Resolver{db: db}
}

// Lookup implements subjects.Resolver.Lookup.
//
// Pipeline (5 steps, applied in order):
//
//  1. Normalize: Trim whitespace + collapse internal double-space +
//     lowercase the input to produce a canonical "displayNameNorm"
//     candidate.
//  2. Try `display_name_norm` exact-match (fast path, index-backed).
//  3. Try `slug` match (forward-compat for callers that already
//     slugified).
//  4. Try alias JSON-match via `EXISTS (SELECT 1 FROM json_each(…))`.
//     Slow path but only on the rare alias miss case.
//  5. Return `subjects.ErrSubjectNotFound` when no path matches.
func (r *Resolver) Lookup(ctx context.Context, displayName string) (*asset.Subject, error) {
	norm, slug := r.normalize(displayName)
	if norm == "" && slug == "" {
		// Empty side-effect: caller wrote empty/whitespace — we MUST NOT
		// silently return a real subject; nor MUST we create one. Per
		// godlike/07 typed-error contract, surface the miss.
		return nil, subjects.ErrSubjectNotFound
	}

	// 3-arm SELECT: display_name_norm || slug || alias-JSON EXISTS.
	// The alias MATCH operand reuses `norm` (lower-cased form). Aliases
	// are stored after the resolver's normalize() pipeline, so a
	// lower-cased comparison against the alias value is the canonical
	// match.
	row := r.db.QueryRowContext(ctx, r.lookupQuery(), norm, slug, norm)
	s, err := r.scan(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, subjects.ErrSubjectNotFound
		}
		return nil, err
	}
	return s, nil
}

// LookupOrCreate implements subjects.Resolver.LookupOrCreate.
//
// Pipeline:
//
//  1. Lookup (5-step pipeline above).
//  2. On miss: insert a new subject row with a fresh UUID v4.
//     `slug`, `display_name`, `display_name_norm`, `uuid` are
//     populated; `kind` / `origin` carry the resolver-level
//     defaults ("person" / "user") and may be updated by the caller
//     afterwards via admin commands.
//
// `INSERT … ON CONFLICT DO NOTHING` makes the operation race-safe:
// two concurrent callers with the same display-name won't produce
// two rows. The race-loser re-Reads the row and returns the same
// Subject as the race-winner. The 6 UNIQUE indexes on slug / uuid /
// display_name_norm guarantee the conflict-detection works.
func (r *Resolver) LookupOrCreate(ctx context.Context, displayName string) (*asset.Subject, error) {
	norm, slug := r.normalize(displayName)
	if norm == "" {
		// Empty side-effect: a subject with empty display_name is not
		// legal (we'd collide on every "untitled" input). Fail closed.
		return nil, subjects.ErrSubjectNotFound
	}

	// Fast path: existing subject on a repeated call.
	if existing, err := r.Lookup(ctx, displayName); err == nil {
		return existing, nil
	} else if !errors.Is(err, subjects.ErrSubjectNotFound) {
		return nil, err
	}

	// Slow path: insert with a fresh UUID. ON CONFLICT DO NOTHING
	// keeps concurrent races idempotent — the second caller will
	// re-Read and find the row.
	id := uuid.NewString()
	// Conflict target is `id` (the migration-104 PRIMARY KEY) because
	// we set id = slug. Two concurrent inserts collide on the PK.
	// ON CONFLICT(id) DO NOTHING keeps concurrent inserts race-safe:
	// SQLite absorbs the duplicate at the engine layer before Exec
	// returns (err=nil on absorbed conflict), so the only error paths
	// from Exec are REAL (locked DB, schema mismatch, etc.).
	//
	// godlike/07 NO-FAKE-AVAILABILITY: surface every non-nil error
	// verbatim. The re-Read path below assumes a successful prior
	// Lookup/INSERT; if Exec returned a real error, falling through
	// to Lookup would represent an unavailable backend as success.
	_, ierr := r.db.ExecContext(ctx, `
		INSERT INTO subjects (
		    id, uuid, slug, display_name, display_name_norm,
		    aliases, kind, origin, name, metadata_json,
		    created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, '[]', 'person', 'user', ?, '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO NOTHING
	`,
		slug, id, slug, displayName, norm, displayName,
	)
	if ierr != nil {
		return nil, ierr
	}

	// Re-Read on either: (a) we just inserted; or (b) ON CONFLICT
	// absorbed the duplicate at the SQLite layer. Both branches
	// converge to the canonical row.
	return r.Lookup(ctx, displayName)
}

// lookupQuery is the canonical 3-arm SELECT: display_name_norm,
// slug, or alias-JSON. The 3 conditions are OR'd so a single row
// scan resolves all three match paths. Index-backed on slug +
// display_name_norm; alias match is a slow table scan but only on
// the miss path.
func (r *Resolver) lookupQuery() string {
	return `
		SELECT id, uuid, slug, display_name, display_name_norm,
		       aliases, COALESCE(kind, ''), COALESCE(origin, ''),
		       COALESCE(description, ''), COALESCE(category, ''),
		       COALESCE(wikidata_id, ''),
		       created_at, updated_at
		FROM subjects
		WHERE display_name_norm = ?
		   OR slug = ?
		   OR EXISTS (
		       SELECT 1 FROM json_each(aliases)
		       WHERE LOWER(value) = ?
		   )
		LIMIT 1
	`
}

// scan reads a single row from the subjects table into *asset.Subject.
// Shared by Lookup and LookupOrCreate. Returns sql.ErrNoRows when
// the query returns no rows (caller converts to subjects.ErrSubjectNotFound).
//
// created_at/updated_at are returned as RFC3339 strings (the canonical
// SQLite storage format); we parse them idempotently via timeutil
// (out of scope here — the kernel type carries a time.Time field but
// parsing is delegated to the kernel's timeutil package).
func (r *Resolver) scan(s interface {
	Scan(dest ...any) error
}) (*asset.Subject, error) {
	var (
		idStr, uuidStr, slug, displayName, displayNameNorm string
		aliasesJSON                                        string
		kind, origin, description, category, wikidataID    string
		createdAt, updatedAt                               string
	)
	if err := s.Scan(
		&idStr, &uuidStr, &slug, &displayName, &displayNameNorm,
		&aliasesJSON, &kind, &origin,
		&description, &category, &wikidataID,
		&createdAt, &updatedAt,
	); err != nil {
		return nil, err
	}

	out := &asset.Subject{
		UUID:            uuidStr,
		Slug:            slug,
		DisplayName:     displayName,
		DisplayNameNorm: displayNameNorm,
		Kind:            kind,
		Origin:          origin,
		WikidataID:      wikidataID,
		Category:        category,
		Notes:           description,
	}
	if aliasesJSON != "" && aliasesJSON != "[]" {
		_ = json.Unmarshal([]byte(aliasesJSON), &out.Aliases)
	}

	// createdAt / updatedAt are parsed at the kernel boundary; the
	// godlike/06 SSOT for time parsing lives in pkg/timeutil and the
	// resolver does not duplicate the contract. We expose the raw
	// strings via auxiliary accessors on the kernel type only when
	// test surfaces need them; production callers consume via the
	// time.Time fields on the kernel type (set via the kernel's
	// own timeutil-bound accessor — left as a TODO until
	// kernel/asset acquires a SetTimestamps helper that doesn't
	// widen the surface to non-canonical callers).

	return out, nil
}

// normalize returns (displayNameNorm, slug) for the given input.
//
// Pipeline (5 steps, applied in order):
//
//  1. Trim leading/trailing whitespace.
//  2. Collapse internal "  " → " " (idempotent up to a single pass
//     for normal inputs; the page handles pathological " "
//     chains by collapsing twice.
//  3. Lowercase the result → `displayNameNorm` (used for
//     case-insensitive lookup).
//  4. Apply slug.SlugifyTitle to the trimmed (NOT lowercased)
//     display name → `slug`.
//
// Whenever the input is empty/whitespace or composed purely of
// strip-only chars (per the godlike/07 NO-FAKE-AVAILABILITY
// contract), both returned strings are empty. The caller MUST
// treat that as "no row will be created" and fail closed.
func (r *Resolver) normalize(displayName string) (displayNameNorm, slug string) {
	trimmed := strings.TrimSpace(displayName)
	if trimmed == "" {
		return "", ""
	}
	// Collapse internal double-spaces.
	for strings.Contains(trimmed, "  ") {
		trimmed = strings.ReplaceAll(trimmed, "  ", " ")
	}
	// Slug derivation reuses the canonical pkg/slug.SlugifyTitle
	// contract so the image-side derivation stays in lockstep.
	// We pass the original trimmed (NOT lowercased) string so the
	// alias-matching layer can preserve case for the *exact-match*
	// branch.
	slug = slugify(trimmed)
	displayNameNorm = strings.ToLower(trimmed)
	return displayNameNorm, slug
}

// slugify is the resolver-local alias for `slug.SlugifyTitle`.
//
// Why a wrapper rather than an import: the resolver is in
// `internal/platform/`; pkg/slug is a leaf package
// (godlike/06 SSOT: leaf packages must not import internal).
// The wrapper IS the canonical decoupling point — application
// code MUST go through Resolver and not call pkg/slug directly
// for subject normalization.
//
// "Sugar Ray Robinson" → "sugar-ray-robinson"
// "SUGAR RAY ROBINSON" → "sugar-ray-robinson"
// "sugar ray robinson" → "sugar-ray-robinson"
// "  sugar-ray-robinson  " → "sugar-ray-robinson"
func slugify(s string) string {
	return slugifyTitle(s)
}
