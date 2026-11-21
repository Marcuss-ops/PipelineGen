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
	"time"

	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

// Canonical production families — must match 003_media_hnsw_indexes.sql
// and the kernel/models registry identity (godlike/06 SSOT).
const (
	hnswSemanticModel = "intfloat/multilingual-e5-base"
	hnswVisualModel   = "google/siglip-so400m-patch14-384"
	hnswSemanticDim   = 768
	hnswVisualDim     = 1152
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
	if err := vectors.RegisterEmbeddingFamily(ctx, "text", hnswSemanticModel, hnswSemanticDim); err != nil {
		t.Fatalf("register semantic family: %v", err)
	}
	if err := vectors.RegisterEmbeddingFamily(ctx, "visual", hnswVisualModel, hnswVisualDim); err != nil {
		t.Fatalf("register visual family: %v", err)
	}

	// Realistic fixture volume (measured with pgvector 0.8.x on PostgreSQL
	// 18): below a few thousand rows per family the planner HONESTLY
	// prefers a top-N sort over the ANN scan — at 5000 rows the HNSW index
	// drives the plan naturally. The historical fixture (64/24 rows) only
	// passed on stale statistics; seeding real volume keeps the assertion
	// at its strongest (NO planner knobs: the plan must choose the index).
	// Bulk INSERT is test-only support (the media-writer gate exempts
	// _test.go); the families above are registered first so the embedding
	// validation trigger accepts the rows. Each family gets a vector at
	// ITS OWN pinned dimension for the query literal.
	const fixtureRows = 5000
	semanticVec := make([]float32, hnswSemanticDim)
	visualVec := make([]float32, hnswVisualDim)
	for i := range semanticVec {
		semanticVec[i] = 0.01 * float32(i%13)
	}
	for i := range visualVec {
		visualVec[i] = 0.01 * float32(i%13)
	}
	for _, fam := range []struct {
		prefix string
		kind   string
		model  string
		dim    int
	}{
		{"hnsw_bulk_sem_", "text", hnswSemanticModel, hnswSemanticDim},
		{"hnsw_bulk_vis_", "visual", hnswVisualModel, hnswVisualDim},
	} {
		vec, err := pgvectorLiteralFor(make([]float32, fam.dim))
		if err != nil {
			t.Fatalf("vector literal: %v", err)
		}
		if _, err := db.ExecContext(ctx, fmt.Sprintf(`
			INSERT INTO media_assets (id, lifecycle_state, deleted_at)
			SELECT '%s' || g, 'ACTIVE', '' FROM generate_series(1, %d) g`, fam.prefix, fixtureRows)); err != nil {
			t.Fatalf("seed %s assets: %v", fam.kind, err)
		}
		if _, err := db.ExecContext(ctx, fmt.Sprintf(`
			INSERT INTO media_embeddings (asset_id, embedding_type, model_id, embedding)
			SELECT '%s' || g, '%s', '%s', '%s'::vector FROM generate_series(1, %d) g`,
			fam.prefix, fam.kind, fam.model, vec, fixtureRows)); err != nil {
			t.Fatalf("seed %s embeddings: %v", fam.kind, err)
		}
	}

	// Fresh planner statistics: without ANALYZE the planner keeps the
	// pre-insert row estimates and can mis-cost the ANN path against a
	// top-N sort. The plan under test must reflect the seeded volume.
	if _, err := db.ExecContext(ctx, "ANALYZE media_embeddings, media_assets"); err != nil {
		t.Fatalf("analyze fixtures: %v", err)
	}

	visualLiteral, err := pgvectorLiteralFor(visualVec)
	if err != nil {
		t.Fatalf("vector literal: %v", err)
	}
	semanticLiteral, err := pgvectorLiteralFor(semanticVec)
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
		// EXPLAIN with the cast at the family's own pinned dimension.
		explainDim := hnswVisualDim
		if tc.embedding == "text" {
			explainDim = hnswSemanticDim
		}
		explain := fmt.Sprintf(`
			EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)
			SELECT e.asset_id, 1 - (e.embedding::vector(%d) <=> $1::vector) AS similarity
			FROM media_embeddings e
			JOIN media_assets a ON a.id = e.asset_id
			WHERE e.embedding_type = '%s'
			  AND e.model_id = '%s'
			  AND a.deleted_at = ''
			  AND a.lifecycle_state IN ('ACTIVE')
			ORDER BY e.embedding::vector(%d) <=> $1::vector
			LIMIT 20
		`, explainDim, tc.embedding, tc.modelID, explainDim)
		// The query vector must be at the family's own pinned dimension too.
		queryLiteral := visualLiteral
		if tc.embedding == "text" {
			queryLiteral = semanticLiteral
		}
		rows, err := db.QueryContext(ctx, explain, queryLiteral)
		if err != nil {
			t.Fatalf("%s: explain query: %v", tc.label, err)
		}
		var plan strings.Builder
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				_ = rows.Close()
				t.Fatalf("%s: scan plan line: %v", tc.label, err)
			}
			plan.WriteString(line)
			plan.WriteString("\n")
		}
		_ = rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatalf("%s: iterate plan: %v", tc.label, err)
		}
		planText := plan.String()
		if strings.Contains(planText, "Seq Scan") {
			t.Fatalf("%s: planner chose a Seq Scan — HNSW index not used. Plan:\n%s", tc.label, planText)
		}
		// Accept any index-scan form: HNSW surfaces as "Index Scan using",
		// but the planner may legitimately emit a Bitmap Index Scan on
		// partial-index predicates at low row counts. What is forbidden is
		// a plain Seq Scan / Bitmap Heap Scan without any index node.
		if !strings.Contains(planText, "Index Scan") && !strings.Contains(planText, "Index Only Scan") {
			t.Fatalf("%s: plan contains no Index Scan node — ANN index unused. Plan:\n%s", tc.label, planText)
		}
		// Referencing the per-family partial index IS the family-predicate
		// proof: the index exists only for (embedding_type, model_id) rows
		// of this family, so a plan that scans it has matched the predicate
		// (the planner elides implied filters from the plan text).
		if !strings.Contains(planText, "idx_media_embeddings_"+tc.embedding+"_hnsw") {
			t.Fatalf("%s: plan does not reference the family HNSW index — partial index not matched. Plan:\n%s", tc.label, planText)
		}

		// STK-09: the pgvector share of the semantic search budget must stay
		// under 500 ms. This is the real ANN query (not EXPLAIN) on the same
		// 5k-row family fixture, so the assertion measures execution rather
		// than planning. Embedding latency is logged separately by the
		// semantic backend as `embed_ms`.
		latencyQuery := fmt.Sprintf(`
			SELECT e.asset_id
			FROM media_embeddings e
			JOIN media_assets a ON a.id = e.asset_id
			WHERE e.embedding_type = '%s'
			  AND e.model_id = '%s'
			  AND a.deleted_at = ''
			  AND a.lifecycle_state IN ('ACTIVE')
			ORDER BY e.embedding::vector(%d) <=> $1::vector
			LIMIT 8
		`, tc.embedding, tc.modelID, explainDim)
		started := time.Now()
		latencyRows, err := db.QueryContext(ctx, latencyQuery, queryLiteral)
		if err != nil {
			t.Fatalf("%s: latency query: %v", tc.label, err)
		}
		for latencyRows.Next() {
		}
		if err := latencyRows.Err(); err != nil {
			_ = latencyRows.Close()
			t.Fatalf("%s: latency query rows: %v", tc.label, err)
		}
		_ = latencyRows.Close()
		if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
			t.Fatalf("%s: ANN query latency %s exceeds 500ms", tc.label, elapsed)
		}
	}
}

// TestHNSW_MediaSearcherPinsProductionFamily exercises the production
// searcher end-to-end against the registered production families: the
// family-pinned search resolves the active family, applies the typed
// vector(dim) cast, and returns hydrated SSOT results.
func TestHNSW_MediaSearcherPinsProductionFamily(t *testing.T) {
	db := newMediaTestDB(t)
	ctx := context.Background()

	vectors := pgmedia.NewVectorSurfaceWriter(db)
	if err := vectors.RegisterEmbeddingFamily(ctx, "text", hnswSemanticModel, hnswSemanticDim); err != nil {
		t.Fatalf("register semantic family: %v", err)
	}

	searcher := pgmedia.NewMediaSearcher(db)

	// No family registered for the visual channel → fail closed.
	visualReq := pgmedia.SystemSearchRequest(make([]float32, hnswVisualDim))
	visualReq.VectorName = "visual"
	if _, err := searcher.Search(ctx, visualReq); err == nil {
		t.Fatal("expected fail-closed error when the channel has no registered family")
	}

	// Dimension mismatch on the registered text family → fail closed.
	if _, err := searcher.Search(ctx, pgmedia.SystemSearchRequest(make([]float32, 4))); err == nil {
		t.Fatal("expected fail-closed error for query vector dim != family dim")
	}

	// Happy path: seed one asset + 768d embedding, search by the same
	// vector, expect the asset back with hydrated SSOT metadata.
	box := pgmedia.NewOutboxRepository(db)
	ledger, ledgerErr := pgmedia.NewRegistry(db)
	if ledgerErr != nil {
		t.Fatalf("registry: %v", ledgerErr)
	}
	committers := pgmedia.NewPostgresMediaCommitter(db, box, ledger, nil)
	if _, err := committers.CommitAndIndex(ctx, txCommitRequestFor("yt_hnsw_search_001")); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := vectors.UpsertEmbedding(ctx, "yt_hnsw_search_001", "text", hnswSemanticModel, queryVector768()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	results, err := searcher.Search(ctx, pgmedia.SystemSearchRequest(queryVector768()))
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 || results[0].AssetID != "yt_hnsw_search_001" {
		t.Fatalf("results = %v, want [yt_hnsw_search_001]", results)
	}
}

// TestHNSW_FilteredANNRecall guards the production failure mode where the
// HNSW index returns its default candidate window BEFORE the asset filters
// (media_type/source/...) are applied. With a selective filter, every one of
// those candidates can be filtered out and the caller sees a false empty
// page even though a matching vector exists. The searcher must widen the
// candidate window (and fall back to an exact scan when needed).
func TestHNSW_FilteredANNRecall(t *testing.T) {
	db := newMediaTestDB(t)
	ctx := context.Background()

	vectors := pgmedia.NewVectorSurfaceWriter(db)
	if err := vectors.RegisterEmbeddingFamily(ctx, "text", hnswSemanticModel, hnswSemanticDim); err != nil {
		t.Fatalf("register semantic family: %v", err)
	}

	// The query is the first axis. The 200 near images are almost identical
	// to it; the video clip is a valid semantic match but ranks behind all of
	// them. With the pgvector default ef_search=40 the HNSW scan returns only
	// images, the media_type='video' filter removes them all, and the clip is
	// never seen. A wider candidate window must surface it.
	query := make([]float32, hnswSemanticDim)
	query[0] = 1
	nearImage := make([]float32, hnswSemanticDim)
	nearImage[0] = 1
	nearImage[1] = 0.01
	clipVector := make([]float32, hnswSemanticDim)
	clipVector[0] = 0.7
	clipVector[1] = 0.7
	farImage := make([]float32, hnswSemanticDim)
	farImage[0] = -1

	for _, seed := range []struct {
		prefix    string
		count     int
		mediaType string
		vec       []float32
	}{
		{"img_near_", 200, "image", nearImage},
		{"img_far_", 4800, "image", farImage},
	} {
		lit, err := pgvectorLiteralFor(seed.vec)
		if err != nil {
			t.Fatalf("vector literal: %v", err)
		}
		if _, err := db.ExecContext(ctx, fmt.Sprintf(`
			INSERT INTO media_assets (id, source, name, media_type, lifecycle_state, index_state, deleted_at, created_at, updated_at)
			SELECT '%s' || g, 'internet_images', 'fixture', '%s', 'ACTIVE', 'INDEXED', '', '', ''
			FROM generate_series(1, %d) g`, seed.prefix, seed.mediaType, seed.count)); err != nil {
			t.Fatalf("seed %s assets: %v", seed.prefix, err)
		}
		if _, err := db.ExecContext(ctx, fmt.Sprintf(`
			INSERT INTO media_embeddings (asset_id, embedding_type, model_id, embedding)
			SELECT '%s' || g, 'text', '%s', '%s'::vector FROM generate_series(1, %d) g`,
			seed.prefix, hnswSemanticModel, lit, seed.count)); err != nil {
			t.Fatalf("seed %s embeddings: %v", seed.prefix, err)
		}
	}

	// Commit the video clip through the canonical writer so every hydration
	// column is populated by the same path production uses.
	box := pgmedia.NewOutboxRepository(db)
	ledger, ledgerErr := pgmedia.NewRegistry(db)
	if ledgerErr != nil {
		t.Fatalf("registry: %v", ledgerErr)
	}
	committers := pgmedia.NewPostgresMediaCommitter(db, box, ledger, nil)
	if _, err := committers.CommitAndIndex(ctx, txCommitRequestFor("yt_filtered_ann_clip")); err != nil {
		t.Fatalf("commit clip: %v", err)
	}
	if err := vectors.UpsertEmbedding(ctx, "yt_filtered_ann_clip", "text", hnswSemanticModel, clipVector); err != nil {
		t.Fatalf("seed clip embedding: %v", err)
	}
	if _, err := db.ExecContext(ctx, "ANALYZE media_embeddings, media_assets"); err != nil {
		t.Fatalf("analyze fixtures: %v", err)
	}
	queryLit, err := pgvectorLiteralFor(query)
	if err != nil {
		t.Fatalf("query literal: %v", err)
	}
	t.Logf("query vector: %s", queryLit[:32]+"...")

	searcher := pgmedia.NewMediaSearcher(db)
	req := pgmedia.SystemSearchRequest(query)
	req.MediaType = "video"
	req.Limit = 20
	results, err := searcher.Search(ctx, req)
	if err != nil {
		t.Fatalf("filtered search: %v", err)
	}
	if len(results) != 1 || results[0].AssetID != "yt_filtered_ann_clip" {
		t.Fatalf("filtered ANN results = %v, want the single video clip (false empty page regression)", results)
	}
}

// queryVector768 builds the deterministic 768d fixture vector shared by
// the HNSW tests.
func queryVector768() []float32 {
	vec := make([]float32, hnswSemanticDim)
	for i := range vec {
		vec[i] = 0.01 * float32(i%13)
	}
	return vec
}

// TestHNSW_FamilyGateStillFailsClosed proves the HNSW availability never
// relaxed the 002 fail-closed family gate: an unregistered family insert
// and a dimension mismatch are both rejected by the database trigger.
func TestHNSW_FamilyGateStillFailsClosed(t *testing.T) {
	db := newMediaTestDB(t)
	ctx := context.Background()

	vec := make([]float32, hnswSemanticDim)
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
	if err := vectors.RegisterEmbeddingFamily(ctx, "text", hnswSemanticModel, hnswSemanticDim); err != nil {
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
