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

// EnrichmentCandidateReader is the narrow media-SSOT read behind the VLM sweep
// selector.
//
// CONTRACT: it returns the ids of assets whose canonical enrich_state is PENDING
// and whose enrich_state_updated_at stamp is older than claimFence, OLDEST FIRST,
// capped at limit. The PENDING clause is part of the contract, not an
// implementation detail — the retired selector returned the state alongside each
// id so the caller could re-check it defensively, and that returned state was
// always PENDING because the same predicate had already filtered it. Ids are
// enough, and keeping the return type free of a shared struct is what lets the
// platform implementation satisfy this port structurally without importing this
// capability.
//
// Declaring the port here — instead of reaching for a *sql.DB — is what keeps
// this capability from naming an engine. PostgreSQL is the only production
// implementation (pgmedia.MediaEnrichmentCandidateReader), resolved by the
// composition root from the canonical committer's handle, which is also what
// resolves the enrichment state machine: the scan and the claim therefore share
// one engine by construction. A nil implementation is a media-plane-closed
// signal and ProcessByEnrichCandidates fails closed.
type EnrichmentCandidateReader interface {
	PendingEnrichCandidates(ctx context.Context, claimFence time.Duration, limit int) ([]string, error)
}

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

	// MEDIA-SSOT P2-9 Phase 2: the candidate scan reads the media SSOT through
	// the narrow EnrichmentCandidateReader port, not through s.db (the
	// operational store). media_assets is owned by PostgreSQL, so the previous
	// SQLite scan graded — and the sweeper then claimed — rows on a database no
	// canonical media writer maintains. Read and claim now share one engine by
	// construction: the claim goes through EnrichStateMachine, whose repository
	// port is resolved from the same canonical committer (see
	// wiring.enrichStateStoreFromCommitter).
	//
	// A nil reader fails closed rather than silently reporting an empty sweep:
	// "no candidates" and "cannot see the candidate catalog" are different
	// facts, and only one of them is safe to act on.
	if s.enrichCandidates == nil {
		return 0, fmt.Errorf("enrichment candidate reader is not wired (media SSOT closed)")
	}
	candidates, err := s.enrichCandidates.PendingEnrichCandidates(ctx, claimFence, limit)
	if err != nil {
		return 0, fmt.Errorf("query enrich candidates: %w", err)
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
		zap.String("claim_fence", claimFence.String()))

	processed := 0
	for _, candidateID := range candidates {
		// The PENDING state is guaranteed by the EnrichmentCandidateReader
		// contract (the selector's own predicate filters it), so the retired
		// defensive re-check is now an invariant of the port rather than a
		// branch here. The claim below is the authoritative gate either way: a
		// row that is not PENDING loses the CAS and is skipped.

		// 1. Atomically claim the row via the typed state machine.
		// If another worker claimed it concurrently, this will fail
		// and we move on to the next candidate.
		if err := s.enrichState.ClaimForEnrichment(ctx, candidateID, asset.EnrichStatePending); err != nil {
			s.log.Debug("failed to claim asset for enrichment (likely already claimed)",
				zap.String("id", candidateID), zap.Error(err))
			continue
		}

		// 2. Re-fetch the full Asset row (the scan only reads id; TagAsset
		// needs the full row to drive the tags merge + metadata writes).
		a, fetchErr := s.repo.Get(ctx, candidateID)
		if fetchErr != nil {
			s.log.Warn("failed to fetch asset for enrichment, marking failed",
				zap.String("id", candidateID), zap.Error(fetchErr))
			_ = s.enrichState.MarkFailed(ctx, candidateID)
			continue
		}
		if a == nil {
			_ = s.enrichState.MarkFailed(ctx, candidateID)
			continue
		}

		// 3. Run VLM tagging.
		if err := s.TagAsset(ctx, a); err != nil {
			s.log.Warn("failed to tag asset (typed-state sweep), marking failed",
				zap.String("id", candidateID), zap.Error(err))
			_ = s.enrichState.MarkFailed(ctx, candidateID)
			continue
		}

		// 4. Close the success terminal.
		if err := s.enrichState.MarkEnriched(ctx, candidateID); err != nil {
			s.log.Error("failed to mark asset as enriched", zap.String("id", candidateID), zap.Error(err))
			continue
		}

		processed++
	}

	return processed, nil
}
