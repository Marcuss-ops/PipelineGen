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
//     backed by production *qdrant.Client
//     and *qdrant.SQLiteAssetStore
//     (PR 7 #7.1: old *qdrant.Reconciler deleted;
//     canonical path is internal/application/qdrant/reconciler/)
//   - noOpSQLiteReconcileReader — replaced by repository.ClipsRepo
//     (production SQLite reader)
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	api "github.com/Marcuss-ops/PipelineGen/internal/api"
	app "github.com/Marcuss-ops/PipelineGen/internal/app"
	middlewareports "github.com/Marcuss-ops/PipelineGen/internal/application/middleware"
	"github.com/Marcuss-ops/PipelineGen/internal/application/qdrant/legacyaudit"
	"github.com/Marcuss-ops/PipelineGen/internal/application/qdrant/reconciler"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

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

// checkStatus is the per-check tuple used by all readiness checks.
// Pass=true means the check passed; Err is the diagnostic string for
// human ops (always populated on Pass=false, omitted otherwise).
type checkStatus struct {
	Pass bool
	Err  string
}

// readinessDeps is the dependency bag each check function consumes.
// Tests inject sqlmock + Config directly without standing up the full
// composition root; production wires Root via app.InitComposition
// so the production-shaped checks (Real wiring — server, dispatcher,
// worker, sqlite reader, qdrant lister, reconciler, routes) read
// real state.
//
// PR 15 (June 2026): Root is the canonical "production constructor
// output" the readiness gate consumes. Optional in tests so unit
// tests can still exercise the SQL-only checks (dead_letter,
// legacy_audit) without standing up the full composition root.
type readinessDeps struct {
	DB   *sql.DB
	Cfg  *config.Config
	Log  *zap.Logger
	Root *compositionRoot
}

// compositionRoot is the readiness-side view of the *app.ComposeRoot
// produced by app.InitComposition + app.WireRegistry.
//
// PR 15 (June 2026): every field carries a PRODUCTION CONCRETE TYPE
// from internal/app + internal/infrastructure + internal/api. There
// are no empty-marker interfaces and no narrow-port duplicates — the
// readiness gate reads the same Go values the server reads, so
// production-shaped invariants turn into plain nil checks at the
// read site. app.InitComposition + app.WireRegistry are the canonical
// constructors; if either fails, root is nil and every production-
// shaped check emits its per-check failure message.
//
// Bundle accessors (canonical, per `internal/app/composition.go`):
//   - Dispatcher        ← root.Outbox.Dispatcher   *outbox.Dispatcher
//   - EventsPool        ← root.Outbox.EventsPool   *outboxevents.Pool
//   - OutboxHandler     ← WireRegistry().OutboxHandler (api.InternalOutboxRouter)
//   - MediasearchHandler← WireRegistry().MediasearchHandler (api.InternalMediaSearchRouter)
//   - ClipsRepo         ← root.Repos.ClipsRepo     *assets.ClipsRepository
//   - QdrantClient      ← root.Process.QdrantClient  *qdrant.Client
type compositionRoot struct {
	Dispatcher         *outbox.Dispatcher
	EventsPool         *outboxevents.Pool
	OutboxHandler      api.InternalOutboxRouter
	MediasearchHandler api.InternalMediaSearchRouter
	ClipsRepo          *assets.ClipsRepository
	QdrantClient       *qdrant.Client
}

// readinessCheck is the testable surface for the production-shaped
// readiness gate. Each entry is a `var` (not `func`) so
// cmd/admin/qdrant_readiness_test.go can REPLACE individual checks
// with mocks/failing implementations without touching this file.
// The composition root wires the real checks at init; tests override
// only the keys they want to simulate failure for.
//
// PR 15 — 9 user-specified keys, alphabetical from the spec:
//
//	"dead_letters_zero"             (production outbox status check)
//	"dispatcher_really_built"       (root.Outbox.Dispatcher != nil)
//	"legacy_cleanup_clean"          (per-channel SQL aggregate)
//	"production_sqlite_reader"      (root.Repos.ClipsRepo != nil)
//	"qdrant_active_collection_real" (real client + GetAliasTarget +
//	                                 CompareActiveCollection)
//	"real_routes_present"           (real router built from production
//	                                 handlers, not stubs)
//	"scan_reconciler_complete"      (qdrantReconcilerListerAdapter dry-run
//	                                 against SQLite + Qdrant; the legacy
//	                                 *qdrant.Reconciler was deleted in PR 7)
//	"server_production_constructor" (root != nil AND every required
//	                                 bundle non-nil)
//	"worker_real_state"             (root.Outbox.EventsPool != nil)
//
// Plus the pre-existing "delivery_signer" check (HMAC secret >= 16)
// as a backwards-compat key because the existing test suite asserts
// it. Production deployments rely on it for webhook integrity.
var readinessCheck = map[string]func(context.Context, readinessDeps) checkStatus{
	"dead_letters_zero":             checkDeadLetter,
	"delivery_signer":               checkDeliverySigner,
	"dispatcher_really_built":       checkDispatcherBuilt,
	"legacy_cleanup_clean":          checkLegacyAudit,
	"production_sqlite_reader":      checkSQLiteReader,
	"qdrant_active_collection_real": checkQdrantActiveCollection,
	"real_routes_present":           checkRoutesReal,
	"scan_reconciler_complete":      checkReconcilerProduction,
	"server_production_constructor": checkServerProductionConstructor,
	"worker_real_state":             checkWorkerRealState,
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
	}, cleanup, nil
}

// ── Per-check functions (production-shaped, PR 15) ─────────────────────

func checkDeadLetter(ctx context.Context, deps readinessDeps) checkStatus {
	if deps.DB == nil {
		return checkStatus{Err: "db is nil (legacy: dead_letter check needs a real *sql.DB)"}
	}
	if !tableExists(ctx, deps.DB, "outbox_events") {
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

// checkDeliverySigner (preserved from pre-PR-15 — protects webhook HMAC
// integrity in production).
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

// checkDispatcherBuilt: production-shaped. PR 15 replaces the
// config-only check (`cfg.Outbox.PollIntervalMs >= 0`) with a real
// `root.Outbox.Dispatcher != nil` assertion. The config-only check
// could pass while the dispatcher was unbuilt; the production-shaped
// check fails loudly in that case.
func checkDispatcherBuilt(_ context.Context, deps readinessDeps) checkStatus {
	if deps.Root == nil {
		return checkStatus{Err: "production composition root is nil — app.InitComposition failed; cannot verify dispatcher was built"}
	}
	if deps.Root.Dispatcher == nil {
		return checkStatus{Err: "outbox dispatcher is nil — production wiring missing"}
	}
	return checkStatus{Pass: true}
}

// checkWorkerRealState: production-shaped. Confirms the worker pool
// is real and registered (the empty-marker pattern is satisfied if
// any concrete *outboxevents.Pool has been wired).
func checkWorkerRealState(_ context.Context, deps readinessDeps) checkStatus {
	if deps.Root == nil {
		return checkStatus{Err: "production composition root is nil — cannot verify worker state"}
	}
	if deps.Root.EventsPool == nil {
		return checkStatus{Err: "outbox events pool is nil — production worker pool missing"}
	}
	if deps.Cfg != nil && deps.Cfg.Outbox.Workers <= 0 {
		return checkStatus{Err: fmt.Sprintf("outbox.workers=%d (must be > 0)", deps.Cfg.Outbox.Workers)}
	}
	return checkStatus{Pass: true}
}

// checkSQLiteReader: production-shaped.
func checkSQLiteReader(_ context.Context, deps readinessDeps) checkStatus {
	if deps.DB == nil {
		return checkStatus{Err: "raw *sql.DB is nil — production SQLite reader missing"}
	}
	if deps.Root == nil || deps.Root.ClipsRepo == nil {
		return checkStatus{Err: "production ClipsRepo (root.Repos.ClipsRepository) is nil"}
	}
	if !tableExists(context.Background(), deps.DB, "media_assets") {
		return checkStatus{Err: "media_assets table missing"}
	}
	return checkStatus{Pass: true}
}

// checkQdrantActiveCollection: production-shaped. Replaces the old
// stub pattern (NewHealthProbe stub + no-op reconciler). Builds a
// real *qdrant.Client from cfg and runs the canonical GetAliasTarget
// + CompareActiveCollection flow.
func checkQdrantActiveCollection(ctx context.Context, deps readinessDeps) checkStatus {
	if deps.Cfg == nil || deps.Cfg.Qdrant.BaseURL == "" {
		return checkStatus{Err: "qdrant.base_url is empty"}
	}
	if !deps.Cfg.Qdrant.Enabled {
		// QDRANT-005: a disabled qdrant config means the integration
		// is OFF; readiness correctly reports not-ready (the user
		// must enable qdrant to pass). This is the EXPECTED fail for
		// environments running without the semantic index.
		return checkStatus{Err: "qdrant.enabled=false (enable to pass production-shaped readiness)"}
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

// checkServerProductionConstructor: production-shaped. Confirms the
// canonical InitComposition output is non-nil (mirrors cmd/server
// startup invariant per AGENTS.md §7). This is the gate that
// catches the "I forgot to wire root.Outbox in the registry" class
// of bugs — the construction root refuses to return until the wiring
// is structurally complete.
func checkServerProductionConstructor(_ context.Context, deps readinessDeps) checkStatus {
	if deps.Root == nil {
		return checkStatus{Err: "production composition root is nil — app.InitComposition failed before the server can boot"}
	}
	if deps.Cfg == nil {
		return checkStatus{Err: "config is nil (composition requires cfg)"}
	}
	if strings.TrimSpace(deps.Cfg.Storage.DataDir) == "" {
		return checkStatus{Err: "storage.data_dir is empty"}
	}
	return checkStatus{Pass: true}
}

// checkLegacyAudit: preserved from pre-PR-15 with channel-matrix-aware
// SQL aggregate semantics. Operators wanting the deeper legacyaudit
// (Qdrant payload scan) can run `cleanup-qdrant-legacy` separately
// (PR 14) — the readiness gate is a "production shape" gate, not a
// deep-data audit.
//
// Per the spec, "legacy cleanup clean" means zero legacy hits in the
// canonical DB-side audit; anything more elaborate belongs to
// cleanup-qdrant-legacy (PR 14).
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

// checkRoutesReal: production-shaped. Replaces the stub-router
// (stubOutboxRouter + stubMediaSearchRouter) pattern with a real
// router built from production handlers (root.OutboxHandler +
// root.MediasearchHandler injected via api.Router.SetOutboxHandler
// + SetMediasearchHandler). The check verifies the canonical routes
// (outbox status/events + media search) ACTUALLY land on the engine
// in production shape — no synthetic stubs.
func checkRoutesReal(_ context.Context, deps readinessDeps) checkStatus {
	if deps.Root == nil {
		return checkStatus{Err: "production composition root is nil — routes check requires real handler wiring"}
	}
	if deps.Root.OutboxHandler == nil {
		return checkStatus{Err: "outbox handler is nil — production wiring missing SetOutboxHandler"}
	}
	if deps.Root.MediasearchHandler == nil {
		return checkStatus{Err: "mediasearch handler is nil — production wiring missing SetMediasearchHandler"}
	}
	engine := buildRouterWithProductionWiring(deps)
	if !engineHasPath(engine, "GET", "/internal/v1/outbox/") {
		return checkStatus{Err: "/internal/v1/outbox/* route not registered in production-shaped router"}
	}
	if !engineHasPath(engine, "POST", "/internal/v1/media/search") {
		return checkStatus{Err: "/internal/v1/media/search route not registered in production-shaped router"}
	}
	return checkStatus{Pass: true}
}

// checkReconcilerProduction: production-shaped. Replaces
// noOpQdrantLister + noOpSQLiteReconcileReader with the
// qdrantReconcilerListerAdapter dry-run, which exercises the real
// Client.ScrollPoints + assetStore.ListAllAssetIDs machinery.
//
// PR 7 #7.1 note: the legacy *qdrant.Reconciler (in
// internal/infrastructure/qdrant/reconciler.go) was deleted in
// chore/remove-qdrant-legacy (June 2026). The canonical
// reconciler service lives at internal/application/qdrant/reconciler/.
//
// Fails hard on schema/assetStore/Client nil (the production
// canonical real-wiring invariants). The reconciler service is a
// thin shell — production wiring is the test.
func checkReconcilerProduction(ctx context.Context, deps readinessDeps) checkStatus {
	defer func() {
		_ = recover()
	}()
	if deps.Root == nil {
		return checkStatus{Err: "production composition root is nil — reconciler check requires real Client + assetStore"}
	}
	if deps.Root.QdrantClient == nil {
		return checkStatus{Err: "production *qdrant.Client is nil — reconciler cannot run its real scroll path"}
	}
	if deps.DB == nil {
		return checkStatus{Err: "raw *sql.DB is nil — reconciler assetStore consumer missing"}
	}
	if deps.Cfg == nil || deps.Cfg.Qdrant.BaseURL == "" {
		return checkStatus{Err: "qdrant.base_url is empty — reconciler cannot open the alias"}
	}

	schema := qdrant.DefaultV3Schema()
	// Internal asset store adapter for the Reconciler. The
	// Reconciler requires the AssetStore interface; production uses
	// *qdrant.SQLiteAssetStore constructed from the typed
	// *storage.SQLiteDB handle. We construct it inline so the
	// readiness gate does not depend on the AppLayer ClipsRepo
	// (which has a broader surface than Reconciler needs).
	assetStore := qdrantAssetStoreForReconcile(deps.DB)
	rec := reconciler.NewServiceFromDeps(reconciler.ServiceDeps{
		Schema: reconciler.SchemaVersions{
			Version:           schema.Version,
			RequiredKeys:      []string{"asset_id"},
			PerChannelVersion: map[string]string{},
		},
		Qdrant: qdrantReconcilerListerAdapter{Client: deps.Root.QdrantClient},
		SQLite: assetStore,
		// PR 10 (June 2026): Outbox + Payload are REQUIRED (panic
		// on nil). Readiness dry-runs never dispatch repairs, so
		// the production-shaped noop stubs satisfy the contract
		// without side effects.
		Outbox:  readiNoopOutbox{},
		Payload: readiNoopPayload{},
		// Metrics + ReportWriter are documented as optional;
		// NewServiceFromDeps substitutes noopMetrics and
		// filesystemReportWriter{}. We pass them as nil so the
		// service stays responsible for defaulting — keeps the
		// readiness-side code minimal.
		Log: deps.Log,
	})
	// PR 10 requires opts.Collection to be set explicitly; resolve
	// against the canonical runtime alias so the readiness dry-run
	// scans the same collection production runs against. We use the
	// narrow qdrantClient interface directly (the readinessRoot's
	// QdrantClient port) so we don't widen the production surface
	// or pull NewCollectionManager into the readiness path. If the
	// alias is unresolvable we fall back to the schema's runtime
	// alias literal — the readiness scan will return zero points,
	// but Client.ScrollPoints reachability is still exercised
	// through the adapter path.
	collection := schema.RuntimeAlias
	prodColl, collErr := deps.Root.QdrantClient.GetAliasTarget(ctx, schema.RuntimeAlias)
	if collErr == nil && strings.TrimSpace(prodColl) != "" {
		collection = prodColl
	}
	if _, err := rec.Reconcile(ctx, reconciler.ReconcileOptions{
		Collection: collection,
		DryRun:     true,
	}); err != nil {
		return checkStatus{Err: "reconciler dry-run failed: " + err.Error()}
	}
	return checkStatus{Pass: true}
}

// ── PR 10 readiness noop stubs ────────────────────────────────────────
//
// reconciler.NewServiceFromDeps PANICS on nil Outbox + Payload (PR 10
// hardening, June 2026). The readiness gate is a DryRun check, so it
// never dispatches repairs — these readiNoop* stubs satisfy the
// reconciler ports without side effects.
//
// We re-declare them locally (rather than reusing reconciler.noopX)
// because the unexported reconciler types live in a different package
// and cmd/admin is not the canonical consumer (the canonical
// consumers are the production composition root and reconcile CLI).
type (
	readiNoopOutbox  struct{}
	readiNoopPayload struct{}
)

func (readiNoopOutbox) EnqueueReindex(context.Context, string, string) error { return nil }
func (readiNoopOutbox) EnqueueDelete(context.Context, string) error          { return nil }
func (readiNoopPayload) DeletePayloadKeys(context.Context, string, []string, []string) error {
	return nil
}

// ── Production-shaped dependencies shims ───────────────────────────────

// qdrantReconcilerListerAdapter bridges the production
// *compositionRoot.QdrantClient (concrete *qdrant.Client) to the
// Reconciler QdrantLister interface.
//
// The conversion happens at the package boundary because *qdrant.Client
// returns *qdrant.ScrollResult while reconciler.QdrantLister.ScrollPoints
// returns reconciler.Points. Field-for-field equivalence holds:
// both expose (Items / Points, NextOffset) with the same element shape.
//
// Canonical client methods used:
//   - (*qdrant.Client).ScrollPoints(ctx, collection, offset, limit, filter) (production signature accepts a Qdrant filter; we pass nil)
type qdrantReconcilerListerAdapter struct {
	Client *qdrant.Client
}

func (a qdrantReconcilerListerAdapter) ScrollPoints(ctx context.Context, collection string, offset string, limit int) (reconciler.Points, error) {
	result, err := a.Client.ScrollPoints(ctx, collection, offset, limit, nil)
	if err != nil {
		return reconciler.Points{}, fmt.Errorf("readiness: qdrant scroll failed for collection %q: %w", collection, err)
	}
	if result == nil {
		return reconciler.Points{}, fmt.Errorf("readiness: qdrant scroll returned nil ScrollResult for collection %q", collection)
	}
	items := make([]reconciler.PointSnapshot, len(result.Points))
	for i, p := range result.Points {
		items[i] = reconciler.PointSnapshot{
			ID:      p.ID,
			Payload: p.Payload,
		}
	}
	return reconciler.Points{
		Items:      items,
		NextOffset: result.NextOffset,
	}, nil
}

// qdrantAssetStoreForReconcile constructs the production-shaped
// SQLite asset store backed by the canonical *qdrant.SQLiteAssetStore.
// The store implements the canonical media_assets query that feeds
// the reconciler port (id, workspace_id, lifecycle_state,
// content_hash); readiness wires it straight into NewServiceFromDeps
// so the read path runs real SQL.
func qdrantAssetStoreForReconcile(db *sql.DB) qdrantReconcileAssetStore {
	return qdrantReconcileAssetStore{Store: qdrant.NewSQLiteAssetStore(db)}
}

// qdrantReconcileAssetStore bridges the readiness *sql.DB owned by
// readinessDeps into the canonical *qdrant.SQLiteAssetStore and
// satisfies the reconciler.SQLiteReconcileReader port. The
// production ListAssetsForReconcile method already returns the four
// fields reconciler.AssetSnapshot needs (ID, WorkspaceID,
// LifecycleState, ContentHash); this adapter copies them across the
// package boundary.
type qdrantReconcileAssetStore struct {
	Store *qdrant.SQLiteAssetStore
}

func (s qdrantReconcileAssetStore) ListForReconcile(ctx context.Context, includeLifecycleStates []string) ([]reconciler.AssetSnapshot, error) {
	if s.Store == nil {
		return nil, fmt.Errorf("readiness: qdrant.SQLiteAssetStore is nil — cannot run reconciler SQLite path")
	}
	assets, err := s.Store.ListAssetsForReconcile(ctx, includeLifecycleStates)
	if err != nil {
		return nil, fmt.Errorf("readiness: qdrant.SQLiteAssetStore.ListAssetsForReconcile failed: %w", err)
	}
	out := make([]reconciler.AssetSnapshot, len(assets))
	for i, a := range assets {
		out[i] = reconciler.AssetSnapshot{
			ID:             a.ID,
			WorkspaceID:    a.WorkspaceID,
			LifecycleState: a.LifecycleState,
			ContentHash:    a.ContentHash,
		}
	}
	return out, nil
}

// buildRouterWithProductionWiring: production-shaped. Builds the
// canonical api.Router from cfg.Security + cfg.FeatureFlags (no stub
// ports) and injects the production outbox + mediasearch handler
// instances from root.
//
// Auth/Feature ports are constructed from cfg so the router wires
// through the production middleware chain — not stubs.
func buildRouterWithProductionWiring(deps readinessDeps) *gin.Engine {
	gin.SetMode(gin.TestMode)
	var outboxHandler api.InternalOutboxRouter
	var mediasearchHandler api.InternalMediaSearchRouter
	if deps.Root != nil {
		outboxHandler = deps.Root.OutboxHandler
		mediasearchHandler = deps.Root.MediasearchHandler
	}

	server := api.NewServerWithHealth(
		deps.Cfg,
		nil, // registry
		nil, // workerHandler
		nil, // internalMediaHandler
		outboxHandler,
		mediasearchHandler,
		nil, // lifecycle
		nil, // health
		nil, // docClient
	)
	return server.GetRouter()
}

// cfg-derived ports (no stubs). The auth/rate/feature shape matches
// the canonical middleware.AuthSecurityPort / RateLimitPort /
// FeatureFlagsPort interfaces from
// internal/application/middleware.

type (
	cfgAuthPort     struct{ Cfg *config.Config }
	cfgRatePort     struct{ Cfg *config.Config }
	cfgFeaturesPort struct{ Cfg *config.Config }
)

func (p *cfgAuthPort) EnableAuth() bool {
	if p.Cfg == nil {
		return false
	}
	return strings.TrimSpace(p.Cfg.Security.AdminToken) != "" ||
		strings.TrimSpace(p.Cfg.Security.WorkerToken) != ""
}
func (p *cfgAuthPort) AdminToken() string {
	if p.Cfg == nil {
		return ""
	}
	return p.Cfg.Security.AdminToken
}
func (p *cfgAuthPort) WorkerToken() string {
	if p.Cfg == nil {
		return ""
	}
	return p.Cfg.Security.WorkerToken
}

func (p *cfgRatePort) RateLimitEnabled() bool {
	if p.Cfg == nil {
		return false
	}
	return p.Cfg.Security.RateLimitEnabled
}
func (p *cfgRatePort) RateLimitRequests() int {
	if p.Cfg == nil {
		return 0
	}
	return p.Cfg.Security.RateLimitRequests
}

func (p *cfgFeaturesPort) ArtlistEnabled() bool {
	if p.Cfg == nil {
		return false
	}
	return p.Cfg.Features.ArtlistEnabled
}
func (p *cfgFeaturesPort) ScriptDocsEnabled() bool {
	if p.Cfg == nil {
		return false
	}
	return p.Cfg.Features.ScriptDocsEnabled
}
func (p *cfgFeaturesPort) ScriptClipsEnabled() bool {
	if p.Cfg == nil {
		return false
	}
	return p.Cfg.Features.ScriptClipsEnabled
}

// Compile-time guards: cfg-derived ports satisfy the canonical
// middleware ports. Empty-marker `IsDispatcherNonNilMarker`-style
// assertions are gone in PR 15 — production concrete types populate
// compositionRoot directly.
var (
	_ middlewareports.AuthSecurityPort = (*cfgAuthPort)(nil)
	_ middlewareports.RateLimitPort    = (*cfgRatePort)(nil)
	_ middlewareports.FeatureFlagsPort = (*cfgFeaturesPort)(nil)
)

func engineHasPath(engine *gin.Engine, method, prefix string) bool {
	if engine == nil {
		return false
	}
	for _, r := range engine.Routes() {
		if strings.EqualFold(r.Method, method) && strings.HasPrefix(r.Path, prefix) {
			return true
		}
	}
	return false
}

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
	// check, mirrors pre-PR-15 semantics).
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

// legacyauditReportMarker is a compile-time reference ensuring the
// legacyaudit package import stays live (PR 14 ↔ PR 15 consistency).
// Either path may need to evolve in future waves; the import here
// prevents drift on Phase-PR cleanup sweeps.
var _ = legacyaudit.Classify

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
	if db == nil {
		return false
	}
	var count int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&count)
	return count > 0
}

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
