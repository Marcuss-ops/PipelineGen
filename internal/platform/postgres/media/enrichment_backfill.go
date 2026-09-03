// Package media — enrichment_backfill.go: the FASE-4 enrichment backfill
// engine (POSTGRES-MEDIA-CUTOVER TODO 4).
//
// For every media_assets row in the SSOT the engine computes the missing
// derived surfaces and measures the coverage after the run:
//
//	asset → MediaFeatureAnalyzer.AnalyzeAndStore   (media_asset_features)
//	asset → VisualEmbeddingPipeline.EmbedAndStore  (media_embeddings visual)
//	asset → semantic backfill via AssetEmbedder    (media_embeddings text)
//
// The engine is STREAMING and IDEMPOTENT: assets are processed in keyset
// order, each write is an upsert, and already-covered assets are skipped
// unless Force is set — a re-run converges to 100% coverage without
// re-embedding what is already present.
//
// godlike/07: the engine never fabricates a feature or a vector to make a
// coverage number look right. Per-asset failures are recorded in the
// report (bounded list) and counted as Uncovered; a run that ends below
// 100% coverage reports the shortfall — the certification gate decides
// pass/fail, not the engine.
package media

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// EnrichmentBackfillConfig configures one enrichment run.
type EnrichmentBackfillConfig struct {
	// Limit caps the number of assets processed (0 = all).
	Limit int
	// BatchSize is the keyset-pagination page size. Zero means 100.
	BatchSize int
	// Force re-processes assets that already carry the derived surfaces.
	Force bool
	// AnalyzeFeatures / BackfillSemantic / BackfillVisual select the legs.
	// Zero-value (all false) means ALL legs run.
	AnalyzeFeatures  bool
	BackfillSemantic bool
	BackfillVisual   bool
}

// EnrichmentCoverageReport is the machine-readable outcome: per-surface
// coverage over the TOTAL video-asset population (the certification SQL
// from the cutover checklist, executed live).
type EnrichmentCoverageReport struct {
	TotalAssets      int64    `json:"total_assets"`
	FeatureCoverage  int64    `json:"feature_coverage"`
	SemanticCoverage int64    `json:"semantic_coverage"`
	VisualCoverage   int64    `json:"visual_coverage"`
	FeaturesWritten  int      `json:"features_written"`
	SemanticWritten  int      `json:"semantic_written"`
	VisualWritten    int      `json:"visual_written"`
	AssetsProcessed  int      `json:"assets_processed"`
	AssetsSkipped    int      `json:"assets_skipped_already_covered"`
	UncoveredCount   int      `json:"uncovered_assets"`
	Uncovered        []string `json:"uncovered,omitempty"`
	FeatureComplete  bool     `json:"feature_coverage_complete"`
	SemanticComplete bool     `json:"semantic_coverage_complete"`
	VisualComplete   bool     `json:"visual_coverage_complete"`
	AllComplete      bool     `json:"all_coverage_complete"`
}

const (
	enrichmentDefaultBatch = 100
	enrichmentMaxUncovered = 100
) // FeatureLeg is the narrow port the features leg consumes (the
// production concrete is MediaFeatureAnalyzer; tests may stub it).
type FeatureLeg interface {
	AnalyzeAndStore(ctx context.Context, vectors *VectorSurfaceWriter, assetID, localPath string) (*FeatureAnalysisResult, error)
}

// VisualLeg is the narrow port the visual leg consumes (production
// concrete: VisualEmbeddingPipeline).
type VisualLeg interface {
	EmbedAndStore(ctx context.Context, vectors *VectorSurfaceWriter, assetID, localPath string) (*VisualEmbeddingResult, error)
}

// EnrichmentEngine drives the enrichment backfill over the media SSOT.
type EnrichmentEngine struct {
	db              *sql.DB
	vectors         *VectorSurfaceWriter
	features        FeatureLeg
	visual          VisualLeg
	semantic        AssetEmbedder
	semanticModelID string
}

// NewEnrichmentEngine constructs the engine. The legs are optional at
// construction (nil analyzer = skip the features leg) but the engine
// fails closed if NO leg is configured at all.
func NewEnrichmentEngine(db *sql.DB, vectors *VectorSurfaceWriter, features FeatureLeg, visual VisualLeg, semantic AssetEmbedder, semanticModelID string) (*EnrichmentEngine, error) {
	if db == nil {
		return nil, fmt.Errorf("enrichment engine: db is required")
	}
	if vectors == nil {
		return nil, fmt.Errorf("enrichment engine: vector surface writer is required")
	}
	if features == nil && visual == nil && semantic == nil {
		return nil, fmt.Errorf("enrichment engine: at least one enrichment leg must be configured")
	}
	return &EnrichmentEngine{
		db:              db,
		vectors:         vectors,
		features:        features,
		visual:          visual,
		semantic:        semantic,
		semanticModelID: semanticModelID,
	}, nil
}

// ResolveAssetPath is the port that maps a media_assets row onto its
// local media file. Production concrete: asset_locations (kind='local')
// URI resolution, falling back to metadata local_path. The engine injects
// it so tests can stub the filesystem.
type ResolveAssetPath func(ctx context.Context, assetID string) (string, error)

// Run processes every ACTIVE video asset and produces the coverage report.
func (e *EnrichmentEngine) Run(ctx context.Context, resolve ResolveAssetPath, cfg EnrichmentBackfillConfig) (*EnrichmentCoverageReport, error) {
	if resolve == nil {
		return nil, fmt.Errorf("enrichment engine: asset path resolver is required")
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = enrichmentDefaultBatch
	}
	all := !cfg.AnalyzeFeatures && !cfg.BackfillSemantic && !cfg.BackfillVisual

	report := &EnrichmentCoverageReport{Uncovered: []string{}}
	lastID := ""
	for {
		if cfg.Limit > 0 && report.AssetsProcessed+report.AssetsSkipped >= cfg.Limit {
			break
		}
		pageSize := cfg.BatchSize
		if cfg.Limit > 0 && report.AssetsProcessed+report.AssetsSkipped+pageSize > cfg.Limit {
			pageSize = cfg.Limit - report.AssetsProcessed - report.AssetsSkipped
		}
		rows, err := e.db.QueryContext(ctx, `
			SELECT id FROM media_assets
			WHERE id > $1 AND media_type = 'video' AND deleted_at = ''
			  AND lifecycle_state IN ('ACTIVE')
			ORDER BY id
			LIMIT $2
		`, lastID, pageSize)
		if err != nil {
			return nil, fmt.Errorf("enrichment engine: page assets: %w", err)
		}
		var page []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, fmt.Errorf("enrichment engine: scan asset id: %w", err)
			}
			page = append(page, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("enrichment engine: iterate assets: %w", err)
		}
		if len(page) == 0 {
			break
		}
		lastID = page[len(page)-1]

		for _, assetID := range page {
			covered, err := e.assetCoverage(ctx, assetID)
			if err != nil {
				return nil, err
			}
			needFeatures := (all || cfg.AnalyzeFeatures) && !covered.features
			needSemantic := (all || cfg.BackfillSemantic) && !covered.semantic
			needVisual := (all || cfg.BackfillVisual) && !covered.visual
			if !needFeatures && !needSemantic && !needVisual && !cfg.Force {
				report.AssetsSkipped++
				continue
			}
			report.AssetsProcessed++

			localPath := ""
			if needFeatures || needVisual || cfg.Force {
				// The features + visual legs consume the media file itself;
				// the semantic leg reads search_text from the SSOT and does
				// not need a resolvable local path.
				p, err := resolve(ctx, assetID)
				if err != nil || strings.TrimSpace(p) == "" {
					if needFeatures {
						e.markUncovered(report, assetID, fmt.Sprintf("resolve path: %v", err))
					}
					if needVisual {
						e.markUncovered(report, assetID, fmt.Sprintf("resolve path: %v", err))
					}
					localPath = ""
				} else {
					localPath = p
				}
			}

			if (needFeatures || cfg.Force) && localPath != "" {
				if e.features == nil {
					e.markUncovered(report, assetID, "features leg requested but no analyzer configured")
				} else if _, err := e.features.AnalyzeAndStore(ctx, e.vectors, assetID, localPath); err != nil {
					e.markUncovered(report, assetID, fmt.Sprintf("features: %v", err))
				} else {
					report.FeaturesWritten++
				}
			}
			if needSemantic || cfg.Force {
				if e.semantic == nil {
					e.markUncovered(report, assetID, "semantic leg requested but no embedder configured")
				} else {
					report.SemanticWritten += e.embedSemantic(ctx, assetID, report)
				}
			}
			if (needVisual || cfg.Force) && localPath != "" {
				if e.visual == nil {
					e.markUncovered(report, assetID, "visual leg requested but no visual pipeline configured")
				} else if _, err := e.visual.EmbedAndStore(ctx, e.vectors, assetID, localPath); err != nil {
					e.markUncovered(report, assetID, fmt.Sprintf("visual: %v", err))
				} else {
					report.VisualWritten++
				}
			}
		}
	}

	if err := e.measureCoverage(ctx, report); err != nil {
		return nil, err
	}
	return report, nil
}

// embedSemantic backfills ONE asset's text-channel embedding. Returns 1
// on success, 0 after recording the failure (never panics, never skips).
func (e *EnrichmentEngine) embedSemantic(ctx context.Context, assetID string, report *EnrichmentCoverageReport) int {
	vec, err := e.semantic.EmbedAssetText(ctx, assetID)
	if err != nil {
		e.markUncovered(report, assetID, fmt.Sprintf("semantic: %v", err))
		return 0
	}
	if len(vec) == 0 {
		e.markUncovered(report, assetID, "semantic: zero-length embedding")
		return 0
	}
	if err := e.vectors.UpsertEmbedding(ctx, assetID, "text", e.semanticModelID, vec); err != nil {
		e.markUncovered(report, assetID, fmt.Sprintf("semantic store: %v", err))
		return 0
	}
	return 1
}

// assetSurfaceCoverage is the per-asset presence map.
type assetSurfaceCoverage struct {
	features bool
	semantic bool
	visual   bool
}

// assetCoverage reads which derived surfaces exist for one asset.
func (e *EnrichmentEngine) assetCoverage(ctx context.Context, assetID string) (assetSurfaceCoverage, error) {
	var c assetSurfaceCoverage
	if err := e.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM media_asset_features WHERE asset_id = $1)`, assetID).Scan(&c.features); err != nil {
		return c, fmt.Errorf("enrichment engine: feature coverage %q: %w", assetID, err)
	}
	if err := e.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM media_embeddings WHERE asset_id = $1 AND embedding_type = 'text')`, assetID).Scan(&c.semantic); err != nil {
		return c, fmt.Errorf("enrichment engine: semantic coverage %q: %w", assetID, err)
	}
	if err := e.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM media_embeddings WHERE asset_id = $1 AND embedding_type = 'visual')`, assetID).Scan(&c.visual); err != nil {
		return c, fmt.Errorf("enrichment engine: visual coverage %q: %w", assetID, err)
	}
	return c, nil
}

// measureCoverage executes the certification coverage SQL over the FULL
// video-asset population and stamps the completeness flags.
func (e *EnrichmentEngine) measureCoverage(ctx context.Context, report *EnrichmentCoverageReport) error {
	err := e.db.QueryRowContext(ctx, `
		SELECT
		    COUNT(*)::bigint,
		    COUNT(f.asset_id)::bigint,
		    COUNT(*) FILTER (WHERE se.asset_id IS NOT NULL)::bigint,
		    COUNT(*) FILTER (WHERE ve.asset_id IS NOT NULL)::bigint
		FROM media_assets a
		LEFT JOIN media_asset_features f ON f.asset_id = a.id
		LEFT JOIN media_embeddings se ON se.asset_id = a.id AND se.embedding_type = 'text'
		LEFT JOIN media_embeddings ve ON ve.asset_id = a.id AND ve.embedding_type = 'visual'
		WHERE a.media_type = 'video' AND a.deleted_at = '' AND a.lifecycle_state IN ('ACTIVE')
	`).Scan(&report.TotalAssets, &report.FeatureCoverage, &report.SemanticCoverage, &report.VisualCoverage)
	if err != nil {
		return fmt.Errorf("enrichment engine: coverage measure: %w", err)
	}
	report.FeatureComplete = report.TotalAssets > 0 && report.FeatureCoverage == report.TotalAssets
	report.SemanticComplete = report.TotalAssets > 0 && report.SemanticCoverage == report.TotalAssets
	report.VisualComplete = report.TotalAssets > 0 && report.VisualCoverage == report.TotalAssets
	report.AllComplete = report.FeatureComplete && report.SemanticComplete && report.VisualComplete
	report.UncoveredCount = int(report.TotalAssets - report.FeatureCoverage)
	return nil
}

// markUncovered records a per-asset failure (bounded list).
func (e *EnrichmentEngine) markUncovered(report *EnrichmentCoverageReport, assetID, reason string) {
	report.UncoveredCount++
	if len(report.Uncovered) < enrichmentMaxUncovered {
		report.Uncovered = append(report.Uncovered, assetID+": "+reason)
	}
}
