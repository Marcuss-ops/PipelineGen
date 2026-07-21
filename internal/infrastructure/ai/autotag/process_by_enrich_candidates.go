// Package autotag — process_by_enrich_candidates.go is the canonical
// typed-state-aware VLM sweep surface (PR-ENRICHMENT-STATE-MACHINE,
// July 2026, godlike/06 SSOT).
//
// ProcessByEnrichCandidates is the canonical selector used by the
// VLM 15-min sweeper (internal/app/lifecycle_sweepers.go::startVLMAutoTagSweeper).
// It reads the canonical media_assets.enrich_state column (migration 123)
// instead of the retired JSON-extract "tags is null OR tags=” + no
// vlm_tagged flag" filter. The legacy ProcessUntagged path has been
// removed; ProcessByEnrichCandidates is now the only sweep surface
// for VLM auto-tagging.
//
// Design notes:
//   - godlike/06 SSOT "one owner per fact" + AGENTS.md Pattern 0: the
//     typed-state filter is owned by the enrichment package (PR-
//     ENRICHMENT-STATE-MACHINE). ProcessByEnrichCandidates wraps the
//     typed-state filter in its own scoped query.
//   - godlike/07 typed-error contract: ProcessByEnrichCandidates
//     surfaces typed errors via the existing TagAsset error path
//     (which already explicitly marks metadata_json.$.vlm_tagged =
//     "failed" before returning — the typed-error envelope is the
//     metadata_json marker, no added sentinel needed because the
//     existing VLM-mark shape already signals failure to the
//     operator dashboard without a logical silent-success surface).
//
// claimFence invariant: rows whose enrich_state_updated_at is more
// recent than `now-claimFence` are excluded from the query. This is
// the canonical race-mitigation pattern (mirrors PR-EMBEDDING-
// CHANNEL-REGISTRY): a slow VLM call on row X (claimed at T0,
// enrich_state_updated_at stamped when the row entered ENRICHING)
// doesn't get re-claimed at T0+1min by an overlapping sweep tick
// that hasn't seen the updated_at stamp. 30s is the canonical
// default per lifecycle_sweepers.go::startVLMAutoTagSweeper.
package autotag

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// ProcessByEnrichCandidates scans media_assets for rows whose canonical
// media_assets.enrich_state column is in {PENDING, FAILED} AND whose
// enrich_state_updated_at stamp is older than now()-claimFence (the
// sweep claim-fence race-mitigation). Returns the number of rows
// successfully tagged.
//
// Mirrors the legacy sweep's outer contract (size limits, VLM-disabled
// fail-closed, per-row TagAsset invocation) but uses the typed-state
// SQL filter as the godlike/06 SSOT scan surface. Does NOT mutate
// enrich_state directly — the typed transitions
// (PENDING|FAILED→ENRICHING on claim; ENRICHING→ENRICHED on success;
// ENRICHING→FAILED on error) are owned by the application-layer
// EnrichStateMachine wrapper
// (internal/application/assets/enrichment/state_machine.go). The
// autotag service is a TypedStateFilterConsumer: it READS the column
// + INVOKES the wrappers (via the composition root's injected
// enrichState port — see PR-ENRICHMENT-STATE-MACHINE-BACKFILL for the
// composition-root wiring forward-pointer).
//
// EXPAND-phase discipline: this method ships the SQL-filter change
// + race-mitigation claim-fence + typed-state contract; the actual
// Tier-1 transitions (PENDING→ENRICHING via ClaimForEnrichment) are
// a follow-up BACKFILL wiring PR. Until that lands, the typed-state
// scan reads only — TagAsset still drives the metadata_json
// vlm_tagged marker + tags merge.
func (s *Service) ProcessByEnrichCandidates(ctx context.Context, limit int, claimFence time.Duration) (int, error) {
	if !s.vlmClient.IsEnabled() {
		return 0, fmt.Errorf("VLM client is disabled")
	}

	if limit <= 0 {
		limit = 10
	}

	// Canonical godlike/06 SSOT typed-state filter (migration 123):
	//   WHERE enrich_state IN ('PENDING','FAILED')
	//     AND enrich_state_updated_at < datetime('now', ?)
	//     AND media_type != 'folder'
	//     AND local_path != ''
	// ORDER BY enrich_state_updated_at ASC LIMIT ?
	// The ORDER BY surfaces the OLDEST scrape candidate first so
	// long-pending rows get processed before brand-new ones (the
	// expiring-sweep semantics — pre-PR rows that somehow bypassed
	// the ingest canonical stamp surface first).
	query := `
		SELECT id FROM media_assets
		WHERE enrich_state IN ('PENDING','FAILED')
		  AND enrich_state_updated_at < datetime('now', ?)
		  AND media_type != 'folder'
		  AND local_path != ''
		ORDER BY enrich_state_updated_at ASC
		LIMIT ?
	`

	// claimFence is encoded as a relative-time SQL modifier; SQLite
	// accepts negative seconds offset string. claimFence of 30s
	// becomes '-30 seconds' which datetime('now', '-30 seconds') reads
	// as now-30s.
	fenceMod := fmt.Sprintf("-%d seconds", int(claimFence.Seconds()))

	rows, err := s.db.QueryContext(ctx, query, fenceMod, limit)
	if err != nil {
		return 0, fmt.Errorf("query enrich candidates: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			s.log.Warn("scan enrich candidate id failed", zap.Error(err))
			continue
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("enrich candidate rows: %w", err)
	}
	if len(ids) == 0 {
		return 0, nil
	}

	// EXPAND-phase discipline (see package doc): the typed-state Tier-1
	// transitions (PENDING→ENRICHING on claim; ENRICHING→ENRICHED on
	// success; ENRICHING→FAILED on error) are a future BACKFILL wiring
	// PR. Until that lands, TagAsset is invoked against the candidate
	// set selected by the typed-state filter. The TagAsset success/failure
	// path already writes metadata_json.$.vlm_tagged = "success"/"failed"
	// which is the godlike/07 typed-error marker the operator dashboard
	// already reads. Future BACKFILL replaces these with the canonical
	// enrich_state column writes via the typed state-machine wrapper.
	s.log.Info("starting typed-state VLM batch (PR-ENRICHMENT-STATE-MACHINE EXPAND)",
		zap.Int("count", len(ids)),
		zap.String("claim_fence", fenceMod))

	processed := 0
	for _, id := range ids {
		// Re-fetch the full Asset row (the scan only reads id; TagAsset
		// needs the full row to drive the tags merge + metadata writes).
		a, fetchErr := s.repo.Get(ctx, id)
		if fetchErr != nil {
			s.log.Warn("failed to fetch asset for enrichment",
				zap.String("id", id), zap.Error(fetchErr))
			continue
		}
		if a == nil {
			continue
		}
		if err := s.TagAsset(ctx, a); err != nil {
			s.log.Warn("failed to tag asset (typed-state sweep)",
				zap.String("id", id), zap.Error(err))
			continue
		}
		processed++
	}

	return processed, nil
}
