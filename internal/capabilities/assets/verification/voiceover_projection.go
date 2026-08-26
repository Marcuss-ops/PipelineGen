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
// The VoiceoverProjectionReader port is the canonical assets projection
// verification reference for integration tests, downstream code-search
// auditing, and operator-facing SQL verification scripts. The capability
// owns the PORT only; the concrete SQLite implementation lives in
// internal/platform/sqlite/verification and receives the *sql.DB handle
// at construction. This package never imports database/sql.
//
// Migration note: source='voiceover' was the canonical discriminator
// before Step 9/12 (used by the legacy lifecycle.ProcessAsset
// UpsertMedia path). Step 9/12 PRESERVES this discriminator — a
// smoke test for full migration is the existing count of media_assets
// rows with source='voiceover', which should equal the count of
// voiceovers.id rows post-cutover.
package verification

import "context"

// VoiceoverProjectionReader is the capability-owned port for verifying the
// media_assets voiceover projection (P0.7 2-PHASE SPLIT, Step 9/12). The
// canonical SQLite adapter is internal/platform/sqlite/verification.
type VoiceoverProjectionReader interface {
	// HasVoiceoverProjection verifies the SQL contract of the P0.7
	// 2-PHASE SPLIT: a media_assets row exists with `source='voiceover'
	// AND id=?` for the given voiceover ID. Returns (true, nil) on hit,
	// (false, nil) on miss, (false, err) on driver error. An empty
	// voiceoverID short-circuits to (false, nil).
	HasVoiceoverProjection(ctx context.Context, voiceoverID string) (bool, error)

	// CountVoiceoverProjections returns the number of media_assets rows
	// with `source='voiceover'`. Intended for the migration audit
	// checklist (pre/post Step 9/12 equality against count(voiceovers.id)).
	CountVoiceoverProjections(ctx context.Context) (int, error)
}
