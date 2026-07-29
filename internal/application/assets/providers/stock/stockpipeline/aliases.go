// Package stockpipeline — aliases.go (P3 Sessione A: types extraction).
//
// Type aliases and variable aliases pointing to the canonical definitions
// in the types/ sub-package. These make the types available under their
// original names in the parent package so all existing callers are
// unaffected.
//
// godlike/06 SSOT: types/ owns the canonical definitions; this file is
// the bridge layer.
package stockpipeline

import "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline/types"

// ──── run model (was types_run.go) ────

type ClipSpec = types.ClipSpec
type RunInput = types.RunInput
type ChunkMetadataInput = types.ChunkMetadataInput
type PipelineMetadata = types.PipelineMetadata
type ChunkMeta = types.ChunkMeta
type SourceInfo = types.SourceInfo
type ClipInfo = types.ClipInfo
type PipelineInfo = types.PipelineInfo
type PipelineResult = types.PipelineResult
type ChunkResult = types.ChunkResult
type VideoSource = types.VideoSource
type StagedSource = types.StagedSource

// ──── payloads (was payloads.go) ────

type StockRunPayload = types.StockRunPayload
type StockRunPayloadMetadata = types.StockRunPayloadMetadata

// ──── downloader port (was downloader_port.go) ────

type SourceDownloadRequest = types.SourceDownloadRequest
type DownloadedSource = types.DownloadedSource
type SourceDownloader = types.SourceDownloader

// ──── source ports (was source_ports.go) ────

type VideoInfo = types.VideoInfo
type ChannelLister = types.ChannelLister

// ──── render transitions (was render_transitions.go) ────

type Transition = types.Transition
type TransitionSegment = types.TransitionSegment
type TransitionRenderer = types.TransitionRenderer
type TransitionRegistry = types.TransitionRegistry

// ──── render DTOs + ports + batch (P3 Batch 2) ────

type RenderRequest = types.RenderRequest
type RenderResult = types.RenderResult
type SourceDurationProbe = types.SourceDurationProbe
type CutRequest = types.CutRequest
type CutJob = types.CutJob
type CutItemStatus = types.CutItemStatus
type CutItemResult = types.CutItemResult
type CutBatchResult = types.CutBatchResult
type Clip = types.Clip

type ArtifactState = types.ArtifactState
type BatchState = types.BatchState
type GroupState = types.GroupState
type StockBatch = types.StockBatch
type StockBatchGroup = types.StockBatchGroup
type StockArtifact = types.StockArtifact
type StockBatchRepository = types.StockBatchRepository

// Const aliases from types/ — CutItemStatus enum values.
const (
	CutItemStatusUnknown     = types.CutItemStatusUnknown
	CutItemStatusFailed      = types.CutItemStatusFailed
	CutItemStatusSucceeded   = types.CutItemStatusSucceeded
	CutItemStatusValidated   = types.CutItemStatusValidated
	CutItemStatusProbeFailed = types.CutItemStatusProbeFailed
)

// Const aliases from types/ — artifact / batch / group states.
const (
	ArtifactStatePlanned         = types.ArtifactStatePlanned
	ArtifactStateExtracting      = types.ArtifactStateExtracting
	ArtifactStateExtracted       = types.ArtifactStateExtracted
	ArtifactStateComposing       = types.ArtifactStateComposing
	ArtifactStateComposed        = types.ArtifactStateComposed
	ArtifactStatePublishing      = types.ArtifactStatePublishing
	ArtifactStatePublished       = types.ArtifactStatePublished
	ArtifactStateVerified        = types.ArtifactStateVerified
	ArtifactStateRetryWait       = types.ArtifactStateRetryWait
	ArtifactStateFailedPermanent = types.ArtifactStateFailedPermanent
	ArtifactStateQuarantined     = types.ArtifactStateQuarantined

	BatchStatePlanned   = types.BatchStatePlanned
	BatchStateRunning   = types.BatchStateRunning
	BatchStateSucceeded = types.BatchStateSucceeded
	BatchStateFailed    = types.BatchStateFailed
	BatchStateRetryWait = types.BatchStateRetryWait

	GroupStatePlanned   = types.GroupStatePlanned
	GroupStateRunning   = types.GroupStateRunning
	GroupStateSucceeded = types.GroupStateSucceeded
	GroupStateFailed    = types.GroupStateFailed
	GroupStateRetryWait = types.GroupStateRetryWait
)

// Function wrappers for canonical constructors in types/.
func StockArtifactGroupID(batchID, sourceID string) string { return types.StockArtifactGroupID(batchID, sourceID) }
func StockArtifactID(batchID, sourceID string, clipIdx int) string      { return types.StockArtifactID(batchID, sourceID, clipIdx) }

// ──── planner (P3 Batch 3) ────

type ClipPlan = types.ClipPlan
type ClipPlanner = types.ClipPlanner

var (
	ErrPlannerBudgetTooSmall  = types.ErrPlannerBudgetTooSmall
	ErrExplicitPlannerNoClips = types.ErrExplicitPlannerNoClips
)

func NewDeterministicPlanner() ClipPlanner { return types.NewDeterministicPlanner() }
func NewExplicitPlanner(clips []ClipSpec) ClipPlanner { return types.NewExplicitPlanner(clips) }

// ──── error sentinels (was service_errors.go) ────

var (
	ErrStockPipelineNilCfg                       = types.ErrStockPipelineNilCfg
	ErrStockPipelineNilLog                       = types.ErrStockPipelineNilLog
	ErrStockPipelineNilClipsRepo                 = types.ErrStockPipelineNilClipsRepo
	ErrStockPipelineNilAssetIndex                = types.ErrStockPipelineNilAssetIndex
	ErrStockPipelineNilDispatcher                = types.ErrStockPipelineNilDispatcher
	ErrStockPipelineNilCutter                    = types.ErrStockPipelineNilCutter
	ErrStockPipelineNilRenderer                  = types.ErrStockPipelineNilRenderer
	ErrStockPipelineNilJobs                      = types.ErrStockPipelineNilJobs
	ErrStockPipelineNilPublisher                 = types.ErrStockPipelineNilPublisher
	ErrStockPipelineNilFolderCreator             = types.ErrStockPipelineNilFolderCreator
	ErrStockPipelineNilStepStore                 = types.ErrStockPipelineNilStepStore
	ErrStockPipelineNilSourceStager              = types.ErrStockPipelineNilSourceStager
	ErrStockPipelineNilLocalFS                  = types.ErrStockPipelineNilLocalFS
	ErrStockProductionDBMissing                  = types.ErrStockProductionDBMissing
	ErrStockProductionBatchRepositoryMissing     = types.ErrStockProductionBatchRepositoryMissing
	ErrStockPipelineNilDB                        = types.ErrStockPipelineNilDB
	ErrStockPipelineNilFinalizer                 = types.ErrStockPipelineNilFinalizer
	ErrStockPipelineAllQueriesFailed             = types.ErrStockPipelineAllQueriesFailed
)

// ──── step error sentinels (was orchestrator_step_errors.go) ────

var (
	ErrStockPublishArtifactFailed    = types.ErrStockPublishArtifactFailed
	ErrStockFinalizeSpineFailed      = types.ErrStockFinalizeSpineFailed
	ErrStockComposeChunksAllFailed   = types.ErrStockComposeChunksAllFailed
	ErrStockExtractClipsCutterRequired = types.ErrStockExtractClipsCutterRequired
	ErrStockFinalizeLeaseMissing     = types.ErrStockFinalizeLeaseMissing
	ErrStockFinalizeStateLost        = types.ErrStockFinalizeStateLost
	ErrStockFnRequired               = types.ErrStockFnRequired
	ErrStockPublishStateLost         = types.ErrStockPublishStateLost
	ErrStockResumeStateInvalid       = types.ErrStockResumeStateInvalid
	ErrStockStageSourcesAllFailed    = types.ErrStockStageSourcesAllFailed
	ErrStockStageSourcesIncomplete   = types.ErrStockStageSourcesIncomplete
	ErrFinalizerAbsent               = types.ErrFinalizerAbsent
)
