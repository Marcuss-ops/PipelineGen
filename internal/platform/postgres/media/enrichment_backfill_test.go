// Package media — enrichment_backfill_test.go: FASE-4 enrichment engine
// certification. Proves on the live database: coverage math, idempotent
// skip-when-covered, and per-leg failure recording (no fabricated
// surfaces).
package media_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"image/color"

	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

// truncateMediaSurfaces resets the transactional core + derived surfaces
// so each test that opens a bespoke db handle starts order-independent.
func truncateMediaSurfaces(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, stmt := range []string{
		`TRUNCATE asset_text_track_segments, asset_text_tracks`,
		`TRUNCATE registry_events`,
		`TRUNCATE media_asset_sources`,
		`TRUNCATE outbox_events`,
		`TRUNCATE asset_renditions`,
		`TRUNCATE media_embedding_families`,
		`TRUNCATE asset_locations, media_asset_features, media_embeddings, media_assets CASCADE`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("truncate %s: %v", stmt, err)
		}
	}
}

// solidRedPalette is the deterministic solid-red frame palette.
func solidRedPalette() []color.RGBA {
	return []color.RGBA{{R: 0xFF, G: 0x00, B: 0x00, A: 0xFF}}
}

// stubVisualPipeline adapts the shared fake embedder into the visual
// pipeline port without a sidecar (deterministic pooled vectors).
type stubVisualPipeline struct {
	registry *pgmedia.VisualEmbeddingModelRegistry
}

func (s *stubVisualPipeline) EmbedAndStore(ctx context.Context, vectors *pgmedia.VectorSurfaceWriter, assetID, localPath string) (*pgmedia.VisualEmbeddingResult, error) {
	spec, err := s.registry.Resolve(pgmedia.DefaultVisualModelID)
	if err != nil {
		return nil, err
	}
	frames := make([][]float32, 5)
	for i := range frames {
		v := make([]float32, spec.Dim)
		for j := range v {
			v[j] = 0.01
		}
		frames[i] = v
	}
	pooled, err := s.registry.PoolMean(spec, frames)
	if err != nil {
		return nil, err
	}
	if err := vectors.UpsertEmbedding(ctx, assetID, "visual", pgmedia.DefaultVisualModelID, pooled); err != nil {
		return nil, err
	}
	return &pgmedia.VisualEmbeddingResult{AssetID: assetID, ModelID: spec.ModelID, Dim: spec.Dim, FramesEmbedded: 5}, nil
}

// stubSemanticEmbedder embeds deterministic 768d text vectors.
type stubSemanticEmbedder struct{ failFor map[string]bool }

func (s stubSemanticEmbedder) EmbedAssetText(_ context.Context, assetID string) ([]float32, error) {
	if s.failFor[assetID] {
		return nil, errors.New("sidecar down")
	}
	vec := make([]float32, 768)
	for i := range vec {
		vec[i] = 0.03
	}
	return vec, nil
}

// TestEnrichmentEngine_BackfillCoverageConverges proves: first run fills
// features+semantic+visual for the seeded asset (resolve succeeds), a
// second run skips (idempotent), and the coverage report reflects 100%.
func TestEnrichmentEngine_BackfillCoverageConverges(t *testing.T) {
	dsn, ok := requirePostgresDSN(t)
	if !ok {
		return
	}
	db, err := openMediaDB(dsn)
	if err != nil {
		t.Fatalf("open media db: %v", err)
	}
	defer db.Close()
	truncateMediaSurfaces(t, db)

	vectors := pgmedia.NewVectorSurfaceWriter(db)
	if err := vectors.RegisterEmbeddingFamily(context.Background(), "text", "intfloat/multilingual-e5-base", 768); err != nil {
		t.Fatalf("register text family: %v", err)
	}
	if err := vectors.RegisterEmbeddingFamily(context.Background(), "visual", pgmedia.DefaultVisualModelID, pgmedia.DefaultVisualDim); err != nil {
		t.Fatalf("register visual family: %v", err)
	}

	committers := newCommitterOnDB(t, db)
	assetID := "yt_enrich_backfill_001"
	if _, err := committers.CommitAndIndex(context.Background(), txCommitRequestFor(assetID)); err != nil {
		t.Fatalf("commit fixture asset: %v", err)
	}

	// In-memory fake media file so the resolver succeeds without ffmpeg
	// pixel legs... but the features leg REQUIRES the analyzer; give the
	// engine a real analyzer with a real temp source file and deterministic
	// face observations (real ffmpeg color/motion legs run on the sampled
	// PNGs).
	source := touchMediaSource(t)
	resolver := func(context.Context, string) (string, error) { return source, nil }

	registry := pgmedia.DefaultVisualEmbeddingModelRegistry()
	visualPipeline := &stubVisualPipeline{registry: registry}
	analyzer := pgmedia.NewMediaFeatureAnalyzer(pgmedia.FeatureAnalyzerDeps{
		Probe:     fakeProbe{dur: 10 * time.Second},
		Keyframes: fakeSampler{colors: solidRedPalette()},
		Faces:     fakeFaces{perFrame: []pgmedia.FaceObservation{{FaceCount: 1, LargestRatio: 0.5}}},
	})

	engine, err := pgmedia.NewEnrichmentEngine(db, vectors, analyzer, visualPipeline, stubSemanticEmbedder{}, "intfloat/multilingual-e5-base")
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	report, err := engine.Run(context.Background(), resolver, pgmedia.EnrichmentBackfillConfig{})
	if err != nil {
		t.Fatalf("run 1: %v (report: %+v)", err, report)
	}
	if report.AssetsProcessed != 1 || report.FeaturesWritten != 1 || report.SemanticWritten != 1 || report.VisualWritten != 1 {
		t.Fatalf("run 1 counts wrong: %+v", report)
	}
	if !report.AllComplete || report.FeatureCoverage != 1 || report.SemanticCoverage != 1 || report.VisualCoverage != 1 {
		t.Fatalf("run 1 coverage incomplete: %+v", report)
	}

	// Second run: everything covered → nothing re-processed.
	report2, err := engine.Run(context.Background(), resolver, pgmedia.EnrichmentBackfillConfig{})
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if report2.AssetsProcessed != 0 || report2.AssetsSkipped != 1 {
		t.Fatalf("run 2 must skip covered assets: %+v", report2)
	}
	if !report2.AllComplete {
		t.Fatalf("run 2 coverage regressed: %+v", report2)
	}
}

// TestEnrichmentEngine_RecordsFailuresWithoutFabricating proves a failing
// resolver/embedder records the asset as uncovered instead of writing a
// fake surface.
func TestEnrichmentEngine_RecordsFailuresWithoutFabricating(t *testing.T) {
	dsn, ok := requirePostgresDSN(t)
	if !ok {
		return
	}
	db, err := openMediaDB(dsn)
	if err != nil {
		t.Fatalf("open media db: %v", err)
	}
	defer db.Close()
	truncateMediaSurfaces(t, db)

	vectors := pgmedia.NewVectorSurfaceWriter(db)
	if err := vectors.RegisterEmbeddingFamily(context.Background(), "text", "intfloat/multilingual-e5-base", 768); err != nil {
		t.Fatalf("register text family: %v", err)
	}
	committers := newCommitterOnDB(t, db)
	assetID := "yt_enrich_fail_001"
	if _, err := committers.CommitAndIndex(context.Background(), txCommitRequestFor(assetID)); err != nil {
		t.Fatalf("commit fixture asset: %v", err)
	}

	// Semantic-only run against a failing embedder. The semantic leg
	// reads search_text from the SSOT, so the failing embedder — not the
	// resolver — must surface as an uncovered asset with no written row.
	engine, err := pgmedia.NewEnrichmentEngine(db, vectors, nil, nil,
		stubSemanticEmbedder{failFor: map[string]bool{assetID: true}},
		"intfloat/multilingual-e5-base")
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	report, err := engine.Run(context.Background(), func(context.Context, string) (string, error) {
		return "", errors.New("no file")
	}, pgmedia.EnrichmentBackfillConfig{BackfillSemantic: true, AnalyzeFeatures: true, BackfillVisual: true})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if report.SemanticWritten != 0 || report.FeaturesWritten != 0 || report.VisualWritten != 0 {
		t.Fatalf("failing legs must not write: %+v", report)
	}
	// The semantic leg is the only one that can succeed without a local
	// file; it must ALSO fail here (embedder down) so the semantic
	// coverage stays 0 while the failing asset is recorded as uncovered.
	if report.SemanticCoverage != 0 || report.SemanticComplete {
		t.Fatalf("semantic coverage must reflect the embedder failure: %+v", report)
	}
	if report.UncoveredCount == 0 || len(report.Uncovered) == 0 {
		t.Fatalf("failure must be recorded in the uncovered list: %+v", report)
	}
	found := false
	for _, u := range report.Uncovered {
		if contains(u, assetID) {
			found = true
		}
	}
	if !found {
		t.Fatalf("failing asset %q missing from uncovered list: %+v", assetID, report.Uncovered)
	}
	if len(report.Uncovered) == 0 {
		t.Fatalf("failure must be recorded in the uncovered list: %+v", report)
	}
}
