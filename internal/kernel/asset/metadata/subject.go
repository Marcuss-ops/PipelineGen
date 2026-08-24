package metadata

import "time"

// Subject represents a known entity (person, place, thing) in the
// PipelineGen pipeline.
//
// P0-1 (July 2026, stock-pipeline refactor): the kernel type now
// carries the canonical identity contract the user diagnostic
// "Sugar Ray Robinson" / "sugar ray robinson" / "SUGAR RAY
// ROBINSON" required.
//
// Canonical contract:
//
//   - UUID     — stable identity (godlike/06 SSOT). Used as
//     `subject_id` in cross-table FK relations.
//     Never mutable after creation.
//   - Slug     — canonical lookup key (e.g.
//     "sugar-ray-robinson"). Used in slot identifiers,
//     cache keys, and Drive folder naming. Derived once
//     from DisplayName via pkg/slug.SlugifyTitle.
//   - DisplayName — the human-readable label surfaced to operators.
//     Mutable via admin command (out of scope of this
//     kernel type's contract).
//   - DisplayNameNorm — LOWER(TRIM(display_name)). Used as the
//     case-insensitive lookup key. Stable across casing
//     variants so variant spellings resolve to the same
//     UUID.
//   - Aliases  — JSON-encoded list of additional variant spellings
//     accepted by the canonical resolver (e.g.
//     "SR Robinson", "Sugar Ray"). The listing is the
//     canon source — application-level code MUST NOT
//     duplicate the alias-matching logic.
//
// Legacy contract (kept for back-compat with the image-side
// resolver at imagesrepo.GetSubjectBySlugOrAlias):
//
//   - ID       — long-lived column on `subjects` (TEXT PK). PRE-180
//     the column was the lookup key (slug-like). POST-180
//     the column is preserved verbatim on legacy rows
//     but new writes may fill it with the canonical Slug
//     for go-forward consistency.
//
// The CASING/whitespace invariant is enforced by the canonical
// `subjectsrepo.Resolver` (godlike/06 SSOT — exactly one canonical
// owner per resolution step; callers MUST go through the resolver
// rather than recomputing normalization on the application side).
type Subject struct {
	// UUID is the canonical subject_id (godlike/06 SSOT). Stable
	// across migrations and table changes. UUID v4 for newly
	// created subjects, deterministic sha256-derived UUID v5-ish
	// for legacy rows backfilled by migration 180.
	UUID string `json:"uuid"`

	// Slug is the canonical lookup key (e.g. "sugar-ray-robinson").
	// Derived once from DisplayName via pkg/slug.SlugifyTitle at
	// create time.
	Slug string `json:"slug"`

	// DisplayName is the operator-facing label (e.g.
	// "Sugar Ray Robinson"). Free-text; the resolver normalizes
	// it before lookup.
	DisplayName string `json:"display_name"`

	// DisplayNameNorm is LOWER(TRIM(display_name)). Computed by the
	// resolver at create time and on each display_name update.
	// Index-backed so the resolver never needs a user-side
	// LOWER(...) computation.
	DisplayNameNorm string `json:"-"` // not exposed in JSON; transient.

	// Aliases is an additional list of accepted variant spellings
	// resolved to the same UUID. JSON-encoded in SQLite.
	Aliases []string `json:"aliases,omitempty"`

	// WikidataID is the optional Wikidata entity mirror (free-text,
	// no FK).
	WikidataID string `json:"wikidata_id,omitempty"`

	// Category is the free-text classification (legacy column on
	// the image-side resolver).
	Category string `json:"category,omitempty"`

	// Notes is free-text admin annotation.
	Notes string `json:"notes,omitempty"`

	// Kind is the entity kind ("person", "place", "thing", …).
	// Used by future routing layers; not exposed in the
	// godlike/06 identity contract.
	Kind string `json:"kind,omitempty"`

	// Origin is the provenance tag ("image" for legacy image-resolved
	// subjects, "stock" for stock-pipeline-resolved subjects). NOT
	// a routing label; future-routing owners add new origin tags as
	// they land.
	Origin string `json:"origin,omitempty"`

	// ID is the legacy column on `subjects` (TEXT PK). Read-only in
	// the canonical contract; preserved verbatim to avoid breaking
	// the image-side resolver at GetSubjectBySlugOrAlias. New writes
	// copy Slug into this field so legacy readers keep working.
	ID int64 `json:"-"` // legacy int64 placeholder; canonical is UUID.

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
