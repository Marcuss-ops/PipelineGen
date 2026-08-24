package delivery

import capdelivery "github.com/Marcuss-ops/PipelineGen/internal/capabilities/delivery"

type DestinationKey = capdelivery.DestinationKey
type ConflictPolicy = capdelivery.ConflictPolicy
type PublishRequest = capdelivery.PublishRequest

type PublishResult = capdelivery.PublishResult

type Publisher = capdelivery.Publisher

type DocPublisher = capdelivery.DocPublisher

type DestinationSpec = capdelivery.DestinationSpec
type PublicationReceipt = capdelivery.PublicationReceipt

type UploadOutcome = capdelivery.UploadOutcome
type PublishAction = capdelivery.PublishAction

type Operation = capdelivery.Operation
type RemoteReferenceRequest = capdelivery.RemoteReferenceRequest
type RemoteReferenceRegistrar = capdelivery.RemoteReferenceRegistrar
type MaterializationRequest = capdelivery.MaterializationRequest
type Materializer = capdelivery.Materializer

const (
	DestinationYouTubeClip        = capdelivery.DestinationYouTubeClip
	DestinationYouTubeAsset       = capdelivery.DestinationYouTubeAsset
	DestinationArtlist            = capdelivery.DestinationArtlist
	DestinationStock              = capdelivery.DestinationStock
	DestinationImage              = capdelivery.DestinationImage
	DestinationVoiceover          = capdelivery.DestinationVoiceover
	DestinationBook               = capdelivery.DestinationBook
	DestinationScript             = capdelivery.DestinationScript
	DestinationSoundEffect        = capdelivery.DestinationSoundEffect
	DestinationSoundEffectSidecar = capdelivery.DestinationSoundEffectSidecar
	DestinationDocument           = capdelivery.DestinationDocument
	DestinationClipMetadata       = capdelivery.DestinationClipMetadata
	DestinationRenderedClip       = capdelivery.DestinationRenderedClip
	DestinationAdmin              = capdelivery.DestinationAdmin
	ConflictPolicyUnset           = capdelivery.ConflictPolicyUnset
	ConflictOverwrite             = capdelivery.ConflictOverwrite
	ConflictSkip                  = capdelivery.ConflictSkip
	ConflictRename                = capdelivery.ConflictRename
	UploadOutcomeUnknown          = capdelivery.UploadOutcomeUnknown
	UploadOutcomeCreated          = capdelivery.UploadOutcomeCreated
	UploadOutcomeUpdated          = capdelivery.UploadOutcomeUpdated
	UploadOutcomeSkipped          = capdelivery.UploadOutcomeSkipped
	UploadOutcomeRenamed          = capdelivery.UploadOutcomeRenamed
	PublishActionUnknown          = capdelivery.PublishActionUnknown
	PublishActionCreated          = capdelivery.PublishActionCreated
	PublishActionUpdated          = capdelivery.PublishActionUpdated
	PublishActionSkipped          = capdelivery.PublishActionSkipped
	PublishActionRenamed          = capdelivery.PublishActionRenamed

	OperationRegisterRemoteReference = capdelivery.OperationRegisterRemoteReference
	OperationMaterializeLocal        = capdelivery.OperationMaterializeLocal
)

var ErrDestinationParentMismatch = capdelivery.ErrDestinationParentMismatch

func DeriveIdempotencyKey(destination DestinationKey, artifactID, contentHash string, sourceVersion int64) string {
	return capdelivery.DeriveIdempotencyKey(destination, artifactID, contentHash, sourceVersion)
}
