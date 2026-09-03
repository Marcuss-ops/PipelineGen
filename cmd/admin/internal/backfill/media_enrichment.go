package backfill

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	pgmigration "github.com/Marcuss-ops/PipelineGen/migrations/postgres"
)

// RunMediaEnrichmentBackfill implements the `backfill-media-enrichment`
// admin command: the FASE-4 enrichment backfill over the PostgreSQL media
// SSOT (POSTGRES-MEDIA-CUTOVER TODO 4).
//
// For every ACTIVE video asset the command runs the missing enrichment
// legs — hard visual features (MediaFeatureAnalyzer), semantic embedding
// (text channel), visual embedding (SigLIP pipeline) — and prints the
// live coverage report. The command exits non-zero when the coverage is
// incomplete (fail-closed certification evidence).
//
// Usage:
//
//	admin backfill-media-enrichment \
//	  --postgres-dsn postgres://.../pipelinegen_media?sslmode=disable \
//	  [--limit 0] [--batch-size 100] [--force]
//	  [--legs features,semantic,visual]
//
// Face detection (required by the features leg) is provided by the
// canonical sidecar (--sidecar-url); the visual leg uses the same
// sidecar's SigLIP image encoder.
func RunMediaEnrichmentBackfill(args []string) error {
	fs := flag.NewFlagSet("backfill-media-enrichment", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	pgDSN := fs.String("postgres-dsn", "", "PostgreSQL media SSOT DSN (required)")
	sidecarURL := fs.String("sidecar-url", "", "Python embedding sidecar base URL (required for features+visual legs)")
	batchSize := fs.Int("batch-size", 100, "Keyset-pagination page size")
	limit := fs.Int("limit", 0, "Maximum assets to process; zero means all")
	force := fs.Bool("force", false, "Re-process assets even when already covered")
	legs := fs.String("legs", "", "Comma-separated subset: features,semantic,visual (default: all)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*pgDSN) == "" {
		return fmt.Errorf("--postgres-dsn is required")
	}

	db, err := sql.Open("pgx", *pgDSN)
	if err != nil {
		return fmt.Errorf("open postgres: %w", err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping postgres: %w", err)
	}
	// Self-bootstrapping schema (idempotent) so a fresh media database
	// carries the family registry + HNSW indexes before any write.
	for _, ddl := range []string{pgmigration.MediaSchemaDDL, pgmigration.MediaVectorSurfacesDDL, pgmigration.MediaHNSWIndexesDDL} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("apply media migrations: %w", err)
		}
	}

	vectors := pgmedia.NewVectorSurfaceWriter(db)
	cfg := pgmedia.EnrichmentBackfillConfig{BatchSize: *batchSize, Limit: *limit, Force: *force}
	switch strings.TrimSpace(*legs) {
	case "", "all":
		// all legs (zero-value selects all)
	default:
		for _, l := range strings.Split(*legs, ",") {
			switch strings.TrimSpace(l) {
			case "features":
				cfg.AnalyzeFeatures = true
			case "semantic":
				cfg.BackfillSemantic = true
			case "visual":
				cfg.BackfillVisual = true
			default:
				return fmt.Errorf("unknown leg %q (valid: features,semantic,visual)", l)
			}
		}
	}

	// Features + visual legs: both need the sidecar (face detector and
	// SigLIP image encoder respectively) plus the ffmpeg sampler. An
	// unavailable sidecar with those legs requested = typed fail-closed
	// error, never a fake has_faces row or zero vector.
	wantFeatures := cfg.AnalyzeFeatures || (cfg.AnalyzeFeatures == false && cfg.BackfillSemantic == false && cfg.BackfillVisual == false)
	wantVisual := cfg.BackfillVisual || (cfg.AnalyzeFeatures == false && cfg.BackfillSemantic == false && cfg.BackfillVisual == false)
	var analyzer *pgmedia.MediaFeatureAnalyzer
	var visual *pgmedia.VisualEmbeddingPipeline
	var registry *pgmedia.VisualEmbeddingModelRegistry
	if sidecar := strings.TrimSpace(*sidecarURL); sidecar != "" {
		registry = pgmedia.DefaultVisualEmbeddingModelRegistry()
		if wantFeatures {
			faces := pgmedia.NewSidecarFaceDetector(sidecar, 0)
			probe := pgmedia.NewFFMPEGProbeAdapter("")
			sampler, serr := pgmedia.NewFFMPEGKeyframeSamplerAdapter("")
			if serr != nil {
				return serr
			}
			analyzer = pgmedia.NewMediaFeatureAnalyzer(pgmedia.FeatureAnalyzerDeps{
				Probe: probe, Keyframes: sampler, Faces: faces,
			})
		}
		if wantVisual {
			visualEmbedder, verr := pgmedia.NewSidecarVisualEmbedder(sidecar, registry, pgmedia.DefaultVisualModelID, 0)
			if verr != nil {
				return verr
			}
			sampler, serr := pgmedia.NewFFMPEGKeyframeSamplerAdapter("")
			if serr != nil {
				return serr
			}
			visual, err = pgmedia.NewVisualEmbeddingPipeline(pgmedia.VisualEmbeddingDeps{
				Keyframes: sampler, Embedder: visualEmbedder,
				Registry: registry, ModelID: pgmedia.DefaultVisualModelID,
			})
			if err != nil {
				return err
			}
		}
	} else if wantFeatures || wantVisual {
		return fmt.Errorf("--sidecar-url is required when the features or visual leg is requested (no fake availability: faces and visual vectors cannot be fabricated)")
	}

	// Semantic leg: the canonical E5 text embedder over the sidecar
	// (same family as the index worker's embedder).
	var semantic pgmedia.AssetEmbedder
	var semanticModel string
	if sidecar := strings.TrimSpace(*sidecarURL); sidecar != "" {
		semanticModel = "intfloat/multilingual-e5-base"
		semantic = pgmedia.NewEmbedAssetTextAdapter(db, pgmedia.NewSidecarTextEmbedderAdapter(sidecar))
	}

	engine, err := pgmedia.NewEnrichmentEngine(db, vectors, analyzer, visual, semantic, semanticModel)
	if err != nil {
		return err
	}
	report, err := engine.Run(ctx, pgmedia.ResolveAssetPathFromDB(db), cfg)
	if report != nil {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(report)
	}
	if err != nil {
		return err
	}
	if !report.AllComplete {
		return fmt.Errorf("enrichment coverage incomplete: features=%d/%d semantic=%d/%d visual=%d/%d",
			report.FeatureCoverage, report.TotalAssets,
			report.SemanticCoverage, report.TotalAssets,
			report.VisualCoverage, report.TotalAssets)
	}
	fmt.Println("media enrichment OK: 100% feature/semantic/visual coverage")
	return nil
}
