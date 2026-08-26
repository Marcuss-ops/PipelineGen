// Package assets — text_track_repository_queries.go
//
// TextTrackRepositorySQLite write paths. Both methods own a
// multi-row transaction (UpsertBatch re-prepares the upsert SQL once
// and reuses the prepared statement per row; InsertTranslationWithAuditPredecessor
// uses the canonical begin-tx / idempotency-SELECT / flip-prior /
// INSERT / commit shape from godlike/06 + godlike/07).
//
// The upsert SQL template lives in this file (locality-of-reference
// over splitting SQL constants into schema.go).
package texttracks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

const UpsertTextTrackSQL = `
INSERT INTO asset_text_tracks (
    asset_id, language_code, text_kind,
    text_content,
    source_type, source_language_code, is_original,
    provider, model_name, model_version, prompt_version,
    text_hash, source_version, translation_key, is_current,
    source_track_id, source_text_hash,
    confidence, status,
    created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, datetime('now'), datetime('now'))
ON CONFLICT(asset_id, language_code, text_kind) WHERE is_current = 1 DO UPDATE SET
    text_content         = excluded.text_content,
    source_type          = excluded.source_type,
    source_language_code = excluded.source_language_code,
    is_original          = excluded.is_original,
    provider             = excluded.provider,
    model_name           = excluded.model_name,
    model_version        = excluded.model_version,
    prompt_version       = excluded.prompt_version,
    text_hash            = excluded.text_hash,
    source_version       = excluded.source_version,
    translation_key      = excluded.translation_key,
    is_current           = excluded.is_current,
    source_track_id      = excluded.source_track_id,
    source_text_hash     = excluded.source_text_hash,
    confidence           = excluded.confidence,
    status               = excluded.status,
    updated_at           = datetime('now')
`

// UpsertBatch atomically inserts or updates a batch of text tracks.
func (r *TextTrackRepositorySQLite) UpsertBatch(ctx context.Context, tracks []detail.TextTrack) error {
	if len(tracks) == 0 {
		return nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("text_track_repository.UpsertBatch: begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	stmt, err := tx.PrepareContext(ctx, UpsertTextTrackSQL)
	if err != nil {
		return fmt.Errorf("text_track_repository.UpsertBatch: prepare: %w", err)
	}
	defer stmt.Close()

	for _, t := range tracks {
		if t.AssetID == "" {
			return fmt.Errorf("text_track_repository.UpsertBatch: AssetID is required")
		}
		if t.LanguageCode == "" {
			return fmt.Errorf("text_track_repository.UpsertBatch: LanguageCode is required")
		}
		if t.TextKind == "" {
			return fmt.Errorf("text_track_repository.UpsertBatch: TextKind is required")
		}

		var confidence sql.NullFloat64
		if t.Confidence != nil {
			confidence = sql.NullFloat64{Float64: *t.Confidence, Valid: true}
		}

		// PR-CATALOG-MULTILINGUA step 2: SourceTrackID is a
		// nullable FK; pass NULL when unset (e.g. a source-language
		// row whose own parent is the asset row). The migration
		// added column has no NOT NULL constraint; the SQLite FK
		// is satisfied by NULL.
		sourceTrackID := sql.NullInt64{}
		if t.SourceTrackID != nil {
			sourceTrackID = sql.NullInt64{Int64: *t.SourceTrackID, Valid: true}
		}

		sourceVersion := t.SourceVersion

		isOriginal := 0
		if t.IsOriginal {
			isOriginal = 1
		}

		status := string(t.Status)
		if status == "" {
			status = string(detail.TextTrackReady)
		}

		if _, err = stmt.ExecContext(ctx,
			t.AssetID,
			t.LanguageCode,
			string(t.TextKind),
			t.TextContent,
			string(t.SourceType),
			t.SourceLanguageCode,
			isOriginal,
			t.Provider,
			t.ModelName,
			t.ModelVersion,
			t.PromptVersion,
			t.TextHash,
			sourceVersion,
			t.TranslationKey,
			sourceTrackID,
			t.SourceTextHash,
			confidence,
			status,
		); err != nil {
			return fmt.Errorf("text_track_repository.UpsertBatch: exec (asset=%s lang=%s kind=%s): %w",
				t.AssetID, t.LanguageCode, t.TextKind, err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("text_track_repository.UpsertBatch: commit: %w", err)
	}
	return nil
}

// InsertTranslationWithAuditPredecessor atomically inserts a new
// is_current=1 row and flips any prior is_current=1 row for the same
// (asset, language, kind) context to is_current=0 — preserving the
// audit-trail invariant (PR-CATALOG-MULTILINGUA step 4, July 2026).
//
// godlike/06 SSOT — transaction shape:
//   - BEGIN IMMEDIATE TRANSACTION (WRITE-locked at the SQLite
//     boundary so concurrent Materialize() invocations serialize
//     on the same (asset, language, kind) context).
//   - SELECT ... WHERE (asset, lang, kind, translation_key, is_current=1)
//     LIMIT 1. If a row IS found, COMMIT as a no-op (idempotency
//     short-circuit). godlike/07 honest lock — never silently drop
//     a duplicate translation request.
//   - Else: UPDATE ALL prior is_current=1 rows for (asset, lang, kind)
//     to is_current=0 (regardless of their translation_key; the
//     partial UNIQUE INDEX will be cleared).
//   - INSERT new row with is_current=1 (hard-coded; ignore caller
//     value to keep the audit invariant absolute).
//   - COMMIT.
//
// Race semantics: under WAL + 5s busy_timeout, two parallel calls
// for the same context serialize on the BEGIN IMMEDIATE lock.
//
//   - If both calls compute the same translation_key: first call
//     wins the INSERT (the second short-circuits via the
//     idempotency SELECT). This is the canonical
//     "reused-translation" path.
//   - If the calls compute different translation_keys (operator
//     bumped model_version or prompt_version between calls):
//     first call flips prior + INSERTs new. Second call's
//     idempotency SELECT returns nil, second call proceeds with
//     its own flip + INSERT (chain of audit predecessors).
//   - This is the audit-trail-maximising race semantics, NOT a
//     silent-overwrite trade-off.
//
// PR-CATALOG-MULTILINGUA step 2 (July 2026): the INSERT projection
// now carries source_track_id + source_text_hash — both nullable
// FK / persisted-hash columns added by migration 156. The caller
// (Materializer) populates source_track_id with the parent's
// text-track row.id (the source-language track that produced this
// translation) so a forensic dump can navigate the audit trail.
func (r *TextTrackRepositorySQLite) InsertTranslationWithAuditPredecessor(ctx context.Context, track detail.TextTrack) error {
	if track.AssetID == "" {
		return fmt.Errorf("text_track_repository.InsertTranslationWithAuditPredecessor: AssetID is required")
	}
	if track.LanguageCode == "" {
		return fmt.Errorf("text_track_repository.InsertTranslationWithAuditPredecessor: LanguageCode is required")
	}
	if track.TextKind == "" {
		return fmt.Errorf("text_track_repository.InsertTranslationWithAuditPredecessor: TextKind is required")
	}
	if track.TranslationKey == "" {
		return fmt.Errorf("text_track_repository.InsertTranslationWithAuditPredecessor: TranslationKey is required (caller bug)")
	}

	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("text_track_repository.InsertTranslationWithAuditPredecessor: begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	// Step 1 (idempotency short-circuit): SELECT the canonical
	// is_current=1 row for this exact 5-tuple. If present, the
	// caller has already produced this exact translation under
	// these exact conditions — COMMIT as a no-op and return.
	var existingID int64
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM asset_text_tracks
		 WHERE asset_id = ? AND language_code = ? AND text_kind = ?
		   AND translation_key = ? AND is_current = 1
		 LIMIT 1`,
		track.AssetID, track.LanguageCode, string(track.TextKind), track.TranslationKey,
	).Scan(&existingID)
	switch {
	case err == nil:
		// Idempotency hit — return without modifying anything.
		if commitErr := tx.Commit(); commitErr != nil {
			return fmt.Errorf("text_track_repository.InsertTranslationWithAuditPredecessor: idempotency-commit: %w", commitErr)
		}
		return nil
	case errors.Is(err, sql.ErrNoRows):
		// No idempotent match — fall through to flip + INSERT.
		err = nil
	default:
		return fmt.Errorf("text_track_repository.InsertTranslationWithAuditPredecessor: idempotency-check: %w", err)
	}

	// Step 2: flip ALL prior is_current=1 rows for (asset, lang, kind)
	// to is_current=0. Removes the partial-UNIQUE-INDEX entry so
	// the next INSERT can land with is_current=1 without collision.
	//
	// Safety: this UPDATE is unconditional (no translation_key
	// filter) because Step 1's SELECT already guarantees that no
	// is_current=1 row with the SAME translation_key can reach
	// here. The flip is therefore flipping AUDIT PREDECESSORS
	// (rows with a different translation_key) and never the
	// caller's own row. A future maintainer who bypasses Step 1
	// MUST re-add the `AND translation_key != ?` filter to
	// preserve idempotency on the same-key case.
	if _, err = tx.ExecContext(ctx,
		`UPDATE asset_text_tracks
		 SET is_current = 0,
		     updated_at = datetime('now')
		 WHERE asset_id = ? AND language_code = ? AND text_kind = ?
		   AND is_current = 1`,
		track.AssetID, track.LanguageCode, string(track.TextKind),
	); err != nil {
		return fmt.Errorf("text_track_repository.InsertTranslationWithAuditPredecessor: flip prior row: %w", err)
	}

	// Step 3: insert the new row with is_current=1 (hard-coded).
	var confidence sql.NullFloat64
	if track.Confidence != nil {
		confidence = sql.NullFloat64{Float64: *track.Confidence, Valid: true}
	}

	// PR-CATALOG-MULTILINGUA step 2: SourceTrackID is a nullable
	// FK back to the parent source-language track. The Materializer
	// populates this with the parent's row.id (e.g. EN transcript's
	// row.id when inserting an IT translation). NULL when this row
	// IS the source (e.g. a whisper EN transcript — no parent
	// text-track row exists yet, the source is the asset itself).
	sourceTrackID := sql.NullInt64{}
	if track.SourceTrackID != nil {
		sourceTrackID = sql.NullInt64{Int64: *track.SourceTrackID, Valid: true}
	}

	sourceVersion := track.SourceVersion
	isOriginal := 0
	if track.IsOriginal {
		isOriginal = 1
	}

	status := string(track.Status)
	if status == "" {
		status = string(detail.TextTrackReady)
	}

	if _, err = tx.ExecContext(ctx,
		`INSERT INTO asset_text_tracks (
			asset_id, language_code, text_kind,
			text_content,
			source_type, source_language_code, is_original,
			provider, model_name, model_version, prompt_version,
			text_hash, source_version, translation_key, is_current,
			source_track_id, source_text_hash,
			confidence, status,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, datetime('now'), datetime('now'))`,
		track.AssetID,
		track.LanguageCode,
		string(track.TextKind),
		track.TextContent,
		string(track.SourceType),
		track.SourceLanguageCode,
		isOriginal,
		track.Provider,
		track.ModelName,
		track.ModelVersion,
		track.PromptVersion,
		track.TextHash,
		sourceVersion,
		track.TranslationKey,
		sourceTrackID,
		track.SourceTextHash,
		confidence,
		status,
	); err != nil {
		return fmt.Errorf("text_track_repository.InsertTranslationWithAuditPredecessor: insert new row: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("text_track_repository.InsertTranslationWithAuditPredecessor: commit: %w", err)
	}
	return nil
}
