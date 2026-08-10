// Package assets — voiceover_projection.go (P0.7 Step 9/12, June 2026).
//
// SQL verification contract.
//
// The P0.7 2-PHASE SPLIT (Step 9/12, Wave 21) eliminated the
// partial-save bug in the voiceover finalizer by collapsing the
// pre-fix double-write (lifecycle.ProcessAsset wrote media_assets at
// Stage 2, finalizeStage wrote voiceovers at Stage 3 in a SECOND tx)
// into a SINGLE caller-owned tx inside finalizeStage. The
// media_assets projection UPSERT now lives INSIDE the finalizeStage
// tx (lifecycle.Service.UpsertVoiceoverProjectionTx) and commits
// atomically with the voiceovers row + the asset.index.requested
// outbox event.
//
// This file pins that contract via a single SQL verification query:
//
//	SELECT 1 FROM media_assets WHERE id = ? AND source = 'voiceover'.
//
// The contract:
//
//   - On a SUCCESSFUL finalizeStage tx, the query returns 1
//     (media_assets row exists with the canonical id AND the
//     canonical source discriminator).
//   - On a FAILED finalizeStage tx (rolled-back), the query returns
//     0 — the tx rollback undoes the UPSERT atomically, so partial
//     state is impossible. This was the pre-fix bug surface: a
//     success-then-failure pair would leave an orphan row.
//
// The HasVoiceoverProjection helper is the canonical assets projection
// verification reference for integration tests, downstream code-search
// auditing, and operator-facing SQL verification scripts. It is intentionally NOT lint-protected
// (sqlnear inline literals are acceptable in verification/audit helpers
// — the canonical media_assets write path owns SQL composition).
//
// Migration note: source='voiceover' was the canonical discriminator
// before Step 9/12 (used by the legacy lifecycle.ProcessAsset
// UpsertMedia path). Step 9/12 PRESERVES this discriminator — a
// smoke test for full migration is the existing count of media_assets
// rows with source='voiceover', which should equal the count of
// voiceovers.id rows post-cutover.
package assets

import (
	"context"
	"database/sql"
	"fmt"
)

// HasVoiceoverProjection verifies the SQL contract of the P0.7
// 2-PHASE SPLIT (Step 9/12): a media_assets row exists with
// `source='voiceover' AND id=?` for the given voiceover ID. Returns
// (true, nil) on hit, (false, nil) on miss, (false, err) on driver
// error.
//
// Use this helper for:
//   - post-step integration tests (Step 10/12 audit checklist).
//   - operator SQL smoke scripts (admin CLI verification).
//   - documentation examples in AGENTS.md "Known issues" section.
//
// Parameters:
//   - ctx: caller context (the verification is read-only — pass
//     the request context, NOT context.Background).
//   - db: a *sql.DB backed by the canonical media.db.sqlite
//     database. The voiceover package does NOT own this DB; the
//     caller (composition root, tests, ops scripts) supplies it.
//   - voiceoverID: the canonical voiceovers.id (also the canonical
//     media_assets.id — both tables share the same primary key).
//
// Empty voiceoverID short-circuits to (false, nil) so callers can
// safely forward an empty id without a sentinel error.
func HasVoiceoverProjection(ctx context.Context, db *sql.DB, voiceoverID string) (bool, error) {
	if voiceoverID == "" {
		return false, nil
	}
	if db == nil {
		return false, fmt.Errorf("voiceover.HasVoiceoverProjection: nil *sql.DB (caller forgot to supply the canonical media.db.sqlite handle)")
	}
	if err := ctx.Err(); err != nil {
		return false, fmt.Errorf("voiceover.HasVoiceoverProjection: caller context cancelled: %w", err)
	}

	const query = `
		SELECT 1
		  FROM media_assets
		 WHERE id = ?
		   AND source = 'voiceover'
		 LIMIT 1
	`
	var hit int
	if scanErr := db.QueryRowContext(ctx, query, voiceoverID).Scan(&hit); scanErr != nil {
		if scanErr == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("voiceover.HasVoiceoverProjection: SELECT 1 FROM media_assets WHERE id=? AND source='voiceover' LIMIT 1: %w", scanErr)
	}
	return true, nil
}

// CountVoiceoverProjections returns the number of media_assets rows
// with `source='voiceover'`. Intended for the migration audit checklist:
//
//   - pre-Step 9/12: count == count(voiceovers.id rows), because
//     ProcessAsset wrote a media_assets row for every successful
//     Stage-2 upload.
//   - post-Step 9/12 (planned): same equality MUST hold, via the
//     new caller-owned tx in finalizeStage (UpsertVoiceoverProjectionTx
//     inside the same tx as the voiceovers INSERT).
//
// Deviation: count > count(voiceovers.id rows) signals an orphan
// (pre-Step 9/12 partial-save bug surface). count < count(voiceovers.id
// rows) signals a missed projection (post-Step 9/12 backwards drift).
// Operators should treat either as a P0 incident — open a ticket
// referencing architecture/current.yaml::step-9-of-12 and the
// affected voiceovers rows.
//
// Empty parameters short-circuit to (0, nil) so a unit-test caller
// without a populated DB does not error out.
func CountVoiceoverProjections(ctx context.Context, db *sql.DB) (int, error) {
	if db == nil {
		return 0, nil
	}
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("voiceover.CountVoiceoverProjections: caller context cancelled: %w", err)
	}

	const query = `
		SELECT COUNT(*)
		  FROM media_assets
		 WHERE source = 'voiceover'
	`
	var count int
	if err := db.QueryRowContext(ctx, query).Scan(&count); err != nil {
		return 0, fmt.Errorf("voiceover.CountVoiceoverProjections: SELECT COUNT(*) FROM media_assets WHERE source='voiceover': %w", err)
	}
	return count, nil
}
