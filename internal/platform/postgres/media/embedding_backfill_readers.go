// Package media — embedding_backfill_readers.go: the media reads the
// embedding-repair commands depend on (searchable-scope candidate scan by
// keyset/source, retry-by-id, and the missing-vector scan).
//
// MEDIA LEGACY READ-PLANE DEMOLITION (2026-09-20): these replace the
// media_assets candidate scans the admin embedding backfills ran on the
// operational SQLite handle. media_assets is PostgreSQL-owned and that mirror
// holds no committed media rows, so a repair could only ever report zero
// candidates while the SSOT held them. The reads are read-only by
// construction; the enqueue/reindex path stays on the canonical outbox.
package media

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

// EmbeddingCandidate is one media asset the canonical embedding backfill may
// have to embed, together with which embedding channels it already has.
type EmbeddingCandidate struct {
	ID            string
	Source        string
	Name          string
	MediaType     string
	LocalPath     string `json:"-"`
	ContentHash   string
	HasText       bool
	HasTranscript bool
	HasVisual     bool
	HasAudio      bool
}

// EmbeddingCandidateQuery bounds the embedding-backfill candidate scan.
type EmbeddingCandidateQuery struct {
	// AfterID resumes a keyset scan: only rows with a greater id are returned.
	AfterID string
	// Source restricts the scan to one provider when non-empty.
	Source string
	// Limit is the row cap; zero means all.
	Limit int
}

// embeddingPresenceExpr renders the "channel already populated" test for one
// embedding column. A blank, '[]' or '{}' value counts as MISSING, matching the
// retired SQLite CASE expressions exactly (a legacy row that stored an empty
// JSON array never had a usable vector).
func embeddingPresenceExpr(column string) string {
	return "CASE WHEN " + column + " IS NOT NULL AND " + column + " NOT IN ('', '[]', '{}') THEN 1 ELSE 0 END"
}

// mediaContentHashExpr is the canonical content-hash fallback chain
// (metadata content_hash → metadata file_hash → legacy_file_md5 → empty).
func mediaContentHashExpr(alias string) string {
	json := "COALESCE(NULLIF(" + alias + ".metadata_json, ''), '{}')::jsonb"
	return "COALESCE(" + json + "->>'content_hash', " + json + "->>'file_hash', " + alias + ".legacy_file_md5, '')"
}

// ListEmbeddingBackfillCandidates returns the searchable media assets the
// embedding backfill may embed, in id order.
//
// godlike/06 SSOT: the taxonomy boundary is
// capregistry.SearchIndexTaxonomySQL — the same constant the projection planes
// use — so a registered audio/document/text row is never sent through the
// semantic pipeline, and the backfill cannot grade a different scope than the
// indexer it feeds.
func (r *BackfillReader) ListEmbeddingBackfillCandidates(ctx context.Context, q EmbeddingCandidateQuery) ([]EmbeddingCandidate, error) {
	if err := r.ready("list embedding candidates"); err != nil {
		return nil, err
	}
	query := `
		SELECT m.id, COALESCE(m.source, ''), COALESCE(m.name, ''), COALESCE(m.media_type, ''),
		       COALESCE(m.local_path, ''),
		       ` + mediaContentHashExpr("m") + `,
		       ` + embeddingPresenceExpr("m.embedding_json") + `,
		       ` + embeddingPresenceExpr("m.transcript_embedding") + `,
		       ` + embeddingPresenceExpr("m.visual_embedding") + `,
		       ` + embeddingPresenceExpr("m.audio_embedding") + `
		FROM media_assets m
		WHERE ` + capregistry.SearchIndexTaxonomySQL

	args := []any{}
	if q.AfterID != "" {
		args = append(args, q.AfterID)
		query += fmt.Sprintf(" AND m.id > $%d", len(args))
	}
	if q.Source != "" {
		args = append(args, q.Source)
		query += fmt.Sprintf(" AND m.source = $%d", len(args))
	}
	query += ` ORDER BY m.id ASC`
	if q.Limit > 0 {
		args = append(args, q.Limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres media backfill: query embedding candidates: %w", err)
	}
	defer rows.Close()

	var out []EmbeddingCandidate
	for rows.Next() {
		var rec EmbeddingCandidate
		var hasText, hasTranscript, hasVisual, hasAudio int
		if err := rows.Scan(&rec.ID, &rec.Source, &rec.Name, &rec.MediaType, &rec.LocalPath, &rec.ContentHash,
			&hasText, &hasTranscript, &hasVisual, &hasAudio); err != nil {
			return nil, fmt.Errorf("postgres media backfill: scan embedding candidate: %w", err)
		}
		rec.HasText = hasText == 1
		rec.HasTranscript = hasTranscript == 1
		rec.HasVisual = hasVisual == 1
		rec.HasAudio = hasAudio == 1
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres media backfill: iterate embedding candidates: %w", err)
	}
	return out, nil
}

// ListEmbeddingCandidatesByID returns the same projection restricted to an
// explicit asset set (the retry path: only the failed ids are re-read).
func (r *BackfillReader) ListEmbeddingCandidatesByID(ctx context.Context, ids []string) ([]EmbeddingCandidate, error) {
	if err := r.ready("list embedding candidates by id"); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	marks := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
		marks[i] = fmt.Sprintf("$%d", i+1)
	}
	query := `
		SELECT m.id, COALESCE(m.source, ''), COALESCE(m.name, ''), COALESCE(m.media_type, ''),
		       COALESCE(m.local_path, ''),
		       ` + mediaContentHashExpr("m") + `,
		       ` + embeddingPresenceExpr("m.embedding_json") + `,
		       ` + embeddingPresenceExpr("m.transcript_embedding") + `,
		       ` + embeddingPresenceExpr("m.visual_embedding") + `,
		       ` + embeddingPresenceExpr("m.audio_embedding") + `
		FROM media_assets m
		WHERE m.id IN (` + strings.Join(marks, ",") + `)
		  AND ` + capregistry.SearchIndexTaxonomySQL + `
		ORDER BY m.id ASC`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres media backfill: query embedding candidates by id: %w", err)
	}
	defer rows.Close()

	var out []EmbeddingCandidate
	for rows.Next() {
		var rec EmbeddingCandidate
		var hasText, hasTranscript, hasVisual, hasAudio int
		if err := rows.Scan(&rec.ID, &rec.Source, &rec.Name, &rec.MediaType, &rec.LocalPath, &rec.ContentHash,
			&hasText, &hasTranscript, &hasVisual, &hasAudio); err != nil {
			return nil, fmt.Errorf("postgres media backfill: scan embedding candidate by id: %w", err)
		}
		rec.HasText = hasText == 1
		rec.HasTranscript = hasTranscript == 1
		rec.HasVisual = hasVisual == 1
		rec.HasAudio = hasAudio == 1
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres media backfill: iterate embedding candidates by id: %w", err)
	}
	return out, nil
}

// MissingEmbeddingAsset is one asset with no text vector at all, as the
// `backfill-missing` command needs it (id, provenance, content hash).
type MissingEmbeddingAsset struct {
	ID          string
	Source      string
	Name        string
	ContentHash string
}

// ListMissingEmbeddingAssets returns the assets whose embedding_json is empty,
// newest first, optionally restricted to an explicit id set and/or one source.
//
// The retired SQLite scan had NO taxonomy filter (unlike the embedding
// backfill), because this command re-enqueues assets the operator explicitly
// asked about; that scope is preserved deliberately rather than tightened.
func (r *BackfillReader) ListMissingEmbeddingAssets(ctx context.Context, ids []string, source string, limit int) ([]MissingEmbeddingAsset, error) {
	if err := r.ready("list missing embedding assets"); err != nil {
		return nil, err
	}
	args := []any{}
	query := `
		SELECT m.id, COALESCE(m.source, ''), m.name, ` + mediaContentHashExpr("m") + ` AS content_hash
		FROM media_assets m
		WHERE `
	if len(ids) > 0 {
		marks := make([]string, len(ids))
		for i, id := range ids {
			args = append(args, id)
			marks[i] = fmt.Sprintf("$%d", len(args))
		}
		query += "m.id IN (" + strings.Join(marks, ",") + ")"
	} else {
		query += "(m.embedding_json IS NULL OR m.embedding_json = '[]' OR m.embedding_json = '')"
	}
	if strings.TrimSpace(source) != "" {
		args = append(args, source)
		query += fmt.Sprintf(" AND m.source = $%d", len(args))
	}
	query += ` ORDER BY m.created_at DESC`
	if limit > 0 {
		args = append(args, limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres media backfill: query missing embedding assets: %w", err)
	}
	defer rows.Close()

	var out []MissingEmbeddingAsset
	for rows.Next() {
		var rec MissingEmbeddingAsset
		var name sql.NullString
		var contentHash sql.NullString
		if err := rows.Scan(&rec.ID, &rec.Source, &name, &contentHash); err != nil {
			return nil, fmt.Errorf("postgres media backfill: scan missing embedding asset: %w", err)
		}
		rec.Name = name.String
		if contentHash.Valid && contentHash.String != "" {
			rec.ContentHash = contentHash.String
		} else {
			rec.ContentHash = "legacy_no_hash_" + rec.ID
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres media backfill: iterate missing embedding assets: %w", err)
	}
	return out, nil
}
