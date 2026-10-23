// cmd/admin/db_retention.go (October 2026 disk-retention):
//
// `admin db retention` bounds the SQLite telemetry/history tables that
// have no other retention path. It is the generalisation of
// `admin db rotate` (which only covers the observability `api_requests`
// table, today empty in production):
//
//	jobs DB          job_registry_metrics, job_registry_events, job_events
//	observability DB run_observability (cascade → run_*_observations),
//	                 api_requests
//	media DB         resource_observations, api_requests
//
// Two windows are applied:
//
//	--telemetry-max-age-days  default cfg.Storage.ObservabilityMaxAgeDays (7)
//	                          metrics, resource/run observations, api_requests
//	--history-max-age-days    default cfg.Jobs.RetentionDays (30)
//	                          job registry/event ledger
//
// Each database is checkpointed and VACUUM'd (unless --no-vacuum), so the
// freed pages are actually returned to the filesystem. Output is one
// JSON object with per-table rows deleted and per-database bytes
// reclaimed; --dry-run only counts.
//
// Operators schedule it on a daily cron, mirroring `db rotate`.
package database

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
)

type retentionTotals struct {
	DeletedRows    int64 `json:"deleted_rows"`
	BytesReclaimed int64 `json:"bytes_reclaimed"`
}

type retentionReport struct {
	DryRun          bool                       `json:"dry_run"`
	TelemetryCutoff string                     `json:"telemetry_cutoff"`
	HistoryCutoff   string                     `json:"history_cutoff"`
	Databases       []*storage.RetentionResult `json:"databases"`
	Skipped         []string                   `json:"skipped,omitempty"`
	Totals          retentionTotals            `json:"totals"`
}

func RunDBRetention(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("db retention", flag.ExitOnError)
	dataDir := fs.String("data-dir", "./data", "root data directory")
	telemetryDays := fs.Int("telemetry-max-age-days", 0, "retention window in days for telemetry tables (default cfg.storage.observability_max_age_days)")
	historyDays := fs.Int("history-max-age-days", 0, "retention window in days for job registry/event history (default cfg.jobs.retention_days)")
	dryRun := fs.Bool("dry-run", false, "count matching rows without deleting or vacuuming")
	noVacuum := fs.Bool("no-vacuum", false, "delete rows but skip VACUUM (freed pages stay on the freelist)")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	fullCfg, err := config.Get()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if *dataDir != "" && *dataDir != "./data" {
		fullCfg.Storage.DataDir = *dataDir
	}

	if *telemetryDays == 0 {
		*telemetryDays = fullCfg.Storage.ObservabilityMaxAgeDays
	}
	if *historyDays == 0 {
		*historyDays = fullCfg.Jobs.RetentionDays
	}
	if *telemetryDays <= 0 {
		return fmt.Errorf("-telemetry-max-age-days must be > 0 (got %d)", *telemetryDays)
	}
	if *historyDays <= 0 {
		return fmt.Errorf("-history-max-age-days must be > 0 (got %d)", *historyDays)
	}

	now := time.Now().UTC()
	telemetryCutoff := now.AddDate(0, 0, -*telemetryDays)
	historyCutoff := now.AddDate(0, 0, -*historyDays)

	dataRoot := fullCfg.Storage.AbsDataDir()
	plans := []storage.RetentionPlan{
		{
			Name: "jobs",
			Path: filepath.Join(dataRoot, "jobs", "jobs.db.sqlite"),
			Ops: []storage.RetentionOp{
				{Table: "job_registry_metrics", TimeColumn: "created_at", Cutoff: telemetryCutoff},
				{Table: "job_registry_events", TimeColumn: "created_at", Cutoff: historyCutoff},
				{Table: "job_events", TimeColumn: "created_at", Cutoff: historyCutoff},
			},
		},
		{
			Name: "observability",
			Path: fullCfg.Storage.ObservabilityDBFullPath(),
			Ops: []storage.RetentionOp{
				{Table: "run_observability", TimeColumn: "created_at", Cutoff: telemetryCutoff},
				{Table: "api_requests", TimeColumn: "ts", Cutoff: telemetryCutoff, TimeFormat: storage.SQLiteTimestampFormat},
			},
		},
		{
			Name: "media",
			Path: filepath.Join(dataRoot, "media", "media.db.sqlite"),
			Ops: []storage.RetentionOp{
				{Table: "resource_observations", TimeColumn: "observed_at", Cutoff: telemetryCutoff},
				{Table: "api_requests", TimeColumn: "ts", Cutoff: telemetryCutoff, TimeFormat: storage.SQLiteTimestampFormat},
			},
		},
	}

	report := retentionReport{
		DryRun:          *dryRun,
		TelemetryCutoff: telemetryCutoff.Format(time.RFC3339),
		HistoryCutoff:   historyCutoff.Format(time.RFC3339),
	}

	for _, plan := range plans {
		if _, err := os.Stat(plan.Path); err != nil {
			if os.IsNotExist(err) {
				report.Skipped = append(report.Skipped, plan.Path)
				continue
			}
			return fmt.Errorf("stat %s: %w", plan.Path, err)
		}

		db, err := storage.OpenWritable(plan.Path)
		if err != nil {
			return fmt.Errorf("open %s: %w", plan.Path, err)
		}
		res, applyErr := storage.ApplyRetentionPlan(ctx, db, plan, *dryRun, !*noVacuum)
		closeErr := db.Close()
		if applyErr != nil {
			return fmt.Errorf("retention %s: %w", plan.Name, applyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close %s: %w", plan.Path, closeErr)
		}

		report.Databases = append(report.Databases, res)
		for _, table := range res.Tables {
			report.Totals.DeletedRows += table.DeletedRows
		}
		report.Totals.BytesReclaimed += res.BytesReclaimed
	}

	enc := json.NewEncoder(os.Stdout)
	return enc.Encode(report)
}
