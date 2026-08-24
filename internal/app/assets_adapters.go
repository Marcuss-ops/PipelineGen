package app

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	appdiag "github.com/Marcuss-ops/PipelineGen/internal/application/assets/diagnostics"
	appsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	assetsrepo "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/catalog"
)

// ── Diagnostics adapters ───────────────────────────────────────────────

// diagIndexHealthAdapter adapts real dependencies to diagnostics.IndexHealthPort.
// QDRANT-005 Fase 1 (June 2026): rewired with real SQLite + Qdrant deps.
// Counts SQLite assets/indexed/indexable + Qdrant points, computes drift.
type diagIndexHealthAdapter struct {
	clips  *assetsrepo.ClipsRepository
	qdrant appdiag.QdrantHealthPort
	// collectionName is the Qdrant collection to query (resolved at wiring time).
	collectionName string
}

func (a *diagIndexHealthAdapter) IndexHealth(ctx context.Context) (*appdiag.IndexHealthReport, error) {
	report := &appdiag.IndexHealthReport{OK: true}

	// ── SQLite counts (real data) ──
	total, err := a.clips.CountAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("CountAll: %w", err)
	}
	report.SQLiteAssets = int(total)

	indexed, err := a.clips.CountIndexed(ctx)
	if err != nil {
		return nil, fmt.Errorf("CountIndexed: %w", err)
	}
	report.SQLiteIndexed = int(indexed)

	indexable, err := a.clips.CountIndexable(ctx)
	if err != nil {
		// CountIndexable might not be implemented on all repos; fall back gracefully.
		report.SQLiteIndexable = report.SQLiteIndexed
	} else {
		report.SQLiteIndexable = int(indexable)
	}

	// ── Qdrant count (real data) ──
	if a.qdrant != nil && a.collectionName != "" {
		qdrantPoints, qerr := a.qdrant.CountPoints(ctx, a.collectionName)
		if qerr != nil {
			report.Degraded = true
			report.OK = false
			report.DegradedSources = append(report.DegradedSources,
				fmt.Sprintf("qdrant CountPoints: %v", qerr))
		} else {
			report.QdrantPoints = qdrantPoints
			// Drift: negative = missing in Qdrant, positive = orphan (more in Qdrant than SQLite).
			report.MissingInQdrant = report.SQLiteIndexed - qdrantPoints
			if report.MissingInQdrant < 0 {
				report.OrphanInQdrant = -report.MissingInQdrant
				report.MissingInQdrant = 0
			}
			if report.MissingInQdrant > 0 || report.OrphanInQdrant > 0 {
				report.Degraded = true
			}
		}
	}

	// ── Outbox health ──
	// CountPendingOutbox and CountDeadLetter are on ClipsRepository.
	if pending, perr := a.clips.CountPendingOutbox(ctx); perr == nil {
		report.PendingOutbox = int(pending)
	}
	if dead, derr := a.clips.CountDeadLetter(ctx); derr == nil {
		report.DeadLetter = int(dead)
		if dead > 0 {
			report.Degraded = true
		}
	}

	report.IndexVersion = "v3"
	report.CheckedAt = time.Now().UTC().Format(time.RFC3339)
	return report, nil
}

// diagAssetStatsAdapter adapts *assets.ClipsRepository to diagnostics.AssetStatsPort.
type diagAssetStatsAdapter struct {
	clips *assetsrepo.ClipsRepository
}

func (a *diagAssetStatsAdapter) GetStats(ctx context.Context) (*appdiag.AssetStats, error) {
	total, err := a.clips.CountAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("CountAll: %w", err)
	}
	indexed, err := a.clips.CountIndexed(ctx)
	if err != nil {
		return nil, fmt.Errorf("CountIndexed: %w", err)
	}
	return &appdiag.AssetStats{
		Total: int(total),
		ByType: map[string]int{
			"total":   int(total),
			"indexed": int(indexed),
		},
		ByStatus: map[string]int{
			"ready": int(total),
		},
	}, nil
}

// ── Search adapters ────────────────────────────────────────────────────

// searchCatalogAdapter adapts *catalog.Repository to search.LocalCatalogPort.
type searchCatalogAdapter struct {
	catalog *catalog.Repository
}

func (a *searchCatalogAdapter) SearchAll(ctx context.Context, query string) ([]appsearch.CatalogSearchResult, error) {
	records, err := a.catalog.SearchAll(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make([]appsearch.CatalogSearchResult, len(records))
	for i, r := range records {
		out[i] = appsearch.CatalogSearchResult{
			ID:    r.ID,
			Name:  r.Name,
			Type:  r.MediaType,
			Score: 0,
		}
	}
	return out, nil
}

// zapDiagLogAdapter adapts *zap.Logger to diagnostics.Logger.
type zapDiagLogAdapter struct {
	log *zap.Logger
}

func (a *zapDiagLogAdapter) Info(msg string, keysAndValues ...any) {
	a.log.Sugar().Infow(msg, keysAndValues...)
}
func (a *zapDiagLogAdapter) Warn(msg string, keysAndValues ...any) {
	a.log.Sugar().Warnw(msg, keysAndValues...)
}
func (a *zapDiagLogAdapter) Error(msg string, keysAndValues ...any) {
	a.log.Sugar().Errorw(msg, keysAndValues...)
}

// zapSearchLogAdapter adapts *zap.Logger to search.Logger.
type zapSearchLogAdapter struct {
	log *zap.Logger
}

func (a *zapSearchLogAdapter) Info(msg string, keysAndValues ...any) {
	a.log.Sugar().Infow(msg, keysAndValues...)
}
func (a *zapSearchLogAdapter) Warn(msg string, keysAndValues ...any) {
	a.log.Sugar().Warnw(msg, keysAndValues...)
}
func (a *zapSearchLogAdapter) Error(msg string, keysAndValues ...any) {
	a.log.Sugar().Errorw(msg, keysAndValues...)
}
func (a *zapSearchLogAdapter) Debug(msg string, keysAndValues ...any) {
	a.log.Sugar().Debugw(msg, keysAndValues...)
}
