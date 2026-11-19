// Package voiceover — process.go.
//
// Shared stageLog helper emits structured lifecycle telemetry.
package voiceover

import (
	"time"

	"go.uber.org/zap"
)

// stageLog returns a closure that emits pipeline_stage_started now
// and pipeline_stage_completed when invoked. Mirrors the
// scripts/jobs/job_helpers.go canonical emission pattern (Pattern
// 3, AGENTS.md).
//
// FASE 5 (VO-OPERATIONAL-READINESS, July 2026): added assetID and
// project fields per the structured logging contract. Every stage
// now logs: job_id, asset_id, project, language, stage, status,
// duration_ms.
//
// Stage timings are recorded by the canonical observability run when one is
// bound; this helper only emits the structured lifecycle log.
func stageLog(log *zap.Logger, jobID, assetID, project, stage, language string) func(status string) {
	start := time.Now()
	logFields := []zap.Field{
		zap.String("stage", stage),
		zap.String("job_id", jobID),
		zap.String("asset_id", assetID),
		zap.String("language", language),
	}
	if project != "" {
		logFields = append(logFields, zap.String("project", project))
	}
	log.Info("pipeline_stage_started", logFields...)
	return func(status string) {
		dur := time.Since(start)
		completedFields := append(logFields,
			zap.Int64("duration_ms", dur.Milliseconds()),
			zap.String("status", status),
		)
		log.Info("pipeline_stage_completed", completedFields...)
	}
}
