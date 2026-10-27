package imageindex

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	coreasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/models"
	_ "github.com/mattn/go-sqlite3"
)

type fakeEmbedder struct{ calls int }

func (e *fakeEmbedder) Embed(context.Context, string) (coreasset.EmbeddingResult, error) {
	e.calls++
	return validEmbedding(), nil
}

func (e *fakeEmbedder) EmbedPassagesBatch(_ context.Context, texts []string) ([]coreasset.EmbeddingResult, error) {
	e.calls++
	if len(texts) == 0 {
		return nil, nil
	}
	return []coreasset.EmbeddingResult{validEmbedding()}, nil
}

func validEmbedding() coreasset.EmbeddingResult {
	vector := make([]float32, models.E5.Dimensions)
	vector[0] = 1
	return coreasset.EmbeddingResult{Vector: vector, Dimensions: len(vector), Model: models.E5.ID, ModelVersion: models.E5.Revision}
}

func newTestIndex(t *testing.T) (*Index, *fakeEmbedder) {
	t.Helper()
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "images.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	embedder := &fakeEmbedder{}
	index, err := New(db, embedder)
	if err != nil {
		t.Fatal(err)
	}
	if err := index.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	return index, embedder
}

func TestSearchIndexesVectorsAndServesWarmQueryFromSQLiteCache(t *testing.T) {
	index, embedder := newTestIndex(t)
	ctx := context.Background()
	if err := index.Index(ctx, "asset-a", "illustrative Italian courtroom"); err != nil {
		t.Fatalf("Index: %v", err)
	}
	ids, err := index.Search(ctx, "Italian courtroom hearing", 5)
	if err != nil {
		t.Fatalf("Search cold: %v", err)
	}
	if len(ids) != 1 || ids[0] != "asset-a" {
		t.Fatalf("cold IDs = %v", ids)
	}
	callsAfterCold := embedder.calls
	ids, err = index.Search(ctx, "  ITALIAN   courtroom hearing ", 5)
	if err != nil {
		t.Fatalf("Search warm: %v", err)
	}
	if len(ids) != 1 || ids[0] != "asset-a" {
		t.Fatalf("warm IDs = %v", ids)
	}
	if embedder.calls != callsAfterCold {
		t.Fatalf("warm search called embedder: calls %d -> %d", callsAfterCold, embedder.calls)
	}
}

func TestIndexInvalidatesWarmSearchCache(t *testing.T) {
	index, _ := newTestIndex(t)
	ctx := context.Background()
	if err := index.Index(ctx, "asset-a", "first image about a courtroom"); err != nil {
		t.Fatal(err)
	}
	if ids, err := index.Search(ctx, "courtroom", 5); err != nil || len(ids) != 1 {
		t.Fatalf("initial Search = %v, %v", ids, err)
	}
	if err := index.Index(ctx, "asset-b", "second image about a courtroom"); err != nil {
		t.Fatal(err)
	}
	ids, err := index.Search(ctx, "courtroom", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("Search after index update = %v, want both assets", ids)
	}
}
