// Package media — media_searcher.go is the pgvector implementation of the
// canonical search.VectorStorePort (internal/capabilities/assets/search/
// ports.go). It is the read-side twin of VectorSurfaceWriter: candidates
// are retrieved by cosine ANN over media_embeddings, hard scalar filters
// (workspace, lifecycle, source, category, media_type, language) are
// enforced INSIDE the same SQL query, and returned metadata is hydrated
// from media_assets in the same PostgreSQL SSOT — no second database, no
// projection drift, no Qdrant on the media path.
//
// Scope contract (mirrors qdrant.CompileQdrantFilter invariants):
//   - WorkspaceID must-clause is ALWAYS present when IsSystem is false.
//     An empty WorkspaceID with IsSystem=false fails closed; the reserved
//     "default" sentinel is rejected exactly like the Qdrant compiler.
//   - LifecycleState allow-list is ALWAYS present (defaults to {"ACTIVE"}).
//   - Optional equality filters drop out when empty — no zero-value
//     predicates that would degrade the plan.
//
// MinScore semantics: pgvector cosine distance d ∈ [0,2] maps to
// similarity 1 - d (identical to the documented pgvector recipe and
// monotonic with Qdrant cosine scores). Rows below MinScore are dropped.
//
// Family pinning (POSTGRES-MEDIA-CUTOVER ANN contract): every search is
// restricted to ONE registered (embedding_type, model_id) family resolved
// through the fail-closed ActiveEmbeddingFamily registry. The family
// predicate (a) keeps incompatible vector spaces out of one ranking, and
// (b) lets the planner match the per-family HNSW partial index created by
// 003_media_hnsw_indexes.sql — the ORDER BY operand is cast to the
// family's exact vector(dim), which is the expression the partial index
// is built on. Zero registered families fail closed (typed error, never
// an unscoped scan mixing every model's vectors); more than one family
// per channel fails closed too (ActiveEmbeddingFamily "ambiguous") — an
// operator must pin the production model before search.
package media

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	appsearch "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
)

// MediaSearcher implements appsearch.VectorStorePort against the
// PostgreSQL media SSOT (pgvector).
type MediaSearcher struct {
	db *sql.DB
}

// Compile-time assertion: MediaSearcher satisfies the canonical port.
// Drift on the interface is a build failure, not a runtime panic.
var _ appsearch.VectorStorePort = (*MediaSearcher)(nil)

// NewMediaSearcher constructs the pgvector searcher. db is required.
func NewMediaSearcher(db *sql.DB) *MediaSearcher {
	if db == nil {
		panic("media.NewMediaSearcher: db is required")
	}
	return &MediaSearcher{db: db}
}

// SystemSearchRequest builds a cross-workspace admin/reconcile ANN
// request (IsSystem=true): the only sanctioned escape hatch from the
// workspace must-clause, mirroring qdrant.CompileQdrantFilter semantics.
func SystemSearchRequest(queryVector []float32) appsearch.VectorSearchRequest {
	return appsearch.VectorSearchRequest{
		QueryVector: queryVector,
		VectorName:  appsearch.ChannelText,
		IsSystem:    true,
	}
}

// embeddingFamily is one pinned (embedding_type, model_id, dim) triple.
type embeddingFamily struct {
	embeddingType string
	modelID       string
	dim           int
}

// resolveFamily resolves THE production family for a channel through the
// fail-closed ActiveEmbeddingFamily registry contract.
func (s *MediaSearcher) resolveFamily(ctx context.Context, channel string) (embeddingFamily, error) {
	modelID, dim, err := NewVectorSurfaceWriter(s.db).ActiveEmbeddingFamily(ctx, channel)
	if err != nil {
		return embeddingFamily{}, fmt.Errorf("pgvector search: %w", err)
	}
	return embeddingFamily{embeddingType: channel, modelID: modelID, dim: dim}, nil
}

// appendFamilyScope binds the family predicate placeholders and extends
// the WHERE fragment with them. The family placeholders occupy the NEXT
// two positions after the base scope args (len(args)+1 / len(args)+2);
// every later placeholder must be computed AFTER this call.
func appendFamilyScope(where string, args []any, f embeddingFamily) (string, []any) {
	args = append(args, f.embeddingType)
	args = append(args, f.modelID)
	where = fmt.Sprintf("%s\n\t\t  AND e.embedding_type = $%d\n\t\t  AND e.model_id = $%d",
		where, len(args)-1, len(args))
	return where, args
}

// Search runs a cosine ANN query over the pinned production family of the
// requested channel with hard scalar filters compiled in-query. The
// ORDER BY operand carries the typed vector(dim) cast — the exact
// expression of the per-family HNSW partial index (003) — so the planner
// can prove the partial predicate matches and plan an Index Scan instead
// of a Seq Scan.
func (s *MediaSearcher) Search(ctx context.Context, req appsearch.VectorSearchRequest) ([]appsearch.VectorSearchResult, error) {
	channel := strings.TrimSpace(req.VectorName)
	if channel == "" {
		channel = appsearch.ChannelText
	}
	family, err := s.resolveFamily(ctx, channel)
	if err != nil {
		return nil, err
	}
	where, args, err := compileMediaSearchWhere(req.WorkspaceID, req.IsSystem, req.Source, req.Category, req.MediaType, req.Language, req.LifecycleState)
	if err != nil {
		return nil, err
	}
	limit, err := normalizeSearchLimit(req.Limit)
	if err != nil {
		return nil, err
	}
	if len(req.QueryVector) == 0 {
		return nil, fmt.Errorf("pgvector search: query vector is required")
	}
	vec, err := pgVectorLiteral(req.QueryVector)
	if err != nil {
		return nil, fmt.Errorf("pgvector search: %w", err)
	}
	if len(req.QueryVector) != family.dim {
		return nil, fmt.Errorf("pgvector search: query vector dim %d does not match registered %s family dim %d (model %s)",
			len(req.QueryVector), channel, family.dim, family.modelID)
	}

	// Family scope FIRST: its two placeholders precede the vector/limit
	// bindings, and the WHERE fragment gains the family predicate.
	where, args = appendFamilyScope(where, args, family)
	// The vector literal is bound ONCE and referenced by both the
	// similarity projection and the ORDER BY (same placeholder index).
	vecPlaceholder := len(args) + 1
	limitPlaceholder := vecPlaceholder + 1
	query := fmt.Sprintf(`
		SELECT e.asset_id,
		       1 - (e.embedding::vector(%d) <=> $%d::vector) AS similarity
		FROM media_embeddings e
		JOIN media_assets a ON a.id = e.asset_id
		%s
		ORDER BY e.embedding::vector(%d) <=> $%d::vector
		LIMIT $%d
	`, family.dim, vecPlaceholder, where, family.dim, vecPlaceholder, limitPlaceholder)
	args = append(args, vec, limit)

	return s.queryResults(ctx, req.MinScore, query, args...)
}

// HybridSearch fuses the dense ANN ranking with lexical BM25 relevance
// over media_assets.search_text (to_tsquery) using weighted RRF — the
// pgvector-native analogue of Qdrant server-side BM25 fusion. The dense
// channel carries the semantic signal; the lexical channel carries exact
// term matching; the fusion is computed in-database over the same SSOT.
// The dense leg is pinned to the production family of the channel (same
// contract as Search, including the typed vector(dim) cast that exposes
// the HNSW partial index to the planner).
func (s *MediaSearcher) HybridSearch(ctx context.Context, req appsearch.HybridSearchRequest) ([]appsearch.VectorSearchResult, error) {
	channel := strings.TrimSpace(req.DenseVectorName)
	if channel == "" {
		channel = appsearch.ChannelText
	}
	family, err := s.resolveFamily(ctx, channel)
	if err != nil {
		return nil, err
	}
	where, args, err := compileMediaSearchWhere(req.WorkspaceID, req.IsSystem, req.Source, req.Category, req.MediaType, req.Language, req.LifecycleState)
	if err != nil {
		return nil, err
	}
	limit, err := normalizeSearchLimit(req.Limit)
	if err != nil {
		return nil, err
	}
	if len(req.DenseVector) == 0 {
		return nil, fmt.Errorf("pgvector hybrid search: dense vector is required")
	}
	vec, err := pgVectorLiteral(req.DenseVector)
	if err != nil {
		return nil, fmt.Errorf("pgvector hybrid search: %w", err)
	}
	if len(req.DenseVector) != family.dim {
		return nil, fmt.Errorf("pgvector hybrid search: dense vector dim %d does not match registered %s family dim %d (model %s)",
			len(req.DenseVector), channel, family.dim, family.modelID)
	}

	// The sparse-text path (SparseText) is the canonical live-retrieval
	// surface (PR2 parity). A pre-computed SparseVector has no pgvector
	// translation and is rejected fail-closed.
	if strings.TrimSpace(req.SparseText) == "" {
		return nil, fmt.Errorf("pgvector hybrid search: SparseText is required (pre-computed sparse vectors are not supported)")
	}

	// Placeholder map (computed in binding order): base scope args, then
	// family type/model, then dense vector, limit, sparse text.
	where, args = appendFamilyScope(where, args, family)
	vecPh := len(args) + 1
	limitPh := vecPh + 1
	sparsePh := vecPh + 2

	// Weighted reciprocal-rank fusion, computed inside the same query:
	// rank positions from the ANN leg and the ts_rank leg are fused per
	// asset, then ordered by fused score. k=1 mirrors Qdrant's RRF
	// constant so score scales stay comparable across engines. Only the
	// dense leg is family-scoped (lexical ranking reads media_assets).
	query := fmt.Sprintf(`
		WITH dense AS (
		    SELECT e.asset_id,
		           row_number() OVER (ORDER BY e.embedding::vector(%[6]d) <=> $%[1]d::vector) AS rank
		    FROM media_embeddings e
		    JOIN media_assets a ON a.id = e.asset_id
		    %[2]s
		    ORDER BY e.embedding::vector(%[6]d) <=> $%[1]d::vector
		    LIMIT $%[3]d
		),
		lexical AS (
		    SELECT a.id AS asset_id,
		           row_number() OVER (ORDER BY ts_rank(to_tsvector('english', a.search_text),
		               to_tsquery('english', websearch_to_tsquery('english', $%[5]d)::text)) DESC) AS rank
		    FROM media_assets a
		    %[2]s
		      AND to_tsvector('english', a.search_text) @@ websearch_to_tsquery('english', $%[5]d)
		    LIMIT $%[3]d
		),
		fused AS (
		    SELECT asset_id, 1.0 / (1 + rank) AS score FROM dense
		    UNION ALL
		    SELECT asset_id, 1.0 / (1 + rank) AS score FROM lexical
		)
		SELECT f.asset_id, SUM(f.score) AS similarity
		FROM fused f
		GROUP BY f.asset_id
		ORDER BY similarity DESC
		LIMIT $%[3]d
	`, vecPh, where, limitPh, sparsePh, sparsePh, family.dim)
	args = append(args, vec, limit, req.SparseText)

	return s.queryResults(ctx, req.MinScore, query, args...)
}

// queryResults executes a (asset_id, similarity) query and hydrates the
// full result surface from media_assets — the same PostgreSQL SSOT.
// Rows below MinScore are dropped; missing rows cannot occur (the join
// is in-query) but are skipped defensively.
func (s *MediaSearcher) queryResults(ctx context.Context, minScore float64, query string, args ...any) ([]appsearch.VectorSearchResult, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("pgvector search: query: %w", err)
	}
	defer rows.Close()

	type hit struct {
		assetID string
		score   float64
	}
	hits := make([]hit, 0, 32)
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.assetID, &h.score); err != nil {
			return nil, fmt.Errorf("pgvector search: scan: %w", err)
		}
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pgvector search: iterate: %w", err)
	}
	if len(hits) == 0 {
		return []appsearch.VectorSearchResult{}, nil
	}

	out := make([]appsearch.VectorSearchResult, 0, len(hits))
	for _, h := range hits {
		if minScore > 0 && h.score < minScore {
			continue
		}
		asset, err := s.fetchAsset(ctx, h.assetID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return nil, fmt.Errorf("pgvector search: hydrate asset %q: %w", h.assetID, err)
		}
		result := assetToVectorSearchResult(asset)
		result.Score = h.score
		out = append(out, result)
	}
	return out, nil
}

// fetchAsset hydrates one asset from media_assets inside the same SSOT.
func (s *MediaSearcher) fetchAsset(ctx context.Context, assetID string) (*assetRow, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, source, media_type, category, language,
		       tags, search_text, duration_ms, lifecycle_state,
		       youtube_video_id, youtube_url, start_time, end_time, style
		FROM media_assets
		WHERE id = $1
	`, assetID)
	var a assetRow
	if err := row.Scan(&a.ID, &a.Name, &a.Source, &a.MediaType, &a.Category, &a.Language,
		&a.Tags, &a.SearchText, &a.DurationMs, &a.LifecycleState,
		&a.YouTubeVideoID, &a.YouTubeURL, &a.StartTime, &a.EndTime, &a.Style); err != nil {
		return nil, err
	}
	return &a, nil
}

// assetRow is the hydration projection of media_assets.
type assetRow struct {
	ID             string
	Name           string
	Source         string
	MediaType      string
	Category       string
	Language       string
	Tags           string
	SearchText     string
	DurationMs     int64
	LifecycleState string
	YouTubeVideoID string
	YouTubeURL     string
	StartTime      string
	EndTime        string
	Style          string
}

// assetToVectorSearchResult maps a hydrated SSOT row onto the canonical
// result DTO. The point-ID surface (QdrantPointID) is intentionally left
// empty — there is no Qdrant point on this path; the asset ID is the
// identity (godlike/06: PostgreSQL is the only media authority).
func assetToVectorSearchResult(a *assetRow) appsearch.VectorSearchResult {
	if a == nil {
		return appsearch.VectorSearchResult{}
	}
	return appsearch.VectorSearchResult{
		AssetID:        a.ID,
		Score:          0,
		Source:         a.Source,
		Name:           a.Name,
		Category:       a.Category,
		MediaType:      a.MediaType,
		Style:          a.Style,
		Language:       a.Language,
		YouTubeVideoID: a.YouTubeVideoID,
		YouTubeURL:     a.YouTubeURL,
		StartTime:      a.StartTime,
		EndTime:        a.EndTime,
		Tags:           decodeTagsJSON(a.Tags),
		SearchText:     a.SearchText,
	}
}

// decodeTagsJSON parses the canonical tags TEXT JSON column ('[]' default).
// Malformed JSON degrades to an empty list, never a crash — search
// hydration must not fail on a single legacy row.
func decodeTagsJSON(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return nil
	}
	var tags []string
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		return nil
	}
	return tags
}

// compileMediaSearchWhere builds the WHERE fragment shared by Search and
// HybridSearch. Invariants mirror qdrant.CompileQdrantFilter:
//   - workspace must-clause ALWAYS present unless IsSystem
//   - lifecycle allow-list ALWAYS present (default {"ACTIVE"})
//   - empty optional filters drop out
func compileMediaSearchWhere(workspaceID string, isSystem bool, source, category, mediaType, language string, lifecycleStates []string) (string, []any, error) {
	if !isSystem {
		if strings.TrimSpace(workspaceID) == "" {
			return "", nil, fmt.Errorf("pgvector search: WorkspaceID is required (set IsSystem=true for admin/reconcile paths)")
		}
		if workspaceID == "default" {
			return "", nil, fmt.Errorf(`pgvector search: WorkspaceID is the reserved "default" sentinel; set a real workspace or IsSystem=true`)
		}
	}

	clauses := []string{"a.deleted_at = ''"}
	args := make([]any, 0, 8)

	if !isSystem {
		args = append(args, workspaceID)
		clauses = append(clauses, fmt.Sprintf("a.workspace_id = $%d", len(args)))
	}

	for _, eq := range []struct {
		col   string
		value string
	}{
		{"source", strings.TrimSpace(source)},
		{"category", strings.TrimSpace(category)},
		{"media_type", strings.TrimSpace(mediaType)},
		{"language", strings.TrimSpace(language)},
	} {
		if eq.value == "" {
			continue
		}
		args = append(args, eq.value)
		clauses = append(clauses, fmt.Sprintf("a.%s = $%d", eq.col, len(args)))
	}

	if len(lifecycleStates) == 0 {
		lifecycleStates = []string{"ACTIVE"}
	}
	placeholders := make([]string, 0, len(lifecycleStates))
	for _, s := range lifecycleStates {
		args = append(args, strings.TrimSpace(s))
		placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
	}
	clauses = append(clauses, fmt.Sprintf("a.lifecycle_state IN (%s)", strings.Join(placeholders, ", ")))

	return "WHERE " + strings.Join(clauses, "\n\t\t  AND "), args, nil
}

// normalizeSearchLimit clamps the requested limit into the canonical
// bounds shared with the semantic backend (0 → DefaultLimit).
func normalizeSearchLimit(limit int) (int, error) {
	if limit < 0 {
		return 0, fmt.Errorf("pgvector search: limit must not be negative")
	}
	if limit == 0 {
		limit = appsearch.DefaultLimit
	}
	if limit > appsearch.MaxLimit {
		limit = appsearch.MaxLimit
	}
	return limit, nil
}
