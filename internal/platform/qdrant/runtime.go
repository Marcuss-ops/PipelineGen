// Package qdrant — runtime.go is the single canonical facade for all
// Qdrant infrastructure subsystems. Constructed ONCE at boot.
//
// PR 4 (June 2026, refactor/single-qdrant-runtime) — sections #2 + #6
// of the verdict Qdrant:
//
//   - Before PR 4: NewClient was called from
//     composition.go::buildQdrantDeps AND from
//     build_bundles_process.go::BuildProcessBundle; each constructed its
//     own *Client / *IndexSchema. The two *Clients were structurally
//     distinct but functionally identical; a regression where one
//     called cfg.Qdrant.APIKey=... and the other didn't would silently
//     route half the traffic unauthenticated.
//
//   - After PR 4: a single NewRuntime(...) call returns a *QdrantRuntime
//     whose Client/Writer/Searcher/Manager/Health/Cleaner/Mapper all
//     share the same *IndexSchema and *Client. composition.go::buildQdrantDeps
//     becomes a thin wrapper around NewRuntime; build_bundles_process.go
//     reads the same instance via *QdrantDeps.Runtime.
//
// Layering rationale: *QdrantRuntime lives in platform/qdrant
// because its fields are CONCRETE types (*Client, *IndexWriter, etc.).
// The port surfaces derived from it (e.g. outbox.VectorPointDeleter)
// live in the application layer per AGENTS.md Pattern 0.
package qdrant

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"go.uber.org/zap"

	appsearch "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	capmediaregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/maintenance"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/collections"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/disasterrecovery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/indexing"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/search"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/verification"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/searchtext"
)

// RuntimeConfig is the bundle NewRuntime consumes. Avoiding a direct
// dependency on platform/config keeps the qdrant package free of
// platform-layer imports — composition.go translates platform.Config
// to RuntimeConfig at the boundary.
type RuntimeConfig struct {
	// QdrantCfg is the canonical wire/auth configuration (base URL,
	// api key, timeout). Required.
	QdrantCfg *schema.Config
	// DB is the main SQLite handle used by the asset store for
	// mapper fetches. Optional — nil disables the mapper (e.g. admin
	// tools that only need schema inspection).
	DB *sql.DB
	// Logger is the zap logger used by every subsystem. Optional;
	// nil maps to zap.NewNop().
	Logger *zap.Logger
	// Schema overrides the default V3 schema when the caller needs a
	// different version for diagnostics / DR tests. Optional; nil
	// falls back to DefaultV3Schema().
	Schema *schema.IndexSchema
	// RegistryLedger is the canonical Media Registry port. When wired,
	// projection sequence checks read the SSOT directly and lifecycle
	// transitions are persisted before the manager reports success.
	RegistryLedger capmediaregistry.Ledger
}

// QdrantRuntime composes every Qdrant infrastructure subsystem behind
// a single struct. Constructed exactly once per process via NewRuntime.
//
// Fields are concrete (not port interfaces) — runtime is a
// composition-root helper, not a port. The application-layer ports
// derived from this struct (outbox.VectorPointDeleter, system.health.
// QdrantChecker, etc.) are satisfied by the concrete types directly;
// see the compile-time assertions at the bottom of this file for the
// contractual matrix.
type QdrantRuntime struct {
	// Schema is the canonical IndexSchema used by every subsystem
	// below. There is ONE schema instance for the whole runtime; the
	// previous duplication (composition.go + build_bundles_process.go
	// each calling DefaultV3Schema() independently) is gone.
	Schema *schema.IndexSchema
	// Client is the canonical *Client used for every HTTP
	// round-trip. ONE Client for the whole runtime — wire-step
	// invariants (api-key header, retry policy, timeout) cannot drift
	// between subsystems.
	Client *transport.Client
	// Writer is the canonical IndexWriter; satisfies
	// outbox.VectorPointDeleter and clipindexer.VectorStoreIndexer.
	Writer *indexing.IndexWriter
	// Searcher is the canonical ANN searcher.
	Searcher *search.Searcher
	// Manager is the canonical CollectionManager (EnsureSchema +
	// alias switch). Used by wire_services.go's qdrant-collection
	// startup step.
	Manager *collections.CollectionManager
	// Health is the canonical readiness probe. Replaces the previous
	// `QdrantHealthProbe any` carrier field on ProcessBundle.
	Health *disasterrecovery.HealthProbe
	// Cleaner is the canonical LocatorCleaner (QDRANT-005 Fase 3).
	Cleaner *maintenance.LocatorCleaner
	// Mapper is the canonical PayloadMapper used by the Writer.
	// nil when RuntimeConfig.DB was nil at NewRuntime call time.
	Mapper *indexing.PayloadMapper
	// Store is the canonical SQLiteAssetStore used by Mapper.
	// nil when RuntimeConfig.DB was nil at NewRuntime call time.
	Store *indexing.SQLiteAssetStore
	// SearchAdapter is the canonical appsearch.VectorStorePort
	// surface wired through search.NewSearchAdapter (sub-package
	// internal/platform/qdrant/search). Composition root reads this field via
	// qd.Runtime.SearchAdapter to populate ProcessBundle.VectorSvc
	// (build_bundles_process.go:290). The interface — not the
	// concrete *searchAdapter — is the field type so the
	// composition root gets a typed-port contract per
	// AGENTS.md Pattern 0; the compile-time assertion at the
	// bottom of search_adapter.go (`var _ appsearch.VectorStorePort
	// = (*searchAdapter)(nil)`) pins the conformance.
	SearchAdapter appsearch.VectorStorePort
}

// NewRuntime constructs the canonical QdrantRuntime. Each subsystem
// references the SAME *Client and *IndexSchema — there is exactly ONE
// instance per process per runtime.
//
// DB is REQUIRED (cfg.DB != nil) because Mapper delegates to the
// SQLiteAssetStore for asset fetches; a nil mapper would panic on the
// first writer op (FetchAsset on nil receiver). Pre-PR4 the
// unconditional mapper construction hid this; PR 4 promotes the
// precondition to a typed error so composition fails closed on a
// misconfigured pathway (e.g. admin tools that bypass dbs.main).
//
// Returns a typed error (not a panic) on misconfiguration; callers
// gate on cfg.Qdrant.Enabled before calling.
func NewRuntime(cfg RuntimeConfig) (*QdrantRuntime, error) {
	if cfg.QdrantCfg == nil {
		return nil, fmt.Errorf("NewRuntime: RuntimeConfig.QdrantCfg is nil (composition forgot to translate cfg.Qdrant to RuntimeConfig?)")
	}
	if cfg.DB == nil {
		return nil, fmt.Errorf("NewRuntime: RuntimeConfig.DB is nil (Mapper requires SQLiteAssetStore; pass dbs.main.DB or equivalent)")
	}
	log := cfg.Logger
	if log == nil {
		log = zap.NewNop()
	}
	schema := cfg.Schema
	if schema == nil {
		version := ""
		if cfg.QdrantCfg != nil && cfg.QdrantCfg.CollectionVersion != "" {
			version = cfg.QdrantCfg.CollectionVersion
		}
		resolved, err := verification.ResolveSchema(version)
		if err != nil {
			return nil, fmt.Errorf("NewRuntime: %w", err)
		}
		schema = resolved
	}

	client := transport.NewClient(cfg.QdrantCfg, log)

	// DB precondition is enforced above (returns nil, error) — this
	// block is now unconditional: mapper+store are always non-nil.
	store := indexing.NewSQLiteAssetStore(cfg.DB)
	mapper := indexing.NewPayloadMapper(store, log)
	// Task 5 (July 2026): wire the canonical SearchTextBuilder
	// registry into the mapper so each AssetToIndexDocument call
	// routes BM25 search-text through per-source strategies
	// (youtube, artlist, voiceover, image, generated_image). The
	// admin reindex_qdrant CLI path uses NewPayloadMapper directly
	// (without this wiring) and falls back to asset.SearchText —
	// the SetSearchTextBuilder path enforces the production
	// contract while keeping the CLI / tests / fixtures nil-tolerant.
	// Wire the canonical SearchTextBuilder registry so each
	// AssetToIndexDocument call routes BM25 search-text through
	// per-source strategies (youtube, artlist, voiceover, image,
	// generated_image). The mapper stays backwards-compatible for
	// callers that bypass NewRuntime (admin reindex_qdrant CLI,
	// unit tests, fixtures) — those callers see the legacy
	// asset.SearchText pass-through because they construct the
	// mapper via NewPayloadMapper(store, log) without
	// SetSearchTextBuilder.
	mapper.SetSearchTextBuilder(searchtext.NewRegistry())

	writer := indexing.NewIndexWriter(client, schema, mapper, log)
	searcher := search.NewSearcher(client, schema, log)
	manager := collections.NewProjectionManager(client, schema, log)
	// The Projection Manager must use the complete reindex verifier, not
	// only the collection non-empty check. This makes point parity,
	// canonical IDs, payloads, embedding versions, full scan and smoke
	// failures blocking before an alias can become ACTIVE.
	manager.SetReindexVerifier(verification.NewReindexVerifier(client, store, nil, schema, nil, log))
	if err := manager.SetRegistryLedger(context.Background(), cfg.RegistryLedger); err != nil {
		return nil, fmt.Errorf("NewRuntime: %w", err)
	}
	// QDRANT-ALIAS-CACHE (July 2026): wire the cache invalidation so
	// PromoteCandidate resets the Searcher's alias-target cache
	// atomically with every alias switch. Chain the automatic projection
	// retention sweep after the cache reset so a blue-green switch closes
	// its own lifecycle instead of leaving retired collections forever.
	manager.OnAliasSwitch = func() {
		searcher.ResetSearchCache()
		if cfg.QdrantCfg == nil || cfg.QdrantCfg.ProjectionRetention <= 0 {
			return
		}
		// Best-effort post-switch retention: drop retired collections
		// beyond the configured rollback keep-count. This runs AFTER the
		// alias switch has committed, so a failure here never affects the
		// switch itself.
		sweepCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		res, err := manager.CleanupWithConfig(sweepCtx, collections.RetentionConfig{
			RetentionDays: 1, // keep-last-N sweep; the day gate is not the limiter
			KeepLastN:     cfg.QdrantCfg.ProjectionRetention,
		})
		if err != nil {
			log.Warn("post-switch projection retention sweep failed", zap.Error(err))
			return
		}
		if res.CollectionsDropped > 0 {
			log.Info("post-switch projection retention swept retired collections",
				zap.Int("dropped", res.CollectionsDropped),
				zap.Strings("names", res.DroppedNames))
		}
	}
	health := disasterrecovery.NewHealthProbe(client)
	cleaner := maintenance.NewLocatorCleaner(client, schema, log)
	searchAdapter := search.NewSearchAdapter(searcher, log)

	log.Info("QdrantRuntime constructed",
		zap.String("schema_version", schema.Version),
		zap.String("runtime_alias", schema.RuntimeAlias),
		zap.Bool("mapper_wired", mapper != nil),
	)

	return &QdrantRuntime{
		Schema:        schema,
		Client:        client,
		Writer:        writer,
		Searcher:      searcher,
		Manager:       manager,
		Health:        health,
		Cleaner:       cleaner,
		Mapper:        mapper,
		Store:         store,
		SearchAdapter: searchAdapter,
	}, nil
}

// PR 4 sentinel constants were removed in the round-3 polish: the
// source-level enforcement lives in composition_test.go's freeze tests
// (TestComposition_Frozen*) which grep internal/app/*.go directly.
// Markdown-only sentinels here added noise without enforcement. The
// freeze tests are the canonical gate; this comment is the breadcrumb.
