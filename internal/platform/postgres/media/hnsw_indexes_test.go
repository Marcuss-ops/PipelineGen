// Package media — hnsw_indexes_test.go: POSTGRES-MEDIA-CUTOVER ANN-index
// certification (SEMANTIC_HNSW_INDEX / VISUAL_HNSW_INDEX).
//
// Migration 003_media_hnsw_indexes.sql creates REAL per-family HNSW
// indexes over media_embeddings. These tests prove, against the live
// pgvector container:
//
//  1. both indexes physically exist (pg_indexes / pg_class inspection);
//  2. vector search under the family predicate PLANS an index scan —
//     never a Seq Scan (the explicit EXPLAIN (ANALYZE, BUFFERS)
//     acceptance criterion from the cutover checklist);
//  3. the family registry rows exist so the 002 trigger accepts vectors
//     for both production families;
//  4. a wrong-dimension vector for a registered family is still rejected
//     (HNSW availability never relaxes the fail-closed family gate).
package media_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

// Canonical production families — must match 003_media_hnsw_indexes.sql
// and the kernel/models registry identity (godlike/06 SSOT).
const (
	hnswSemanticModel = "intfloat/multilingual-e5-base"
	hnswVisualModel   = "google/siglip-so400m-patch14-384"
	hnswDim           = 768
)

// TestHNSW_IndexesExist proves both production HNSW indexes physically
// exist on the live media database with the expected access method.
func TestHNSW_IndexesExist(t *testing.T) {
	db := newMediaTestDB(t)
	ctx := context.Background()

	for _, tc := range []struct {
		indexName string
		label     string
	}{
		{"idx_media_embeddings_text_hnsw", "SEMANTIC_HNSW_INDEX"},
		{"idx_media_embeddings_visual_hnsw", "VISUAL_HNSW_INDEX"},
	} {
		var amname string
		err := db.QueryRowContext(ctx, `
			SELECT am.amname
			FROM pg_indexes i
			JOIN pg_class c ON c.relname = i.indexname
			JOIN pg_am am ON am.oid = c.relam
			WHERE i.indexname = $1
		`, tc.indexName).Scan(&amname)
		if err != nil {
			if err == sql.ErrNoRows {
				t.Fatalf("%s: index %q does not exist on the live media database", tc.label, tc.indexName)
			}
			t.Fatalf("%s: inspect index %q: %v", tc.label, tc.indexName, err)
		}
		if amname != "hnsw" {
			t.Fatalf("%s: index %q has access method %q, expected hnsw", tc.label, tc.indexName, amname)
		}
	}
}

// TestHNSW_VectorSearchPlansIndexScan is the EXPLAIN (ANALYZE, BUFFERS)
// acceptance gate: a cosine ANN search restricted to the semantic family
// must plan an index scan on the HNSW index — never a Seq Scan. Both the
// text (semantic) and visual channels are exercised.
func TestHNSW_VectorSearchPlansIndexScan(t *testing.T) {
	db := newMediaTestDB(t)
	ctx := context.Background()

	vectors := pgmedia.NewVectorSurfaceWriter(db)
	if err := vectors.RegisterEmbeddingFamily(ctx, "text", hnswSemanticModel, hnswDim); err != nil {
		t.Fatalf("register semantic family: %v", err)
	}
	if err := vectors.RegisterEmbeddingFamily(ctx, "visual", hnswVisualModel, hnswDim); err != nil {
		t.Fatalf("register visual family: %v", err)
	}

	// Seed one committed asset per family with a real 768d embedding so
	// the planner has rows to consider (an empty table can still plan a
	// seq scan; the fixture makes the plan realistic).
	committers, dbh := newPostgresCommitter(t)
	_ = dbh
	semAsset := "yt_hnsw_semantic_001"
	if _, err := committers.CommitAndIndex(ctx, txCommitRequestFor(semAsset)); err != nil {
		t.Fatalf("commit semantic fixture asset: %v", err)
	}
	visAsset := "yt_hnsw_visual_001"
	req := txCommitRequestFor(visAsset)
	if _, err := committers.CommitAndIndex(ctx, req); err != nil {
		t.Fatalf("commit visual fixture asset: %v", err)
	}

	queryVec := make([]float32, hnswDim)
	for i := range queryVec {
		queryVec[i] = 0.01 * float32(i%13)
	}
	if err := vectors.UpsertEmbedding(ctx, semAsset, "text", hnswSemanticModel, queryVec); err != nil {
		t.Fatalf("seed semantic embedding: %v", err)
	}
	if err := vectors.UpsertEmbedding(ctx, visAsset, "visual", hnswVisualModel, queryVec); err != nil {
		t.Fatalf("seed visual embedding: %v", err)
	}

	vecLiteral, err := pgvectorLiteralFor(queryVec)
	if err != nil {
		t.Fatalf("vector literal: %v", err)
	}

	for _, tc := range []struct {
		label     string
		embedding string
		modelID   string
	}{
		{"SEMANTIC_HNSW_INDEX", "text", hnswSemanticModel},
		{"VISUAL_HNSW_INDEX", "visual", hnswVisualModel},
	} {
		explain := fmt.Sprintf(`
			EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)
			SELECT e.asset_id, 1 - (e.embedding <=> $1::vector) AS similarity
			FROM media_embeddings e
			JOIN media_assets a ON a.id = e.asset_id
			WHERE e.embedding_type = '%s'
			  AND e.model_id = '%s'
			  AND a.deleted_at = ''
			  AND a.lifecycle_state IN ('ACTIVE')
			ORDER BY e.embedding <=> $1::vector
			LIMIT 20
		`, tc.embedding, tc.modelID)
		rows, err := db.QueryContext(ctx, explain, vecLiteral)
		if err != nil {
			t.Fatalf("%s: explain query: %v", tc.label, err)
		}
		var plan strings.Builder
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				rows.Close()
				t.Fatalf("%s: scan plan line: %v", tc.label, err)
			}
			plan.WriteString(line)
			plan.WriteString("\n")
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatalf("%s: iterate plan: %v", tc.label, err)
		}
		planText := plan.String()
		if strings.Contains(planText, "Seq Scan") {
			t.Fatalf("%s: planner chose a Seq Scan — HNSW index not used. Plan:\n%s", tc.label, planText)
		}
		if !strings.Contains(planText, "Index Scan") {
			t.Fatalf("%s: plan contains no Index Scan node — ANN index unused. Plan:\n%s", tc.label, planText)
		}
		for _, want := range []string{tc.embedding, tc.modelID} {
			if !strings.Contains(planText, want) {
				t.Fatalf("%s: plan does not reference family predicate %q — partial index not matched. Plan:\n%s", tc.label, want, planText)
			}
		}
	}
}

// TestHNSW_FamilyGateStillFailsClosed proves the HNSW availability never
// relaxed the 002 fail-closed family gate: an unregistered family insert
// and a dimension mismatch are both rejected by the database trigger.
func TestHNSW_FamilyGateStillFailsClosed(t *testing.T) {
	db := newMediaTestDB(t)
	ctx := context.Background()

	vec := make([]float32, hnswDim)
	for i := range vec {
		vec[i] = 0.1
	}

	// Unregistered family → rejected.
	unregistered, litErr := pgvectorLiteralFor(vec)
	if litErr != nil {
		t.Fatalf("vector literal: %v", litErr)
	}
	err := db.QueryRowContext(ctx, `
		WITH a AS (INSERT INTO media_assets (id, source, name, lifecycle_state, index_state, created_at, updated_at)
			VALUES ('yt_hnsw_gate_001', 'youtube', 'gate', 'ACTIVE', 'DISCOVERED', '', '')
			RETURNING id)
		INSERT INTO media_embeddings (asset_id, embedding_type, model_id, embedding, created_at)
		SELECT id, 'text', 'unregistered/model-x', $1::vector, '' FROM a
	`, unregistered).Scan(new(string))
	if err == nil || !strings.Contains(err.Error(), "unregistered embedding family") {
		t.Fatalf("unregistered family insert: expected fail-closed trigger error, got %v", err)
	}

	// Registered family, wrong dim → rejected.
	vectors := pgmedia.NewVectorSurfaceWriter(db)
	if err := vectors.RegisterEmbeddingFamily(ctx, "text", hnswSemanticModel, hnswDim); err != nil {
		t.Fatalf("register family: %v", err)
	}
	wrongDim, litErr := pgvectorLiteralFor(vec[:64])
	if litErr != nil {
		t.Fatalf("vector literal: %v", litErr)
	}
	err = db.QueryRowContext(ctx, `
		WITH a AS (INSERT INTO media_assets (id, source, name, lifecycle_state, index_state, created_at, updated_at)
			VALUES ('yt_hnsw_gate_002', 'youtube', 'gate', 'ACTIVE', 'DISCOVERED', '', '')
			RETURNING id)
		INSERT INTO media_embeddings (asset_id, embedding_type, model_id, embedding, created_at)
		SELECT id, 'text', $2, $1::vector, '' FROM a
	`, wrongDim, hnswSemanticModel).Scan(new(string))
	if err == nil || !strings.Contains(err.Error(), "does not match family dim") {
		t.Fatalf("dimension mismatch insert: expected fail-closed trigger error, got %v", err)
	}
}

// pgvectorLiteralFor mirrors the writer-side pgvector text literal format
// for raw-SQL test fixtures.
func pgvectorLiteralFor(vec []float32) (string, error) {
	var sb strings.Builder
	sb.WriteByte('[')
	for i, v := range vec {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, "%v", v)
	}
	sb.WriteByte(']')
	return sb.String(), nil
}
