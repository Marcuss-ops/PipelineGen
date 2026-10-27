// Package imageindex stores E5 text vectors for resolved image metadata in
// SQLite. It keeps only the semantic projection and asset ID; the canonical
// image repository remains responsible for bytes, URLs and rights metadata.
package imageindex

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	coreasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/models"
)

const (
	DefaultLimit    = 10
	MaxLimit        = 50
	MinCosineScore  = 0.62
	queryCacheTTL   = 30 * 24 * time.Hour
	indexSchemaName = "resolved_image_vectors"
)

// Embedder is the canonical query and passage E5 interface implemented by
// the HTTP text sidecar.
type Embedder interface {
	coreasset.Embedder
	EmbedPassagesBatch(ctx context.Context, texts []string) ([]coreasset.EmbeddingResult, error)
}

type Index struct {
	db       *sql.DB
	embed    Embedder
	minScore float64
}

func New(db *sql.DB, embedder Embedder) (*Index, error) {
	if db == nil || embedder == nil {
		return nil, errors.New("resolved image index requires SQLite and the E5 sidecar")
	}
	return &Index{db: db, embed: embedder, minScore: MinCosineScore}, nil
}

func (i *Index) EnsureSchema(ctx context.Context) error {
	if i == nil || i.db == nil {
		return errors.New("resolved image index is not configured")
	}
	_, err := i.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS resolved_image_vectors (
			asset_id TEXT PRIMARY KEY,
			document TEXT NOT NULL,
			model TEXT NOT NULL,
			model_version TEXT NOT NULL,
			dimensions INTEGER NOT NULL,
			embedding BLOB NOT NULL,
			indexed_at INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_resolved_image_vectors_version
			ON resolved_image_vectors(model, model_version, indexed_at);
		CREATE TABLE IF NOT EXISTS resolved_image_query_cache (
			cache_key TEXT PRIMARY KEY,
			asset_ids_json TEXT NOT NULL,
			expires_at INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_resolved_image_query_cache_expiry
			ON resolved_image_query_cache(expires_at);
	`)
	if err != nil {
		return fmt.Errorf("create resolved-image semantic index schema: %w", err)
	}
	return nil
}

func (i *Index) Index(ctx context.Context, assetID, document string) error {
	assetID, document = strings.TrimSpace(assetID), strings.TrimSpace(document)
	if assetID == "" || document == "" {
		return nil
	}
	if i == nil || i.db == nil || i.embed == nil {
		return errors.New("resolved image index is not configured")
	}
	results, err := i.embed.EmbedPassagesBatch(ctx, []string{document})
	if err != nil {
		return fmt.Errorf("embed resolved-image metadata: %w", err)
	}
	if len(results) != 1 {
		return fmt.Errorf("embed resolved-image metadata returned %d vectors, want 1", len(results))
	}
	vector, err := canonicalVector(results[0])
	if err != nil {
		return fmt.Errorf("resolved-image metadata embedding: %w", err)
	}
	if _, err := i.db.ExecContext(ctx, `INSERT INTO resolved_image_vectors
		(asset_id, document, model, model_version, dimensions, embedding, indexed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(asset_id) DO UPDATE SET document=excluded.document,
		model=excluded.model, model_version=excluded.model_version,
		dimensions=excluded.dimensions, embedding=excluded.embedding, indexed_at=excluded.indexed_at`,
		assetID, document, models.E5.ID, models.E5.Revision, len(vector), encodeVector(vector), time.Now().Unix()); err != nil {
		return fmt.Errorf("persist resolved-image vector: %w", err)
	}
	if _, err := i.db.ExecContext(ctx, `DELETE FROM resolved_image_query_cache`); err != nil {
		return fmt.Errorf("invalidate resolved-image query cache: %w", err)
	}
	return nil
}

func (i *Index) Search(ctx context.Context, query string, limit int) ([]string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if i == nil || i.db == nil || i.embed == nil {
		return nil, errors.New("resolved image index is not configured")
	}
	if limit <= 0 || limit > MaxLimit {
		limit = DefaultLimit
	}
	key := normalizeQuery(query) + "\x00" + fmt.Sprint(limit)
	if ids, ok, err := i.cached(ctx, key); err != nil {
		return nil, err
	} else if ok {
		return ids, nil
	}
	queryEmbedding, err := i.embed.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed resolved-image query: %w", err)
	}
	queryVector, err := canonicalVector(queryEmbedding)
	if err != nil {
		return nil, fmt.Errorf("resolved-image query embedding: %w", err)
	}
	rows, err := i.db.QueryContext(ctx, `SELECT asset_id, embedding FROM resolved_image_vectors
		WHERE model=? AND model_version=? AND dimensions=? ORDER BY indexed_at DESC`,
		models.E5.ID, models.E5.Revision, models.E5.Dimensions)
	if err != nil {
		return nil, fmt.Errorf("read resolved-image vectors: %w", err)
	}
	type match struct {
		id    string
		score float64
	}
	matches := make([]match, 0)
	for rows.Next() {
		var id string
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan resolved-image vector: %w", err)
		}
		vector, decodeErr := decodeVector(blob, models.E5.Dimensions)
		if decodeErr != nil {
			continue
		}
		score := cosine(queryVector, vector)
		if score >= i.minScore {
			matches = append(matches, match{id: id, score: score})
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("iterate resolved-image vectors: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	sort.Slice(matches, func(a, b int) bool { return matches[a].score > matches[b].score })
	if len(matches) > limit {
		matches = matches[:limit]
	}
	ids := make([]string, len(matches))
	for n, m := range matches {
		ids[n] = m.id
	}
	if len(ids) > 0 {
		if err := i.cache(ctx, key, ids); err != nil {
			return ids, nil // caching is an optimization; retrieval already succeeded.
		}
	}
	return ids, nil
}

func canonicalVector(result coreasset.EmbeddingResult) ([]float32, error) {
	if result.Model != models.E5.ID || result.ModelVersion != models.E5.Revision {
		return nil, fmt.Errorf("E5 contract mismatch: model=%q revision=%q", result.Model, result.ModelVersion)
	}
	if len(result.Vector) != models.E5.Dimensions {
		return nil, fmt.Errorf("E5 dimensions mismatch: got %d, want %d", len(result.Vector), models.E5.Dimensions)
	}
	return result.Vector, nil
}

func encodeVector(vector []float32) []byte {
	out := make([]byte, len(vector)*4)
	for n, value := range vector {
		binary.LittleEndian.PutUint32(out[n*4:], math.Float32bits(value))
	}
	return out
}

func decodeVector(raw []byte, dimensions int) ([]float32, error) {
	if len(raw) != dimensions*4 {
		return nil, fmt.Errorf("vector has %d bytes, want %d", len(raw), dimensions*4)
	}
	out := make([]float32, dimensions)
	for n := range out {
		out[n] = math.Float32frombits(binary.LittleEndian.Uint32(raw[n*4:]))
	}
	return out, nil
}

func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return -1
	}
	var dot, aa, bb float64
	for n := range a {
		dot += float64(a[n]) * float64(b[n])
		aa += float64(a[n]) * float64(a[n])
		bb += float64(b[n]) * float64(b[n])
	}
	if aa == 0 || bb == 0 {
		return -1
	}
	return dot / (math.Sqrt(aa) * math.Sqrt(bb))
}

func normalizeQuery(query string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(query))), " ")
}

func (i *Index) cached(ctx context.Context, key string) ([]string, bool, error) {
	var raw string
	var expires int64
	err := i.db.QueryRowContext(ctx, `SELECT asset_ids_json, expires_at FROM resolved_image_query_cache WHERE cache_key=?`, key).Scan(&raw, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if expires <= time.Now().Unix() {
		_, _ = i.db.ExecContext(ctx, `DELETE FROM resolved_image_query_cache WHERE cache_key=?`, key)
		return nil, false, nil
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return nil, false, err
	}
	return ids, true, nil
}

func (i *Index) cache(ctx context.Context, key string, ids []string) error {
	raw, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	_, err = i.db.ExecContext(ctx, `INSERT INTO resolved_image_query_cache(cache_key, asset_ids_json, expires_at)
		VALUES(?, ?, ?) ON CONFLICT(cache_key) DO UPDATE SET asset_ids_json=excluded.asset_ids_json, expires_at=excluded.expires_at`,
		key, string(raw), time.Now().Add(queryCacheTTL).Unix())
	return err
}
