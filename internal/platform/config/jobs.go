package config

// JobsConfig holds job-related configuration.
type JobsConfig struct {
	NewJobsPaused         bool   `yaml:"new_jobs_paused" default:"false"`
	LeaseTTLSeconds       int    `yaml:"lease_ttl_seconds" default:"300"`
	MaxParallelPerProject int    `yaml:"max_parallel_per_project" default:"16"`
	AutoCleanupHours      int    `yaml:"auto_cleanup_hours" default:"24"`
	CatalogSyncInterval   string `yaml:"catalog_sync_interval" env:"VELOX_CATALOG_SYNC_INTERVAL" default:"6h"`
	YouTubeExtractTimeout int    `yaml:"youtube_extract_timeout_seconds" env:"VELOX_YOUTUBE_EXTRACT_TIMEOUT" default:"1200"`
	// YoutubeMaxSegmentDurationSeconds caps the per-clip duration the
	// YouTube segment pipeline accepts (SegmentPolicy.MaxDuration). The
	// canonical default is 60s (DefaultSegmentPolicy); production may
	// raise it (e.g. 120 for 2-minute clips) via config/env without
	// touching the shared default policy.
	YoutubeMaxSegmentDurationSeconds int    `yaml:"youtube_max_segment_duration_seconds" env:"VELOX_YOUTUBE_MAX_SEGMENT_DURATION_SECONDS" default:"60"`
	MaintenanceInterval              string `yaml:"maintenance_interval" default:"24h"`
	BackupInterval                   string `yaml:"backup_interval" default:"6h"`
	IndexingInterval                 string `yaml:"indexing_interval" default:"15m"`
	RetentionDays                    int    `yaml:"retention_days" env:"VELOX_RETENTION_DAYS" default:"30"`
	// RetentionInterval is the periodic sweeper tick interval — controls how
	// often the job_events retention sweeper runs (when RetentionDays > 0).
	// Default 12h: balances bounded DELETE-load (lock contention) against
	// operator visibility (operator dashboards refresh 4×/day at this rate,
	// matching the qdrant-stale-cleaner cadence in pre-Wave 22 docs).
	// Accepts a duration string ("30m", "12h", "1h"); empty falls back to 12h.
	RetentionInterval string `yaml:"retention_interval" env:"VELOX_RETENTION_INTERVAL" default:"12h"`
	// PR-Polling / ADR-0002 §D6.5 (June 2026): exponential-backoff
	// knobs for the server-side Worker poll loop. Three new fields
	// (all forwarded into Worker.BackoffConfig at composition time):
	//   - PollMaxBackoff is the cap on the per-poll sleep duration
	//     (exponential: pollEvery → 2× → 4× → … → PollMaxBackoff). The
	//     default 60s matches the qdrant-stale-cleaner historical
	//     cadence and bounds the worst-case Enqueue→Claim latency
	//     under sustained idle.
	//   - PollJitterFraction is the AWS-style full-jitter factor
	//     (https://aws.amazon.com/blogs/architecture/exponential-backoff-and-jitter/).
	//     actual_sleep = rand(0, current_backoff) per Worker per
	//     iteration; spreads thundering-herd wake-ups across the
	//     pool. 0.0 = deterministic burn of full backoff; 1.0 =
	//     uniform [0, current_backoff]. Default 0.5.
	//   - PollConsecutiveEmptyBeforeBackoff is the threshold of
	//     CONSECUTIVE empty Claims before the backoff curve
	//     escalates. Below threshold: stay at BaseInterval (=
	//     PollEvery). Above: doubles every subsequent empty claim,
	//     capped at PollMaxBackoff. Default 3; 0 disables escalation
	//     entirely (legacy fixed-poll behaviour — emergency unblock).
	// Operators alert on:
	//   rate(worker_backoff_events_total[5m]) > 0
	// for sustained periods (idle Workers accumulating backoff = the
	// queue is empty AND jobs are not being enqueued; useful BUT -
	// normally operators see this on a steady-state worker pool).
	PollMaxBackoff                    string  `yaml:"poll_max_backoff" env:"VELOX_POLL_MAX_BACKOFF" default:"60s"`
	PollJitterFraction                float64 `yaml:"poll_jitter_fraction" env:"VELOX_POLL_JITTER_FRACTION" default:"0.5"`
	PollConsecutiveEmptyBeforeBackoff int     `yaml:"poll_consecutive_empty_before_backoff" env:"VELOX_POLL_CONSECUTIVE_EMPTY_BEFORE_BACKOFF" default:"3"`

	// ProgressCoalesceWindow gates the per-jobID in-memory coalescing
	// of broker.Progress(...) calls (PR-Progress / ADR-0002 §D6.4, June 2026).
	// A worker that emits Progress(pct, msg) at >10Hz for one jobID is
	// banner-spam today: each call writes an UPDATE jobs + a separate
	// AddEvent INSERT into job_events. The coalescer buffers the latest
	// (pct, msg) per jobID and flushes 1 UPDATE + 1 INSERT per window.
	// Accepts a duration string ("100ms", "250ms", "500ms", "5s");
	// empty falls back to 100ms. 0 disables coalescing (passthrough;
	// documented as the emergency-unblock escape hatch — every
	// call writes its own row). Operators alert on
	// `rate(job_progress_events_total) / rate(job_progress_calls_total)`
	// dropping below 1.0 once coalescing is enabled: a ratio strictly
	// below 1.0 is the canonical "coalescer is reducing event pressure"
	// signal (the ratio equals calls = exact-passthrough, ratio !=
	// 0 means at least some coalescing happened, ratio = 0 means
	// every call was coalesced away by a faster-emitting same-bucket).
	// The ratio also catches misconfig: a window of 0 should report
	// ratio = 1.0 — if it doesn't the metric wiring is broken.
	ProgressCoalesceWindow string `yaml:"progress_coalesce_window" env:"VELOX_PROGRESS_COALESCE_WINDOW" default:"100ms"`
	// SearchRateLimit limits YouTube search API calls per hour for search_queries.
	// 0 = unlimited. Default 10/hour is safe for YouTube free tier (100 units/day).
	SearchRateLimit int `yaml:"search_rate_limit" default:"10"`

	// PR-Queue-Split-EXPAND / ADR-0003 §"Decider choice" PR #1 (June 2026):
	// Three knobs gate the EXPAND phase of jobs.db.sqlite split. Default
	// behavior today: every flag is OFF — jobs.db.sqlite is NOT opened;
	// *SQLiteStore reads/writes hit the jobs/job_events/dead_letter_jobs
	// tables in media.db.sqlite (unchanged). EXPAND-only; CUTOVER is a
	// future PR after the bench-driven gate in ADR-0003 §"Trigger
	// conditions" §1 lands empirical data.
	//
	// SplitDBEnabled — when true (and cfg boot wiring succeeds), the
	// composition root opens jobs.db.sqlite alongside media.db.sqlite,
	// runs migrations/sqlite_jobs/*.sql on it, and routes *SQLiteStore
	// reads/writes to the new jobs DB instead of media.db.sqlite. Default
	// OFF to keep today's production deployments unaffected.
	//
	// JobsDBPath — filesystem path for jobs.db.sqlite. When empty
	// (default), the composition root derives the path from
	// cfg.Storage.PrimaryDBFullPath() by stripping "media.db.sqlite" from
	// the basename and substituting "jobs.db.sqlite" — the canonical
	// pair lives side-by-side in <DataDir>/media/ (or whatever
	// Storage.MediaDir resolves to). Operators can override for alternate
	// layouts (e.g. a network-mounted queue DB for multi-node prep).
	// The override is a string substitution, not a remap; operators
	// who need the jobs DB on a different volume set the path explicitly.
	SplitDBEnabled bool   `yaml:"split_db_enabled" env:"VELOX_SPLIT_DB_ENABLED" default:"false"`
	JobsDBPath     string `yaml:"jobs_db_path" env:"VELOX_JOBS_DB_PATH" default:""`

	// EnableBackgroundJobs controls whether background workers/schedulers run.
	// Default true; set to false via env VELOX_ENABLE_BACKGROUND_JOBS=false for dev mode.
	EnableBackgroundJobs bool `yaml:"enable_background_jobs" env:"VELOX_ENABLE_BACKGROUND_JOBS" default:"true"`
	// EnableChannelMonitor controls the YouTube channel monitor scheduler.
	// Default false; opt-in via env VELOX_ENABLE_CHANNEL_MONITOR=true.
	EnableChannelMonitor bool `yaml:"enable_channel_monitor" env:"VELOX_ENABLE_CHANNEL_MONITOR" default:"false"`
	// PR-Deletion-Reconciler / Blocco 3.2 (June 2026): two knobs
	// gate the DeletionReconciler ticker.
	//
	// DeletionReconcilerInterval is the periodic tick that scans
	// media_assets for deletion-stuck rows + re-emits the appropriate
	// outbox event. Default 15min balances operator visibility against
	// per-tick DB scan load (the query is bounded by batchSize=100,
	// see internal/infrastructure/database/sqlite/deletion/stuck_row_scanner.go).
	//
	// DeletionReconcilerStuckThreshold is the age cutoff: rows whose
	// updated_at is older than now-threshold are eligible for
	// re-emission. Default 30min matches the Blocco 5 outbox-pool
	// backoff cap (90s × ~20 retries = 30min) — a row stuck beyond
	// this is a worker-crash or infra-fault that the pool cannot
	// self-recover from, not a transient retry storm.
	//
	// Operators alert on:
	//   rate(deletion_reconciler_actions_total[5m]) > 0  (reconciler
	//   is dispatching; expected only on recovery from bumps/crashes)
	//   AND
	//   rate(deletion_reconciler_actions_total[1h]) == 0
	// (reconciler is healthy when no actions are emitted; sustained
	// non-zero rate indicates a recurring bug).
	DeletionReconcilerInterval       string `yaml:"deletion_reconciler_interval" env:"VELOX_DELETION_RECONCILER_INTERVAL" default:"15m"`
	DeletionReconcilerStuckThreshold string `yaml:"deletion_reconciler_stuck_threshold" env:"VELOX_DELETION_RECONCILER_STUCK_THRESHOLD" default:"30m"`
}
