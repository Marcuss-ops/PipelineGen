// Package media — feature_analyzer_test.go: MediaFeatureAnalyzer
// certification. Uses deterministic fakes for probe/sampler/faces plus the
// REAL ffmpeg binary (available in CI and dev) for the pixel-analysis
// legs, and the live PostgreSQL container for the persistence leg.
package media_test

import (
	"context"
	"database/sql"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	pgmigration "github.com/Marcuss-ops/PipelineGen/migrations/postgres"
)

// openMediaDB opens the live test database and applies the canonical
// migrations (used by tests that need a bespoke handle instead of the
// truncating newMediaTestDB fixture).
func openMediaDB(dsn string) (*sql.DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	for _, ddl := range []string{pgmigration.MediaSchemaDDL, pgmigration.MediaVectorSurfacesDDL, pgmigration.MediaHNSWIndexesDDL} {
		if _, err := db.Exec(ddl); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	return db, nil
}

// fakeProbe returns a fixed duration.
type fakeProbe struct{ dur time.Duration }

func (f fakeProbe) Probe(context.Context, string) (*pgmedia.ProbeSummary, error) {
	return &pgmedia.ProbeSummary{Duration: f.dur, HasVideo: true}, nil
}

// fakeSampler writes N deterministic PNG frames of the requested colors
// and returns their paths (order-preserved).
type fakeSampler struct{ colors []color.RGBA }

func (f fakeSampler) ExtractPercentageFrames(ctx context.Context, localPath string, percentages []float64, outDir string) ([]pgmedia.KeyframeSample, error) {
	samples := make([]pgmedia.KeyframeSample, 0, len(percentages))
	for i, p := range percentages {
		c := f.colors[i%len(f.colors)]
		path := filepath.Join(outDir, fmt.Sprintf("frame_%03d_%.0f.png", i, p*100))
		if err := writeSolidPNG(path, c); err != nil {
			return nil, err
		}
		samples = append(samples, pgmedia.KeyframeSample{Path: path, Percentage: p})
	}
	return samples, nil
}

func writeSolidPNG(path string, c color.RGBA) error {
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for x := 0; x < 32; x++ {
		for y := 0; y < 32; y++ {
			img.Set(x, y, c)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

// fakeFaces returns deterministic observations.
type fakeFaces struct{ perFrame []pgmedia.FaceObservation }

func (f fakeFaces) DetectFaces(ctx context.Context, framePaths []string) ([]pgmedia.FaceObservation, error) {
	out := make([]pgmedia.FaceObservation, len(framePaths))
	for i := range framePaths {
		if i < len(f.perFrame) {
			out[i] = f.perFrame[i]
		}
	}
	return out, nil
}

// TestFeatureAnalyzer_ComputesAndStoresFeatures proves the full chain
// probe→keyframes→color/motion/faces→media_asset_features on the live
// database with the real ffmpeg binary for pixel analysis.
func TestFeatureAnalyzer_ComputesAndStoresFeatures(t *testing.T) {
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
	box := pgmedia.NewOutboxRepository(db)
	ledger, err := pgmedia.NewRegistry(db)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	committers := pgmedia.NewPostgresMediaCommitter(db, box, ledger, nil)
	assetID := "yt_feature_analyzer_001"
	if _, err := committers.CommitAndIndex(context.Background(), txCommitRequestFor(assetID)); err != nil {
		t.Fatalf("commit fixture asset: %v", err)
	}

	analyzer := pgmedia.NewMediaFeatureAnalyzer(pgmedia.FeatureAnalyzerDeps{
		Probe: fakeProbe{dur: 10 * time.Second},
		Keyframes: fakeSampler{colors: []color.RGBA{
			{R: 0xFF, G: 0x00, B: 0x00, A: 0xFF}, // red
			{R: 0xFF, G: 0x00, B: 0x00, A: 0xFF},
			{R: 0xFF, G: 0x00, B: 0x00, A: 0xFF},
			{R: 0xFF, G: 0x00, B: 0x00, A: 0xFF},
			{R: 0xFF, G: 0x00, B: 0x00, A: 0xFF},
		}},
		Faces: fakeFaces{perFrame: []pgmedia.FaceObservation{
			{FaceCount: 1, LargestRatio: 0.42},
			{FaceCount: 0, LargestRatio: 0},
			{FaceCount: 1, LargestRatio: 0.1},
			{FaceCount: 0, LargestRatio: 0},
			{FaceCount: 0, LargestRatio: 0},
		}},
		FrameCount: 5,
	})

	res, err := analyzer.AnalyzeAndStore(context.Background(), vectors, assetID, touchMediaSource(t))
	if err != nil {
		t.Fatalf("AnalyzeAndStore: %v", err)
	}
	if res.AssetID != assetID || res.FramesAnalyzed != 5 {
		t.Fatalf("unexpected result: %+v", res)
	}
	// Solid red frames → dominant color must be red family, motion 0.
	if res.DominantColor == "" || res.DominantColor[0] != '#' {
		t.Fatalf("dominant color not hex: %q", res.DominantColor)
	}
	if res.MotionScore != 0 {
		t.Fatalf("solid-color motion score must be 0, got %v", res.MotionScore)
	}
	if !res.HasFaces || res.FaceCount != 2 || res.LargestFaceRatio != 0.42 {
		t.Fatalf("face aggregation wrong: %+v", res)
	}
	if res.AnalyzerVersion != pgmedia.AnalyzerVersion {
		t.Fatalf("analyzer version not stamped: %q", res.AnalyzerVersion)
	}

	// Persistence leg: the feature row must exist with the computed values.
	var dominantColor string
	var hasFaces int
	var faceCount int
	if err := db.QueryRow(`SELECT dominant_color, has_faces, face_count FROM media_asset_features WHERE asset_id = $1`, assetID).
		Scan(&dominantColor, &hasFaces, &faceCount); err != nil {
		t.Fatalf("feature row missing after AnalyzeAndStore: %v", err)
	}
	if hasFaces != 1 || faceCount != 2 {
		t.Fatalf("stored face values wrong: has_faces=%d face_count=%d", hasFaces, faceCount)
	}
}

// TestFeatureAnalyzer_FailsClosedWithoutFaceDetector proves the typed
// no-detector error instead of a silent has_faces=0 row.
func TestFeatureAnalyzer_FailsClosedWithoutFaceDetector(t *testing.T) {
	dsn, ok := requirePostgresDSN(t)
	if !ok {
		return
	}
	db, err := openMediaDB(dsn)
	if err != nil {
		t.Fatalf("open media db: %v", err)
	}
	defer db.Close()

	analyzer := pgmedia.NewMediaFeatureAnalyzer(pgmedia.FeatureAnalyzerDeps{
		Probe:     fakeProbe{dur: 10 * time.Second},
		Keyframes: fakeSampler{colors: []color.RGBA{{R: 255, A: 255}}},
		Faces:     nil, // deliberately unwired
	})
	_, err = analyzer.Analyze(context.Background(), "asset-x", touchMediaSource(t))
	if err == nil || !contains(err.Error(), "no FaceDetector wired") {
		t.Fatalf("expected fail-closed no-detector error, got %v", err)
	}
}

// TestFeatureAnalyzer_FailsClosedOnUnreadableMedia proves the typed
// unreadable-media error.
func TestFeatureAnalyzer_FailsClosedOnUnreadableMedia(t *testing.T) {
	analyzer := pgmedia.NewMediaFeatureAnalyzer(pgmedia.FeatureAnalyzerDeps{
		Probe:     fakeProbe{dur: 10 * time.Second},
		Keyframes: fakeSampler{colors: []color.RGBA{{R: 255, A: 255}}},
		Faces:     fakeFaces{},
	})
	_, err := analyzer.Analyze(context.Background(), "asset-x", "/definitely/not/here.mp4")
	if err == nil || !contains(err.Error(), "media file unreadable") {
		t.Fatalf("expected unreadable-media error, got %v", err)
	}
}

// TestFeatureAnalyzer_MotionScoreRisesWithColorChanges proves the motion
// leg actually measures inter-frame change (red → blue → red cadence).
func TestFeatureAnalyzer_MotionScoreRisesWithColorChanges(t *testing.T) {
	analyzer := pgmedia.NewMediaFeatureAnalyzer(pgmedia.FeatureAnalyzerDeps{
		Probe: fakeProbe{dur: 10 * time.Second},
		Keyframes: fakeSampler{colors: []color.RGBA{
			{R: 0xFF, A: 0xFF},
			{B: 0xFF, A: 0xFF},
			{R: 0xFF, A: 0xFF},
			{B: 0xFF, A: 0xFF},
			{R: 0xFF, A: 0xFF},
		}},
		Faces:      fakeFaces{},
		FrameCount: 5,
	})
	res, err := analyzer.Analyze(context.Background(), "asset-motion", touchMediaSource(t))
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if res.MotionScore <= 0 || res.MotionScore > 1 {
		t.Fatalf("alternating-color motion score must be in (0,1], got %v", res.MotionScore)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// touchMediaSource creates a real (empty) media file so the analyzer's
// os.Stat liveness gate passes while the injected fakes drive behavior.
func touchMediaSource(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source.mp4")
	if err := os.WriteFile(path, []byte{0x00, 0x00, 0x00, 0x18, 0x66, 0x74, 0x79, 0x70}, 0o644); err != nil {
		t.Fatalf("write media source: %v", err)
	}
	return path
}
