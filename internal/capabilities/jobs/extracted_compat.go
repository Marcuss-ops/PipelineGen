package jobs

import (
	"context"
	"time"

	"go.uber.org/zap"

	handlers "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/handlers"
	preparation "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/preparation"
	mwm2m "github.com/Marcuss-ops/PipelineGen/internal/capabilities/middleware"
)

// Compatibility surface for the preparation package. The implementation
// lives in jobs/preparation; these aliases keep existing root callers and
// tests on the historical jobs.* API.

type (
	PreparationState                = preparation.PreparationState
	PreparedUnit                    = preparation.PreparedUnit
	PreparationStore                = preparation.PreparationStore
	PreparationUnitClaim            = preparation.PreparationUnitClaim
	PreparationReadyUpdate          = preparation.PreparationReadyUpdate
	PreparationPlanInput            = preparation.PreparationPlanInput
	PreparationJobUnit              = preparation.PreparationJobUnit
	RegisterPreparationJobUnitInput = preparation.RegisterPreparationJobUnitInput

	PreparationPlanner     = preparation.PreparationPlanner
	PreparationPlan        = preparation.PreparationPlan
	PreparationUnit        = preparation.PreparationUnit
	JobPreparationRegistry = preparation.JobPreparationRegistry

	PreparationCoordinator      = preparation.PreparationCoordinator
	PreparationFingerprintInput = preparation.PreparationFingerprintInput
	PreparationLeaseRouter      = preparation.PreparationLeaseRouter
	PreparationMetrics          = preparation.PreparationMetrics
	PreparationWorkEstimator    = preparation.PreparationWorkEstimator
	PreparationAdoptionEvent    = preparation.PreparationAdoptionEvent
	ArtifactClaimer             = preparation.ArtifactClaimer
	ArtifactClaimRequest        = preparation.ArtifactClaimRequest
	ArtifactClaimResult         = preparation.ArtifactClaimResult
	ClaimSnapshotter            = preparation.ClaimSnapshotter
	CheckpointFirstExecutor     = preparation.CheckpointFirstExecutor
	PreparedResultAdopter       = preparation.PreparedResultAdopter
	PreparationUnitLeaser       = preparation.PreparationUnitLeaser
	QueuedJobReader             = preparation.QueuedJobReader
	WorkObservationsReader      = preparation.WorkObservationsReader
	SpeculationDepth            = preparation.SpeculationDepth
	SpeculationCost             = preparation.SpeculationCost
	SpeculationResource         = preparation.SpeculationResource
	ResourceBudget              = preparation.ResourceBudget
	ActiveWorkGate              = preparation.ActiveWorkGate
	SpeculationConfig           = preparation.SpeculationConfig
	SpeculationCandidate        = preparation.SpeculationCandidate
	ResourceScheduler           = preparation.ResourceScheduler
	SpeculationScheduler        = preparation.SpeculationScheduler
)

const (
	PreparationPlanned = preparation.PreparationPlanned
	PreparationRunning = preparation.PreparationRunning
	PreparationReady   = preparation.PreparationReady
	PreparationFailed  = preparation.PreparationFailed
	PreparationStale   = preparation.PreparationStale

	DepthNPlus1 = preparation.DepthNPlus1
	DepthNPlus2 = preparation.DepthNPlus2
	DepthNPlus3 = preparation.DepthNPlus3

	CostWarm      = preparation.CostWarm
	CostPrepare   = preparation.CostPrepare
	CostSpeculate = preparation.CostSpeculate

	ResourceNetwork = preparation.ResourceNetwork
	ResourceDisk    = preparation.ResourceDisk
	ResourceCPU     = preparation.ResourceCPU
	ResourceLLM     = preparation.ResourceLLM
	ResourceTTS     = preparation.ResourceTTS
	ResourceGPU     = preparation.ResourceGPU
)

var ErrPreparationLeaseBusy = preparation.ErrPreparationLeaseBusy

func ComposeJobPreparationRegistry() (*JobPreparationRegistry, error) {
	return preparation.ComposeJobPreparationRegistry()
}

func NewPreparationCoordinator(reader QueuedJobReader, notifier interface{ Subscribe() <-chan struct{} }, registry *JobPreparationRegistry, scheduler *SpeculationScheduler, lookahead int, execute func(context.Context, SpeculationCandidate) error) (*PreparationCoordinator, error) {
	return preparation.NewPreparationCoordinator(reader, notifier, registry, scheduler, lookahead, execute)
}

func NewPreparationLeaseRouter(leaser PreparationUnitLeaser, claimer ArtifactClaimer, worker string, lease time.Duration) *PreparationLeaseRouter {
	return preparation.NewPreparationLeaseRouter(leaser, claimer, worker, lease)
}

func NewPreparationWorkEstimator(alpha float64) *PreparationWorkEstimator {
	return preparation.NewPreparationWorkEstimator(alpha)
}

func PreparationUnitFingerprint(kind, jobType string, payload []byte, inputs map[string]string, dependsOn []string, processorVersion string) (string, error) {
	return preparation.PreparationUnitFingerprint(kind, jobType, payload, inputs, dependsOn, processorVersion)
}

func DefaultSpeculationConfig() SpeculationConfig {
	return preparation.DefaultSpeculationConfig()
}

func NewSpeculationScheduler(config SpeculationConfig, gate ActiveWorkGate) *ResourceScheduler {
	return preparation.NewSpeculationScheduler(config, gate)
}

// Compatibility surface for the outbox handlers extracted to
// jobs/handlers. The interfaces stay structurally identical to the root
// ports, so production wiring does not need adapter types.

type (
	IndexDeleteHandler          = handlers.IndexDeleteHandler
	AssetDeleter                = handlers.AssetDeleter
	VoiceoverCleanupHandler     = handlers.VoiceoverCleanupHandler
	VoiceoverCleanupDriver      = handlers.VoiceoverCleanupDriver
	DeliveryHandler             = handlers.DeliveryHandler
	DeliveryOperation           = handlers.DeliveryOperation
	Artifact                    = handlers.Artifact
	Destination                 = handlers.Destination
	MetadataExportHandler       = handlers.MetadataExportHandler
	MetadataExportHandlerDeps   = handlers.MetadataExportHandlerDeps
	DriveDeleteHandler          = handlers.DriveDeleteHandler
	AssetPublishedHandler       = handlers.AssetPublishedHandler
	AssetPublishedRequestV1     = handlers.AssetPublishedRequestV1
	BindingIndexingHandler      = handlers.BindingIndexingHandler
	BindingIndexer              = handlers.BindingIndexer
	BindingConceptRepository    = handlers.BindingConceptRepository
	ProviderSyncHandler         = handlers.ProviderSyncHandler
	JobsEnqueuer                = handlers.JobsEnqueuer
	ScriptGenerateQueuedHandler = handlers.ScriptGenerateQueuedHandler
	JobLookup                   = handlers.JobLookup
	M2MQuotaDecision            = handlers.M2MQuotaDecision
)

const (
	DriveDeleteEventType               = handlers.DriveDeleteEventType
	DriveDeleteRequestSchemaVersion    = handlers.DriveDeleteRequestSchemaVersion
	MetadataExportRequestSchemaVersion = handlers.MetadataExportRequestSchemaVersion
	VoiceoverCleanupSchemaVersion      = handlers.VoiceoverCleanupSchemaVersion
	AssetPublishedSchemaVersion        = handlers.AssetPublishedSchemaVersion
	BindingIndexRequestSchemaVersion   = handlers.BindingIndexRequestSchemaVersion
	ProviderSyncJobTypeDrive           = handlers.ProviderSyncJobTypeDrive
	ProviderSyncJobTypeYouTube         = handlers.ProviderSyncJobTypeYouTube
)

var (
	ErrAssetPublishedTerminalEnvelope      = handlers.ErrAssetPublishedTerminalEnvelope
	ErrAssetPublishedPayloadParse          = handlers.ErrAssetPublishedPayloadParse
	ErrAssetPublishedSchemaVersionMismatch = handlers.ErrAssetPublishedSchemaVersionMismatch
	ErrAssetPublishedAssetIDMissing        = handlers.ErrAssetPublishedAssetIDMissing
	ErrAssetPublishedDestinationMissing    = handlers.ErrAssetPublishedDestinationMissing
	ErrAssetPublishedEventIDMissing        = handlers.ErrAssetPublishedEventIDMissing
	ErrAssetPublishedIdempotencyKeyMissing = handlers.ErrAssetPublishedIdempotencyKeyMissing
	ErrUnsupportedProvider                 = handlers.ErrUnsupportedProvider
	ErrSchemaVersionMismatch               = handlers.ErrSchemaVersionMismatch
	ErrOperationUnavailable                = handlers.ErrOperationUnavailable
	ErrUnsupportedOperation                = handlers.ErrUnsupportedOperation
	ErrOperationProviderMismatch           = handlers.ErrOperationProviderMismatch
	ErrRemoteURLRequired                   = handlers.ErrRemoteURLRequired
	ErrUnknownProvider                     = handlers.ErrUnknownProvider
	ErrInvalidMode                         = handlers.ErrInvalidMode
)

func NewIndexDeleteHandler(log *zap.Logger, vectorDeleter VectorPointDeleter, assetDeleter AssetDeleter) *IndexDeleteHandler {
	return handlers.NewIndexDeleteHandler(log, vectorDeleter, assetDeleter)
}

func NewVoiceoverCleanupHandler(driver VoiceoverCleanupDriver, log *zap.Logger) *VoiceoverCleanupHandler {
	return handlers.NewVoiceoverCleanupHandler(driver, log)
}

func NewMetadataExportHandler(deps MetadataExportHandlerDeps) *MetadataExportHandler {
	return handlers.NewMetadataExportHandler(deps)
}

func NewDriveDeleteHandler(log *zap.Logger, drive DriveDeleter, stateReader LifecycleStateReader, stateWriter ClipsLifecycleStateWriter, advancer StateAdvancer) *DriveDeleteHandler {
	return handlers.NewDriveDeleteHandler(log, drive, stateReader, stateWriter, advancer)
}

func NewAssetPublishedHandler(log *zap.Logger) *AssetPublishedHandler {
	return handlers.NewAssetPublishedHandler(log)
}

func NewBindingIndexingHandler(indexer BindingIndexer, concepts BindingConceptRepository, log *zap.Logger) *BindingIndexingHandler {
	return handlers.NewBindingIndexingHandler(indexer, concepts, log)
}

func NewProviderSyncHandler(log *zap.Logger, jobs JobsEnqueuer) *ProviderSyncHandler {
	return handlers.NewProviderSyncHandler(log, jobs)
}

func NewScriptGenerateQueuedHandler(jobs JobLookup) (*ScriptGenerateQueuedHandler, error) {
	return handlers.NewScriptGenerateQueuedHandler(jobs)
}

func checkM2MQuota(client *mwm2m.M2MClient, payload any) *M2MQuotaDecision {
	return handlers.CheckM2MQuota(client, payload)
}

func ComposeSearchText(destination, origin, subject, category, provider string, tags []string, drivePath, contentType string) string {
	return handlers.ComposeSearchText(destination, origin, subject, category, provider, tags, drivePath, contentType)
}
