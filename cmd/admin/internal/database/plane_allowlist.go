package database

// DB-03 allowlists: the only tables a plane may own. Anything else is a leaked
// business table and is reported by `db doctor` as a warning (error under
// --strict). These lists are intentionally conservative: adding a table here
// is a design decision, not a way to silence the gate.
func observabilityTableAllowlist() map[string]bool {
	return setOf(
		"admin_mutation_audit",
		"api_requests",
		// job_attempts is written by the run recorder (StartReport) and is the
		// FK parent of run_observability; dropping it broke /ready on
		// 2026-11-27 and is the reason the allowlist must follow the recorder,
		// not the table-name heuristic.
		"job_attempts",
		// replay_bundles is a DB-04 retention plan entry (observability).
		"replay_bundles",
		"monitor_enqueue_outbox",
		"outbox_events",
		"resource_observations",
		"run_artifact_checkpoints",
		"run_artifact_observations",
		"run_child_observations",
		"run_observability",
		"run_operation_observations",
		"run_report_snapshots",
		"run_resource_aggregates",
		"run_resource_reports",
		"run_resource_samples",
		"run_stage_observations",
		"schema_migrations",
	)
}

func cacheTableAllowlist() map[string]bool {
	return setOf(
		"admin_mutation_audit",
		"artifact_cache_entries",
		"artifact_cache_metrics",
		"media_query_cache",
		"render_attempt_analytics",
		"research_cache",
		"schema_migrations",
		"semantic_plan_cache",
		"stock_source_cache",
		"transcript_cache",
		"translation_cache",
		"vidrush_provider_cache",
	)
}

func setOf(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}
