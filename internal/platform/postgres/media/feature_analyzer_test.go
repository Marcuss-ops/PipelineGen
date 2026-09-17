// Package media — feature_analyzer_test.go: MediaFeatureAnalyzer
// certification. Uses deterministic fakes for probe/sampler plus the
// REAL ffmpeg binary (available in CI and dev) for the pixel-analysis
// legs, and the live PostgreSQL container for the persistence leg.
//
// Face descriptors are RETIRED from this surface: the analyzer is proven to
// run with no face port at all and the retired columns are proven absent
// from the live schema, so a regression cannot re-introduce a requirement
// that made the whole row unproducible.
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
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	pgmigration "github.com/Marcuss-ops/PipelineGen/migrations/postgres"
)

// openMediaDB opens the live test database and applies the canonical
// migrations (used by tests that need a bespoke handle instead of the
// truncating newMediaTestDB fixture).
//
// It is GUARDED, exactly like newMediaTestDB, because "bespoke handle" here
// still means WRITING fixture assets (CommitAndIndex on yt_feature_analyzer_001,
// yt_feature_no_face_dim_001, yt_visual_pipeline_001, …) through the real
// committer. Before this guard existed, three tests opened TEST_POSTGRES_DSN
// directly and committed those fixtures wherever the variable happened to
// point — which is how three `yt_*_001` fixture rows ended up in the
// OPERATIONAL pipelinegen_media catalog with no error anywhere. A fixture that
// writes must refuse to run against anything but a *_test database.
func openMediaDB(t *testing.T, dsn string) (*sql.DB, error) {
	t.Helper()
	requireDestructiveTestDatabase(t)
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	for _, ddl := range []string{pgmigration.MediaSchemaDDL, pgmigration.MediaVectorSurfacesDDL, pgmigration.MediaHNSWIndexesDDL, pgmigration.MediaDropAssetFacesDDL} {
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

// retiredFaceColumns lists the face descriptors this surface used to
// declare. The analyzer is required to be independent of them, the canonical
// DDL is required not to re-declare them, and the live schema is required to
// be free of them.
var retiredFaceColumns = []string{"has_faces", "face_count", "largest_face_ratio"}

// assertFaceColumnsRetired proves a database converged by the canonical
// migration chain carries no face descriptor. It runs AFTER the migrations,
// so on a bootstrapped database it is a smoke test: the real forward-
// prevention pin is TestFeatureAnalyzer_RetiredFaceColumnsAbsentFromDDL,
// which reads the DDL itself.
func assertFaceColumnsRetired(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, column := range retiredFaceColumns {
		var exists bool
		if err := db.QueryRow(`
			SELECT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_name = 'media_asset_features' AND column_name = $1
			)
		`, column).Scan(&exists); err != nil {
			t.Fatalf("probe column %s: %v", column, err)
		}
		if exists {
			t.Fatalf("retired face column %q still exists in media_asset_features", column)
		}
	}
}

// declaredFeatureColumns extracts the declared column names of
// media_asset_features from the canonical DDL text (comments stripped), so
// the contract can be pinned without a database.
func declaredFeatureColumns(t *testing.T, ddl string) []string {
	t.Helper()
	marker := "CREATE TABLE IF NOT EXISTS media_asset_features ("
	start := strings.Index(ddl, marker)
	if start < 0 {
		t.Fatalf("canonical DDL does not declare media_asset_features")
	}
	body := ddl[start+len(marker):]
	end := strings.Index(body, "\n);")
	if end < 0 {
		t.Fatalf("canonical DDL: unterminated media_asset_features declaration")
	}
	columns := make([]string, 0, 8)
	for _, line := range strings.Split(body[:end], "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "--") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		columns = append(columns, fields[0])
	}
	return columns
}

// TestFeatureAnalyzer_RetiredFaceColumnsAbsentFromDDL is the forward-
// prevention gate for the retirement: media_asset_features must not declare a
// face descriptor again, and the retirement migration must keep naming every
// column it retires. Re-adding a column to 002 fails here even though the
// live-database assertion would be silenced by the bootstrap running first.
func TestFeatureAnalyzer_RetiredFaceColumnsAbsentFromDDL(t *testing.T) {
	declared := declaredFeatureColumns(t, pgmigration.MediaVectorSurfacesDDL)
	for _, column := range retiredFaceColumns {
		for _, d := range declared {
			if d == column {
				t.Fatalf("media_asset_features must not declare %q: the endpoint that fed it is served by no service", column)
			}
		}
	}
	if len(declared) == 0 {
		t.Fatalf("parsed zero columns — the DDL pin would be vacuous")
	}
	want := []string{"asset_id", "dominant_color", "motion_score"}
	for _, w := range want {
		found := false
		for _, d := range declared {
			if d == w {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("media_asset_features lost the measurable column %q (declared: %v)", w, declared)
		}
	}
	// The retirement migration must keep DROPPING every retired column:
	// otherwise a database migrated through it silently keeps the contract.
	// The match is the full statement, not the bare identifier — a substring
	// check would pass on a renamed target such as has_faces_v2.
	for _, column := range retiredFaceColumns {
		wantDrop := "DROP COLUMN IF EXISTS " + column + ";"
		if !strings.Contains(pgmigration.MediaDropAssetFacesDDL, wantDrop) {
			t.Fatalf("retirement migration no longer drops %q (want %q)", column, wantDrop)
		}
	}
}

// TestFeatureAnalyzer_ComputesAndStoresFeatures proves the full chain
// probe→keyframes→color/motion→media_asset_features on the live database
// with the real ffmpeg binary for pixel analysis and NO face port wired.
func TestFeatureAnalyzer_ComputesAndStoresFeatures(t *testing.T) {
	dsn, ok := requirePostgresDSN(t)
	if !ok {
		return
	}
	db, err := openMediaDB(t, dsn)
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
	if res.AnalyzerVersion != pgmedia.AnalyzerVersion {
		t.Fatalf("analyzer version not stamped: %q", res.AnalyzerVersion)
	}

	// Persistence leg: the feature row must exist with the computed values.
	var dominantColor string
	var motion float64
	if err := db.QueryRow(`SELECT dominant_color, motion_score FROM media_asset_features WHERE asset_id = $1`, assetID).
		Scan(&dominantColor, &motion); err != nil {
		t.Fatalf("feature row missing after AnalyzeAndStore: %v", err)
	}
	if dominantColor != res.DominantColor || motion != res.MotionScore {
		t.Fatalf("stored values diverge from result: db=%q/%.4f result=%q/%.4f",
			dominantColor, motion, res.DominantColor, res.MotionScore)
	}

	// The retired face dimension must not exist in the live schema.
	assertFaceColumnsRetired(t, db)
}

// TestFeatureAnalyzer_RunsWithoutAnyFaceDimension proves the features leg is
// self-sufficient: with NO face port in existence, the analyzer produces and
// stores the two dimensions it actually measures. This is the regression pin
// for the defect that kept media_asset_features empty — the pipeline used to
// fail the whole row when a detector it could not have was missing.
func TestFeatureAnalyzer_RunsWithoutAnyFaceDimension(t *testing.T) {
	dsn, ok := requirePostgresDSN(t)
	if !ok {
		return
	}
	db, err := openMediaDB(t, dsn)
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
	assetID := "yt_feature_no_face_dim_001"
	if _, err := committers.CommitAndIndex(context.Background(), txCommitRequestFor(assetID)); err != nil {
		t.Fatalf("commit fixture asset: %v", err)
	}

	// Only the two ports the analyzer genuinely needs. There is no face port
	// to wire — the type no longer exists.
	analyzer := pgmedia.NewMediaFeatureAnalyzer(pgmedia.FeatureAnalyzerDeps{
		Probe:     fakeProbe{dur: 10 * time.Second},
		Keyframes: fakeSampler{colors: solidRedPalette()},
	})
	res, err := analyzer.AnalyzeAndStore(context.Background(), vectors, assetID, touchMediaSource(t))
	if err != nil {
		t.Fatalf("AnalyzeAndStore must succeed without any face dimension: %v", err)
	}
	if res.DominantColor == "" || res.FramesAnalyzed == 0 {
		t.Fatalf("feature result incomplete: %+v", res)
	}

	var count int
	if err := db.QueryRow(`SELECT count(*) FROM media_asset_features WHERE asset_id = $1`, assetID).Scan(&count); err != nil {
		t.Fatalf("count feature row: %v", err)
	}
	if count != 1 {
		t.Fatalf("feature row not stored: count=%d", count)
	}
	assertFaceColumnsRetired(t, db)
}

// TestFeatureAnalyzer_FailsClosedOnUnreadableMedia proves the typed
// unreadable-media error.
func TestFeatureAnalyzer_FailsClosedOnUnreadableMedia(t *testing.T) {
	analyzer := pgmedia.NewMediaFeatureAnalyzer(pgmedia.FeatureAnalyzerDeps{
		Probe:     fakeProbe{dur: 10 * time.Second},
		Keyframes: fakeSampler{colors: []color.RGBA{{R: 255, A: 255}}},
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
