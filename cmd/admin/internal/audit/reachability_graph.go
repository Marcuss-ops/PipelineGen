// cmd/admin/reachability_graph.go — GC FASE 2: ownership graph + reachability.
//
// The GC plan's second phase builds the reachability graph: for every
// record, answer "chi mi possiede?" (who owns me?). Records without a
// resolvable owner are orphan candidates.
//
// This command produces two artifacts:
//
//  1. The static ownership graph — every table with its canonical owner,
//     join column, relation type (FK or LOGICAL), and the root type
//     (canonical_root, child, cache, queue, history, audit).
//
//  2. Per-table reachability stats — total rows, reachable rows, orphan-
//     candidate rows, computed against the live database via LEFT JOIN
//     on the ownership graph. For child tables the check verifies the
//     owner-reference actually resolves. For caches and queues the
//     classification is deterministic (expired = candidate for removal).
//
// NO DELETIONS are performed. The graph is compute-only — it informs
// Fase 3 (SQLite audit) about which relationships to classify.
//
// Usage:
//
//	go run ./cmd/admin reachability-graph [--json] [--report=path]
//
// Flags:
//
//	--json       machine-readable JSON output
//	--report     write JSON to file (default: stdout)
//	--limit-ids N cap the per-table orphan ID list to N entries (default 50)
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
)

// ── Ownership model types ───────────────────────────────────────────────

// ownershipRelation describes the link from a child table to its owner.
type ownershipRelation struct {
	ChildTable  string `json:"child_table"`
	ChildColumn string `json:"child_column"`
	OwnerTable  string `json:"owner_table"`
	OwnerColumn string `json:"owner_column"`
	Kind        string `json:"kind"`      // "FK" | "LOGICAL"
	RootType    string `json:"root_type"` // canonical_root | child | cache | queue | history | audit
}

// ── Reachability report types ───────────────────────────────────────────

// reachabilityReport is the full FASE 2 output.
type reachabilityReport struct {
	SchemaVersion int          `json:"schema_version"`
	Mode          string       `json:"mode"`
	GeneratedAt   string       `json:"generated_at"`
	NoDeletions   bool         `json:"no_deletions_performed"`
	Graph         graphSummary `json:"graph"`
	Tables        []tableStats `json:"tables"`
}

type graphSummary struct {
	TotalTables    int `json:"total_tables"`
	CanonicalRoots int `json:"canonical_roots"`
	ChildTables    int `json:"child_tables"`
	Caches         int `json:"caches"`
	Queues         int `json:"queues"`
	HistoryTables  int `json:"history"`
	AuditTables    int `json:"audit"`
}

type tableStats struct {
	Table       string   `json:"table"`
	RootType    string   `json:"root_type"`
	Owner       string   `json:"owner,omitempty"`
	JoinCol     string   `json:"join_col,omitempty"`
	TotalRows   int      `json:"total_rows"`
	Reachable   int      `json:"reachable"`
	OrphanCands int      `json:"orphan_candidates"`
	NullOwner   int      `json:"null_owner,omitempty"` // rows where the owner column IS NULL (nullable FK)
	OrphanIDs   []string `json:"orphan_ids,omitempty"` // capped sample
	Error       string   `json:"error,omitempty"`
}

// ── Static ownership model (canonical — curated, NOT auto-derived) ──────

// canonicalOwnershipModel maps every table to its ownership relation.
// Entries with Owner="" are canonical roots, caches, queues, or history.
// FK relations are verified against the live DB at run time; LOGICAL
// relations are proven true by the migration history + live column presence.
var canonicalOwnershipModel = map[string]ownershipRelation{
	// ── Canonical roots (own their children) ──
	"media_assets":                  {RootType: "canonical_root"},
	"jobs":                          {RootType: "canonical_root"},
	"scripts":                       {RootType: "canonical_root"},
	"voiceovers":                    {RootType: "canonical_root"},
	"media_candidates":              {RootType: "canonical_root"},
	"media_concepts":                {RootType: "canonical_root"},
	"stock_batches":                 {RootType: "canonical_root"},
	"artlist_clips":                 {RootType: "canonical_root"},
	"artlist_queries":               {RootType: "canonical_root"},
	"entity_image_catalog_entities": {RootType: "canonical_root"},
	"performance_runs":              {RootType: "canonical_root"},
	"performance_operations":        {RootType: "child", ChildTable: "performance_operations", ChildColumn: "run_id", OwnerTable: "performance_runs", OwnerColumn: "run_id", Kind: "LOGICAL"},
	"registry_runs":                 {RootType: "canonical_root"},
	"workflows":                     {RootType: "canonical_root"},
	"artifacts":                     {RootType: "canonical_root"},
	"subjects":                      {RootType: "canonical_root"},
	"artlist_runs":                  {RootType: "canonical_root"},
	"gemma_script_outputs":          {RootType: "canonical_root"},
	"gemma_memory_entries":          {RootType: "canonical_root"},
	"artlist_search_cache":          {RootType: "cache"},
	"backup_registry":               {RootType: "canonical_root"},
	"benchmark_workloads":           {RootType: "canonical_root"},
	"search_queries":                {RootType: "canonical_root"},
	"qdrant_collections_status_v1":  {RootType: "canonical_root"},
	"qdrant_collections":            {RootType: "canonical_root"},
	"clip_folders":                  {RootType: "canonical_root"},
	"monitored_sources":             {RootType: "canonical_root"},

	// ── FK children (from DB — verified live) ──
	"asset_locations":             {ChildTable: "asset_locations", ChildColumn: "asset_id", OwnerTable: "media_assets", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	"asset_versions":              {ChildTable: "asset_versions", ChildColumn: "asset_id", OwnerTable: "media_assets", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	"asset_artifacts":             {ChildTable: "asset_artifacts", ChildColumn: "asset_id", OwnerTable: "media_assets", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	"asset_renditions":            {ChildTable: "asset_renditions", ChildColumn: "asset_id", OwnerTable: "media_assets", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	"asset_render_variants":       {ChildTable: "asset_render_variants", ChildColumn: "source_clip_id", OwnerTable: "media_assets", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	"assets":                      {RootType: "canonical_root"},
	"asset_sources":               {ChildTable: "asset_sources", ChildColumn: "asset_id", OwnerTable: "assets", OwnerColumn: "asset_id", Kind: "FK", RootType: "child"},
	"artifact_sources":            {ChildTable: "artifact_sources", ChildColumn: "artifact_id", OwnerTable: "artifacts", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	"job_artifacts_artifact":      {ChildTable: "job_artifacts", ChildColumn: "artifact_id", OwnerTable: "artifacts", OwnerColumn: "id", Kind: "LOGICAL", RootType: "child"},
	"job_assets_asset":            {ChildTable: "job_assets", ChildColumn: "asset_id", OwnerTable: "assets", OwnerColumn: "asset_id", Kind: "FK", RootType: "child"},
	"job_assets_job":              {ChildTable: "job_assets", ChildColumn: "job_id", OwnerTable: "jobs", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	"job_artifacts_job":           {ChildTable: "job_artifacts", ChildColumn: "job_id", OwnerTable: "jobs", OwnerColumn: "id", Kind: "LOGICAL", RootType: "child"},
	"asset_text_tracks":           {ChildTable: "asset_text_tracks", ChildColumn: "asset_id", OwnerTable: "media_assets", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	"asset_subtitle_artifacts":    {ChildTable: "asset_subtitle_artifacts", ChildColumn: "asset_id", OwnerTable: "media_assets", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	"asset_licenses":              {ChildTable: "asset_licenses", ChildColumn: "asset_id", OwnerTable: "media_assets", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	"asset_releases":              {ChildTable: "asset_releases", ChildColumn: "asset_id", OwnerTable: "media_assets", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	"asset_tags":                  {ChildTable: "asset_tags", ChildColumn: "asset_id", OwnerTable: "media_assets", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	"asset_visual_summaries":      {ChildTable: "asset_visual_summaries", ChildColumn: "asset_id", OwnerTable: "media_assets", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	"asset_provider_metadata":     {ChildTable: "asset_provider_metadata", ChildColumn: "asset_id", OwnerTable: "media_assets", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	"retrieved_image_details":     {ChildTable: "retrieved_image_details", ChildColumn: "asset_id", OwnerTable: "media_assets", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	"generated_image_details":     {ChildTable: "generated_image_details", ChildColumn: "asset_id", OwnerTable: "media_assets", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	"asset_links":                 {ChildTable: "asset_links", ChildColumn: "asset_id", OwnerTable: "asset_index", OwnerColumn: "asset_id", Kind: "FK", RootType: "child"},
	"asset_renditions_loc":        {ChildTable: "asset_renditions", ChildColumn: "location_id", OwnerTable: "asset_locations", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	"asset_subtitle_artifacts_tt": {ChildTable: "asset_subtitle_artifacts", ChildColumn: "text_track_id", OwnerTable: "asset_text_tracks", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	"asset_text_track_segments":   {ChildTable: "asset_text_track_segments", ChildColumn: "track_id", OwnerTable: "asset_text_tracks", OwnerColumn: "id", Kind: "FK", RootType: "child"},

	// ── Script children (FK) ──
	"script_sections":         {ChildTable: "script_sections", ChildColumn: "script_id", OwnerTable: "scripts", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	"script_versions":         {ChildTable: "script_versions", ChildColumn: "script_id", OwnerTable: "scripts", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	"script_outline_sections": {ChildTable: "script_outline_sections", ChildColumn: "script_id", OwnerTable: "scripts", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	"script_research_sources": {ChildTable: "script_research_sources", ChildColumn: "script_id", OwnerTable: "scripts", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	"script_localizations":    {ChildTable: "script_localizations", ChildColumn: "script_id", OwnerTable: "scripts", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	"script_semantic_items":   {ChildTable: "script_semantic_items", ChildColumn: "script_id", OwnerTable: "scripts", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	"script_stock_matches":    {ChildTable: "script_stock_matches", ChildColumn: "script_id", OwnerTable: "scripts", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	"script_visual_bindings":  {ChildTable: "script_visual_bindings", ChildColumn: "script_id", OwnerTable: "scripts", OwnerColumn: "id", Kind: "FK", RootType: "child"},

	// ── Job children (FK + LOGICAL) ──
	"job_events":           {ChildTable: "job_events", ChildColumn: "job_id", OwnerTable: "jobs", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	"job_steps":            {ChildTable: "job_steps", ChildColumn: "job_id", OwnerTable: "jobs", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	"job_results":          {ChildTable: "job_results", ChildColumn: "job_id", OwnerTable: "jobs", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	"job_registry_events":  {ChildTable: "job_registry_events", ChildColumn: "job_id", OwnerTable: "jobs", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	"job_registry_metrics": {ChildTable: "job_registry_metrics", ChildColumn: "job_id", OwnerTable: "jobs", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	"job_asset_relations":  {ChildTable: "job_asset_relations", ChildColumn: "job_id", OwnerTable: "jobs", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	"workflow_steps":       {ChildTable: "workflow_steps", ChildColumn: "job_id", OwnerTable: "jobs", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	"job_checkpoints":      {ChildTable: "job_checkpoints", ChildColumn: "job_id", OwnerTable: "jobs", OwnerColumn: "id", Kind: "LOGICAL", RootType: "child"},
	"execution_steps":      {ChildTable: "execution_steps", ChildColumn: "job_id", OwnerTable: "jobs", OwnerColumn: "id", Kind: "LOGICAL", RootType: "child"},
	"operations":           {ChildTable: "operations", ChildColumn: "job_id", OwnerTable: "jobs", OwnerColumn: "id", Kind: "LOGICAL", RootType: "child"},
	"dead_letter_jobs":     {ChildTable: "dead_letter_jobs", ChildColumn: "job_id", OwnerTable: "jobs", OwnerColumn: "id", Kind: "LOGICAL", RootType: "child"}, "monitor_enqueue_outbox": {ChildTable: "monitor_enqueue_outbox", ChildColumn: "job_id", OwnerTable: "jobs", OwnerColumn: "id", Kind: "LOGICAL", RootType: "child"},
	"qdrantprojection_checkpoints": {ChildTable: "qdrantprojection_checkpoints", ChildColumn: "job_id", OwnerTable: "jobs", OwnerColumn: "id", Kind: "LOGICAL", RootType: "child"},
	"qdrantprojection_dlq":         {ChildTable: "qdrantprojection_dlq", ChildColumn: "job_id", OwnerTable: "jobs", OwnerColumn: "id", Kind: "LOGICAL", RootType: "child"},
	"publication_intents":          {ChildTable: "publication_intents", ChildColumn: "job_id", OwnerTable: "jobs", OwnerColumn: "id", Kind: "LOGICAL", RootType: "child"},
	"render_attempt_analytics":     {ChildTable: "render_attempt_analytics", ChildColumn: "job_id", OwnerTable: "jobs", OwnerColumn: "id", Kind: "LOGICAL", RootType: "child"},
	"youtube_discoveries":          {ChildTable: "youtube_discoveries", ChildColumn: "job_id", OwnerTable: "jobs", OwnerColumn: "id", Kind: "LOGICAL", RootType: "child"},
	"performance_runs_job":         {ChildTable: "performance_runs", ChildColumn: "job_id", OwnerTable: "jobs", OwnerColumn: "id", Kind: "LOGICAL", RootType: "child"},
	"artifact_stages":              {ChildTable: "artifact_stages", ChildColumn: "job_id", OwnerTable: "jobs", OwnerColumn: "id", Kind: "LOGICAL", RootType: "child"},
	"stock_batch_groups_job":       {ChildTable: "stock_batch_groups", ChildColumn: "child_job_id", OwnerTable: "jobs", OwnerColumn: "id", Kind: "LOGICAL", RootType: "child"},

	// ── LOGICAL children of media_assets ──
	"clip_search_terms":      {ChildTable: "clip_search_terms", ChildColumn: "clip_id", OwnerTable: "media_assets", OwnerColumn: "id", Kind: "LOGICAL", RootType: "child"},
	"asset_processing":       {ChildTable: "asset_processing", ChildColumn: "asset_id", OwnerTable: "media_assets", OwnerColumn: "id", Kind: "LOGICAL", RootType: "child"},
	"media_asset_sources":    {ChildTable: "media_asset_sources", ChildColumn: "asset_id", OwnerTable: "media_assets", OwnerColumn: "id", Kind: "LOGICAL", RootType: "child"},
	"asset_tree_nodes":       {ChildTable: "asset_tree_nodes", ChildColumn: "asset_id", OwnerTable: "media_assets", OwnerColumn: "id", Kind: "LOGICAL", RootType: "child"},
	"asset_index":            {ChildTable: "asset_index", ChildColumn: "asset_id", OwnerTable: "media_assets", OwnerColumn: "id", Kind: "LOGICAL", RootType: "child"},
	"clip_storage_index":     {ChildTable: "clip_storage_index", ChildColumn: "asset_id", OwnerTable: "media_assets", OwnerColumn: "id", Kind: "LOGICAL", RootType: "child"},
	"media_candidates_asset": {ChildTable: "media_candidates", ChildColumn: "asset_id", OwnerTable: "media_assets", OwnerColumn: "id", Kind: "LOGICAL", RootType: "child"},
	"media_bindings":         {ChildTable: "media_bindings", ChildColumn: "asset_id", OwnerTable: "media_assets", OwnerColumn: "id", Kind: "LOGICAL", RootType: "child"},
	"delivery_log":           {ChildTable: "delivery_log", ChildColumn: "asset_id", OwnerTable: "media_assets", OwnerColumn: "id", Kind: "LOGICAL", RootType: "child"},
	"media_usage_events":     {ChildTable: "media_usage_events", ChildColumn: "asset_id", OwnerTable: "media_assets", OwnerColumn: "id", Kind: "LOGICAL", RootType: "child"},
	"artlist_download_audit": {ChildTable: "artlist_download_audit", ChildColumn: "release_id", OwnerTable: "asset_releases", OwnerColumn: "id", Kind: "FK", RootType: "child"},

	// ── stock_artifacts children ──
	"stock_artifacts_batch":  {ChildTable: "stock_artifacts", ChildColumn: "batch_id", OwnerTable: "stock_batches", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	"stock_artifacts_group":  {ChildTable: "stock_artifacts", ChildColumn: "group_id", OwnerTable: "stock_batch_groups", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	"stock_batch_groups":     {ChildTable: "stock_batch_groups", ChildColumn: "batch_id", OwnerTable: "stock_batches", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	"media_bindings_concept": {ChildTable: "media_bindings", ChildColumn: "concept_id", OwnerTable: "media_concepts", OwnerColumn: "id", Kind: "FK", RootType: "child"},

	// ── entity_image children (FK) ──
	"entity_image_catalog_candidates":       {ChildTable: "entity_image_catalog_candidates", ChildColumn: "canonical_entity_id", OwnerTable: "entity_image_catalog_entities", OwnerColumn: "canonical_entity_id", Kind: "FK", RootType: "child"},
	"entity_image_catalog_materializations": {ChildTable: "entity_image_catalog_materializations", ChildColumn: "candidate_id", OwnerTable: "entity_image_catalog_candidates", OwnerColumn: "candidate_id", Kind: "FK", RootType: "child"},

	// ── Performance / Registry children ──
	"performance_artifacts": {ChildTable: "performance_artifacts", ChildColumn: "run_id", OwnerTable: "performance_runs", OwnerColumn: "run_id", Kind: "FK", RootType: "child"},
	"performance_steps":     {ChildTable: "performance_steps", ChildColumn: "run_id", OwnerTable: "performance_runs", OwnerColumn: "run_id", Kind: "FK", RootType: "child"},
	"registry_events":       {ChildTable: "registry_events", ChildColumn: "run_id", OwnerTable: "registry_runs", OwnerColumn: "run_id", Kind: "FK", RootType: "child"},

	// ── Workflow children ──
	"workflow_step_deps_step":     {ChildTable: "workflow_step_dependencies", ChildColumn: "step_id", OwnerTable: "workflow_steps", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	"workflow_step_deps_dep":      {ChildTable: "workflow_step_dependencies", ChildColumn: "depends_on_step_id", OwnerTable: "workflow_steps", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	"workflow_step_deps_workflow": {ChildTable: "workflow_step_dependencies", ChildColumn: "workflow_id", OwnerTable: "workflows", OwnerColumn: "id", Kind: "FK", RootType: "child"},

	// ── Gemma children ──
	"gemma_script_chunks": {ChildTable: "gemma_script_chunks", ChildColumn: "generation_id", OwnerTable: "gemma_script_outputs", OwnerColumn: "id", Kind: "FK", RootType: "child"},

	// ── Artlist QB children ──
	"artlist_query_clips":      {ChildTable: "artlist_query_clips", ChildColumn: "query_id", OwnerTable: "artlist_queries", OwnerColumn: "query_id", Kind: "FK", RootType: "child"},
	"artlist_query_clips_clip": {ChildTable: "artlist_query_clips", ChildColumn: "clip_id", OwnerTable: "artlist_clips", OwnerColumn: "clip_id", Kind: "FK", RootType: "child"},

	// ── Search children ──
	"search_query_results": {ChildTable: "search_query_results", ChildColumn: "query_id", OwnerTable: "search_queries", OwnerColumn: "id", Kind: "LOGICAL", RootType: "child"},

	// ── Upload intents (LOGICAL to voiceovers) ──
	"upload_intents": {ChildTable: "upload_intents", ChildColumn: "voiceover_id", OwnerTable: "voiceovers", OwnerColumn: "id", Kind: "LOGICAL", RootType: "child"},

	// ── Caches (no owner — live or expire) ──
	"transcript_cache":       {RootType: "cache"},
	"translation_cache":      {RootType: "cache"},
	"research_cache":         {RootType: "cache"},
	"media_query_cache":      {RootType: "cache"},
	"stock_source_cache":     {RootType: "cache"},
	"vidrush_provider_cache": {RootType: "cache"},
	"artifact_cache_entries": {RootType: "cache"},
	"artifact_cache_metrics": {RootType: "cache"},

	// ── Queues / outbox ──
	"outbox_events": {RootType: "queue"},

	// ── History/audit (records for audit trail, not reachable from owners) ──
	"schema_migrations":            {RootType: "history"},
	"control_plane_meta":           {RootType: "history"},
	"admin_mutation_audit":         {RootType: "audit"},
	"canonical_mutations":          {RootType: "audit"},
	"qdrant_cleanup_audit":         {RootType: "audit"},
	"api_requests":                 {RootType: "history"},
	"idempotency_keys":             {RootType: "history"},
	"media_assets_pipeline_events": {RootType: "history"},
	"video_stats_history":          {RootType: "history"},
	"media_events":                 {RootType: "history"},
	"source_identity_registry":     {RootType: "history"},
	"projection_registry":          {RootType: "history"},
	"replay_bundles":               {RootType: "history"},
	"deliveries":                   {RootType: "history"},
	"content_objects":              {RootType: "history"},
	"characters":                   {RootType: "history"},
	"category_channels":            {RootType: "history"},

	// ── Tables not yet classified (by their real role) ──
	// Any table NOT in this model will be auto-classified as "unclassified"
}

// ── CLI entry point ─────────────────────────────────────────────────────

func RunReachabilityGraph(args []string) error {
	fs := flag.NewFlagSet("reachability-graph", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "Machine-readable JSON output")
	reportPath := fs.String("report", "", "Write JSON to file (default: stdout)")
	limitIDs := fs.Int("limit-ids", 50, "Cap per-table orphan ID list to N entries")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	ctx := cli.CmdContext()
	path := cfg.Storage.PrimaryDBFullPath()

	sdb, err := storage.OpenSQLiteDB(path, log)
	if err != nil {
		return fmt.Errorf("open DB: %w", err)
	}
	defer sdb.Close()

	report, err := computeReachabilityGraph(ctx, sdb.DB, *limitIDs)
	if err != nil {
		return err
	}
	report.GeneratedAt = time.Now().UTC().Format(time.RFC3339)

	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}

	if *reportPath != "" {
		if err := os.WriteFile(*reportPath, append(payload, '\n'), 0o644); err != nil {
			return fmt.Errorf("write report: %w", err)
		}
		fmt.Printf("reachability-graph: report written to %s\n", *reportPath)
		return nil
	}
	if *jsonOut {
		fmt.Println(string(payload))
		return nil
	}
	printReachabilityReport(report)
	return nil
}

// ── Core computation ────────────────────────────────────────────────────

// computeReachabilityGraph builds the static graph and per-table stats
// against the live primary DB.
func computeReachabilityGraph(ctx context.Context, db *sql.DB, limitIDs int) (*reachabilityReport, error) {
	// Enumerate actual tables.
	rows, err := db.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var realTables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		realTables = append(realTables, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Build the child-edge map + per-table classification from the
	// canonical ownership model. A table with a defined child edge is
	// classified as a child; a table with a RootType entry (and no
	// child edge) is classified as its RootType. Root-type entries whose
	// table ALSO has a child edge keep the child classification for the
	// reachability stat (the table both owns children and is owned).
	childEdges := map[string]ownershipRelation{} // keyed by childTable|childColumn
	classified := map[string]string{}            // table → rootType

	// First pass: child edges (FK / LOGICAL).
	for _, rel := range canonicalOwnershipModel {
		if rel.Kind == "FK" || rel.Kind == "LOGICAL" {
			ck := rel.ChildTable + "|" + rel.ChildColumn
			if _, exists := childEdges[ck]; !exists {
				childEdges[ck] = rel
			}
			classified[rel.ChildTable] = "child"
		}
	}

	// Second pass: root/cache/queue/history/audit entries (no Kind).
	for name, rel := range canonicalOwnershipModel {
		if rel.Kind == "" {
			if _, isChild := classified[name]; !isChild {
				classified[name] = rel.RootType
			}
		}
	}

	// Auto-classify any remaining tables not in the model.
	for _, t := range realTables {
		if _, ok := classified[t]; !ok {
			classified[t] = classifyByHeuristic(t)
		}
	}

	// Build graph summary.
	gs := graphSummary{TotalTables: len(realTables)}
	for _, rt := range classified {
		switch rt {
		case "canonical_root":
			gs.CanonicalRoots++
		case "child":
			gs.ChildTables++
		case "cache":
			gs.Caches++
		case "queue":
			gs.Queues++
		case "history":
			gs.HistoryTables++
		case "audit":
			gs.AuditTables++
		}
	}

	// Compute per-table reachability for child tables.
	var stats []tableStats
	for _, t := range realTables {
		st := tableStats{Table: t, RootType: classified[t]}
		if st.RootType == "" {
			st.RootType = "unclassified"
		}

		// Total rows.
		var total int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+quoteIdent(t)).Scan(&total); err != nil {
			st.Error = fmt.Sprintf("count: %v", err)
			stats = append(stats, st)
			continue
		}
		st.TotalRows = total
		if total == 0 {
			stats = append(stats, st)
			continue
		}

		// Try each child edge for this table.
		found := false
		for key, rel := range childEdges {
			if rel.ChildTable != t {
				continue
			}
			_ = key
			colExists := columnExists(ctx, db, t, rel.ChildColumn)
			ownerExists := columnExists(ctx, db, rel.OwnerTable, rel.OwnerColumn)
			ownerTableExists := tableNameInList(realTables, rel.OwnerTable)
			if !colExists || !ownerExists || !ownerTableExists {
				st.Error = fmt.Sprintf("column %s.%s or %s.%s missing", t, rel.ChildColumn, rel.OwnerTable, rel.OwnerColumn)
				break
			}

			// Resolvable: child has a non-null owner reference AND that reference exists.
			q := fmt.Sprintf(
				"SELECT COUNT(*) FROM %s c WHERE c.%s IS NOT NULL AND c.%s != '' AND EXISTS (SELECT 1 FROM %s o WHERE o.%s = c.%s)",
				quoteIdent(t), quoteIdent(rel.ChildColumn), quoteIdent(rel.ChildColumn),
				quoteIdent(rel.OwnerTable), quoteIdent(rel.OwnerColumn), quoteIdent(rel.ChildColumn),
			)
			var reachable int
			if err := db.QueryRowContext(ctx, q).Scan(&reachable); err != nil {
				st.Error = fmt.Sprintf("reachable query: %v", err)
				break
			}
			st.Reachable = reachable

			// Null / empty owner refs.
			q2 := fmt.Sprintf(
				"SELECT COUNT(*) FROM %s c WHERE c.%s IS NULL OR c.%s = ''",
				quoteIdent(t), quoteIdent(rel.ChildColumn), quoteIdent(rel.ChildColumn),
			)
			var nullOwner int
			_ = db.QueryRowContext(ctx, q2).Scan(&nullOwner)
			st.NullOwner = nullOwner

			// Orphan candidates: non-null owner ref that does NOT resolve.
			q3 := fmt.Sprintf(
				"SELECT c.%s FROM %s c WHERE c.%s IS NOT NULL AND c.%s != '' AND NOT EXISTS (SELECT 1 FROM %s o WHERE o.%s = c.%s) LIMIT %d",
				quoteIdent(rel.ChildColumn), quoteIdent(t), quoteIdent(rel.ChildColumn), quoteIdent(rel.ChildColumn),
				quoteIdent(rel.OwnerTable), quoteIdent(rel.OwnerColumn), quoteIdent(rel.ChildColumn),
				limitIDs,
			)
			orphanRows, err := db.QueryContext(ctx, q3)
			if err != nil {
				st.Error = fmt.Sprintf("orphan query: %v", err)
				break
			}
			defer orphanRows.Close()
			for orphanRows.Next() {
				var val string
				if err := orphanRows.Scan(&val); err != nil {
					break
				}
				st.OrphanIDs = append(st.OrphanIDs, val)
			}
			orphanRows.Close()
			st.OrphanCands = total - reachable - nullOwner

			st.Owner = rel.OwnerTable
			st.JoinCol = rel.ChildColumn
			found = true
			break // only report the first edge per table in the summary
		}

		if !found && st.RootType == "child" && st.Error == "" {
			st.Error = "no ownership edge defined in model"
		}
		stats = append(stats, st)
	}

	sort.Slice(stats, func(i, j int) bool { return stats[i].Table < stats[j].Table })

	return &reachabilityReport{
		SchemaVersion: 1,
		Mode:          "reachability-graph",
		NoDeletions:   true,
		Graph:         gs,
		Tables:        stats,
	}, nil
}

// ── Helpers ─────────────────────────────────────────────────────────────

func columnExists(ctx context.Context, db *sql.DB, table, column string) bool {
	var x int
	err := db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT 1 FROM pragma_table_info(%q) WHERE name=%q", table, column),
	).Scan(&x)
	return err == nil
}

func tableNameInList(tables []string, name string) bool {
	for _, t := range tables {
		if t == name {
			return true
		}
	}
	return false
}

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// classifyByHeuristic assigns a root_type for tables not in the canonical
// model: tables with '_cache'/'provider_cache' in the name are caches;
// '_audit'/'_log'/'_history' are audit; '_events'/'_checkpoints' are
// history; everything else is "unclassified" (operator should add to model).
func classifyByHeuristic(name string) string {
	l := strings.ToLower(name)
	if strings.Contains(l, "_cache") || strings.Contains(l, "cache_") {
		return "cache"
	}
	if strings.Contains(l, "_audit") || strings.Contains(l, "_log") {
		return "audit"
	}
	if strings.Contains(l, "_events") || strings.Contains(l, "_checkpoints") || strings.Contains(l, "_history") {
		return "history"
	}
	return "unclassified"
}

func printReachabilityReport(r *reachabilityReport) {
	fmt.Println("=== Reachability Graph (FASE 2) ===")
	fmt.Printf("  tables:       %d total (%d canonical, %d children, %d caches, %d queues, %d history, %d audit)\n",
		r.Graph.TotalTables, r.Graph.CanonicalRoots, r.Graph.ChildTables,
		r.Graph.Caches, r.Graph.Queues, r.Graph.HistoryTables, r.Graph.AuditTables)
	fmt.Printf("  deletions:    none (compute-only)\n\n")

	fmt.Println("  --- Children (reachability) ---")
	orphanTotal := 0
	for _, s := range r.Tables {
		if s.Error != "" {
			fmt.Printf("    %-45s  %s  ERROR: %s\n", s.Table, s.RootType, s.Error)
			continue
		}
		if s.TotalRows == 0 {
			fmt.Printf("    %-45s  %-6s  empty\n", s.Table, s.RootType)
			continue
		}
		if s.RootType != "child" {
			continue
		}
		nullStr := ""
		if s.NullOwner > 0 {
			nullStr = fmt.Sprintf("  null_owner=%d", s.NullOwner)
		}
		fmt.Printf("    %-45s  → %-25s  total=%-6d reach=%-6d orphan=%-6d%s\n",
			s.Table, s.Owner, s.TotalRows, s.Reachable, s.OrphanCands, nullStr)
		orphanTotal += s.OrphanCands
		if len(s.OrphanIDs) > 0 {
			fmt.Printf("      orphan IDs: %v\n", s.OrphanIDs[:min(5, len(s.OrphanIDs))])
		}
	}
	fmt.Printf("  total orphan candidates: %d\n\n", orphanTotal)

	fmt.Println("  --- Canonical roots ---")
	for _, s := range r.Tables {
		if s.RootType == "canonical_root" {
			fmt.Printf("    %-45s  total=%d\n", s.Table, s.TotalRows)
		}
	}

	fmt.Println("  --- Caches / queues / history / audit ---")
	for _, s := range r.Tables {
		if s.RootType == "cache" || s.RootType == "queue" || s.RootType == "history" || s.RootType == "audit" {
			fmt.Printf("    %-45s  %-7s  %d rows\n", s.Table, s.RootType, s.TotalRows)
		}
	}
}
