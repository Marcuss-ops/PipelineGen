// Package media — enrich_state_store.go: the PostgreSQL implementation of the
// media_assets.enrich_state transition primitive.
//
// WHY THIS EXISTS. The enrichment state machine (internal/capabilities/assets/
// enrichment) was wired to *ClipsRepository — the OPERATIONAL SQLite store —
// via enrichment.NewEnrichStateMachine(params.repos.ClipsRepo). media_assets is
// owned by PostgreSQL, so every enrichment transition was landing on a mirror
// while pgmedia's own patch path (writer.go, `enrich_state`) wrote the SSOT:
// one fact, two writers, two engines.
//
// The read plane could not be migrated before this: the VLM sweeper selects
// `enrich_state = 'PENDING'` rows and then CLAIMS them through this primitive, so
// moving only the read would have left the sweeper reading rows it could never
// claim. This file is the missing half.
//
// PROJECTION IS DELIBERATELY MINIMAL. The methods mirror
// internal/platform/sqlite/assets/imagesregistry/media_asset_mutations.go
// statement-for-statement:
//
//	enrich_state, enrich_state_updated_at, updated_at
//
// with the added migration-004 dual-write of the two TIMESTAMPTZ mirrors. Both
// mirrors are bound to the SAME ordinal as their TEXT sibling, which is what
// percheck_pg_timestamp_dual_write_contract requires and what makes drift
// impossible rather than merely unlikely.
//
// ERROR-SHAPE PARITY IS LOAD-BEARING. EnrichStateMachine.Transition remaps the
// primitive's failure into ErrEnrichStateMissing by STRING-MATCHING the phrase
// "asset row missing or current state mismatch"
// (internal/capabilities/assets/enrichment/state_machine.go). Reproducing that
// phrase here is not cosmetic: without it, a lost CAS would surface as an
// unclassified error and the sweeper's "another worker claimed this row"
// handling would change. See the forward-pointer note at the bottom of this
// file — the string match should become a kernel-level sentinel, but that is a
// capability-layer change and must not be smuggled in with an engine migration.
//
// PostgreSQL ONLY. No SQLite branch, no fallback: a closed media plane must
// surface as an error to the state machine.
package media

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// MediaEnrichStateStore persists media_assets.enrich_state transitions on the
// PostgreSQL media SSOT. It satisfies enrichment.EnrichRepositoryPort
// structurally, so this package does not import the capability; the
// composition root asserts conformance at the wiring site.
type MediaEnrichStateStore struct {
	db *sql.DB
}

// NewMediaEnrichStateStore returns a store bound to the PostgreSQL media
// database. The handle is required: a nil database is a programming error, not
// a degrade path.
func NewMediaEnrichStateStore(db *sql.DB) *MediaEnrichStateStore {
	if db == nil {
		panic("media.NewMediaEnrichStateStore: db is required")
	}
	return &MediaEnrichStateStore{db: db}
}

// DB exposes the underlying handle so callers can assert the store lives on the
// media SSOT.
func (s *MediaEnrichStateStore) DB() *sql.DB {
	if s == nil {
		return nil
	}
	return s.db
}

// enrichStateUpdateSQL is the shared statement shape. $2 carries BOTH the TEXT
// timestamp and its TIMESTAMPTZ mirror, so the pair cannot drift; $3/$4 are the
// optional CAS predicate.
//
// The explicit ::text cast on the TEXT columns is REQUIRED, not cosmetic. Without
// it PostgreSQL cannot deduce a single type for $2 — it is referenced once as a
// TEXT column and once as a TIMESTAMPTZ expression — and rejects the statement
// with SQLSTATE 42P08 ("inconsistent types deduced for parameter $2"). Pinning
// both sides explicitly is what keeps the SAME value bound to the SAME ordinal,
// which is the property percheck_pg_timestamp_dual_write_contract enforces.
const enrichStateUpdateSQL = `
	UPDATE media_assets
	SET enrich_state = $1,
	    enrich_state_updated_at = $2::text,
	    enrich_state_updated_at_ts = $2::timestamptz,
	    updated_at = $2::text,
	    updated_at_ts = $2::timestamptz
	WHERE id = $3`

// SetEnrichState writes the typed enum column plus the atomic
// enrich_state_updated_at stamp. Unconditional: see SetEnrichStateIfCurrent for
// the compare-and-swap form.
//
// Returns an error containing "asset row missing" when the row is absent, which
// is how the state-machine wrapper classifies a missing asset (error-shape
// parity with ClipsRepository.SetEnrichState).
func (s *MediaEnrichStateStore) SetEnrichState(ctx context.Context, id string, state asset.EnrichState) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("media enrich state store: media SSOT handle is not configured")
	}
	if id == "" {
		return fmt.Errorf("media enrich state store: id is required")
	}
	if state == "" {
		return fmt.Errorf("media enrich state store: state is required (use the canonical 4-state enum)")
	}
	if !state.Valid() {
		return fmt.Errorf("media enrich state store: state %q is not canonical (%v)",
			string(state), asset.CanonicalEnrichStateValues())
	}
	res, err := s.db.ExecContext(ctx, enrichStateUpdateSQL, string(state), timeutil.FormatRFC3339(time.Now()), id)
	if err != nil {
		return fmt.Errorf("media enrich state store: set enrich state for %q: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("media enrich state store: set enrich state rows affected for %q: %w", id, err)
	}
	if affected == 0 {
		return fmt.Errorf("media enrich state store: asset row missing for %q (state %s)", id, state)
	}
	return nil
}

// SetEnrichStateIfCurrent atomically flips the column from `from` to `to` only
// when the row currently holds `from`, stamping enrich_state_updated_at. This is
// the claim fence: two concurrent sweep ticks cannot both claim the same
// PENDING row.
//
// The RowsAffected==0 message reproduces the exact phrase the state-machine
// wrapper string-matches ("asset row missing or current state mismatch"), for
// the reason documented in the package comment.
func (s *MediaEnrichStateStore) SetEnrichStateIfCurrent(ctx context.Context, id string, from, to asset.EnrichState) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("media enrich state store: media SSOT handle is not configured")
	}
	if id == "" {
		return fmt.Errorf("media enrich state store: id is required")
	}
	if from == "" {
		return fmt.Errorf("media enrich state store: from state is required")
	}
	if to == "" {
		return fmt.Errorf("media enrich state store: to state is required")
	}
	if !from.Valid() {
		return fmt.Errorf("media enrich state store: from state %q is not canonical (%v)",
			string(from), asset.CanonicalEnrichStateValues())
	}
	if !to.Valid() {
		return fmt.Errorf("media enrich state store: to state %q is not canonical (%v)",
			string(to), asset.CanonicalEnrichStateValues())
	}
	res, err := s.db.ExecContext(ctx,
		enrichStateUpdateSQL+`
		  AND enrich_state = $4`,
		string(to), timeutil.FormatRFC3339(time.Now()), id, string(from))
	if err != nil {
		return fmt.Errorf("media enrich state store: CAS enrich state %s->%s for %q: %w", from, to, id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("media enrich state store: CAS rows affected for %q: %w", id, err)
	}
	if affected == 0 {
		// DUAL-PURPOSE MESSAGE, LOAD-BEARING: the wrapper string-matches this
		// exact phrase to surface ErrEnrichStateMissing.
		return fmt.Errorf("media enrich state store: asset row missing or current state mismatch for %q (%s->%s)", id, from, to)
	}
	return nil
}

// GetEnrichState reads the canonical typed enum column.
//
// Mirrors ClipsRepository.GetEnrichState: a value outside the canonical enum is
// an explicit error rather than a silent pass-through, so a pre-migration row or
// a typo can never flow into the state machine as a valid state. The column is
// NOT NULL on PostgreSQL (measured), so no COALESCE is needed and a NULL row
// cannot be silently read as "PENDING".
func (s *MediaEnrichStateStore) GetEnrichState(ctx context.Context, id string) (asset.EnrichState, error) {
	if s == nil || s.db == nil {
		return "", fmt.Errorf("media enrich state store: media SSOT handle is not configured")
	}
	if id == "" {
		return "", fmt.Errorf("media enrich state store: id is required")
	}
	var stateStr string
	err := s.db.QueryRowContext(ctx,
		`SELECT enrich_state FROM media_assets WHERE id = $1`, id).Scan(&stateStr)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("media enrich state store: asset row missing for %q", id)
	}
	if err != nil {
		return "", fmt.Errorf("media enrich state store: get enrich state for %q: %w", id, err)
	}
	state := asset.EnrichState(stateStr)
	if !state.Valid() {
		return "", fmt.Errorf("media enrich state store: column value %q for %q is not canonical (%v)",
			stateStr, id, asset.CanonicalEnrichStateValues())
	}
	return state, nil
}

// FORWARD-POINTER (godlike/07 typed-error contract, NOT done here):
// EnrichStateMachine.Transition classifies a lost CAS by STRING-MATCHING
// "asset row missing or current state mismatch". That coupling is why this file
// reproduces the phrase instead of returning a typed sentinel. The correct fix is
// to move ErrEnrichStateMissing (or a "row missing / CAS lost" sentinel) into
// internal/kernel/asset/enrich_state.go and have both this store and
// ClipsRepository wrap it, so capabilities and the platform layer can agree on a
// TYPE rather than on prose. That is a capability+kernel change with its own
// blast radius, so it is recorded here rather than folded into an engine
// migration.
