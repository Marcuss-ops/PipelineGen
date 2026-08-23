// schema_aliases.go — type aliases for types moved to schema/ sub-package.
//
// Increment 1 (June 2026): 7 leaf files extracted to schema/.
// Every exported type, constant, and function re-exported here so
// the 13 external import sites keep compiling without path changes.
package verification

import (
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

// ── schema_types.go ────────────────────────────────────────────────────

type EmbeddingSpec = schema.EmbeddingSpec
type SparseSpec = schema.SparseSpec
type PayloadIndexSpec = schema.PayloadIndexSpec
type IndexSchema = schema.IndexSchema
type IndexHealthReport = schema.IndexHealthReport
type IndexWriterPort = schema.IndexWriterPort

const CurrentEmbeddingVersion = schema.CurrentEmbeddingVersion
const CurrentSearchTextVersion = schema.CurrentSearchTextVersion
const DefaultSparseModel = schema.DefaultSparseModel

// ── schema.go ──────────────────────────────────────────────────────────func DefaultV() *IndexSchema        { return DefaultV3Schema() }
func DefaultV3Schema() *IndexSchema { return schema.DefaultV3Schema() }
func CompareSchema(expected *IndexSchema, actual *CollectionInfo) *SchemaDiff {
	return schema.CompareSchema(expected, actual)
}

// ── types.go ───────────────────────────────────────────────────────────

type Point = schema.Point
type SnapshotDescription = schema.SnapshotDescription
type PointPayload = schema.PointPayload

// ── search_types.go ────────────────────────────────────────────────────

type SearchRequest = schema.SearchRequest
type HybridSearchRequest = schema.HybridSearchRequest
type SparseQueryVector = schema.SparseQueryVector
type SearchResult = schema.SearchResult
type ScrollResult = schema.ScrollResult
type ScrollPoint = schema.ScrollPoint
type DeadLetterChecker = schema.DeadLetterChecker
type GoldenQueryRunner = schema.GoldenQueryRunner

// ── collection_types.go ────────────────────────────────────────────────

type Config = schema.Config
type CollectionInfo = schema.CollectionInfo
type SparseConfig = schema.SparseConfig
type VectorConfig = schema.VectorConfig
type PayloadIndexInfo = schema.PayloadIndexInfo
type SchemaDiff = schema.SchemaDiff
type DimensionDiff = schema.DimensionDiff
type DistanceDiff = schema.DistanceDiff
type ReindexResult = schema.ReindexResult
type SwitchReport = schema.SwitchReport
type LocatorCleanupReport = schema.LocatorCleanupReport

func DefaultConfig() *Config { return schema.DefaultConfig() }

// ── pointid.go ─────────────────────────────────────────────────────────

var PipelineGenQdrantNamespace = schema.PipelineGenQdrantNamespace

// Re-exports of unexported symbols that test files still reference.
func IsValidDistance(d string) bool  { return schema.IsValidDistance(d) }
func IsValidFieldType(t string) bool { return schema.IsValidFieldType(t) }
