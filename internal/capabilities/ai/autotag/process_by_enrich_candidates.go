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

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ProcessByEnrichCandidates scans media_assets for rows whose canonical
// media_assets.enrich_state column is PENDING AND whose
// enrich_state_updated_at stamp is older than now()-claimFence (the
// sweep claim-fence race-mitigation). Returns the number of rows
// successfully tagged.
//
// Mirrors the legacy sweep's outer contract (size limits, VLM-disabled
// fail-closed, per-row TagAsset invocation) but uses the typed-state
// SQL filter as the godlike/06 SSOT scan surface. State transitions
// (PENDING→ENRICHING on claim; ENRICHING→ENRICHED on success;
// ENRICHING→FAILED on error) are owned by the application-layer
// EnrichStateMachine wrapper
// (internal/capabilities/assets/enrichment/state_machine.go). The
// autotag service is the typed-state consumer: it READS the column
// and INVOKES the state-machine wrappers, which are injected via the
// composition root.
func (s *Service) ProcessByEnrichCandidates(ctx context.Context, limit int, claimFence time.Duration) (int, error) {
	if !s.vlmClient.IsEnabled() {
		return 0, fmt.Errorf("VLM client is disabled")
	}
	if s.enrichState == nil {
		return 0, fmt.Errorf("enrichment state machine is not wired")
	}

	if limit <= 0 {
		limit = 10
	}

	// Canonical godlike/06 SSOT typed-state filter (migration 123):
	//   WHERE enrich_state = 'PENDING'
	//     AND enrich_state_updated_at < datetime('now', ?)
	//     AND media_type != 'folder'
	//     AND local_path != ''
	// ORDER BY enrich_state_updated_at ASC LIMIT ?
	// The ORDER BY surfaces the OLDEST scrape candidate first so
	// long-pending rows get processed before brand-new ones.
	query := `
		SELECT id, enrich_state FROM media_assets
		WHERE enrich_state = 'PENDING'
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

	type candidate struct {
		id    string
		state asset.EnrichState
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		var rawState string
		if err := rows.Scan(&c.id, &rawState); err != nil {
			s.log.Warn("scan enrich candidate failed", zap.Error(err))
			continue
		}
		c.state = asset.EnrichState(rawState)
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("enrich candidate rows: %w", err)
	}
	if len(candidates) == 0 {
		return 0, nil
	}

	// The state machine owns the Tier-1 transitions: each candidate is
	// atomically claimed (PENDING→ENRICHING), processed by TagAsset,
	// and then marked ENRICHED or FAILED by the typed state-machine
	// wrapper. The legacy metadata_json.$.vlm_tagged marker is still
	// written by TagAsset for dashboard compatibility.
	s.log.Info("starting typed-state VLM batch (PR-ENRICHMENT-STATE-MACHINE EXPAND)",
		zap.Int("count", len(candidates)),
		zap.String("claim_fence", fenceMod))

	processed := 0
	for _, c := range candidates {
		if c.state != asset.EnrichStatePending {
			s.log.Debug("skipping non-scrape candidate", zap.String("id", c.id), zap.String("state", string(c.state)))
			continue
		}

		// 1. Atomically claim the row via the typed state machine.
		// If another worker claimed it concurrently, this will fail
		// and we move on to the next candidate.
		if err := s.enrichState.ClaimForEnrichment(ctx, c.id, c.state); err != nil {
			s.log.Debug("failed to claim asset for enrichment (likely already claimed)",
				zap.String("id", c.id), zap.Error(err))
			continue
		}

		// 2. Re-fetch the full Asset row (the scan only reads id; TagAsset
		// needs the full row to drive the tags merge + metadata writes).
		a, fetchErr := s.repo.Get(ctx, c.id)
		if fetchErr != nil {
			s.log.Warn("failed to fetch asset for enrichment, marking failed",
				zap.String("id", c.id), zap.Error(fetchErr))
			_ = s.enrichState.MarkFailed(ctx, c.id)
			continue
		}
		if a == nil {
			_ = s.enrichState.MarkFailed(ctx, c.id)
			continue
		}

		// 3. Run VLM tagging.
		if err := s.TagAsset(ctx, a); err != nil {
			s.log.Warn("failed to tag asset (typed-state sweep), marking failed",
				zap.String("id", c.id), zap.Error(err))
			_ = s.enrichState.MarkFailed(ctx, c.id)
			continue
		}

		// 4. Close the success terminal.
		if err := s.enrichState.MarkEnriched(ctx, c.id); err != nil {
			s.log.Error("failed to mark asset as enriched", zap.String("id", c.id), zap.Error(err))
			continue
		}

		processed++
	}

	return processed, nil
}
