package diagnostics

import (
	"context"
)

// Service orchestrates diagnostics operations through narrow ports.
type Service struct {
	indexHealth IndexHealthPort
	assetStats  AssetStatsPort
	log         Logger
}

// NewService creates a DiagnosticsService.
func NewService(indexHealth IndexHealthPort, assetStats AssetStatsPort, log Logger) *Service {
	return &Service{indexHealth: indexHealth, assetStats: assetStats, log: log}
}

// HealthCommand is the input for a health check.
type HealthCommand struct{}

// HealthResult is the output of a health check.
type HealthResult struct {
	OK       bool
	Degraded bool
	Checks   map[string]any
}

// Check runs all diagnostics and returns a unified report.
func (s *Service) Check(ctx context.Context, cmd HealthCommand) (*HealthResult, error) {
	result := &HealthResult{OK: true, Checks: make(map[string]any)}

	// Run index-health via the realtime port if available.
	if s.indexHealth != nil {
		report, err := s.indexHealth.IndexHealth(ctx)
		if err != nil {
			s.log.Warn("index-health check failed", "error", err)
			result.Checks["index_health"] = map[string]any{
				"ok":    false,
				"error": err.Error(),
			}
		} else if report != nil {
			result.Degraded = report.Degraded
			result.Checks["index_health"] = map[string]any{
				"ok":               report.OK,
				"sqlite_assets":    report.SQLiteAssets,
				"sqlite_indexed":   report.SQLiteIndexed,
				"degraded_sources": report.DegradedSources,
			}
		}
	}

	// Asset stats.
	if s.assetStats != nil {
		stats, err := s.assetStats.GetStats(ctx)
		if err != nil {
			s.log.Warn("asset stats check failed", "error", err)
			result.Checks["asset_stats"] = map[string]any{"ok": false, "error": err.Error()}
		} else if stats != nil {
			result.Checks["asset_stats"] = map[string]any{
				"ok":        true,
				"total":     stats.Total,
				"by_type":   stats.ByType,
				"by_status": stats.ByStatus,
			}
		}
	}

	return result, nil
}
