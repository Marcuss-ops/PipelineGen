package clips

import (
	"context"
	"fmt"
)

// SegmentEmbeddingRecord stores the semantic cache for a script segment.
type SegmentEmbeddingRecord struct {
	ID                    int64
	ScriptKey             string
	SourceHash            string
	Topic                 string
	Language              string
	Template              string
	Duration              int
	SegmentIndex          int
	RawSubject            string
	CanonicalSubject      string
	RawKeywordsJSON       string
	CanonicalKeywordsJSON string
	RawEntitiesJSON       string
	CanonicalEntitiesJSON string
	SegmentJSON           string
	EmbeddingJSON         string
	BestSource            string
	BestPath              string
	BestLink              string
	BestScore             int
}

// DeleteSegmentEmbeddingsByScriptKey removes all cached segments for a script key.
func (r *Repository) DeleteSegmentEmbeddingsByScriptKey(ctx context.Context, scriptKey string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM segment_embeddings WHERE script_key = ?`, scriptKey)
	return err
}

// GetSegmentEmbeddingsByScriptKey loads cached segments for a script key.
func (r *Repository) GetSegmentEmbeddingsByScriptKey(ctx context.Context, scriptKey string) ([]SegmentEmbeddingRecord, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, script_key, source_hash, topic, language, template, duration, segment_index,
		       raw_subject, canonical_subject, raw_keywords_json, canonical_keywords_json,
		       raw_entities_json, canonical_entities_json, segment_json, embedding_json,
		       best_source, best_path, best_link, best_score
		FROM segment_embeddings
		WHERE script_key = ?
		ORDER BY segment_index ASC
	`, scriptKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]SegmentEmbeddingRecord, 0)
	for rows.Next() {
		var rec SegmentEmbeddingRecord
		if err := rows.Scan(
			&rec.ID, &rec.ScriptKey, &rec.SourceHash, &rec.Topic, &rec.Language, &rec.Template,
			&rec.Duration, &rec.SegmentIndex, &rec.RawSubject, &rec.CanonicalSubject,
			&rec.RawKeywordsJSON, &rec.CanonicalKeywordsJSON, &rec.RawEntitiesJSON,
			&rec.CanonicalEntitiesJSON, &rec.SegmentJSON, &rec.EmbeddingJSON,
			&rec.BestSource, &rec.BestPath, &rec.BestLink, &rec.BestScore,
		); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// UpsertSegmentEmbedding stores a segment cache row.
func (r *Repository) UpsertSegmentEmbedding(ctx context.Context, rec *SegmentEmbeddingRecord) error {
	if rec == nil {
		return fmt.Errorf("nil segment embedding record")
	}
	if rec.ScriptKey == "" {
		return fmt.Errorf("segment embedding script key is required")
	}
	if rec.SegmentIndex <= 0 {
		return fmt.Errorf("segment embedding index must be positive")
	}

	if rec.RawKeywordsJSON == "" {
		rec.RawKeywordsJSON = "[]"
	}
	if rec.CanonicalKeywordsJSON == "" {
		rec.CanonicalKeywordsJSON = "[]"
	}
	if rec.RawEntitiesJSON == "" {
		rec.RawEntitiesJSON = "[]"
	}
	if rec.CanonicalEntitiesJSON == "" {
		rec.CanonicalEntitiesJSON = "[]"
	}
	if rec.SegmentJSON == "" {
		rec.SegmentJSON = "{}"
	}
	if rec.EmbeddingJSON == "" {
		rec.EmbeddingJSON = "[]"
	}

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO segment_embeddings (
			script_key, source_hash, topic, language, template, duration, segment_index,
			raw_subject, canonical_subject, raw_keywords_json, canonical_keywords_json,
			raw_entities_json, canonical_entities_json, segment_json, embedding_json,
			best_source, best_path, best_link, best_score, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT(script_key, segment_index) DO UPDATE SET
			source_hash=excluded.source_hash,
			topic=excluded.topic,
			language=excluded.language,
			template=excluded.template,
			duration=excluded.duration,
			raw_subject=excluded.raw_subject,
			canonical_subject=excluded.canonical_subject,
			raw_keywords_json=excluded.raw_keywords_json,
			canonical_keywords_json=excluded.canonical_keywords_json,
			raw_entities_json=excluded.raw_entities_json,
			canonical_entities_json=excluded.canonical_entities_json,
			segment_json=excluded.segment_json,
			embedding_json=excluded.embedding_json,
			best_source=excluded.best_source,
			best_path=excluded.best_path,
			best_link=excluded.best_link,
			best_score=excluded.best_score,
			updated_at=datetime('now')
	`, rec.ScriptKey, rec.SourceHash, rec.Topic, rec.Language, rec.Template, rec.Duration, rec.SegmentIndex,
		rec.RawSubject, rec.CanonicalSubject, rec.RawKeywordsJSON, rec.CanonicalKeywordsJSON,
		rec.RawEntitiesJSON, rec.CanonicalEntitiesJSON, rec.SegmentJSON, rec.EmbeddingJSON,
		rec.BestSource, rec.BestPath, rec.BestLink, rec.BestScore)
	if err != nil {
		return err
	}
	return nil
}
