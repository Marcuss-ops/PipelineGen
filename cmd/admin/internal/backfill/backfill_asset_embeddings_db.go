// cmd/admin/backfill_asset_embeddings_db.go — SQL query helpers for the
// embedding backfill CLI (Task 5, July 2026).
//
// Split rationale (Commit E, July 2026): the canonical backfill command
// (backfill_asset_embeddings.go) owns the entry point, arg parsing,
// output formatting, and the SQL-backed candidate fetcher wiring. This
// sibling owns the 2 SQL-pure candidate-fetch functions:
//
//   - fetchEmbeddingCandidates: SELECT media_assets rows for the active
//     backfill, applying resume-anchor + source filter + OnlyMissing
//     filter + LIMIT ordering. Used in normal (forward) mode.
//   - fetchFailedCandidates: SELECT media_assets rows restricted to a
//     caller-supplied id set; same row shape as fetchEmbeddingCandidates
//     but with IN(...) clause for the --retry-failed mode.
//
// Commit F (August 2026): the reusable core moved to
// internal/application/indexing/backfill; the shared row/run shapes
// (indexing.Candidate, indexing.Deps, indexing.Checkpoint) now come from
// that package. Both helpers additionally select the content_hash
// expression so the core can build the deterministic event_key
// fingerprint without owning SQL.
//
// godlike/06 SSOT (one canonical owner per fact):
//   - The media_assets column selected here MUST match the schema
//     declared in internal/platform/sqlite/.../migrations.
//     If a column is added or removed there, both helpers + their
//     Scan() destructuring MUST be updated in lockstep — these
//     queries read production shape by SSOT contract, no inline schema
//     derived here.
//   - The CASE WHEN embedding_* IS NOT NULL AND ... != '[]' AND ... !=
//     '{}' truthiness idiom is preserved verbatim across both helpers.
//     The "empty blob ⇒ false" semantics IS the production contract
//     for the embedding channels (an empty JSON object/array is
//     indistinguishable from NULL for the backfill purpose).
//
// godlike/07 honest lock:
//   - The empty-id-list fast-path on fetchFailedCandidates returns
//     (nil, nil), NOT (nil, err) — the only valid empty-input probe
//     of this function. Any other empty-list code-path would crash
//     on the IN(...) join, so the early return is the canonical
//     "no-op when nothing to retry" pattern.
//
// Sibling-file constraint (Commit E user spec): this file lives in
// cmd/admin (package main), NOT in internal/infrastructure. The
// helpers are one-shot-CLI-only SQL queries. Promoting them to
// internal/infrastructure would force a typed-port interface that
// no other consumer uses — a "dead interface" anti-pattern per
// the PipelineGen architecture rules. Keep them here, period.
package backfill

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/indexing/backfill"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

// embeddingCandidateSource is the narrow media read the embedding backfill
// depends on.
//
// MEDIA-SSOT: production binds the PostgreSQL reader (pgmedia.BackfillReader),
// whose scan applies capregistry.SearchIndexTaxonomySQL — the same eligibility
// boundary the projection planes use. The retired SQLite scan graded the
// operational mirror, which holds no committed media rows.
type embeddingCandidateSource interface {
	ListEmbeddingBackfillCandidates(ctx context.Context, q pgmedia.EmbeddingCandidateQuery) ([]pgmedia.EmbeddingCandidate, error)
	ListEmbeddingCandidatesByID(ctx context.Context, ids []string) ([]pgmedia.EmbeddingCandidate, error)
}

// fetchEmbeddingCandidates queries the media SSOT for assets that need
// embedding backfill. In --only-missing mode, only returns assets with at least
// one empty embedding channel.
func fetchEmbeddingCandidates(
	ctx context.Context,
	src embeddingCandidateSource,
	deps indexing.Deps,
	cp *indexing.Checkpoint,
) ([]indexing.Candidate, error) {
	if src == nil {
		return nil, fmt.Errorf("query embedding candidates: media SSOT reader is not wired")
	}

	q := pgmedia.EmbeddingCandidateQuery{Source: deps.Source, Limit: deps.Limit}
	// Resume: start after the last processed ID.
	if cp != nil && cp.LastProcessedID != "" && deps.Resume {
		q.AfterID = cp.LastProcessedID
	}

	found, err := src.ListEmbeddingBackfillCandidates(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("query embedding candidates: %w", err)
	}
	return embeddingCandidatesFromRows(found, deps.OnlyMissing), nil
}

// embeddingCandidatesFromRows maps SSOT rows onto the indexing candidate model,
// dropping fully-embedded rows in --only-missing mode.
func embeddingCandidatesFromRows(found []pgmedia.EmbeddingCandidate, onlyMissing bool) []indexing.Candidate {
	out := make([]indexing.Candidate, 0, len(found))
	for _, rec := range found {
		candidate := indexing.Candidate{
			ID:            rec.ID,
			Source:        rec.Source,
			Name:          rec.Name,
			MediaType:     rec.MediaType,
			LocalPath:     rec.LocalPath,
			ContentHash:   rec.ContentHash,
			HasText:       rec.HasText,
			HasTranscript: rec.HasTranscript,
			HasVisual:     rec.HasVisual,
			HasAudio:      rec.HasAudio,
		}
		if onlyMissing && candidate.HasText && candidate.HasTranscript && candidate.HasVisual && candidate.HasAudio {
			continue // fully embedded, skip in --only-missing mode
		}
		out = append(out, candidate)
	}
	return out
}

// fetchFailedCandidates returns candidate rows only for the given asset IDs.
func fetchFailedCandidates(ctx context.Context, src embeddingCandidateSource, ids []string) ([]indexing.Candidate, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if src == nil {
		return nil, fmt.Errorf("query failed candidates: media SSOT reader is not wired")
	}
	found, err := src.ListEmbeddingCandidatesByID(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("query failed candidates: %w", err)
	}
	return embeddingCandidatesFromRows(found, false), nil
}
