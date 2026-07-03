// Package qdrant — aliases.go forwards types moved to sub-packages during
// HIGH #10 (July 2026) so external importers (internal/app/*, internal/api/*,
// cmd/admin/*, internal/application/qdrant/*) continue to compile without
// import-path changes.
//
// Each alias below mirrors the original single-package surface. When a
// future migration removes the last external reference to a particular
// type, the corresponding alias SHOULD be deleted in the same commit
// so the tree shrinks monotonically (godlike/07 forward-contract).
package qdrant

import (
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/collections"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/disasterrecovery"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/indexing"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/maintenance"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/search"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/verification"
)

// ── transport ────────────────────────────────────────────────────────

type Client = transport.Client
type Config = schema.Config
type APIError = transport.APIError
type ErrCollectionNotFound = transport.ErrCollectionNotFound
type ErrAliasNotFound = transport.ErrAliasNotFound
type ErrSchemaIncompatible = transport.ErrSchemaIncompatible
type ErrVectorDimensionMismatch = transport.ErrVectorDimensionMismatch
type ErrNaNOrInf = transport.ErrNaNOrInf
type ErrEmptyVector = transport.ErrEmptyVector
type ErrChannelUnavailable = transport.ErrChannelUnavailable
type ErrAliasSwitchNotReady = transport.ErrAliasSwitchNotReady
type ErrReindexRequired = transport.ErrReindexRequired
type ErrSparseRequired = transport.ErrSparseRequired
type PartialUpsertError = transport.PartialUpsertError
type AssetUpsertFailure = transport.AssetUpsertFailure

var NewClient = transport.NewClient
var DefaultConfig = schema.DefaultConfig
var IsRetryable = transport.IsRetryable
var NewErrSchemaIncompatible = transport.NewErrSchemaIncompatible

// ── schema (already a sub-package, re-export for convenience) ────────

type IndexSchema = schema.IndexSchema
type EmbeddingSpec = schema.EmbeddingSpec
type SearchRequest = schema.SearchRequest
type SearchResult = schema.SearchResult
type HybridSearchRequest = schema.HybridSearchRequest
type Point = schema.Point
type SchemaDiff = schema.SchemaDiff
type ScrollPoint = schema.ScrollPoint
type ScrollResult = schema.ScrollResult
type SnapshotDescription = schema.SnapshotDescription
type IndexDocument = indexing.IndexDocument

var DefaultV3Schema = schema.DefaultV3Schema
var AssetIDToQdrantPointID = schema.AssetIDToQdrantPointID
var PipelineGenQdrantNamespace = schema.PipelineGenQdrantNamespace

// ── search ───────────────────────────────────────────────────────────

type Searcher = search.Searcher
type TextEmbedder = search.TextEmbedder
type ImageEmbedder = search.ImageEmbedder
type AudioEmbedder = search.AudioEmbedder
type ImageEmbedderConfig = search.ImageEmbedderConfig

var NewSearcher = search.NewSearcher
var NewTextEmbedderAdapter = search.NewTextEmbedderAdapter
var NewImageEmbedderAdapter = search.NewImageEmbedderAdapter
var NewSearchAdapter = search.NewSearchAdapter
var NewClipSearchAdapter = search.NewClipSearchAdapter
var NewStockSearchAdapter = search.NewStockSearchAdapter
var NewOutboxEventsDeadLetterAdapter = search.NewOutboxEventsDeadLetterAdapter

// ── indexing ─────────────────────────────────────────────────────────

type IndexWriter = indexing.IndexWriter
type PayloadMapper = indexing.PayloadMapper
type SQLiteAssetStore = indexing.SQLiteAssetStore
type AssetStore = indexing.AssetStore
type AssetData = indexing.AssetData
type ReindexResult = schema.ReindexResult

var NewIndexWriter = indexing.NewIndexWriter
var NewPayloadMapper = indexing.NewPayloadMapper
var NewSQLiteAssetStore = indexing.NewSQLiteAssetStore
var ValidatePoint = indexing.ValidatePoint

// ── collections ──────────────────────────────────────────────────────

type CollectionManager = collections.CollectionManager
type RetentionConfig = collections.RetentionConfig

var NewCollectionManager = collections.NewCollectionManager

// ── verification ─────────────────────────────────────────────────────

type ReindexVerifier = verification.ReindexVerifier
type SchemaRegistry = verification.SchemaRegistry

// ResolveSchema resolves a registered schema version and returns a deep
// copy. Empty version defaults to "v3". PR #11 (July 2026): replaces
// the former mutable var DefaultSchemaRegistry.
var ResolveSchema = verification.ResolveSchema
var MustResolveSchema = verification.MustResolveSchema
var RegisteredVersions = verification.RegisteredVersions

var NewReindexVerifier = verification.NewReindexVerifier

// ── maintenance ──────────────────────────────────────────────────────

type LocatorCleaner = maintenance.LocatorCleaner
type Reaper = maintenance.Reaper

var NewLocatorCleaner = maintenance.NewLocatorCleaner
var NewReaper = maintenance.NewReaper
var NewSnapshotStoreAdapter = maintenance.NewSnapshotStoreAdapter
var NewVerifierAdapter = maintenance.NewVerifierAdapter
var NewRetentionExecutorAdapter = maintenance.NewRetentionExecutorAdapter
var NewCollectionCreatorAdapter = maintenance.NewCollectionCreatorAdapter
var NewPromDRMetricsAdapter = maintenance.NewPromDRMetricsAdapter

// ── disasterrecovery ─────────────────────────────────────────────────

type HealthProbe = disasterrecovery.HealthProbe

var NewHealthProbe = disasterrecovery.NewHealthProbe

// ── root (runtime.go — stays in the root package) ────────────────────

// QdrantRuntime and RuntimeConfig are defined directly in runtime.go
// (same package qdrant). No aliases needed.
