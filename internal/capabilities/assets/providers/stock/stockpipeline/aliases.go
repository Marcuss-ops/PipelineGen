// Package stockpipeline — aliases.go.
//
// Public compatibility surface for the stock pipeline. The implementation now
// lives in the core engine and in the per-phase subpackages (plan, ingest,
// extract, publish, finalize, cleanup); this file keeps the historical
// stockpipeline identifiers available to existing callers and fixtures.
package stockpipeline

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/core"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/extract"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/finalize"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/ingest"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/pipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/plan"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/publish"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── Core data types ──────────────────────────────────────────────────────────

type (
	RunInput                 = core.RunInput
	ClipSpec                 = core.ClipSpec
	ChunkMetadataInput       = core.ChunkMetadataInput
	PipelineMetadata         = core.PipelineMetadata
	ChunkMeta                = core.ChunkMeta
	SourceInfo               = core.SourceInfo
	ClipInfo                 = core.ClipInfo
	PipelineInfo             = core.PipelineInfo
	PipelineResult           = core.PipelineResult
	ChunkResult              = core.ChunkResult
	VideoSource              = core.VideoSource
	StagedSource             = core.StagedSource
	PipelineConfig           = core.PipelineConfig
	ClipPlan                 = core.ClipPlan
	ClipPlanner              = core.ClipPlanner
	ChunkState               = core.ChunkState
	MetadataState            = core.MetadataState
	StockRunMetadata         = core.StockRunMetadata
	ChunkMetadataEntry       = core.ChunkMetadataEntry
	RunCounts                = core.RunCounts
	StageSnapshot            = core.StageSnapshot
	RunSummary               = core.RunSummary
	Step                     = core.Step
	StepRunner               = core.StepRunner
	RunState                 = core.RunState
	Orchestrator             = core.Orchestrator
	OrchestratorConfig       = core.OrchestratorConfig
	OrchestratorDeps         = core.OrchestratorDeps
	SourceDurationProbe      = core.SourceDurationProbe
	VideoInfo                = core.VideoInfo
	ChannelLister            = core.ChannelLister
	SourceDownloader         = core.SourceDownloader
	SourceDownloadRequest    = core.SourceDownloadRequest
	DownloadedSource         = core.DownloadedSource
	LocalFSPort              = core.LocalFSPort
	DriveFileInfo            = core.DriveFileInfo
	DriveReaderPort          = core.DriveReaderPort
	DestinationArtifact      = core.DestinationArtifact
	DestinationArtifactStore = core.DestinationArtifactStore
	DestinationReconciler    = core.DestinationReconciler
	StaleArtifactReport      = core.StaleArtifactReport
	StockBatch               = core.StockBatch
	StockBatchGroup          = core.StockBatchGroup
	StockArtifact            = core.StockArtifact
	StockBatchRepository     = core.StockBatchRepository
	ArtifactState            = core.ArtifactState
	BatchState               = core.BatchState
	GroupState               = core.GroupState
	StockAssetUpsertRecord   = core.StockAssetUpsertRecord
	stockChunkDispatcher     = core.StockChunkDispatcher
	ManifestBuilder          = core.ManifestBuilder
	TransactionalAssetWriter = core.TransactionalAssetWriter
	ProjectionPort           = core.ProjectionPort
	stockManifestBuilder     = core.StockManifestBuilder
	noopWriter               = core.NoopWriter
	noopProjection           = core.NoopProjection

	StockPlanStep         = plan.StockPlanStep
	StockStageSourcesStep = ingest.StockStageSourcesStep
	StockExtractClipsStep = extract.StockExtractClipsStep
	StockPublishStep      = publish.StockPublishStep
	StockFinalizeStep     = finalize.StockFinalizeStep
)

// ── Cut DTOs ─────────────────────────────────────────────────────────────────

type (
	CutRequest     = core.CutRequest
	CutJob         = core.CutJob
	CutItemStatus  = core.CutItemStatus
	CutItemResult  = core.CutItemResult
	CutBatchResult = core.CutBatchResult
	Clip           = core.Clip
	VideoCutter    = core.VideoCutter
)

const (
	CutItemStatusUnknown     = core.CutItemStatusUnknown
	CutItemStatusSucceeded   = core.CutItemStatusSucceeded
	CutItemStatusFailed      = core.CutItemStatusFailed
	CutItemStatusValidated   = core.CutItemStatusValidated
	CutItemStatusProbeFailed = core.CutItemStatusProbeFailed
)

// ── Canonical constants ──────────────────────────────────────────────────────

const (
	ArtifactStatePlanned         = core.ArtifactStatePlanned
	ArtifactStateExtracting      = core.ArtifactStateExtracting
	ArtifactStateExtracted       = core.ArtifactStateExtracted
	ArtifactStateComposing       = core.ArtifactStateComposing
	ArtifactStateComposed        = core.ArtifactStateComposed
	ArtifactStatePublishing      = core.ArtifactStatePublishing
	ArtifactStatePublished       = core.ArtifactStatePublished
	ArtifactStateVerified        = core.ArtifactStateVerified
	ArtifactStateRetryWait       = core.ArtifactStateRetryWait
	ArtifactStateFailedPermanent = core.ArtifactStateFailedPermanent
	ArtifactStateQuarantined     = core.ArtifactStateQuarantined

	BatchStatePlanned   = core.BatchStatePlanned
	BatchStateRunning   = core.BatchStateRunning
	BatchStateSucceeded = core.BatchStateSucceeded
	BatchStateFailed    = core.BatchStateFailed
	BatchStateRetryWait = core.BatchStateRetryWait

	GroupStatePlanned   = core.GroupStatePlanned
	GroupStateRunning   = core.GroupStateRunning
	GroupStateSucceeded = core.GroupStateSucceeded
	GroupStateFailed    = core.GroupStateFailed
	GroupStateRetryWait = core.GroupStateRetryWait

	StockArtifactIdMetadata  = core.StockArtifactIdMetadata
	StockArtifactIdThumbnail = core.StockArtifactIdThumbnail
	StockArtifactIdBindings  = core.StockArtifactIdBindings
	StockArtifactIdReport    = core.StockArtifactIdReport
	StockArtifactIdSummary   = core.StockArtifactIdSummary

	DefaultMaxConcurrentJobs      = core.DefaultMaxConcurrentJobs
	DefaultMaxConcurrentDownloads = core.DefaultMaxConcurrentDownloads
	DefaultOrchestratorJobId      = core.DefaultOrchestratorJobId

	StepKeyStockPlan         = core.StepKeyStockPlan
	StepKeyStockStageSources = core.StepKeyStockStageSources
	StepKeyStockExtractClips = core.StepKeyStockExtractClips
	StepKeyStockPublish      = core.StepKeyStockPublish
	StepKeyStockFinalize     = core.StepKeyStockFinalize

	DownloadModeSectionsOnly = core.DownloadModeSectionsOnly

	SourceProviderYouTube = core.SourceProviderYouTube
	SourceProviderPexels  = core.SourceProviderPexels
	SourceProviderPixabay = core.SourceProviderPixabay
	SourceProviderUnknown = core.SourceProviderUnknown

	StockTimestampPolicyVersionV1 = core.StockTimestampPolicyVersionV1
	IndexingStatusPending         = core.IndexingStatusPending
	StockAssetKind                = core.StockAssetKind
	StockSemanticRole             = core.StockSemanticRole
)

// ── Typed sentinels ──────────────────────────────────────────────────────────

var (
	ErrOrchestratorNilDeps                 = core.ErrOrchestratorNilDeps
	ErrNoOpCutter                          = core.ErrNoOpCutter
	ErrStockNoChunksFinalized              = core.ErrStockNoChunksFinalized
	ErrStockChunkNotFinalized              = core.ErrStockChunkNotFinalized
	ErrStockChunkHashMissing               = core.ErrStockChunkHashMissing
	ErrStockChunkHashInvalid               = core.ErrStockChunkHashInvalid
	ErrStockMetadataHashInvalid            = core.ErrStockMetadataHashInvalid
	ErrStockChunkLocalMissing              = core.ErrStockChunkLocalMissing
	ErrStockMetadataNotPublished           = core.ErrStockMetadataNotPublished
	ErrStockPublishArtifactFailed          = core.ErrStockPublishArtifactFailed
	ErrStockFinalizeSpineFailed            = core.ErrStockFinalizeSpineFailed
	ErrStockFinalizeLeaseMissing           = core.ErrStockFinalizeLeaseMissing
	ErrStockFnRequired                     = core.ErrStockFnRequired
	ErrStockStageSourcesAllFailed          = core.ErrStockStageSourcesAllFailed
	ErrStockStageSourcesIncomplete         = core.ErrStockStageSourcesIncomplete
	ErrStockExtractClipsCutterRequired     = core.ErrStockExtractClipsCutterRequired
	ErrStockExtractClipsLocalFSRequired    = core.ErrStockExtractClipsLocalFSRequired
	ErrStockExtractClipsDurableStateFailed = core.ErrStockExtractClipsDurableStateFailed
	ErrStockPublishStateLost               = core.ErrStockPublishStateLost
	ErrStockFinalizeStateLost              = core.ErrStockFinalizeStateLost
	ErrFinalizerAbsent                     = core.ErrFinalizerAbsent
	ErrStockResumeStateReadFailed          = core.ErrStockResumeStateReadFailed
	ErrStockResumeStateInvalid             = core.ErrStockResumeStateInvalid
	ErrStockManifestUnprojectable          = core.ErrStockManifestUnprojectable
	ErrManifestIncomplete                  = core.ErrManifestIncomplete
	ErrAtomicDispatchFailed                = core.ErrAtomicDispatchFailed
	ErrProjectionResilience                = core.ErrProjectionResilience
	ErrNoProducedChunk                     = core.ErrNoProducedChunk
	ErrMetadataMissing                     = core.ErrMetadataMissing
	ErrStockProductionArtifactPrepMissing  = core.ErrStockProductionArtifactPrepMissing
	ErrStockProductionJobFinalizerMissing  = core.ErrStockProductionJobFinalizerMissing
	ErrProductionCutterMissing             = pipeline.ErrProductionCutterMissing
	ErrPlannerBudgetTooSmall               = plan.ErrPlannerBudgetTooSmall
	ErrExplicitPlannerNoClips              = plan.ErrExplicitPlannerNoClips
)

// ── Compatibility functions ──────────────────────────────────────────────────

var (
	NewDeterministicPlanner         = plan.NewDeterministicPlanner
	NewExplicitPlanner              = plan.NewExplicitPlanner
	InferSourceProvider             = core.InferSourceProvider
	NormalizeSourceURL              = ingest.NormalizeSourceURL
	ExtractVideoID                  = ingest.ExtractVideoID
	StagingSourceURL                = ingest.StagingSourceURL
	InferSourceVideoID              = core.InferSourceVideoID
	IsSectionedRun                  = core.IsSectionedRun
	SectionWindowForPlans           = core.SectionWindowForPlans
	SectionDownloadString           = core.SectionDownloadString
	SectionTimestamp                = core.SectionTimestamp
	VerifyChunks                    = core.VerifyChunks
	VerifyMetadata                  = core.VerifyMetadata
	ReconcileDestination            = core.ReconcileDestination
	ManifestBytes                   = core.ManifestBytes
	DeriveRunCounts                 = core.DeriveRunCounts
	ValidateRunCounts               = core.ValidateRunCounts
	BuildChunkedStockManifest       = core.BuildChunkedStockManifest
	BuildStockRunMetadata           = core.BuildStockRunMetadata
	WriteAndHashMetadata            = core.WriteAndHashMetadata
	ChunkArtifactID                 = core.ChunkArtifactID
	ChunkArtifactFilename           = core.ChunkArtifactFilename
	MetadataArtifactID              = core.MetadataArtifactID
	TimestampArtifactID             = core.TimestampArtifactID
	ProjectManifestToPipelineResult = pipeline.ProjectManifestToPipelineResult
	NewTestStockOrchestrator        = pipeline.NewTestStockOrchestrator
	newStockPipeline                = pipeline.NewStockPipeline
	manifestBytes                   = core.ManifestBytes
	buildStockRunMetadata           = core.BuildStockRunMetadata
	NewProductionStockOrchestrator  = pipeline.NewProductionStockOrchestrator
	DefaultStockSteps               = pipeline.DefaultStockSteps
	DefaultPipelineConfig           = core.DefaultPipelineConfig
	RootFolderName                  = publish.RunInputRootFolderName
	ResolvedFolderID                = publish.RunInputResolvedFolderID
	TimestampGroupName              = publish.RunInputTimestampGroupName
	TimestampParentGroupName        = publish.RunInputTimestampParentGroupName
	ClipFolderName                  = publish.RunInputClipFolderName
	PerClipLeafName                 = publish.PerClipLeafName
	SlugifyTitle                    = publish.SlugifyTitle
)

type (
	ProductionPipelineDeps      = pipeline.ProductionPipelineDeps
	ProductionPersistenceDeps   = pipeline.ProductionPersistenceDeps
	ProductionRuntimeDeps       = pipeline.ProductionRuntimeDeps
	ProductionStockPipelineDeps = pipeline.ProductionStockPipelineDeps
)

// projectManifestToPipelineResult keeps the historical unexported helper name
// used by the remaining root-file call sites.

func projectManifestToPipelineResult(manifest *job.ArtifactManifest) (*PipelineResult, error) {
	return pipeline.ProjectManifestToPipelineResult(manifest)
}

type stockDispatcherWriter = core.StockDispatcherWriter
