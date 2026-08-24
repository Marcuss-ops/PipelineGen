// Package mediamemory — port surface re-exports from
// capabilities/mediamemory (godlike/06 SSOT: one canonical owner).
package mediamemory

import "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediamemory"

type Logger = mediamemory.Logger
type Clock = mediamemory.Clock
type MetricsSink = mediamemory.MetricsSink
type ConceptRepository = mediamemory.ConceptRepository
type BindingRepository = mediamemory.BindingRepository
type BindingMutationDispatcher = mediamemory.BindingMutationDispatcher
type QueryCacheRepository = mediamemory.QueryCacheRepository
type CandidateRepository = mediamemory.CandidateRepository
type UsageRepository = mediamemory.UsageRepository
type SearchFanOut = mediamemory.SearchFanOut
type SemanticLookup = mediamemory.SemanticLookup
type KeyframeVisualIndexer = mediamemory.KeyframeVisualIndexer
type EmbeddingIndexer = mediamemory.EmbeddingIndexer
type StockPipelineAcquirer = mediamemory.StockPipelineAcquirer
type RightsValidator = mediamemory.RightsValidator
type TranscriptExtractor = mediamemory.TranscriptExtractor
type KeyframeExtractor = mediamemory.KeyframeExtractor
type VisualDescriptionGenerator = mediamemory.VisualDescriptionGenerator
type EntityDetector = mediamemory.EntityDetector
type EmbeddingEncoder = mediamemory.EmbeddingEncoder
type LinkerWorker = mediamemory.LinkerWorker

var NoopLogger = mediamemory.NoopLogger
var RealClock = mediamemory.RealClock
var NoopMetrics = mediamemory.NoopMetrics