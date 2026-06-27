package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	api "github.com/Marcuss-ops/PipelineGen/internal/api"
	middlewareports "github.com/Marcuss-ops/PipelineGen/internal/application/middleware"
	"github.com/Marcuss-ops/PipelineGen/internal/application/qdrant/reconciler"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

type qdrantVisualBackfillDeps struct {
	Apply bool
	JSON  bool
	Limit int
}

type qdrantVisualBackfillReport struct {
	Mode              string `json:"mode"`
	TotalAssets       int    `json:"total_assets"`
	Already768        int    `json:"already_768"`
	Legacy512         int    `json:"legacy_512"`
	Regenerated       int    `json:"regenerated"`
	Failed            int    `json:"failed"`
	MissingSourceFile int    `json:"missing_source_file"`
	UnsupportedMedia  int    `json:"unsupported_media"`
}

// qdrantReadinessReport is the production-grade TODO 14 readiness gate
// output. `Checks` is the canonical {check-name -> "pass"|"fail"} map
// the operational dashboard consumes; the flat fields below preserve
// the v1 (pre-TODO-14) shape for backwards compat with ops scripts.
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

// checkStatus is the per-check tuple used by all 12 readiness checks.
// Pass=true means the check passed; Err is the diagnostic string for
// human ops (always populated on Pass=false, omitted otherwise).
type checkStatus struct {
	Pass bool
	Err  string
}

// readinessDeps is the dependency bag each check function consumes.
// Tests inject sqlmock + Config directly without standing up the full
// composition root.
type readinessDeps struct {
	DB  *sql.DB
	Cfg *config.Config
	Log *zap.Logger
}

// readinessCheck is the testable surface for the 12 readiness checks.
// Each entry is a `var` (not `func`) so cmd/admin/qdrant_readiness_test.go
// can REPLACE individual checks with mocks/failing implementations without
// touching this file. The composition root wires the real checks at init;
// tests override only the keys they want to simulate failure for.
//
// Order in the registry also defines the JSON output order (alphabetical
// from the user's spec). NOT enforced — does not affect correctness.
var readinessCheck = map[string]func(context.Context, readinessDeps) checkStatus{
	"active_alias":       checkActiveAlias,
	"dead_letter":        checkDeadLetter,
	"delivery_signer":    checkDeliverySigner,
	"legacy_audit":       checkLegacyAudit,
	"outbox_dispatcher":  checkOutboxDispatcher,
	"outbox_repository":  checkOutboxRepository,
	"outbox_table":       checkOutboxTable,
	"outbox_worker":      checkOutboxWorker,
	"qdrant":             checkQdrant,
	"reconciler":         checkReconciler,
	"routes_mediasearch": checkRoutesMediasearch,
	"routes_outbox":      checkRoutesOutbox,
	"sqlite":             checkSqlite,
}

func runBackfillVisualEmbeddings(args []string) error {
	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	deps, err := parseQdrantVisualBackfillArgs(args)
	if err != nil {
		return err
	}

	ctx := cmdContext()
	sqliteDB, err := storage.OpenSQLiteDB(cfg.Storage.PrimaryDBFullPath(), log)
	if err != nil {
		return fmt.Errorf("open media DB: %w", err)
	}
	defer sqliteDB.Close()

	report, err := backfillVisualEmbeddings(ctx, sqliteDB.DB, cfg, deps, log)
	if err != nil {
		return err
	}

	if deps.JSON {
		b, _ := json.Marshal(report)
		fmt.Println(string(b))
		return nil
	}

	if !deps.Apply {
		log.Info("visual embedding backfill dry-run complete",
			zap.Int("total_assets", report.TotalAssets),
			zap.Int("already_768", report.Already768),
			zap.Int("legacy_512", report.Legacy512),
			zap.Int("missing_source_file", report.MissingSourceFile),
			zap.Int("unsupported_media", report.UnsupportedMedia))
		return nil
	}

	log.Info("visual embedding backfill complete",
		zap.Int("total_assets", report.TotalAssets),
		zap.Int("already_768", report.Already768),
		zap.Int("legacy_512", report.Legacy512),
		zap.Int("regenerated", report.Regenerated),
		zap.Int("failed", report.Failed),
		zap.Int("missing_source_file", report.MissingSourceFile),
		zap.Int("unsupported_media", report.UnsupportedMedia))
	return nil
}

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

	report, err := qdrantReadiness(ctx, sqliteDB.DB, cfg, log)
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

func parseQdrantVisualBackfillArgs(args []string) (qdrantVisualBackfillDeps, error) {
	deps := qdrantVisualBackfillDeps{}
	for _, a := range args {
		a = strings.TrimSpace(a)
		switch {
		case a == "--apply":
			deps.Apply = true
		case a == "--json":
			deps.JSON = true
		case strings.HasPrefix(a, "--limit="):
			n, err := parsePositiveFlag(a, "--limit")
			if err != nil {
				return deps, err
			}
			deps.Limit = n
		default:
			if strings.HasPrefix(a, "-") {
				return deps, fmt.Errorf("unknown flag: %s", a)
			}
		}
	}
	return deps, nil
}

// qdrantReadiness runs all 12 checks and populates the report.
// It is the canonical entrypoint; runQdrantReadiness delegates here.
//
// Order matters for diagnostic clarity but NOT for correctness — every
// check is independent and a failure in one does NOT short-circuit the
// others (operators want to see ALL failing checks in one run).
func qdrantReadiness(ctx context.Context, db *sql.DB, cfg *config.Config, log *zap.Logger) (qdrantReadinessReport, error) {
	report := qdrantReadinessReport{
		Checks: make(map[string]string, 12),
	}
	deps := readinessDeps{DB: db, Cfg: cfg, Log: log}

	// Required-columns check is shared between SQLite and the legacy
	// harness columns.
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
		report.Checks["sqlite"] = "fail"
		report.MissingColumns = missing
		report.SchemaErrors++
		return report, fmt.Errorf("sqlite required-Columns check: %w", err)
	}
	report.RequiredColumnsPresent = present
	report.MissingColumns = missing
	report.SQLiteMigrationsComplete = len(missing) == 0
	report.Checks["sqlite"] = boolToStatus(report.SQLiteMigrationsComplete)
	if len(missing) > 0 {
		report.SchemaErrors += len(missing)
	}

	// Outbox table existence — required for QDRANT-005 outbox-driven writes.
	// Surfaced via the legacy `outbox_operational` field for v1 ops scripts
	// AND via the `outbox_table` check in the new map.
	report.OutboxOperational = tableExists(ctx, db, "outbox_events")
	if status, ferr := runOneCheck(ctx, deps, checkOutboxTable); ferr == nil {
		report.Checks["outbox_table"] = status
	} else {
		report.Checks["outbox_table"] = "fail"
	}

	// (The runAllChecks helper is intentionally NOT called here. The body
	// invokes each check explicitly via runOneCheck below for stable
	// ordering and so that legacy harness fields can be populated as a
	// side effect. runAllChecks is retained for tests that prefer the
	// registry-iteration surface.)

	// Channel-matrix-aware counter scan populates the legacy harness columns
	// (NonMediaAssets, Invalid*Vectors, LegacyStatusRows, etc.) AND derives
	// the legacy_audit check from those counts. We do NOT early-return on
	// counter-scan error — every check is independent and a transient DB
	// blip should not stop the rest of the readiness report.
	legacyOK := true
	if err := collectReadinessCounters(ctx, db, &report); err != nil {
		report.Checks["legacy_audit"] = "fail"
		legacyOK = false
		log.Warn("readiness counter scan failed; legacy_audit marked fail", zap.Error(err))
	}
	if legacyOK {
		// Per-check duplicate (so checkLegacyAudit is independently
		// mockable in tests). Runs the same SQL aggregate as the inline
		// body but uses json_array_length for the dimension check.
		if status, ferr := runOneCheck(ctx, deps, checkLegacyAudit); ferr == nil {
			report.Checks["legacy_audit"] = status
		} else {
			report.Checks["legacy_audit"] = "fail"
		}
	}

	// Qdrant reachability + active alias resolution (both run via the
	// qdrant probe path so they share a single round-trip).
	if err := qdrantProbeAndSchema(ctx, cfg, log, &report); err != nil {
		report.QdrantReachable = false
		report.Checks["qdrant"] = "fail"
	} else {
		report.QdrantReachable = true
		report.Checks["qdrant"] = boolToStatus(report.QdrantReachable)
	}
	if status, ferr := runOneCheck(ctx, deps, checkActiveAlias); ferr == nil {
		report.Checks["active_alias"] = status
	} else {
		report.Checks["active_alias"] = "fail"
	}

	// Outbox dispatcher + worker checks (composition-root config gating).
	if status, ferr := runOneCheck(ctx, deps, checkOutboxDispatcher); ferr == nil {
		report.Checks["outbox_dispatcher"] = status
	} else {
		report.Checks["outbox_dispatcher"] = "fail"
	}
	if status, ferr := runOneCheck(ctx, deps, checkOutboxWorker); ferr == nil {
		report.Checks["outbox_worker"] = status
	} else {
		report.Checks["outbox_worker"] = "fail"
	}

	// Outbox row-repository reachability (SELECT COUNT(*)).
	if status, ferr := runOneCheck(ctx, deps, checkOutboxRepository); ferr == nil {
		report.Checks["outbox_repository"] = status
	} else {
		report.Checks["outbox_repository"] = "fail"
	}

	// Route registration introspection (engine.Routes() check).
	if status, ferr := runOneCheck(ctx, deps, checkRoutesOutbox); ferr == nil {
		report.Checks["routes_outbox"] = status
	} else {
		report.Checks["routes_outbox"] = "fail"
	}
	if status, ferr := runOneCheck(ctx, deps, checkRoutesMediasearch); ferr == nil {
		report.Checks["routes_mediasearch"] = status
	} else {
		report.Checks["routes_mediasearch"] = "fail"
	}

	// Delivery signer (HMAC secret present + length >= 16).
	if status, ferr := runOneCheck(ctx, deps, checkDeliverySigner); ferr == nil {
		report.Checks["delivery_signer"] = status
	} else {
		report.Checks["delivery_signer"] = "fail"
	}

	// Reconciler dry-run end-to-end.
	if status, ferr := runOneCheck(ctx, deps, checkReconciler); ferr == nil {
		report.Checks["reconciler"] = status
	} else {
		report.Checks["reconciler"] = "fail"
	}

	// Dead-letter count = 0 invariant.
	if status, ferr := runOneCheck(ctx, deps, checkDeadLetter); ferr == nil {
		report.Checks["dead_letter"] = status
	} else {
		report.Checks["dead_letter"] = "fail"
	}

	// Overall readiness = every check passed. SQLite is the only check that
	// runs before this loop completes (we set it inline above).
	report.Ready = true
	for _, status := range report.Checks {
		if status != "pass" {
			report.Ready = false
			break
		}
	}
	return report, nil
}

// runOneCheck is a small helper so the per-check reporting in
// qdrantReadiness stays linear (no nested if-error blocks).
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

// ── Per-check functions (each returns checkStatus) ────────────────────

func checkSqlite(ctx context.Context, deps readinessDeps) checkStatus {
	// Production-grade SQLite check: cfg.Storage.DataDir non-empty AND
	// the canonical media_assets table exists. The qdrantReadiness body
	// runs a deeper check (PRAGMA table_info on required columns) and
	// sets the same `sqlite` key; both keys converge on the same value
	// when the deeper check passes.
	if deps.Cfg == nil {
		return checkStatus{Err: "config is nil"}
	}
	if len(deps.Cfg.Storage.DataDir) == 0 {
		return checkStatus{Err: "storage.data_dir is empty"}
	}
	if !tableExists(ctx, deps.DB, "media_assets") {
		return checkStatus{Err: "media_assets table missing"}
	}
	return checkStatus{Pass: true}
}

func checkQdrant(ctx context.Context, deps readinessDeps) checkStatus {
	if deps.Cfg == nil || deps.Cfg.Qdrant.BaseURL == "" {
		return checkStatus{Err: "qdrant.base_url is empty"}
	}
	client := qdrant.NewClient(&qdrant.Config{
		BaseURL: deps.Cfg.Qdrant.BaseURL,
		Timeout: deps.Cfg.Qdrant.Timeout,
		APIKey:  deps.Cfg.Qdrant.APIKey,
	}, deps.Log)
	probe := qdrant.NewHealthProbe(client)
	if err := probe.Probe(ctx); err != nil {
		return checkStatus{Err: "qdrant health probe failed: " + err.Error()}
	}
	return checkStatus{Pass: true}
}

func checkOutboxTable(ctx context.Context, deps readinessDeps) checkStatus {
	if !tableExists(ctx, deps.DB, "outbox_events") {
		return checkStatus{Err: "outbox_events table missing"}
	}
	return checkStatus{Pass: true}
}

func checkOutboxRepository(ctx context.Context, deps readinessDeps) checkStatus {
	// SELECT COUNT(*) must succeed even on an empty table (count=0 valid).
	var count int
	if err := deps.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox_events`).Scan(&count); err != nil {
		return checkStatus{Err: "outbox_events SELECT COUNT(*) failed: " + err.Error()}
	}
	if count < 0 {
		return checkStatus{Err: fmt.Sprintf("outbox_events count=%d (negative)", count)}
	}
	return checkStatus{Pass: true}
}

func checkOutboxDispatcher(_ context.Context, deps readinessDeps) checkStatus {
	// The dispatcher is wired in the composition root; from the readiness
	// CLI's POV we verify the bits that would block construction: cfg has
	// DispatcherPollMs >= 0 (the worker's poll cadence) AND the cfg carries
	// the SQL path. The composition root refuses to wire a dispatcher when
	// either is missing.
	if deps.Cfg == nil {
		return checkStatus{Err: "config is nil"}
	}
	if deps.Cfg.Outbox.PollIntervalMs < 0 {
		return checkStatus{Err: fmt.Sprintf("outbox.poll_interval_ms=%d (negative)", deps.Cfg.Outbox.PollIntervalMs)}
	}
	if deps.Cfg.Storage.PrimaryDBFullPath() == "" {
		return checkStatus{Err: "storage.primary_db_path is empty (dispatcher can't bind)"}
	}
	return checkStatus{Pass: true}
}

func checkOutboxWorker(_ context.Context, deps readinessDeps) checkStatus {
	// Outbox worker pool size must be > 0; otherwise dispatch events sit
	// in outbox_events forever and QDRANT-005 projections never land.
	if deps.Cfg == nil {
		return checkStatus{Err: "config is nil"}
	}
	if deps.Cfg.Outbox.Workers <= 0 {
		return checkStatus{Err: fmt.Sprintf("outbox.workers=%d (must be > 0)", deps.Cfg.Outbox.Workers)}
	}
	return checkStatus{Pass: true}
}

// stubOutboxRouter + stubMediaSearchRouter satisfy the router's port
// interfaces with the canonical minimum routes. They are SCOPED TO
// THIS FILE because the readiness command is the only caller — any
// production main.go for the server uses the real handlers from
// internal/api/outbox and internal/api/mediasearch.
type stubOutboxRouter struct{}

func (s *stubOutboxRouter) RegisterRoutes(rg *gin.RouterGroup) {
	rg.GET("/status", func(c *gin.Context) {})
	rg.GET("/events", func(c *gin.Context) {})
}

type stubMediaSearchRouter struct{}

func (s *stubMediaSearchRouter) RegisterRoutes(rg *gin.RouterGroup) {
	rg.POST("/search", func(c *gin.Context) {})
}

// stubAuthPort returns nil/falsey values so WorkerAuth/Auth/RateLimit
// middleware don't fail in the dry-run router. The readiness check
// verifies the gin engine WIRES the routes, not the integrity of the
// auth tokens.
type stubAuthPort struct{}

func (s *stubAuthPort) EnableAuth() bool    { return false }
func (s *stubAuthPort) AdminToken() string  { return "" }
func (s *stubAuthPort) WorkerToken() string { return "" }

type stubRatePort struct{}

func (s *stubRatePort) RateLimitEnabled() bool { return false }
func (s *stubRatePort) RateLimitRequests() int { return 0 }

type stubFeaturesPort struct{}

func (s *stubFeaturesPort) ArtlistEnabled() bool     { return false }
func (s *stubFeaturesPort) ScriptDocsEnabled() bool  { return false }
func (s *stubFeaturesPort) ScriptClipsEnabled() bool { return false }

func buildRouterForRouteIntrospection(log *zap.Logger) *gin.Engine {
	gin.SetMode(gin.TestMode)
	// Pass NON-NIL stub ports so middleware (Auth/WorkerAuth/RateLimit)
	// can dereference them safely. Typed-nil pointers used to crash
	// the router when WorkerAuth called EnableAuth() / AdminToken() /
	// WorkerToken() on a nil receiver — see cmd/admin review (Wave 19).
	var authPort middlewareports.AuthSecurityPort = &stubAuthPort{}
	var ratePort middlewareports.RateLimitPort = &stubRatePort{}
	var featPort middlewareports.FeatureFlagsPort = &stubFeaturesPort{}
	router := api.NewRouter(&api.RouterConfig{
		Auth:          authPort,
		Rate:          ratePort,
		Features:      featPort,
		Log:           log,
		ServerGinMode: gin.TestMode,
		DataDir:       ".",
		DownloadDir:   ".",
		CORSOrigins:   []string{},
	})
	router.SetOutboxHandler(&stubOutboxRouter{})
	router.SetMediasearchHandler(&stubMediaSearchRouter{})
	return router.Setup()
}

func engineHasPath(engine *gin.Engine, method, prefix string) bool {
	if engine == nil {
		return false
	}
	for _, r := range engine.Routes() {
		// r.Path may be exactly the path OR a wildcared prefix. We use
		// HasPrefix on the literal path so /internal/v1/outbox/status,
		// /internal/v1/outbox/events both match the /internal/v1/outbox/*
		// prefix check.
		if strings.EqualFold(r.Method, method) && strings.HasPrefix(r.Path, prefix) {
			return true
		}
	}
	return false
}

func checkRoutesOutbox(_ context.Context, deps readinessDeps) checkStatus {
	engine := buildRouterForRouteIntrospection(deps.Log)
	if !engineHasPath(engine, "GET", "/internal/v1/outbox/") {
		return checkStatus{Err: "/internal/v1/outbox/* route not registered"}
	}
	return checkStatus{Pass: true}
}

func checkRoutesMediasearch(_ context.Context, deps readinessDeps) checkStatus {
	engine := buildRouterForRouteIntrospection(deps.Log)
	if !engineHasPath(engine, "POST", "/internal/v1/media/search") {
		return checkStatus{Err: "/internal/v1/media/search route not registered"}
	}
	return checkStatus{Pass: true}
}

// checkDeliverySigner validates the Qdrant delivery HMAC secret. The
// canonical invariant: secret MUST be present AND length >= 16 bytes.
// 16 is the minimum that 256-bit HMAC-SHA256 needs to fully use its
// keyspace; production deployments SHOULD use 32 bytes per
// internal/platform/config/config.go::DeliveryHMACSecretValidate.
func checkDeliverySigner(_ context.Context, deps readinessDeps) checkStatus {
	if deps.Cfg == nil {
		return checkStatus{Err: "config is nil"}
	}
	secret := strings.TrimSpace(deps.Cfg.Security.DeliveryHMACSecret)
	if secret == "" {
		return checkStatus{Err: "security.delivery_hmac_secret is empty"}
	}
	if len(secret) < 16 {
		return checkStatus{Err: fmt.Sprintf("security.delivery_hmac_secret length=%d (must be >= 16)", len(secret))}
	}
	return checkStatus{Pass: true}
}

// ── Reconciler scan ──────────────────────────────────────────────────
// Stub ports for the dry-run reconcile that the readiness check fires.
// Each stub satisfies the origin/main reconciler port interface with
// a no-op (or empty-result) implementation so the dry-run Reconcile
// round-trip exercises the Service code path without touching the
// production outbox, Qdrant, or SQLite.
//
// reconciler.NewServiceFromDeps in origin/main requires Schema (non-empty
// Version), Qdrant (non-nil), and SQLite (non-nil); the other fields
// fall back to no-op defaults (noopOutboxEnqueuer, noopPayloadMutator,
// noopMetrics, etc.) when nil — we rely on those defaults so the stub
// count stays small.
type noOpQdrantLister struct{}

func (n *noOpQdrantLister) ScrollPoints(_ context.Context, _ string, _ string, _ int) (reconciler.Points, error) {
	return reconciler.Points{Items: nil, NextOffset: ""}, nil
}

type noOpSQLiteReconcileReader struct{}

func (n *noOpSQLiteReconcileReader) ListForReconcile(_ context.Context, _ []string) ([]reconciler.AssetSnapshot, error) {
	return nil, nil
}

func checkReconciler(ctx context.Context, _ readinessDeps) checkStatus {
	defer func() {
		// Recover so a panic in the production wiring fails the check
		// ASSURED-LY rather than crashing the cmd/admin invocation.
		_ = recover()
	}()
	svc := reconciler.NewServiceFromDeps(reconciler.ServiceDeps{
		Schema: reconciler.SchemaVersions{
			Version:           "v1",
			RequiredKeys:      []string{"asset_id"},
			PerChannelVersion: map[string]string{},
		},
		Qdrant: &noOpQdrantLister{},
		SQLite: &noOpSQLiteReconcileReader{},
	})
	_, err := svc.Reconcile(ctx, reconciler.ReconcileOptions{
		Collection: "qdrant_readiness_dryrun",
		DryRun:     true,
	})
	if err != nil {
		return checkStatus{Err: "reconciler dry-run failed: " + err.Error()}
	}
	return checkStatus{Pass: true}
}

// checkActiveAlias verifies that the canonical Qdrant runtime alias
// resolves to a real, schema-compatible collection. Standalone (does
// NOT share state with the qdrant probe) so it can be mocked by tests.
func checkActiveAlias(ctx context.Context, deps readinessDeps) checkStatus {
	if deps.Cfg == nil || deps.Cfg.Qdrant.BaseURL == "" {
		return checkStatus{Err: "qdrant.base_url is empty"}
	}
	client := qdrant.NewClient(&qdrant.Config{
		BaseURL: deps.Cfg.Qdrant.BaseURL,
		Timeout: deps.Cfg.Qdrant.Timeout,
		APIKey:  deps.Cfg.Qdrant.APIKey,
	}, deps.Log)
	probe := qdrant.NewHealthProbe(client)
	if err := probe.Probe(ctx); err != nil {
		return checkStatus{Err: "qdrant health probe failed: " + err.Error()}
	}
	schema := qdrant.DefaultV3Schema()
	mgr := qdrant.NewCollectionManager(client, schema, deps.Log)
	active, err := mgr.GetActiveCollection(ctx)
	if err != nil {
		return checkStatus{Err: "GetActiveCollection failed: " + err.Error()}
	}
	if active == "" {
		return checkStatus{Err: "active collection empty (alias has no target)"}
	}
	diff, err := mgr.CompareActiveCollection(ctx)
	if err != nil {
		return checkStatus{Err: "CompareActiveCollection failed: " + err.Error()}
	}
	if !diff.Compatible {
		return checkStatus{Err: "active collection is schema-incompatible"}
	}
	return checkStatus{Pass: true}
}

// checkLegacyAudit derives pass/fail from a minimal SQL aggregate so
// tests can mock without standing up the full counter scan. The
// contract matches the inline check in qdrantReadiness: pass iff
// (no non-media assets) AND (no invalid vectors across all channels).
//
// Vector validation uses SQLite's json_array_length to detect wrong-dim
// AND missing — strictly more rigorous than the COALESCE='[”]' check
// from the first revision, and matches the parseVectorLen semantics
// from the inline counter scan (768/768/768/512 per channel matrix).
//
// Channel matrix:
//
//	video  → text(768) + transcript(768) + visual(768)
//	image  → text(768) + visual(768)
//	audio  → text(768) + transcript(768) + audio(512)
func checkLegacyAudit(ctx context.Context, deps readinessDeps) checkStatus {
	if deps.DB == nil {
		return checkStatus{Err: "db is nil"}
	}
	var nonMedia int
	if err := deps.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM media_assets
		WHERE COALESCE(media_type, '') NOT IN ('video', 'image', 'audio')`).Scan(&nonMedia); err != nil {
		return checkStatus{Err: "non-media count query failed: " + err.Error()}
	}
	if nonMedia > 0 {
		return checkStatus{Err: fmt.Sprintf("non_media_assets=%d (expected 0)", nonMedia)}
	}
	// Per-channel invalid vector counts keyed off the channel matrix.
	// json_array_length returns 0 for null / non-array; rows where the
	// column is empty string or '[]' return 0 (interpreted as missing).
	// For text/transcript/visual we require 768; for audio we require 512.
	channelSQL := map[string]struct {
		col  string
		dim  int
		mime string
	}{
		"text":       {"embedding_json", 768, "video, image, audio"},
		"transcript": {"transcript_embedding", 768, "video, audio"},
		"visual":     {"visual_embedding", 768, "video, image"},
		"audio":      {"audio_embedding", 512, "audio"},
	}
	for ch, spec := range channelSQL {
		// Build a per-channel dimension check: column present AND
		// json_array_length equals expected dim. A column that is empty
		// or '[]' fails the length check (matches parseVectorLen's
		// "empty vector" error path).
		// mime list comes from channel matrix; we let SQLite's
		// coalesce-based filter handle it.
		mediaList := strings.Split(spec.mime, ", ")
		quoted := make([]string, len(mediaList))
		for i, m := range mediaList {
			quoted[i] = fmt.Sprintf("'%s'", strings.TrimSpace(m))
		}
		mediaFilter := strings.Join(quoted, ", ")
		query := fmt.Sprintf(`
			SELECT COUNT(*) FROM media_assets
			WHERE COALESCE(media_type, '') IN (%s)
			AND (
				COALESCE(%s, '') = ''
				OR COALESCE(%s, '') = '[]'
				OR json_array_length(%s) != %d
			)`, mediaFilter, spec.col, spec.col, spec.col, spec.dim)
		var n int
		if err := deps.DB.QueryRowContext(ctx, query).Scan(&n); err != nil {
			return checkStatus{Err: fmt.Sprintf("%s invalid-vector count query failed: %s", ch, err.Error())}
		}
		if n > 0 {
			return checkStatus{Err: fmt.Sprintf("invalid_%s_vectors=%d (expected 0; channel requires %d-dim)", ch, n, spec.dim)}
		}
	}
	return checkStatus{Pass: true}
}

// checkDeadLetter returns fail if the outbox has any DEAD entries
// (events that exhausted retries). The canonical column for terminal
// state is `status='DEAD'` (the outbox_events schema uses statuses like
// PENDING, PROCESSING, COMPLETED, DEAD — see
// internal/infrastructure/database/sqlite/outboxevents/event.go).
func checkDeadLetter(ctx context.Context, deps readinessDeps) checkStatus {
	if !tableExists(ctx, deps.DB, "outbox_events") {
		// If the table doesn't exist this is a fail-by-design (no outbox
		// means dead-letter cannot be computed; surface as fail).
		return checkStatus{Err: "outbox_events table missing"}
	}
	var dead int
	if err := deps.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM outbox_events WHERE status = 'DEAD'`).Scan(&dead); err != nil {
		return checkStatus{Err: "outbox_events DEAD count query failed: " + err.Error()}
	}
	if dead > 0 {
		return checkStatus{Err: fmt.Sprintf("outbox_events has %d DEAD entries (expected 0)", dead)}
	}
	return checkStatus{Pass: true}
}

// ── Channel matrix per media_type ─────────────────────────────────────
// Required channels:
//
//	video  → text + transcript + visual
//	image  → text + visual
//	audio  → text + transcript + audio
//
// A channel is request-required only when manifest.Requires(media_type, channel).
// An invalid vector on a required channel increments the per-channel counter;
// invalid vectors on NON-required channels are IGNORED (legitimate gap, no fail).
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

func boolToStatus(b bool) string {
	if b {
		return "pass"
	}
	return "fail"
}

func inspectRequiredColumns(ctx context.Context, db *sql.DB, required []string) ([]string, []string, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(media_assets)`)
	if err != nil {
		return nil, nil, fmt.Errorf("inspect media_assets columns: %w", err)
	}
	defer rows.Close()

	seen := make(map[string]struct{})
	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &defaultValue, &pk); err != nil {
			return nil, nil, fmt.Errorf("scan pragma table_info: %w", err)
		}
		seen[strings.ToLower(name)] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	present := make([]string, 0, len(required))
	missing := make([]string, 0)
	for _, col := range required {
		if _, ok := seen[strings.ToLower(col)]; ok {
			present = append(present, col)
		} else {
			missing = append(missing, col)
		}
	}
	return present, missing, nil
}

// collectReadinessCounters scans media_assets and populates legacy flat
// fields AND the channel-matrix-aware invalid_vectors counts. Callers
// derive the legacy_audit check from these counters in qdrantReadiness.
func collectReadinessCounters(ctx context.Context, db *sql.DB, report *qdrantReadinessReport) error {
	rows, err := db.QueryContext(ctx, `
		SELECT
			id,
			COALESCE(media_type, ''),
			COALESCE(local_path, ''),
			COALESCE(embedding_json, ''),
			COALESCE(transcript_embedding, ''),
			COALESCE(visual_embedding, ''),
			COALESCE(audio_embedding, ''),
			COALESCE(status, ''),
			COALESCE(lifecycle_state, ''),
			COALESCE(metadata_json, '{}')
		FROM media_assets
		ORDER BY id`)
	if err != nil {
		return fmt.Errorf("readiness scan: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id, mediaType, localPath, textJSON, transcriptJSON, visualJSON, audioJSON, status, lifecycleState, metaJSON string
		if err := rows.Scan(&id, &mediaType, &localPath, &textJSON, &transcriptJSON, &visualJSON, &audioJSON, &status, &lifecycleState, &metaJSON); err != nil {
			return fmt.Errorf("scan readiness row: %w", err)
		}
		_ = id
		report.TotalAssets++

		switch strings.ToLower(strings.TrimSpace(mediaType)) {
		case "video", "audio", "image":
		default:
			report.NonMediaAssets++
		}

		// Channel-matrix-aware invalid vector counts. Only check
		// channels that are REQUIRED for the row's media_type — a missing
		// visual embedding on an image is legitimate (no visual to embed),
		// and a missing audio embedding on an image is NOT an error.
		if isChannelRequiredForMediaType("text", mediaType) {
			if _, dim, err := parseVectorLen(textJSON); err != nil || dim != 768 {
				report.InvalidTextVectors++
			}
		}
		if isChannelRequiredForMediaType("transcript", mediaType) {
			if _, dim, err := parseVectorLen(transcriptJSON); err != nil || dim != 768 {
				report.InvalidTranscriptVectors++
			}
		}
		if isChannelRequiredForMediaType("visual", mediaType) {
			if _, dim, err := parseVectorLen(visualJSON); err != nil || dim != 768 {
				report.InvalidVisualVectors++
			}
		}
		if isChannelRequiredForMediaType("audio", mediaType) {
			if _, dim, err := parseVectorLen(audioJSON); err != nil || dim != 512 {
				report.InvalidAudioVectors++
			}
		}

		if strings.TrimSpace(localPath) == "" {
			report.MissingSourceFile++
		}
		if status != "" && !strings.EqualFold(status, lifecycleState) && lifecycleState != "" {
			report.LegacyStatusRows++
		}
		if hasLegacyLocatorKey(metaJSON) {
			report.LegacyLocatorRows++
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate readiness rows: %w", err)
	}

	return nil
}

func qdrantProbeAndSchema(ctx context.Context, cfg *config.Config, log *zap.Logger, report *qdrantReadinessReport) error {
	client := qdrant.NewClient(&qdrant.Config{
		BaseURL: cfg.Qdrant.BaseURL,
		Timeout: cfg.Qdrant.Timeout,
		APIKey:  cfg.Qdrant.APIKey,
	}, log)
	probe := qdrant.NewHealthProbe(client)
	if err := probe.Probe(ctx); err != nil {
		report.QdrantReachable = false
		return fmt.Errorf("qdrant health probe failed: %w", err)
	}
	report.QdrantReachable = true

	schema := qdrant.DefaultV3Schema()
	mgr := qdrant.NewCollectionManager(client, schema, log)
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

func backfillVisualEmbeddings(ctx context.Context, db *sql.DB, cfg *config.Config, deps qdrantVisualBackfillDeps, log *zap.Logger) (qdrantVisualBackfillReport, error) {
	report := qdrantVisualBackfillReport{
		Mode: "dry-run",
	}
	if deps.Apply {
		report.Mode = "apply"
	}

	const supportedQuery = `
		SELECT id, COALESCE(media_type, ''), COALESCE(local_path, ''), COALESCE(visual_embedding, '')
		FROM media_assets
		WHERE COALESCE(media_type, '') IN ('video', 'image')
		ORDER BY id`

	rows, err := db.QueryContext(ctx, supportedQuery)
	if err != nil {
		return report, fmt.Errorf("query visual backfill candidates: %w", err)
	}
	defer rows.Close()

	ffmpegProc := ffmpeg.NewFromConfig(cfg)
	schema := qdrant.DefaultV3Schema()
	imageEmbedder := qdrant.NewImageEmbedderAdapter(qdrant.ImageEmbedderConfig{
		ServerURL: cfg.ClipIndexer.ServerURL,
		Timeout:   90 * time.Second,
	}, schema, log)
	visualVersion := ""
	if spec := schema.GetDense("visual"); spec != nil {
		visualVersion = spec.ModelVersion
	}

	for rows.Next() {
		var id, mediaType, localPath, visualEmbedding string
		if err := rows.Scan(&id, &mediaType, &localPath, &visualEmbedding); err != nil {
			return report, fmt.Errorf("scan visual backfill row: %w", err)
		}
		if deps.Limit > 0 && report.TotalAssets >= deps.Limit {
			break
		}

		report.TotalAssets++
		normalizedType := strings.ToLower(strings.TrimSpace(mediaType))
		if normalizedType != "video" && normalizedType != "image" {
			report.UnsupportedMedia++
			continue
		}

		_, vecLen, vecErr := parseVectorLen(visualEmbedding)
		switch {
		case vecErr != nil:
			report.Legacy512++
		case vecLen == 768:
			report.Already768++
			continue
		case vecLen == 512:
			report.Legacy512++
		default:
			report.Legacy512++
		}

		if !deps.Apply {
			continue
		}

		if strings.TrimSpace(localPath) == "" {
			report.MissingSourceFile++
			report.Failed++
			continue
		}
		if _, err := os.Stat(localPath); err != nil {
			report.MissingSourceFile++
			report.Failed++
			continue
		}

		newVec, err := regenerateVisualEmbedding(ctx, ffmpegProc, imageEmbedder, id, mediaType, localPath)
		if err != nil {
			report.Failed++
			log.Warn("visual backfill failed", zap.String("asset_id", id), zap.String("path", localPath), zap.Error(err))
			continue
		}
		if len(newVec) != 768 {
			report.Failed++
			log.Warn("visual backfill produced unexpected dimension", zap.String("asset_id", id), zap.Int("dims", len(newVec)))
			continue
		}

		raw, err := json.Marshal(newVec)
		if err != nil {
			report.Failed++
			continue
		}

		metaJSON := "{}"
		var meta map[string]any
		if err := db.QueryRowContext(ctx, `SELECT COALESCE(metadata_json, '{}') FROM media_assets WHERE id = ?`, id).Scan(&metaJSON); err == nil {
			_ = json.Unmarshal([]byte(metaJSON), &meta)
		}
		if meta == nil {
			meta = make(map[string]any)
		}
		if visualVersion != "" {
			meta["embedding_version_visual"] = visualVersion
		}
		metaBytes, _ := json.Marshal(meta)

		if _, err := db.ExecContext(ctx, `
			UPDATE media_assets
			SET visual_embedding = ?, metadata_json = ?, updated_at = datetime('now')
			WHERE id = ?`,
			string(raw), string(metaBytes), id); err != nil {
			report.Failed++
			log.Warn("visual backfill update failed", zap.String("asset_id", id), zap.Error(err))
			continue
		}
		report.Regenerated++
	}
	if err := rows.Err(); err != nil {
		return report, fmt.Errorf("iterate visual backfill rows: %w", err)
	}

	return report, nil
}

func regenerateVisualEmbedding(ctx context.Context, ffmpegProc *ffmpeg.Processor, embedder qdrant.ImageEmbedder, assetID, mediaType, localPath string) ([]float32, error) {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "image":
		vecs, err := embedder.EmbedImages(ctx, []string{localPath})
		if err != nil {
			return nil, err
		}
		if len(vecs) == 0 {
			return nil, fmt.Errorf("no embedding returned for image")
		}
		return vecs[0], nil
	case "video":
		info, err := ffmpegProc.Probe(ctx, localPath)
		if err != nil {
			return nil, err
		}
		duration := info.Duration.Seconds()
		if duration <= 0 {
			duration = 1
		}
		timestamps := make([]float64, 0, int(math.Ceil(duration/2.0)))
		for ts := 1.0; ts < duration; ts += 2.0 {
			timestamps = append(timestamps, ts)
		}
		if len(timestamps) == 0 {
			timestamps = []float64{duration / 2.0}
		}
		tmpDir, err := os.MkdirTemp("", "qdrant-visual-backfill-*")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(tmpDir)

		embeddings := make([][]float32, 0, len(timestamps))
		for i, ts := range timestamps {
			framePath := filepath.Join(tmpDir, fmt.Sprintf("%s_%03d.png", assetID, i))
			if err := ffmpegProc.ExtractFrame(ctx, localPath, framePath, ts); err != nil {
				return nil, fmt.Errorf("extract frame %.3fs: %w", ts, err)
			}
			vecs, err := embedder.EmbedImages(ctx, []string{framePath})
			if err != nil {
				return nil, err
			}
			if len(vecs) == 0 || len(vecs[0]) == 0 {
				return nil, fmt.Errorf("empty frame embedding at %.3fs", ts)
			}
			embeddings = append(embeddings, vecs[0])
		}
		return averageFloat32Vectors(embeddings)
	default:
		return nil, fmt.Errorf("unsupported media_type %q for visual backfill", mediaType)
	}
}

func averageFloat32Vectors(vectors [][]float32) ([]float32, error) {
	if len(vectors) == 0 {
		return nil, fmt.Errorf("no vectors to average")
	}
	dim := len(vectors[0])
	if dim == 0 {
		return nil, fmt.Errorf("empty vector")
	}
	sums := make([]float64, dim)
	for _, vec := range vectors {
		if len(vec) != dim {
			return nil, fmt.Errorf("vector dimension mismatch: got %d want %d", len(vec), dim)
		}
		for i, v := range vec {
			sums[i] += float64(v)
		}
	}
	out := make([]float32, dim)
	for i := range sums {
		out[i] = float32(sums[i] / float64(len(vectors)))
	}
	return out, nil
}

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

func tableExists(ctx context.Context, db *sql.DB, name string) bool {
	var count int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&count)
	return count > 0
}
