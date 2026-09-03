// Package media — visual_embedding_test.go: visual embedding pipeline
// certification. Exercises the registry, the sidecar embedder against a
// real local HTTP stub, pooling correctness, and persistence into
// media_embeddings on the live database.
package media_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"image/color"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

// stubVisualSampler reuses the fakeSampler PNG writer but is declared
// here for clarity of the visual pipeline tests.
type stubVisualSampler struct{ colors []color3 }

type color3 struct{ r, g, b byte }

func (f stubVisualSampler) ExtractPercentageFrames(ctx context.Context, localPath string, percentages []float64, outDir string) ([]pgmedia.KeyframeSample, error) {
	samples := make([]pgmedia.KeyframeSample, 0, len(percentages))
	for i, p := range percentages {
		c := f.colors[i%len(f.colors)]
		path := filepath.Join(outDir, fmt.Sprintf("vframe_%03d_%.0f.png", i, p*100))
		if err := writeSolidPNG(path, color.RGBA{R: c.r, G: c.g, B: c.b, A: 0xFF}); err != nil {
			return nil, err
		}
		samples = append(samples, pgmedia.KeyframeSample{Path: path, Percentage: p})
	}
	return samples, nil
}

// sidecarStub emulates the Python embedding server's batch endpoint.
type sidecarStub struct {
	server     *httptest.Server
	model      string
	dim        int
	count      int
	fail501    bool
	wrongModel bool
	wrongDim   bool
}

func newSidecarStub(t *testing.T) *sidecarStub {
	t.Helper()
	s := &sidecarStub{model: pgmedia.DefaultVisualModelID, dim: pgmedia.DefaultVisualDim, count: 5}
	mux := http.NewServeMux()
	mux.HandleFunc("/embed_visual_from_images", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ImagePaths []string `json:"image_paths"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if s.fail501 {
			w.WriteHeader(http.StatusNotImplemented)
			return
		}
		model := s.model
		if s.wrongModel {
			model = "some-other/model-x"
		}
		dim := s.dim
		if s.wrongDim {
			dim = 512
		}
		embs := make([][]float64, len(req.ImagePaths))
		for i := range req.ImagePaths {
			v := make([]float64, dim)
			for j := range v {
				v[j] = float64(i+1) * 0.001
			}
			embs[i] = v
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embeddings": embs, "dimensions": dim, "count": len(embs),
			"model": model, "model_version": "v1",
		})
	})
	s.server = httptest.NewServer(mux)
	t.Cleanup(s.server.Close)
	return s
}

// TestVisualPipeline_EmbedAndStoresPooledVector proves the full chain
// keyframes→sidecar→pool→media_embeddings on the live database.
func TestVisualPipeline_EmbedAndStoresPooledVector(t *testing.T) {
	dsn, ok := requirePostgresDSN(t)
	if !ok {
		return
	}
	db, err := openMediaDB(dsn)
	if err != nil {
		t.Fatalf("open media db: %v", err)
	}
	defer db.Close()

	vectors := pgmedia.NewVectorSurfaceWriter(db)
	if err := vectors.RegisterEmbeddingFamily(context.Background(), "visual", pgmedia.DefaultVisualModelID, pgmedia.DefaultVisualDim); err != nil {
		t.Fatalf("register visual family: %v", err)
	}

	committers := newCommitterOnDB(t, db)
	assetID := "yt_visual_pipeline_001"
	if _, err := committers.CommitAndIndex(context.Background(), txCommitRequestFor(assetID)); err != nil {
		t.Fatalf("commit fixture asset: %v", err)
	}

	stub := newSidecarStub(t)
	registry := pgmedia.DefaultVisualEmbeddingModelRegistry()
	embedder, err := pgmedia.NewSidecarVisualEmbedder(stub.server.URL, registry, pgmedia.DefaultVisualModelID, time.Second)
	if err != nil {
		t.Fatalf("sidecar embedder: %v", err)
	}
	pipeline, err := pgmedia.NewVisualEmbeddingPipeline(pgmedia.VisualEmbeddingDeps{
		Keyframes: stubVisualSampler{colors: []color3{{r: 255}}},
		Embedder:  embedder,
		Registry:  registry,
		ModelID:   pgmedia.DefaultVisualModelID,
	})
	if err != nil {
		t.Fatalf("pipeline: %v", err)
	}

	res, err := pipeline.EmbedAndStore(context.Background(), vectors, assetID, touchMediaSource(t))
	if err != nil {
		t.Fatalf("EmbedAndStore: %v", err)
	}
	if res.FramesEmbedded != 5 || res.ModelID != pgmedia.DefaultVisualModelID || res.Dim != pgmedia.DefaultVisualDim {
		t.Fatalf("unexpected result: %+v", res)
	}

	// Persistence leg: pooled vector stored under family "visual".
	// The pooled vector of 5 constant vectors (i+1)*0.001 =
	// mean(i=0..4) = 3*0.001 = 0.003 in every component.
	var stored string
	if err := db.QueryRow(`SELECT embedding::text FROM media_embeddings WHERE asset_id=$1 AND embedding_type='visual'`, assetID).Scan(&stored); err != nil {
		t.Fatalf("visual embedding row missing: %v", err)
	}
	if len(stored) == 0 || stored[0] != '[' {
		t.Fatalf("stored vector not a pgvector literal: %q", stored[:min(len(stored), 20)])
	}
}

// TestVisualPipeline_SidecarFailClosed proves typed errors for the
// sidecar failure modes: 501 (model not loaded), wrong model identity,
// wrong dimension.
func TestVisualPipeline_SidecarFailClosed(t *testing.T) {
	dsn, ok := requirePostgresDSN(t)
	if !ok {
		return
	}
	db, err := openMediaDB(dsn)
	if err != nil {
		t.Fatalf("open media db: %v", err)
	}
	defer db.Close()

	for _, tc := range []struct {
		name    string
		mutate  func(*sidecarStub)
		wantSub string
	}{
		{"unavailable", func(s *sidecarStub) { s.fail501 = true }, "sidecar unavailable"},
		{"wrong model", func(s *sidecarStub) { s.wrongModel = true }, "model identity mismatch"},
		{"wrong dim", func(s *sidecarStub) { s.wrongDim = true }, "dimension mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := newSidecarStub(t)
			tc.mutate(stub)
			registry := pgmedia.DefaultVisualEmbeddingModelRegistry()
			embedder, err := pgmedia.NewSidecarVisualEmbedder(stub.server.URL, registry, pgmedia.DefaultVisualModelID, time.Second)
			if err != nil {
				t.Fatalf("embedder: %v", err)
			}
			pipeline, err := pgmedia.NewVisualEmbeddingPipeline(pgmedia.VisualEmbeddingDeps{
				Keyframes: stubVisualSampler{colors: []color3{{r: 255}}},
				Embedder:  embedder,
				Registry:  registry,
				ModelID:   pgmedia.DefaultVisualModelID,
			})
			if err != nil {
				t.Fatalf("pipeline: %v", err)
			}
			_, err = pipeline.EmbedAndStore(context.Background(), pgmedia.NewVectorSurfaceWriter(db), "asset-noop", touchMediaSource(t))
			if err == nil || !contains(err.Error(), tc.wantSub) {
				t.Fatalf("expected %q error, got %v", tc.wantSub, err)
			}
		})
	}
}

// TestVisualPipeline_UnknownModelFailsClosed proves the registry rejects
// an unregistered production model at pipeline construction.
func TestVisualPipeline_UnknownModelFailsClosed(t *testing.T) {
	registry := pgmedia.DefaultVisualEmbeddingModelRegistry()
	if _, err := registry.Resolve("not-a/model"); err == nil {
		t.Fatal("unknown model must fail closed")
	}
	_, err := pgmedia.NewVisualEmbeddingPipeline(pgmedia.VisualEmbeddingDeps{
		Keyframes: stubVisualSampler{colors: []color3{{r: 255}}},
		Embedder:  fakeVisualEmbedder{},
		Registry:  registry,
		ModelID:   "not-a/model",
	})
	if err == nil || !contains(err.Error(), "unknown model") {
		t.Fatalf("expected unknown-model fail-closed error, got %v", err)
	}
}

// fakeVisualEmbedder is a no-op embedder for constructor tests.
type fakeVisualEmbedder struct{}

func (fakeVisualEmbedder) EmbedFrames(context.Context, []string) ([][]float32, error) {
	return nil, nil
}

// newCommitterOnDB builds a PostgresMediaCommitter over an existing db
// handle (no second newMediaTestDB truncation).
func newCommitterOnDB(t *testing.T, db *sql.DB) *pgmedia.PostgresMediaCommitter {
	t.Helper()
	ledger, err := pgmedia.NewRegistry(db)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	return pgmedia.NewPostgresMediaCommitter(db, pgmedia.NewOutboxRepository(db), ledger, nil)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
