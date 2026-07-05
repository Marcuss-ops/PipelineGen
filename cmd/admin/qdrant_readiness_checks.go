// cmd/admin/qdrant_readiness_checks.go — check functions + adapter types + cfg
// ports + noop stubs for the production-shaped readiness gate.
// Extracted from qdrant_readiness.go (Fase 4 Step 2, June 2026).
//
// Owns: checkStatus, readinessDeps, compositionRoot, readinessCheck,
//
//	all 10 check functions, noop stubs (readiNoopOutbox, readiNoopPayload),
//	adapter types (qdrantReconcilerListerAdapter, qdrantReconcileAssetStore,
//	qdrantAssetStoreForReconcile), cfg-derived ports (cfgAuthPort,
//	cfgRatePort, cfgFeaturesPort), router helper (buildRouterWithProductionWiring,
//	engineHasPath), and middleware compile-time assertions.
//
// The orchestrator (qdrantReadiness), CLI entry (runQdrantReadiness),
// bridge (appInitCompositionForReadiness), report type (qdrantReadinessReport),
// runOneCheck, and utility functions stay in qdrant_readiness.go.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	api "github.com/Marcuss-ops/PipelineGen/internal/api"
	middlewareports "github.com/Marcuss-ops/PipelineGen/internal/application/middleware"
	"github.com/Marcuss-ops/PipelineGen/internal/application/qdrant/legacyaudit"
	"github.com/Marcuss-ops/PipelineGen/internal/application/qdrant/reconciler"
	searchpkg "github.com/Marcuss-ops/PipelineGen/internal/application/search"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

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
//   - SemanticSearch    ← registryWiring.SearchFanOut (search.SearchFanOut)
type compositionRoot struct {
	Dispatcher         *outbox.Dispatcher
	EventsPool         *outboxevents.Pool
	OutboxHandler      api.InternalOutboxRouter
	MediasearchHandler api.InternalMediaSearchRouter
	ClipsRepo          *assets.ClipsRepository
	QdrantClient       *qdrant.Client
	SemanticSearch     searchpkg.SearchFanOut
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
	"semantic_search_real":          checkSemanticSearchReal,
	"server_production_constructor": checkServerProductionConstructor,
	"worker_real_state":             checkWorkerRealState,
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
// startup invariant per AGENTS.md §7).
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
// SQL aggregate semantics.
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
// pattern with a real router built from production handlers.
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
// qdrantReconcilerListerAdapter dry-run.
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
	assetStore := qdrantAssetStoreForReconcile(deps.DB)
	rec := reconciler.NewServiceFromDeps(reconciler.ServiceDeps{
		Schema: reconciler.SchemaVersions{
			Version:           schema.Version,
			RequiredKeys:      []string{"asset_id"},
			PerChannelVersion: map[string]string{},
		},
		Qdrant:  qdrantReconcilerListerAdapter{Client: deps.Root.QdrantClient},
		SQLite:  assetStore,
		Outbox:  readiNoopOutbox{},
		Payload: readiNoopPayload{},
		Log:     deps.Log,
	})
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

func qdrantAssetStoreForReconcile(db *sql.DB) qdrantReconcileAssetStore {
	return qdrantReconcileAssetStore{Store: qdrant.NewSQLiteAssetStore(db)}
}

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
func buildRouterWithProductionWiring(deps readinessDeps) *gin.Engine {
	gin.SetMode(gin.TestMode)
	authPort := &cfgAuthPort{Cfg: deps.Cfg}
	ratePort := &cfgRatePort{Cfg: deps.Cfg}
	featPort := &cfgFeaturesPort{Cfg: deps.Cfg}
	router := api.NewRouter(&api.RouterConfig{
		Auth:          authPort,
		Rate:          ratePort,
		Features:      featPort,
		Log:           deps.Log,
		ServerGinMode: gin.TestMode,
		DataDir:       ".",
		DownloadDir:   ".",
		CORSOrigins:   []string{},
	})
	if deps.Root != nil && deps.Root.OutboxHandler != nil {
		router.SetOutboxHandler(deps.Root.OutboxHandler)
	}
	if deps.Root != nil && deps.Root.MediasearchHandler != nil {
		router.SetMediasearchHandler(deps.Root.MediasearchHandler)
	}
	return router.Setup()
}

// cfg-derived ports (no stubs).

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

// Compile-time guards: cfg-derived ports satisfy the canonical middleware ports.
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

// legacyauditReportMarker is a compile-time reference ensuring the
// legacyaudit package import stays live (PR 14 ↔ PR 15 consistency).
var _ = legacyaudit.Classify
