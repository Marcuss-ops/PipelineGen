// Package qdrant provides the canonical Qdrant vector-database infrastructure
// for PipelineGen. It implements schema-versioned collections, atomic aliases,
// configurable payload mapping, and real-model embedding contracts as specified
// by QDRANT-003.
//
// Architecture:
//   - Physical collections are immutable w.r.t. vector schema.
//   - Breaking changes create a new physical collection.
//   - Runtime reads/writes go through a canonical alias.
//   - Alias switch only after reindex and verification.
//   - SQLite holds the canonical index version per asset.
//   - No synthetic/fake vectors are ever written.
//
// File layout (PR3 mechanical split, June 2026):
//
//	types.go                 Package doc only (this file). Historical home
//	                          of the package's type families.
//	schema_types.go          EmbeddingSpec + SparseSpec + PayloadIndexSpec
//	                          + IndexSchema (+ physicalName) + manifest constants
//	                          + IndexHealthReport + IndexWriterPort
//	collection_types.go      Config + DefaultConfig + CollectionInfo
//	                          + SparseConfig + VectorConfig + PayloadIndexInfo
//	                          + sortPayloadIndexes + SchemaDiff/DimensionDiff/DistanceDiff
//	                          + ReindexResult + SwitchReport + LocatorCleanupReport
//	collection_wire.go       CollectionInfo.UnmarshalJSON + result-shape
//	                          decode helpers (unmarshalQdrantEnvelope,
//	                          unmarshalLegacyLeaf) + json.Unmarshaler assertion
//	point_types.go           Point (the upsert wire shape). PointPayload lives
//	                          in types_dr.go (infra-only, QDRANT-005C PR3)
//	search_types.go          SearchRequest + HybridSearchRequest + SparseQueryVector
//	                          + SearchResult + ScrollResult + ScrollPoint
//	                          + DeadLetterChecker + GoldenQueryRunner
//	filter_types.go          (doc-only marker — no dedicated Filter/Condition/Match
//	                          types; filters are inline map[string]interface{})
//	snapshot_types.go        (doc-only marker — SnapshotDescription is a type
//	                          alias in types_dr.go since QDRANT-005C PR3)
//	api_errors.go            (doc-only marker — APIError + sentinel errors live
//	                          in errors.go since PR1)
//
// Co-located files (NOT touched by PR3): client.go + client_*.go (PR2 split),
// errors.go (PR1 wire-level error DTO), types_dr.go (QDRANT-005C PR3 DR shapes),
// dr_adapter.go (QDRANT-005C PR3 port adapters), client_dr.go (snapshot methods).
//
// All types stay in the same package `qdrant`; PR3 is a relocation pass — every
// type body, JSON tag, and method receiver is preserved 1:1 against the
// pre-split types.go.
package qdrant
