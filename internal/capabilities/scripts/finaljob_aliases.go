// finaljob_aliases.go — facade for the remote final-job stage.
//
// The 20 final_job*.go files, the overlay window/manifest projections and
// the overlay canvas helpers moved to the leaf scripts/finaljob: they are a
// pure pipeline over the generate contract, the run-stage vocabulary and the
// render-unit projection. The six *Runner methods and the QueueRenderEnqueuer
// publication methods stay in the root (they own run state), and call into
// the package through the aliases below.
package scriptgeneration

import finaljob "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/finaljob"

type (
	FinalJobSubmitter            = finaljob.FinalJobSubmitter
	FinalJobCompiledPlanV2       = finaljob.FinalJobCompiledPlanV2
	FinalJobVideoSegmentPreparer = finaljob.FinalJobVideoSegmentPreparer
	OverlayCanvasSpec            = finaljob.OverlayCanvasSpec
)

const (
	FinalJobReportOwner        = finaljob.FinalJobReportOwner
	heavyPhrasePriorityDefault = finaljob.HeavyPhrasePriorityDefault
)

var (
	ErrFinalJobInvalidPlan          = finaljob.ErrFinalJobInvalidPlan
	ErrFinalJobPending              = finaljob.ErrFinalJobPending
	ErrFinalJobReceiptCheckpoint    = finaljob.ErrFinalJobReceiptCheckpoint
	ErrFinalJobStaleReceipt         = finaljob.ErrFinalJobStaleReceipt
	FreezeFinalJobOverlayPlans      = finaljob.FreezeFinalJobOverlayPlans
	OverlayPlanRenderDigest         = finaljob.OverlayPlanRenderDigest
	RepairFinalJobOverlayCheckpoint = finaljob.RepairFinalJobOverlayCheckpoint
	applyOverlayImageStyle          = finaljob.ApplyOverlayImageStyle
	certifiedLocalizedRenderForClip = finaljob.CertifiedLocalizedRenderForClip
	cloneOverlayGroupLimits         = finaljob.CloneOverlayGroupLimits
	dumpOverlayGateDebug            = finaljob.DumpOverlayGateDebug
	effectiveImageStyle             = finaljob.EffectiveImageStyle
	finalJobAudioInput              = finaljob.FinalJobAudioInput
	finalJobImageRequested          = finaljob.FinalJobImageRequested
	finalJobObjectStoreConfigured   = finaljob.FinalJobObjectStoreConfigured
	finalJobPendingID               = finaljob.FinalJobPendingID
	finalJobPreRenderPreflight      = finaljob.FinalJobPreRenderPreflight
	finalJobReceiptSucceeded        = finaljob.FinalJobReceiptSucceeded
	finalJobReceiptTerminal         = finaljob.FinalJobReceiptTerminal
	finalJobRenderLanguage          = finaljob.FinalJobRenderLanguage
	finalJobSettleContext           = finaljob.FinalJobSettleContext
	isImageOverlayItem              = finaljob.IsImageOverlayItem
	overlayBackgroundFromPayload    = finaljob.OverlayBackgroundFromPayload
	overlayRenderBindsPlan          = finaljob.OverlayRenderBindsPlan
	overlayRenderableCandidates     = finaljob.OverlayRenderableCandidates
	overlayResumeStageAfterRepair   = finaljob.OverlayResumeStageAfterRepair
	overlayStatusBackground         = finaljob.OverlayStatusBackground
	overlayStatusPlanSummary        = finaljob.OverlayStatusPlanSummary
	overlayStatusRequest            = finaljob.OverlayStatusRequest
	reconcileInterruptedFinalJob    = finaljob.ReconcileInterruptedFinalJob
	validateOverlayEditorialDensity = finaljob.ValidateOverlayEditorialDensity
)

type (
	FinalJobAttacher = finaljob.FinalJobAttacher
	FinalJobStarter  = finaljob.FinalJobStarter
)

const (
	overlayGroupLongPhrases  = finaljob.OverlayGroupLongPhrases
	overlayGroupShortPhrases = finaljob.OverlayGroupShortPhrases
)

type (
	OverlayArtifactPublisher = finaljob.OverlayArtifactPublisher
	OverlayPublicationSpec   = finaljob.OverlayPublicationSpec
)

type (
	FinalJobAssetResolver           = finaljob.FinalJobAssetResolver
	FinalJobFinalizeV1              = finaljob.FinalJobFinalizeV1
	FinalJobOverlayCheckpointRepair = finaljob.FinalJobOverlayCheckpointRepair
	FinalJobRuntimeAssetV1          = finaljob.FinalJobRuntimeAssetV1
	FinalJobStockFile               = finaljob.FinalJobStockFile
	FinalJobVideoSegmentRequest     = finaljob.FinalJobVideoSegmentRequest
	finalJobBaseSegment             = finaljob.FinalJobBaseSegment
	finalJobCompiledPlan            = finaljob.FinalJobCompiledPlan
	finalJobFrameWindow             = finaljob.FinalJobFrameWindow
	finalJobOverlayWindow           = finaljob.FinalJobOverlayWindow
	finalJobPlanAsset               = finaljob.FinalJobPlanAsset
	finalJobPlanAudio               = finaljob.FinalJobPlanAudio
	finalJobPlanSegment             = finaljob.FinalJobPlanSegment
	finalJobPlanVideoTrack          = finaljob.FinalJobPlanVideoTrack
)

const (
	FinalJobEnvelopeSchemaV1 = finaljob.FinalJobEnvelopeSchemaV1
	overlayGroupImages       = finaljob.OverlayGroupImages
)

var finalJobStringValue = finaljob.FinalJobStringValue

var (
	BuildFinalJobEnvelopeV1                  = finaljob.BuildFinalJobEnvelopeV1
	BuildFinalJobPayloads                    = finaljob.BuildFinalJobPayloads
	ErrFinalJobCancelRejected                = finaljob.ErrFinalJobCancelRejected
	GoldenOverlayCanvas                      = finaljob.GoldenOverlayCanvas
	NormalizeFinalJobHTTPSRuntimeAssets      = finaljob.NormalizeFinalJobHTTPSRuntimeAssets
	PublishFinalJobLocalObject               = finaljob.PublishFinalJobLocalObject
	PublishFinalJobLocalObjectVerified       = finaljob.PublishFinalJobLocalObjectVerified
	ValidateOverlayPlanRenderBinding         = finaljob.ValidateOverlayPlanRenderBinding
	alignFinalJobCompiledPlanAssetIDs        = finaljob.AlignFinalJobCompiledPlanAssetIDs
	alignFinalJobSegmentSourceWindows        = finaljob.AlignFinalJobSegmentSourceWindows
	appendFinalJobRuntimeAsset               = finaljob.AppendFinalJobRuntimeAsset
	cloneOverlayPlan                         = finaljob.CloneOverlayPlan
	compositeVideoScene                      = finaljob.CompositeVideoScene
	enforceFinalJobMinimumSceneDuration      = finaljob.EnforceFinalJobMinimumSceneDuration
	enforceFinalJobPreflight                 = finaljob.EnforceFinalJobPreflight
	errFinalJobOptionalOverlayNoFreeGap      = finaljob.ErrFinalJobOptionalOverlayNoFreeGap
	finalJobAttemptIdempotencyKey            = finaljob.FinalJobAttemptIdempotencyKey
	finalJobAudioAsset                       = finaljob.FinalJobAudioAsset
	finalJobCompiledPlanPayload              = finaljob.FinalJobCompiledPlanPayload
	finalJobCompiledPlanPayloadWithStockPool = finaljob.FinalJobCompiledPlanPayloadWithStockPool
	finalJobCompleteVideoFrameCoverage       = finaljob.FinalJobCompleteVideoFrameCoverage
	finalJobComposePlanSegments              = finaljob.FinalJobComposePlanSegments
	finalJobDedicatedOverlayRows             = finaljob.FinalJobDedicatedOverlayRows
	finalJobEntityImageGate                  = finaljob.FinalJobEntityImageGate
	finalJobFrameAtUS                        = finaljob.FinalJobFrameAtUS
	finalJobFrameStartUS                     = finaljob.FinalJobFrameStartUS
	finalJobIdempotencyKey                   = finaljob.FinalJobIdempotencyKey
	finalJobIdentitySuffix                   = finaljob.FinalJobIdentitySuffix
	finalJobImageBytesVerified               = finaljob.FinalJobImageBytesVerified
	finalJobImagePreflight                   = finaljob.FinalJobImagePreflight
	finalJobImagePreflightOnce               = finaljob.FinalJobImagePreflightOnce
	finalJobIntermediateClipWindows          = finaljob.FinalJobIntermediateClipWindows
	finalJobOverlayHandoff                   = finaljob.FinalJobOverlayHandoff
	finalJobOverlayStreamWire                = finaljob.FinalJobOverlayStreamWire
	finalJobPlanOutputBlock                  = finaljob.FinalJobPlanOutputBlock
	finalJobPreflightCoversPlan              = finaljob.FinalJobPreflightCoversPlan
	finalJobSceneDurationMS                  = finaljob.FinalJobSceneDurationMS
	finalJobValidateVideoCoverage            = finaljob.FinalJobValidateVideoCoverage
	finalJobVideoName                        = finaljob.FinalJobVideoName
	localizedRenderIsCertifiedClip           = finaljob.LocalizedRenderIsCertifiedClip
	prepareFinalJobVideoSegments             = finaljob.PrepareFinalJobVideoSegments
	renderedClipAssetRef                     = finaljob.RenderedClipAssetRef
	restoreFinalJobFixedMedia                = finaljob.RestoreFinalJobFixedMedia
	retainFinalJobRuntimeAssets              = finaljob.RetainFinalJobRuntimeAssets
	scheduleFinalJobOverlayFrames            = finaljob.ScheduleFinalJobOverlayFrames
	trimFinalJobSceneTail                    = finaljob.TrimFinalJobSceneTail
	validateFinalJobPlanBindingRules         = finaljob.ValidateFinalJobPlanBindingRules
)

type (
	finalJobStockAssetResolver = finaljob.FinalJobStockAssetResolver
)

type (
	FinalJobFrameWindow   = finaljob.FinalJobFrameWindow
	FinalJobOverlayWindow = finaljob.FinalJobOverlayWindow
	FinalJobBaseSegment   = finaljob.FinalJobBaseSegment
)

var (
	FinalJobStringValue = finaljob.FinalJobStringValue

	// finalJobImageAllowLoopback is the preflight test switch.
	finalJobImageAllowLoopback = &finaljob.ImageAllowLoopback
)
