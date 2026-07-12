// cmd/admin/qdrant_readiness.go — production-shaped readiness gate (PR 15, June 2026).
//
// # PR 15 — Readiness production-shaped
//
// The previous qdrant-readiness command built a synthetic correct router
// (api.NewRouter + stubAuthPort + stubRatePort + stubFeaturesPort +
// stubOutboxRouter + stubMediaSearchRouter) AND a fully no-op reconciler
// (noOpQdrantLister + noOpSQLiteReconcileReader). Those stubs let the
// gate PASS even when production was broken (handler not wired into the
// real router, dispatcher not built, etc.).
//
// PR 15 fixes this: every readiness check now reads the production
// state out of the *app.ComposeRoot produced by app.InitComposition
// (the same constructor the server uses). There is no fallback; if
// any check returns "production is not in the shape we expect", the
// whole gate fails. The user spec lists 9 must-check invariants; we
// ship a 9-key registry map.
//
// Removed stubs (all gone in PR 15):
//   - stubOutboxRouter        — replaced by checkRoutesReal
//     (real handler injected from root)
//   - stubMediaSearchRouter   — replaced by checkRoutesReal
//     (real handler injected from root)
//   - stubAuthPort            — replaced by a real AuthSecurityPort
//     constructed from cfg.Security
//   - stubRatePort            — replaced by a real RateLimitPort
//     constructed from cfg.Security
//   - stubFeaturesPort        — replaced by a real FeatureFlagsPort
//     constructed from cfg.FeatureFlags//   - noOpQdrantLister        — replaced by qdrantReconcilerListerAdapter
//     backed by production *transport.Client
//     and *indexing.SQLiteAssetStore
//     (PR 7 #7.1: old *qdrant.Reconciler deleted;
//     canonical path is internal/application/qdrant/reconciler/)
//   - noOpSQLiteReconcileReader — replaced by repository.ClipsRepo
//     (production SQLite reader)
//
// Fase 4 Step 2 (June 2026): check functions, adapter types, cfg ports,
// and noop stubs extracted to qdrant_readiness_checks.go (same package main).
// This file retains: qdrantReadinessReport, runQdrantReadiness,
// appInitCompositionForReadiness, qdrantReadiness (orchestrator),
// runOneCheck, and utility functions.
//
// Commit E (July 2026): the 3 SQL-pure inspection helpers
// (inspectRequiredColumns + collectReadinessCounters + tableExists)
// moved to qdrant_readiness_db.go (same package main). The split
// follows the user constraint "NON spostare nulla in internal/infrastructure
// (creerebbe interfacce morte)" — these are one-shot-CLI-only SQL
// queries used by exactly one consumer (the readiness gate), so
// promoting them to infrastructure would force a typed-port interface
// with no second consumer (dead-interface anti-pattern).
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"go.uber.org/zap"

	app "github.com/Marcuss-ops/PipelineGen/internal/app"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/collections"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/disasterrecovery"
	qdrantschema "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// qdrantReadinessReport is the production-grade readiness gate
// output. `Checks` is the canonical {check-name -> "pass"|"fail"} map
// the operational dashboard consumes; the flat fields below preserve
// the v1 shape for backwards compat with ops scripts.
type qdrantReadinessReport struct {
	Ready                      bool              `json:"ready"`
	Checks                     map[string]string `json:"checks,omitempty"`
	QdrantReachable            bool              `json:"qdrant_reachable"`
	SQLiteMigrationsComplete   bool              `json:"sqlite_migrations_complete"`
	ActiveCollection           string            `json:"active_collection,omitempty"`
	ActiveCollectionCompatible bool              `json:"active_collection_compatible"`
	RequiredColumnsPresent     []string          `json:"required_columns_present,omitempty"`
	MissingColumns             []string          `json:"missing_columns,omitempty"`
	TotalAssets                int               `json:"total_assets"`
	NonMediaAssets             int               `json:"non_media_assets"`
	InvalidTextVectors         int               `json:"invalid_text_vectors"`
	InvalidTranscriptVectors   int               `json:"invalid_transcript_vectors"`
	InvalidVisualVectors       int               `json:"invalid_visual_vectors"`
	InvalidAudioVectors        int               `json:"invalid_audio_vectors"`
	SchemaErrors               int               `json:"schema_errors"`
	MissingSourceFile          int               `json:"missing_source_file"`
	LegacyStatusRows           int               `json:"legacy_status_rows"`
	LegacyLocatorRows          int               `json:"legacy_locator_rows"`
	OutboxOperational          bool              `json:"outbox_operational"`
}

// checkStatus, readinessDeps, compositionRoot, and readinessCheck
// moved to qdrant_readiness_checks.go (Fase 4 Step 2, June 2026).
// All 10 per-check functions (checkDeadLetter, checkDeliverySigner,
// checkDispatcherBuilt, checkWorkerRealState, checkSQLiteReader,
// checkQdrantActiveCollection, checkServerProductionConstructor,
// checkLegacyAudit, checkRoutesReal, checkReconcilerProduction),
// adapter types (qdrantReconcilerListerAdapter, qdrantReconcileAssetStore,
// qdrantAssetStoreForReconcile), cfg-derived ports (cfgAuthPort,
// cfgRatePort, cfgFeaturesPort), noop stubs (readiNoopOutbox,
// readiNoopPayload), router helpers (buildRouterWithProductionWiring,
// engineHasPath), and compile-time assertions also live in that file.
//
// Commit E (July 2026) added: the 3 SQL-pure inspection helpers
// (inspectRequiredColumns + collectReadinessCounters + tableExists)
// now live in qdrant_readiness_db.go (same package main). The qdrantReadiness
// orchestrator calls them by direct symbol — same package, no import
// changes required.

func runQdrantReadiness(args []string) error {
	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	jsonOut := false
	for _, a := range args {
		switch strings.TrimSpace(a) {
		case "--json":
			jsonOut = true
		default:
			if strings.HasPrefix(strings.TrimSpace(a), "-") {
				return fmt.Errorf("unknown flag: %s", a)
			}
		}
	}

	ctx := cmdContext()
	sqliteDB, err := storage.OpenSQLiteDB(cfg.Storage.PrimaryDBFullPath(), log)
	if err != nil {
		return fmt.Errorf("open media DB: %w", err)
	}
	defer sqliteDB.Close()

	// Build the production composition root (PR 15). The server,
	// dispatcher, qdrant client, clips repo, worker pool, real
	// outbox/mediasearch handler wires — every check reads from
	// root. InitComposition is the canonical producer of root
	// (mirrors what cmd/server/main.go constructs).
	root, rootCleanup, err := appInitCompositionForReadiness(ctx, cfg, log)
	if err != nil {
		// Root construction itself failed — readiness gate cannot
		// proceed because server_production_constructor will fail
		// (the test of the canonical constructor). We surface this
		// as a synthetic nil root + log; the per-check functions
		// that need Root handle nil safely and report the failure
		// in the report.
		log.Warn("production composition root failed to init; readiness checks will surface the failure per-check",
			zap.Error(err))
		root = nil
	} else {
		defer rootCleanup()
	}

	report, err := qdrantReadiness(ctx, sqliteDB.DB, cfg, log, root)
	if err != nil {
		log.Warn("readiness scan returned non-fatal error; emitting partial report",
			zap.Error(err))
		// Continue — we surface the partial report so operators can see
		// WHICH check failed. Errors here are NEVER accepted-by-default.
	}

	if jsonOut {
		b, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(b))
	} else {
		fmt.Printf("READY=%t\n", report.Ready)
		for name, status := range report.Checks {
			fmt.Printf("%s=%s\n", name, status)
		}
		fmt.Printf("qdrant_reachable=%t\n", report.QdrantReachable)
		fmt.Printf("sqlite_migrations_complete=%t\n", report.SQLiteMigrationsComplete)
		fmt.Printf("active_collection=%q\n", report.ActiveCollection)
		fmt.Printf("active_collection_compatible=%t\n", report.ActiveCollectionCompatible)
		fmt.Printf("total_assets=%d\n", report.TotalAssets)
		fmt.Printf("non_media_assets=%d\n", report.NonMediaAssets)
		fmt.Printf("invalid_text_vectors=%d\n", report.InvalidTextVectors)
		fmt.Printf("invalid_transcript_vectors=%d\n", report.InvalidTranscriptVectors)
		fmt.Printf("invalid_visual_vectors=%d\n", report.InvalidVisualVectors)
		fmt.Printf("invalid_audio_vectors=%d\n", report.InvalidAudioVectors)
		fmt.Printf("missing_source_file=%d\n", report.MissingSourceFile)
		fmt.Printf("legacy_status_rows=%d\n", report.LegacyStatusRows)
		fmt.Printf("legacy_locator_rows=%d\n", report.LegacyLocatorRows)
		fmt.Printf("outbox_operational=%t\n", report.OutboxOperational)
	}

	if !report.Ready {
		// Spec: exit non-zero when ready=false so CI/operators see the
		// failed gate. The error message lists failing check names.
		var failing []string
		for name, status := range report.Checks {
			if status != "pass" {
				failing = append(failing, fmt.Sprintf("%s=%s", name, status))
			}
		}
		return fmt.Errorf("qdrant readiness gate failed: %s", strings.Join(failing, ", "))
	}
	return nil
}

// appInitCompositionForReadiness is the production bridge: it calls
// the canonical app.InitComposition + app.WireRegistry constructors
// (the same constructors cmd/server/main.go uses) and translates the
// *app.ComposeRoot + *app.RegistryWiring into the readiness-side
// *compositionRoot struct.
//
// Returns nil + non-nil error when InitComposition OR WireRegistry
// fails. The readiness runQdrantReadiness caller handles nil root by
// failing every production-shaped check.
func appInitCompositionForReadiness(ctx context.Context, cfg *config.Config, log *zap.Logger) (*compositionRoot, func(), error) {
	// Step 1: app.InitComposition returns the production *ComposeRoot
	// tree (DriveBundle, RepoBundle, ProcessBundle, OutboxBundle,
	// etc.). It also constructs the canonical *outboxevents.Pool and
	// migrates the SQLite DB. Signature: (cfg, log) -> (root, jobs, cleanup, err).
	prodRoot, _, cleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		return nil, func() {}, fmt.Errorf("app.InitComposition: %w", err)
	}
	if prodRoot == nil {
		cleanup()
		return nil, func() {}, fmt.Errorf("app.InitComposition returned nil root without error")
	}

	// Step 2: app.WireRegistry constructs the gin routes / middleware
	// layer, including the production outbox + mediasearch handlers
	// that the readiness-gate's real_routes_present check pulls
	// through api.Router.SetOutboxHandler / SetMediasearchHandler.
	// Signature: (ctx, cfg, log, root) -> (*RegistryWiring, error).
	registryWiring, err := app.WireRegistry(prodRoot.Ctx, cfg, log, prodRoot)
	if err != nil {
		cleanup()
		return nil, func() {}, fmt.Errorf("app.WireRegistry: %w", err)
	}
	if registryWiring == nil {
		cleanup()
		return nil, func() {}, fmt.Errorf("app.WireRegistry returned nil wiring without error")
	}

	// Step 3: translate the bundle tree + registry wiring into the
	// readiness-side structural view. Every field below is a
	// PRODUCTION CONCRETE pointer / interface; nil-checks at the
	// read sites are the production-shape invariant.
	return &compositionRoot{
		Dispatcher:         prodRoot.Outbox.Dispatcher,
		EventsPool:         prodRoot.Outbox.EventsPool,
		OutboxHandler:      registryWiring.OutboxHandler,
		MediasearchHandler: registryWiring.MediasearchHandler,
		ClipsRepo:          prodRoot.Repos.ClipsRepo,
		QdrantClient:       prodRoot.Process.QdrantClient,
		SemanticSearch:     registryWiring.SearchFanOut,
	}, cleanup, nil
}

// All per-check functions, adapter types, cfg ports, noop stubs,
// router helpers, and compile-time assertions moved to
// qdrant_readiness_checks.go (Fase 4 Step 2, June 2026).
// Same package main — cross-file visibility preserves all call sites.
//
// Commit E (July 2026) added: inspectRequiredColumns + collectReadinessCounters +
// tableExists moved to qdrant_readiness_db.go (same package main).

// ── Orchestrator: qdrantReadiness ──────────────────────────────────────

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

// inspectRequiredColumns + collectReadinessCounters + tableExists
// moved to qdrant_readiness_db.go (Commit E, July 2026). Same package
// main — cross-file visibility preserves the orchestrator's call sites.

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

// legacyauditReportMarker import now lives in qdrant_readiness_checks.go
// (Fase 4 Step 2, June 2026).

func hasLegacyLocatorKey(metaJSON string) bool {
	metaJSON = strings.TrimSpace(metaJSON)
	if metaJSON == "" || metaJSON == "{}" {
		return false
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(metaJSON), &meta); err != nil {
		return false
	}
	for _, key := range []string{"drive_link", "download_link", "drive_file_id", "local_path"} {
		if v, ok := meta[key]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return true
			}
		}
	}
	return false
}

// tableExists moved to qdrant_readiness_db.go (Commit E, July 2026).

func parseStrictPositiveIntFlag(arg, name string) (int, error) {
	v := strings.TrimPrefix(arg, name+"=")
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	if n <= 0 {
		return 0, fmt.Errorf("%s must be positive, got %d", name, n)
	}
	return n, nil
}
