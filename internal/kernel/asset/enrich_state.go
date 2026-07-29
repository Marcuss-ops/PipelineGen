// Package asset — enrich_state.go is the canonical 4-state typed enum
// for the media_assets.enrich_state column (PR-ENRICHMENT-STATE-MACHINE,
// July 2026, owner: internal/application/assets/enrichment).
//
// State machine (canonical 4-state closed set — godlike/06 SSOT one
// owner per fact):
//
//	       ┌─→ ENRICHING ─┬─→ ENRICHED  (terminal success)
//	       │               └─→ FAILED    (terminal operator-must-intervene)
//	PENDING ┤
//	       │                ...
//	       (initial sentinel stamped by canonical ingest path)
//	       │
//	       (VLM 15-min sweeper also flips PENDING→ENRICHING on claim,
//	        as the backfill path for rows that somehow bypassed the
//	        canonical ingest stamp — see lifecycle_sweepers.go)
//
// Companion to IndexState (index_state.go). They are orthogonal:
//   - IndexState tracks Qdrant-side indexing progress (7 states).
//   - EnrichState tracks VLM-side classification enrichment (4 states).
//
// A row can be IndexState=INDEXED AND EnrichState=PENDING simultaneously
// (indexed for search but not yet auto-tagged); the canonical ingest
// path stamps EnrichState=PENDING at row creation so no future row
// is "mai classificato".
//
// godlike/07 typed-error contract (surface:
// internal/application/assets/enrichment/errors.go): the state-machine
// wrapper rejects illegal transitions via ErrIllegalEnrichTransition
// (typed envelope {From, To}). Callers errors.As(err, &ite) to inspect
// the rejected edge.
package asset

// EnrichState is the canonical per-asset enrichment progress. Stored
// on the media_assets.enrich_state first-class column (migration 123).
// Mirrors the typed-enum discipline of IndexState (7 enum) and
// ProcessingStatus (4 enum) — the typed enum lives ONLY in this file
// per godlike/06 one-owner-per-fact.
type EnrichState string

const (
	// EnrichStatePending — initial sentinel. The canonical ingest
	// pipeline stamps this on every new media_assets row via the
	// typed state-machine wrapper (mark on ingest success). The VLM
	// 15-min sweeper also considers rows at this state as scrape
	// candidates (the recovery path for rows that bypassed ingest,
	// e.g. pre-PR historical rows or admin-tooling direct inserts).
	EnrichStatePending EnrichState = "PENDING"

	// EnrichStateEnriching — VLM is actively holding a claim on the
	// row and performing classification. The claim fence is a
	// WHERE-enrich_state_updated_at < now()-30s pre-check inside the
	// sweeper; a competing sweep tick sees a different stamp and
	// skips the row. The state-machine wrapper is the only writer
	// of this transition.
	EnrichStateEnriching EnrichState = "ENRICHING"

	// EnrichStateEnriched — terminal success. VLM emitted VLM tags
	// + a classification payload; the row's metadata_json (or
	// dedicated sidecar JSON columns in a future PR) carries the
	// stamps. No further automatic transitions.
	EnrichStateEnriched EnrichState = "ENRICHED"

	// EnrichStateFailed — terminal failure. VLM exhausted retries
	// or hit a typed terminal classification (per godlike/07
	// no-fake-availability: failed is a permanent operator-visible
	// terminal, NOT a backstop for retry). Future re-enrichment
	// requires operator action via the admin reindex endpoint (which
	// is OUT OF SCOPE this PR).
	EnrichStateFailed EnrichState = "FAILED"
)

// CanonicalEnrichStateValues is the closed set of canonical values
// (godlike/06 SSOT). Use this for the typed-state enumeration test.
// The order matches the canonical lifecycle narrative (PENDING first;
// terminals after).
func CanonicalEnrichStateValues() []EnrichState {
	return []EnrichState{
		EnrichStatePending,
		EnrichStateEnriching,
		EnrichStateEnriched,
		EnrichStateFailed,
	}
}

// Valid returns true if s is one of the canonical 4 values. Empty
// string and unknown values are intentionally rejected so a
// pre-migration-123 row that has NOT been touched yet returns
// false (not silently mapped to PENDING).
func (s EnrichState) Valid() bool {
	switch s {
	case EnrichStatePending,
		EnrichStateEnriching,
		EnrichStateEnriched,
		EnrichStateFailed:
		return true
	}
	return false
}

// IsTerminal returns true if s is one of the two terminal values
// (ENRICHED or FAILED — neither has a further automatic transition).
// Used by the VLM sweeper filter to skip already-terminal rows even
// if they are old.
func (s EnrichState) IsTerminal() bool {
	switch s {
	case EnrichStateEnriched, EnrichStateFailed:
		return true
	}
	return false
}

// IsFailedTerminal distinguishes the "operator must intervene"
// terminal from the "successful terminal". EnrichStateFailed is the
// only failure terminal.
func (s EnrichState) IsFailedTerminal() bool {
	return s == EnrichStateFailed
}

// IsScrapeCandidate returns true if a row at this state is eligible
// to be picked up by the VLM 15-min sweeper. Only PENDING is a
// scrape candidate; FAILED is terminal and requires operator reset
// back to PENDING before it can be claimed again. ENRICHING (claim
// held by another worker) and ENRICHED (terminal success) are NOT.
func (s EnrichState) IsScrapeCandidate() bool {
	return s == EnrichStatePending
}
