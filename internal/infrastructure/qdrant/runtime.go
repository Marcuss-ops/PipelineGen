// Package qdrant — runtime.go is the single canonical facade for all
// Qdrant infrastructure subsystems. Constructed ONCE at boot.
//
// PR 4 (June 2026, refactor/single-qdrant-runtime) — sections #2 + #6
// of the verdict Qdrant:
//
//   - Before PR 4: qdrant.NewClient was called from
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
// Layering rationale: *QdrantRuntime lives in infrastructure/qdrant
// because its fields are CONCRETE types (*Client, *IndexWriter, etc.).
// The port surfaces derived from it (e.g. outbox.VectorPointDeleter)
// live in the application layer per AGENTS.md Pattern 0.
package qdrant

import (
	"database/sql"
	"fmt"

	"go.uber.org/zap"

	appsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
)

// RuntimeConfig is the bundle NewRuntime consumes. Avoiding a direct
// dependency on platform/config keeps the qdrant package free of
// platform-layer imports — composition.go translates platform.Config
// to RuntimeConfig at the boundary.
type RuntimeConfig struct {
	// QdrantCfg is the canonical wire/auth configuration (base URL,
	// api key, timeout). Required.
	QdrantCfg *Config
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
	Schema *IndexSchema
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
	Schema *IndexSchema
	// Client is the canonical *qdrant.Client used for every HTTP
	// round-trip. ONE Client for the whole runtime — wire-step
	// invariants (api-key header, retry policy, timeout) cannot drift
	// between subsystems.
	Client *Client
	// Writer is the canonical IndexWriter; satisfies
	// outbox.VectorPointDeleter and clipindexer.VectorStoreIndexer.
	Writer *IndexWriter
	// Searcher is the canonical ANN searcher.
	Searcher *Searcher
	// Manager is the canonical CollectionManager (EnsureSchema +
	// alias switch). Used by wire_services.go's qdrant-collection
	// startup step.
	Manager *CollectionManager
	// Health is the canonical readiness probe. Replaces the previous
	// `QdrantHealthProbe any` carrier field on ProcessBundle.
	Health *HealthProbe
	// Cleaner is the canonical LocatorCleaner (QDRANT-005 Fase 3).
	Cleaner *LocatorCleaner
	// Mapper is the canonical PayloadMapper used by the Writer.
	// nil when RuntimeConfig.DB was nil at NewRuntime call time.
	Mapper *PayloadMapper
	// Store is the canonical SQLiteAssetStore used by Mapper.
	// nil when RuntimeConfig.DB was nil at NewRuntime call time.
	Store *SQLiteAssetStore
	// SearchAdapter is the canonical appsearch.VectorStorePort
	// surface wired through NewSearchAdapter (in same-package
	// search_adapter.go). Composition root reads this field via
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
		return nil, fmt.Errorf("qdrant.NewRuntime: RuntimeConfig.QdrantCfg is nil (composition forgot to translate cfg.Qdrant to RuntimeConfig?)")
	}
	if cfg.DB == nil {
		return nil, fmt.Errorf("qdrant.NewRuntime: RuntimeConfig.DB is nil (Mapper requires SQLiteAssetStore; pass dbs.main.DB or equivalent)")
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
		resolved, err := ResolveSchema(version)
		if err != nil {
			return nil, fmt.Errorf("qdrant.NewRuntime: %w", err)
		}
		schema = resolved
	}

	client := NewClient(cfg.QdrantCfg, log)

	// DB precondition is enforced above (returns nil, error) — this
	// block is now unconditional: mapper+store are always non-nil.
	store := NewSQLiteAssetStore(cfg.DB)
	mapper := NewPayloadMapper(store, log)

	writer := NewIndexWriter(client, schema, mapper, log)
	searcher := NewSearcher(client, schema, log)
	manager := NewCollectionManager(client, schema, log)
	// QDRANT-ALIAS-CACHE (July 2026): wire the cache invalidation so
	// PromoteCandidate resets the Searcher's alias-target cache
	// atomically with every alias switch.
	manager.OnAliasSwitch = searcher.ResetSearchCache
	health := NewHealthProbe(client)
	cleaner := NewLocatorCleaner(client, schema, log)
	searchAdapter := NewSearchAdapter(searcher, log)

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
