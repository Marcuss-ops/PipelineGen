package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/disasterrecovery"
	qdrantschema "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/collections"
)

// qdrantReadiness runs the 9 production-shaped checks and populates
// the report. Every check is independent; a failure in one does NOT
// short-circuit the others (operators want to see ALL failing checks
// in one run).
func qdrantReadiness(ctx context.Context, db *sql.DB, cfg *config.Config, log *zap.Logger, root *compositionRoot) (qdrantReadinessReport, error) {
	report := qdrantReadinessReport{
		Checks: make(map[string]string, len(readinessCheck)),
	}
	deps := readinessDeps{DB: db, Cfg: cfg, Log: log, Root: root}

	// Required-columns check (SQLite shape; not a production-wiring
	// check, mirrors pre-PR-15 semantics). The inspection helper
	// lives in qdrant_readiness_db.go (Commit E); the orchestrator
	// calls it by direct symbol.
	requiredColumns := []string{
		"audio_embedding",
		"youtube_video_id",
		"youtube_url",
		"start_time",
		"end_time",
		"workspace_id",
		"channel_id",
		"license",
		"source_version",
		"style",
	}
	present, missing, err := inspectRequiredColumns(ctx, db, requiredColumns)
	if err != nil {
		report.Checks["sqlite_required_columns"] = "fail"
		report.MissingColumns = missing
		report.SchemaErrors++
	} else {
		report.RequiredColumnsPresent = present
		report.MissingColumns = missing
		report.SQLiteMigrationsComplete = len(missing) == 0
		if len(missing) > 0 {
			report.SchemaErrors += len(missing)
		}
	}

	// Outbox table existence — depended on by dead_letter check.
	report.OutboxOperational = tableExists(ctx, db, "outbox_events")

	// Channel-matrix-aware counter scan populates legacy flat
	// fields. legacy_cleanup_clean derives from these counts.
	if err := collectReadinessCounters(ctx, db, &report); err != nil {
		log.Warn("readiness counter scan failed; legacy_cleanup_clean marked fail", zap.Error(err))
		report.Checks["legacy_cleanup_clean"] = "fail"
	} else if status, ferr := runOneCheck(ctx, deps, checkLegacyAudit); ferr == nil {
		report.Checks["legacy_cleanup_clean"] = status
	} else {
		report.Checks["legacy_cleanup_clean"] = "fail"
	}

	// Qdrant reachability + active alias resolution.
	qdrantProbeAndSchema(ctx, cfg, log, &report)
	if report.QdrantReachable {
		report.Checks["qdrant_active_collection_real"] = "pass"
	} else {
		report.Checks["qdrant_active_collection_real"] = "fail"
	}

	// Run every named readiness check.
	for name, fn := range readinessCheck {
		if _, already := report.Checks[name]; !already {
			if status, ferr := runOneCheck(ctx, deps, fn); ferr == nil {
				report.Checks[name] = status
			} else {
				report.Checks[name] = "fail"
			}
		}
	}

	// Final aggregation: ready iff every check returned "pass".
	report.Ready = true
	for _, status := range report.Checks {
		if status != "pass" {
			report.Ready = false
			break
		}
	}
	return report, nil
}

func runOneCheck(ctx context.Context, deps readinessDeps, fn func(context.Context, readinessDeps) checkStatus) (string, error) {
	if fn == nil {
		return "fail", fmt.Errorf("nil check fn")
	}
	res := fn(ctx, deps)
	if res.Pass {
		return "pass", nil
	}
	msg := res.Err
	if msg == "" {
		msg = "check failed (no message)"
	}
	return "fail", fmt.Errorf("%s", msg)
}

// ── Channel matrix (preserved from predecessor) ───────────────────────

func isChannelRequiredForMediaType(channel, mediaType string) bool {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "video":
		return channel == "text" || channel == "transcript" || channel == "visual"
	case "image":
		return channel == "text" || channel == "visual"
	case "audio":
		return channel == "text" || channel == "transcript" || channel == "audio"
	}
	return false
}

func parseVectorLen(raw string) ([]float32, int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" || raw == "{}" {
		return nil, 0, fmt.Errorf("empty vector")
	}
	var vec []float32
	if err := json.Unmarshal([]byte(raw), &vec); err != nil {
		return nil, 0, err
	}
	return vec, len(vec), nil
}

func qdrantProbeAndSchema(ctx context.Context, cfg *config.Config, log *zap.Logger, report *qdrantReadinessReport) error {
	client := transport.NewClient(&qdrantschema.Config{
		BaseURL: cfg.Qdrant.BaseURL,
		Timeout: cfg.Qdrant.Timeout,
		APIKey:  cfg.Qdrant.APIKey,
	}, log)
	probe := disasterrecovery.NewHealthProbe(client)
	if err := probe.Probe(ctx); err != nil {
		report.QdrantReachable = false
		return fmt.Errorf("qdrant health probe failed: %w", err)
	}
	report.QdrantReachable = true

	schema := qdrantschema.DefaultV3Schema()
	mgr := collections.NewCollectionManager(client, schema, log)
	active, err := mgr.GetActiveCollection(ctx)
	if err != nil {
		return fmt.Errorf("resolve active collection: %w", err)
	}
	report.ActiveCollection = active
	if active == "" {
		return fmt.Errorf("qdrant runtime alias %q has no target", schema.RuntimeAlias)
	}
	diff, err := mgr.CompareActiveCollection(ctx)
	if err != nil {
		return fmt.Errorf("compare active collection: %w", err)
	}
	report.ActiveCollectionCompatible = diff.Compatible
	if !diff.Compatible {
		report.SchemaErrors++
	}
	return nil
}
