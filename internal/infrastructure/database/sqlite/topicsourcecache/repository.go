package topicsourcecache

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// Resolver-version discriminators for research_cache rows. They MUST match
// the literals written by the source resolvers:
//   - "webresearch"        → per-candidate evidence rows (single-candidate resolve)
//   - "webresearch-fanout" → the fanout aggregate row (assembled evidence pack
//     PLUS the ranking, persisted together in research_report_json.ranking).
//
// There is no separate ranking table: the ranking lives inside the aggregate
// row, so invalidating the aggregate also invalidates its ranking.
const (
	researchResolverVersionCandidate = "webresearch"
	researchResolverVersionFanout    = "webresearch-fanout"
)

type Repository struct{ db *sql.DB }

func NewRepository(db *sql.DB) *Repository { return &Repository{db: db} }

// GetResearchCache returns a non-expired source_text for the given key.
// On hit it atomically increments hit_count and refreshes last_used.
func (r *Repository) GetResearchCache(ctx context.Context, key string) (string, error) {
	if r == nil || r.db == nil {
		return "", nil
	}
	var v string
	err := r.db.QueryRowContext(ctx, `
		SELECT source_text FROM research_cache
		WHERE key = ? AND (expires_at IS NULL OR expires_at > datetime('now'))
	`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	_, _ = r.db.ExecContext(ctx, `
		UPDATE research_cache
		SET last_used = datetime('now'), hit_count = hit_count + 1
		WHERE key = ?
	`, key)
	return v, nil
}

// GetResearchCacheRecord reads provenance for a validated cache hit without
// incrementing hit_count; GetResearchCache owns hit accounting.
func (r *Repository) GetResearchCacheRecord(ctx context.Context, key string) (scriptpkg.ResearchCacheRecord, error) {
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
// canonical ResearchCacheRecord.
func (r *Repository) SaveResearchCache(ctx context.Context, rec scriptpkg.ResearchCacheRecord) error {
	if r == nil || r.db == nil {
		return nil
	}
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

// DeleteResearchCache deletes research_cache rows matching the given scope.
//
// scope "aggregate" targets fanout aggregate rows (resolver_version =
// "webresearch-fanout"), which embed BOTH the assembled evidence pack and the
// ranking — there is no separate ranking table, so this also invalidates the
// ranking. scope "candidate" targets per-candidate evidence rows
// (resolver_version = "webresearch").
//
// topic narrows the match against the stored topic column: the parent topic
// for aggregate rows, the candidate canonical name for candidate rows.
// rankingMetric optionally narrows aggregate rows by the requested_metric
// recorded in research_report_json.ranking; it is ignored for candidate scope.
func (r *Repository) DeleteResearchCache(ctx context.Context, scope, topic, rankingMetric string) (int64, error) {
	if r == nil || r.db == nil {
		return 0, nil
	}
	topic = strings.TrimSpace(topic)
	scope = strings.TrimSpace(scope)
	if topic == "" {
		return 0, fmt.Errorf("DeleteResearchCache: topic is required")
	}

	switch scope {
	case "aggregate":
		if metric := strings.TrimSpace(rankingMetric); metric != "" {
			metric = scriptpkg.NormalizeRankingMetric(metric).String()
			return r.execDelete(ctx,
				`DELETE FROM research_cache WHERE resolver_version = ? AND topic = ? AND json_extract(research_report_json, '$.ranking.requested_metric') = ?`,
				researchResolverVersionFanout, topic, metric)
		}
		return r.execDelete(ctx,
			`DELETE FROM research_cache WHERE resolver_version = ? AND topic = ?`,
			researchResolverVersionFanout, topic)
	case "candidate":
		return r.execDelete(ctx,
			`DELETE FROM research_cache WHERE resolver_version = ? AND topic = ?`,
			researchResolverVersionCandidate, topic)
	default:
		return 0, fmt.Errorf("DeleteResearchCache: unsupported scope %q (want aggregate or candidate)", scope)
	}
}

func (r *Repository) execDelete(ctx context.Context, query string, args ...any) (int64, error) {
	res, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func toSQLiteDatetime(t time.Time) interface{} {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format("2006-01-02 15:04:05")
}
