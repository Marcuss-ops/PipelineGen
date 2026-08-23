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
// File layout (PR3 mechanical split, June 2026; consolidated Phase 5 June 2026):
//
//	types.go                 Package doc + Point struct + DR type aliases
//	                          + PointPayload (consolidated from canonical.go,
//	                          point_types.go, types_dr.go, filter_types.go,
//	                          snapshot_types.go, api_errors.go — Phase 5).
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
//	search_types.go          SearchRequest + HybridSearchRequest + SparseQueryVector
//	                          + SearchResult + ScrollResult + ScrollPoint
//	                          + DeadLetterChecker + GoldenQueryRunner
//	                          + Filter marker (inline map[string]any)
//	error types:             APIError + sentinel errors → errors.go (PR1)
//
// Co-located files: client.go + client_*.go (PR2), errors.go (PR1),
// dr_adapter.go, client_dr.go (snapshot+restore methods).
//
// All types stay in the same package `qdrant`; PR3 is a relocation pass — every
// type body, JSON tag, and method receiver is preserved 1:1 against the
// pre-split types.go.
package schema

import (
	qdrantdr "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/qdrantdr"
)

// ── Point (canonical upsert wire shape) ────────────────────────────────
// Relocated from point_types.go (Phase 5 consolidation, June 2026).

// Point is a single Qdrant point ready for upsert.
// Note: the Vectors field uses the Qdrant REST API key "vector" (singular).
type Point struct {
	ID      string         `json:"id"`
	Vectors map[string]any `json:"vector"`
	Payload map[string]any `json:"payload"`
}

// ── DR type aliases + PointPayload ─────────────────────────────────────
// Relocated from types_dr.go (Phase 5 consolidation, June 2026).

// SnapshotDescription is the canonical DR snapshot shape (type alias).
type SnapshotDescription = qdrantdr.SnapshotDescription

// PointPayload is the per-point payload write shape used by the
// Qdrant REST /points/payload endpoint with `merge=true`. Distinct
// from Point: Point carries vectors, PointPayload does NOT. The
// canonical use is the reaper service which needs to overwrite a
// subset of payload keys without touching vectors (UpsertPoints
// would null vectors on partial payload, which was the prior bug
// the reaper commit 07292503 fixed in the qdrant.reaper path).
type PointPayload struct {
	ID      string         `json:"id"`
	Payload map[string]any `json:"payload"`
}
