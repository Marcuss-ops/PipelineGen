package scripts

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// GetResearchCache returns a non-expired source_text for the given key.
// On hit it atomically increments hit_count and refreshes last_used.
// On miss or expiry it returns ("", nil).
func (r *ScriptRepository) GetResearchCache(ctx context.Context, key string) (string, error) {
	var sourceText string
	err := r.db.QueryRowContext(ctx, `
		SELECT source_text FROM research_cache
		WHERE key = ? AND (expires_at IS NULL OR expires_at > datetime('now'))
	`, key).Scan(&sourceText)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}

	// Atomic hit accounting: refresh last_used and increment hit_count.
	_, _ = r.db.ExecContext(ctx, `
		UPDATE research_cache
		SET last_used = datetime('now'), hit_count = hit_count + 1
		WHERE key = ?
	`, key)

	return sourceText, nil
}

// GetResearchCacheRecord reads provenance for a validated cache hit without
// incrementing hit_count; GetResearchCache owns hit accounting.
func (r *ScriptRepository) GetResearchCacheRecord(ctx context.Context, key string) (scriptpkg.ResearchCacheRecord, error) {
	var rec scriptpkg.ResearchCacheRecord
	err := r.db.QueryRowContext(ctx, `
		SELECT key, topic, language, max_steps, source_text, source_text_hash,
		       research_report_json, sources_count, claims_verified, claims_rejected,
		       search_query_count, pages_fetched, topic_fingerprint, source_fingerprint,
		       resolver_version, research_version, hit_count
		FROM research_cache
		WHERE key = ? AND (expires_at IS NULL OR expires_at > datetime('now'))
	`, key).Scan(&rec.Key, &rec.Topic, &rec.Language, &rec.MaxSteps, &rec.SourceText,
		&rec.SourceTextHash, &rec.ResearchReportJSON, &rec.SourcesCount, &rec.ClaimsVerified,
		&rec.ClaimsRejected, &rec.SearchQueryCount, &rec.PagesFetched, &rec.TopicFingerprint,
		&rec.SourceFingerprint, &rec.ResolverVersion, &rec.ResearchVersion, &rec.HitCount)
	if err == sql.ErrNoRows {
		return scriptpkg.ResearchCacheRecord{}, nil
	}
	return rec, err
}

// SaveResearchCache inserts or replaces a research_cache row from the
// canonical ResearchCacheRecord. The caller must compute rec.Key with
// scriptpkg.ComputeResearchCacheKey.
func (r *ScriptRepository) SaveResearchCache(ctx context.Context, rec scriptpkg.ResearchCacheRecord) error {
	if rec.Key == "" {
		return fmt.Errorf("SaveResearchCache: key is required")
	}

	now := time.Now().UTC()
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = now
	}
	if rec.UpdatedAt.IsZero() {
		rec.UpdatedAt = now
	}

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO research_cache (
			key, topic, language, max_steps, source_text,
			source_text_hash, research_report_json, sources_count,
			claims_verified, claims_rejected, search_query_count, pages_fetched,
			concept_id, topic_fingerprint, source_fingerprint,
			resolver_version, research_version,
			hit_count, expires_at, created_at, updated_at, last_used
		) VALUES (
			?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?, ?,
			?, ?, ?,
			?, ?,
			?, ?, ?, ?, datetime('now')
		)
		ON CONFLICT(key) DO UPDATE SET
			topic=excluded.topic, language=excluded.language, max_steps=excluded.max_steps,
			source_text=excluded.source_text, source_text_hash=excluded.source_text_hash,
			research_report_json=excluded.research_report_json, sources_count=excluded.sources_count,
			claims_verified=excluded.claims_verified, claims_rejected=excluded.claims_rejected,
			search_query_count=excluded.search_query_count, pages_fetched=excluded.pages_fetched,
			topic_fingerprint=excluded.topic_fingerprint, source_fingerprint=excluded.source_fingerprint,
			resolver_version=excluded.resolver_version, research_version=excluded.research_version,
			expires_at=excluded.expires_at, updated_at=excluded.updated_at
	`,
		rec.Key, rec.Topic, rec.Language, rec.MaxSteps, rec.SourceText,
		rec.SourceTextHash, rec.ResearchReportJSON, rec.SourcesCount,
		rec.ClaimsVerified, rec.ClaimsRejected, rec.SearchQueryCount, rec.PagesFetched,
		rec.ConceptID, rec.TopicFingerprint, rec.SourceFingerprint,
		rec.ResolverVersion, rec.ResearchVersion,
		rec.HitCount, toSQLiteDatetime(rec.ExpiresAt), toSQLiteDatetime(rec.CreatedAt), toSQLiteDatetime(rec.UpdatedAt),
	)
	return err
}

// TouchResearchCache refreshes last_used for a key and returns the number
// of rows affected.
func (r *ScriptRepository) TouchResearchCache(ctx context.Context, key string) (int64, error) {
	result, err := r.db.ExecContext(ctx, "UPDATE research_cache SET last_used = datetime('now') WHERE key = ?", key)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// SweepExpiredResearchCache deletes rows whose expires_at is in the past.
func (r *ScriptRepository) SweepExpiredResearchCache(ctx context.Context) (int64, error) {
	result, err := r.db.ExecContext(ctx, "DELETE FROM research_cache WHERE expires_at IS NOT NULL AND expires_at < datetime('now')")
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// SweepStaleResearchCache deletes rows whose last_used is older than
// maxAgeDays. This is the legacy sweeper; prefer SweepExpiredResearchCache.
func (r *ScriptRepository) SweepStaleResearchCache(ctx context.Context, maxAgeDays int) (int64, error) {
	if maxAgeDays <= 0 {
		maxAgeDays = 30
	}
	result, err := r.db.ExecContext(ctx,
		"DELETE FROM research_cache WHERE last_used < datetime('now', ?)",
		fmt.Sprintf("-%d days", maxAgeDays),
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// toSQLiteDatetime formats a time.Time for SQLite TEXT datetime columns.
// It returns NULL when t.IsZero() so callers can rely on the column default.
func toSQLiteDatetime(t time.Time) interface{} {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format("2006-01-02 15:04:05")
}
