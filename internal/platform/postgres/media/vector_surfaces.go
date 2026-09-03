// Package media — vector_surfaces.go owns the DERIVED media surfaces
// (media_asset_features + media_embeddings) of the PostgreSQL + pgvector
// SSOT (migration 002_media_vector_surfaces.sql).
//
// godlike/06 SSOT: these surfaces are never written by producers. The
// enrichment pipeline writes hard visual features and embeddings through
// this single writer family; the family validation trigger on
// media_embeddings rejects any vector whose (embedding_type, model_id)
// pair is unregistered or whose dimension does not match the registered
// family — the database is the fail-closed gate (godlike/07).
//
// Transactional contract (POSTGRES-MEDIA-CUTOVER): the Tx variants accept
// a caller-owned *sql.Tx so an asset commit, its locations, its features,
// and its embeddings can land in ONE PostgreSQL transaction. Rollback
// leaves zero partial state across all four surfaces.
package media

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// VectorSurfaceWriter is the canonical writer for the derived media
// surfaces (media_asset_features, media_embeddings, media_embedding_families).
type VectorSurfaceWriter struct {
	db *sql.DB
}

// NewVectorSurfaceWriter constructs the writer. db is required — a nil
// handle is a programming error and fails closed at construction.
func NewVectorSurfaceWriter(db *sql.DB) *VectorSurfaceWriter {
	if db == nil {
		panic("media.NewVectorSurfaceWriter: db is required")
	}
	return &VectorSurfaceWriter{db: db}
}

// AssetFeatureRecord is one row of media_asset_features: the hard visual
// descriptors the MediaSampler filters on (duration/motion/faces live
// alongside the embedding, never inside it).
type AssetFeatureRecord struct {
	AssetID          string
	DominantColor    string
	MotionScore      *float64
	HasFaces         bool
	FaceCount        *int
	LargestFaceRatio *float64
	AnalyzedAt       string
	AnalyzerVersion  string
}

// RegisterEmbeddingFamily registers an allowed (embedding_type, model_id,
// dim) triple in media_embedding_families. This is the single gate that
// unlocks a model's vectors: the media_embeddings trigger rejects every
// insert whose family is unregistered or whose dim mismatches.
func (w *VectorSurfaceWriter) RegisterEmbeddingFamily(ctx context.Context, embeddingType, modelID string, dim int) error {
	if strings.TrimSpace(embeddingType) == "" {
		return fmt.Errorf("media embedding family: embedding_type is required")
	}
	if strings.TrimSpace(modelID) == "" {
		return fmt.Errorf("media embedding family: model_id is required")
	}
	if dim <= 0 {
		return fmt.Errorf("media embedding family: dim must be greater than zero")
	}
	_, err := w.db.ExecContext(ctx, `
		INSERT INTO media_embedding_families (embedding_type, model_id, dim, created_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (embedding_type, model_id) DO UPDATE
		    SET dim = EXCLUDED.dim
	`, embeddingType, modelID, dim, nowRFC3339())
	if err != nil {
		return fmt.Errorf("media embedding family: register %s/%s dim=%d: %w", embeddingType, modelID, dim, err)
	}
	return nil
}

// ErrFamilyDimDrift is returned when the requested (type, model) family
// already exists with a DIFFERENT dimension. Overwriting would silently
// corrupt the ANN space (existing vectors keep the old geometry), so the
// drift fails closed — an operator must migrate embeddings deliberately.
var ErrFamilyDimDrift = errors.New("embedding family dimension drift")

// EnsureEmbeddingFamily registers the family ONLY when absent. If a row
// already exists: same dim → no-op (idempotent boot); different dim →
// ErrFamilyDimDrift (fail-closed, never overwrite). This is the boot-time
// bootstrap path for the canonical index worker; explicit re-registration
// with a new dim stays possible only via RegisterEmbeddingFamily (an
// operator action, not a side effect of starting the service).
func (w *VectorSurfaceWriter) EnsureEmbeddingFamily(ctx context.Context, embeddingType, modelID string, dim int) error {
	if strings.TrimSpace(embeddingType) == "" {
		return fmt.Errorf("media embedding family: embedding_type is required")
	}
	if strings.TrimSpace(modelID) == "" {
		return fmt.Errorf("media embedding family: model_id is required")
	}
	if dim <= 0 {
		return fmt.Errorf("media embedding family: dim must be greater than zero")
	}
	var existingDim int
	err := w.db.QueryRowContext(ctx, `
		SELECT dim FROM media_embedding_families
		WHERE embedding_type = $1 AND model_id = $2
	`, embeddingType, modelID).Scan(&existingDim)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return w.RegisterEmbeddingFamily(ctx, embeddingType, modelID, dim)
	case err != nil:
		return fmt.Errorf("media embedding family: read %s/%s: %w", embeddingType, modelID, err)
	case existingDim != dim:
		return fmt.Errorf("media embedding family: %s/%s registered dim=%d, requested dim=%d: %w",
			embeddingType, modelID, existingDim, dim, ErrFamilyDimDrift)
	default:
		return nil
	}
}

// ActiveEmbeddingFamily resolves THE production family for an embedding
// channel. Zero registered families is "no vectors for this channel yet"
// (typed error, never a fake match); more than one family per channel is
// ambiguous — the caller must pin the production model before search.
func (w *VectorSurfaceWriter) ActiveEmbeddingFamily(ctx context.Context, embeddingType string) (modelID string, dim int, err error) {
	rows, err := w.db.QueryContext(ctx, `
		SELECT model_id, dim FROM media_embedding_families
		WHERE embedding_type = $1
		ORDER BY model_id
	`, embeddingType)
	if err != nil {
		return "", 0, fmt.Errorf("media embedding family: query %s: %w", embeddingType, err)
	}
	defer rows.Close()

	families := make([][2]any, 0, 1)
	for rows.Next() {
		if err := rows.Scan(&modelID, &dim); err != nil {
			return "", 0, fmt.Errorf("media embedding family: scan %s: %w", embeddingType, err)
		}
		families = append(families, [2]any{modelID, dim})
	}
	if err := rows.Err(); err != nil {
		return "", 0, fmt.Errorf("media embedding family: iterate %s: %w", embeddingType, err)
	}
	switch len(families) {
	case 0:
		return "", 0, fmt.Errorf("media embedding family: no registered family for embedding_type %q", embeddingType)
	case 1:
		return modelID, dim, nil
	default:
		names := make([]string, 0, len(families))
		for _, f := range families {
			names = append(names, fmt.Sprint(f[0]))
		}
		return "", 0, fmt.Errorf("media embedding family: ambiguous families for embedding_type %q: [%s]", embeddingType, strings.Join(names, ", "))
	}
}

// UpsertAssetFeatures writes one media_asset_features row (idempotent
// upsert on asset_id). The asset row must already exist (FK enforced).
func (w *VectorSurfaceWriter) UpsertAssetFeatures(ctx context.Context, rec AssetFeatureRecord) error {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("media features: begin tx: %w", err)
	}
	if err := w.upsertAssetFeaturesTx(ctx, tx, rec); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("media features: commit: %w", err)
	}
	return nil
}

// UpsertAssetFeaturesTx writes one media_asset_features row inside a
// caller-owned transaction (POSTGRES-MEDIA-CUTOVER single-transaction
// contract). The caller owns Commit/Rollback.
func (w *VectorSurfaceWriter) UpsertAssetFeaturesTx(ctx context.Context, tx *sql.Tx, rec AssetFeatureRecord) error {
	if tx == nil {
		return fmt.Errorf("media features: tx is required")
	}
	return w.upsertAssetFeaturesTx(ctx, tx, rec)
}

func (w *VectorSurfaceWriter) upsertAssetFeaturesTx(ctx context.Context, tx *sql.Tx, rec AssetFeatureRecord) error {
	if strings.TrimSpace(rec.AssetID) == "" {
		return fmt.Errorf("media features: asset_id is required")
	}
	hasFaces := 0
	if rec.HasFaces {
		hasFaces = 1
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO media_asset_features
		    (asset_id, dominant_color, motion_score, has_faces, face_count, largest_face_ratio, analyzed_at, analyzer_version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (asset_id) DO UPDATE SET
		    dominant_color     = EXCLUDED.dominant_color,
		    motion_score       = EXCLUDED.motion_score,
		    has_faces          = EXCLUDED.has_faces,
		    face_count         = EXCLUDED.face_count,
		    largest_face_ratio = EXCLUDED.largest_face_ratio,
		    analyzed_at        = EXCLUDED.analyzed_at,
		    analyzer_version   = EXCLUDED.analyzer_version
	`, rec.AssetID, rec.DominantColor, rec.MotionScore, hasFaces, rec.FaceCount, rec.LargestFaceRatio, rec.AnalyzedAt, rec.AnalyzerVersion)
	if err != nil {
		return fmt.Errorf("media features: upsert asset %q: %w", rec.AssetID, err)
	}
	return nil
}

// UpsertEmbedding writes one pgvector embedding (idempotent upsert on the
// (asset_id, embedding_type, model_id) primary key). The family validation
// trigger rejects unregistered families and dimension mismatches — this
// writer never bypasses the database contract.
func (w *VectorSurfaceWriter) UpsertEmbedding(ctx context.Context, assetID, embeddingType, modelID string, vec []float32) error {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("media embeddings: begin tx: %w", err)
	}
	if err := w.upsertEmbeddingTx(ctx, tx, assetID, embeddingType, modelID, vec); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("media embeddings: commit: %w", err)
	}
	return nil
}

// UpsertEmbeddingTx writes one pgvector embedding inside a caller-owned
// transaction (POSTGRES-MEDIA-CUTOVER single-transaction contract).
func (w *VectorSurfaceWriter) UpsertEmbeddingTx(ctx context.Context, tx *sql.Tx, assetID, embeddingType, modelID string, vec []float32) error {
	if tx == nil {
		return fmt.Errorf("media embeddings: tx is required")
	}
	return w.upsertEmbeddingTx(ctx, tx, assetID, embeddingType, modelID, vec)
}

func (w *VectorSurfaceWriter) upsertEmbeddingTx(ctx context.Context, tx *sql.Tx, assetID, embeddingType, modelID string, vec []float32) error {
	if strings.TrimSpace(assetID) == "" {
		return fmt.Errorf("media embeddings: asset_id is required")
	}
	if strings.TrimSpace(embeddingType) == "" {
		return fmt.Errorf("media embeddings: embedding_type is required")
	}
	if strings.TrimSpace(modelID) == "" {
		return fmt.Errorf("media embeddings: model_id is required")
	}
	if len(vec) == 0 {
		return fmt.Errorf("media embeddings: empty vector for asset %q", assetID)
	}
	lit, err := pgVectorLiteral(vec)
	if err != nil {
		return fmt.Errorf("media embeddings: asset %q: %w", assetID, err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO media_embeddings (asset_id, embedding_type, model_id, embedding, created_at)
		VALUES ($1, $2, $3, $4::vector, $5)
		ON CONFLICT (asset_id, embedding_type, model_id) DO UPDATE SET
		    embedding  = EXCLUDED.embedding,
		    created_at = EXCLUDED.created_at
	`, assetID, embeddingType, modelID, lit, nowRFC3339())
	if err != nil {
		return fmt.Errorf("media embeddings: upsert asset %q type %q model %q: %w", assetID, embeddingType, modelID, err)
	}
	return nil
}

// pgVectorLiteral formats a float32 slice as a pgvector text literal
// ("[1,2,3]"). Values must be finite — pgvector rejects NaN/Inf.
func pgVectorLiteral(vec []float32) (string, error) {
	var sb strings.Builder
	sb.WriteByte('[')
	for i, v := range vec {
		if i > 0 {
			sb.WriteByte(',')
		}
		if v != v || v > 3.4028235e38 || v < -3.4028235e38 {
			return "", fmt.Errorf("vector component %d is not finite", i)
		}
		fmt.Fprintf(&sb, "%v", v)
	}
	sb.WriteByte(']')
	return sb.String(), nil
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}
