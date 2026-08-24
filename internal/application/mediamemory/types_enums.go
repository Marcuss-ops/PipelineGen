// Package mediamemory — enum types re-exported from capabilities/mediamemory/.
package mediamemory

import "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediamemory"

type ConceptType = mediamemory.ConceptType
type ApprovalStatus = mediamemory.ApprovalStatus
type Origin = mediamemory.Origin
type DiscoveryStatus = mediamemory.DiscoveryStatus
type MaterializationStatus = mediamemory.MaterializationStatus
type RightsStatus = mediamemory.RightsStatus
type BatchMode = mediamemory.BatchMode
type BatchState = mediamemory.BatchState
type FeedbackAction = mediamemory.FeedbackAction

const (
	ConceptPhrase   = mediamemory.ConceptPhrase
	ConceptEntity   = mediamemory.ConceptEntity
	ConceptPerson   = mediamemory.ConceptPerson
	ConceptLocation = mediamemory.ConceptLocation
	ConceptEvent    = mediamemory.ConceptEvent
	ConceptAction   = mediamemory.ConceptAction
	ConceptObject   = mediamemory.ConceptObject
	ConceptTopic    = mediamemory.ConceptTopic
	ConceptEmotion  = mediamemory.ConceptEmotion

	ApprovalPending  = mediamemory.ApprovalPending
	ApprovalApproved = mediamemory.ApprovalApproved
	ApprovalRejected = mediamemory.ApprovalRejected

	OriginManual   = mediamemory.OriginManual
	OriginAutoLink = mediamemory.OriginAutoLink
	OriginPhraseEq = mediamemory.OriginPhraseEq
	OriginSemantic = mediamemory.OriginSemantic

	DiscoveryQueued       = mediamemory.DiscoveryQueued
	DiscoverySearched     = mediamemory.DiscoverySearched
	DiscoveryAnalyzed     = mediamemory.DiscoveryAnalyzed
	DiscoveryIndexed      = mediamemory.DiscoveryIndexed
	DiscoveryFailed       = mediamemory.DiscoveryFailed
	DiscoveryMaterialized = mediamemory.DiscoveryMaterialized

	MaterializationCold   = mediamemory.MaterializationCold
	MaterializationWarm   = mediamemory.MaterializationWarm
	MaterializationHot    = mediamemory.MaterializationHot
	MaterializationFailed = mediamemory.MaterializationFailed

	RightsVerified = mediamemory.RightsVerified
	RightsUnknown  = mediamemory.RightsUnknown
	RightsDenied   = mediamemory.RightsDenied
	RightsExpired  = mediamemory.RightsExpired

	ModeCatalogOnly     = mediamemory.ModeCatalogOnly
	ModeMaterializeTopK = mediamemory.ModeMaterializeTopK

	BatchPending     = mediamemory.BatchPending
	BatchReconciling = mediamemory.BatchReconciling
	BatchCompleted   = mediamemory.BatchCompleted
	BatchFailed      = mediamemory.BatchFailed

	FeedbackAccepted       = mediamemory.FeedbackAccepted
	FeedbackRejected       = mediamemory.FeedbackRejected
	FeedbackReplaced       = mediamemory.FeedbackReplaced
	FeedbackTrimmed        = mediamemory.FeedbackTrimmed
	FeedbackUsedSuccessful = mediamemory.FeedbackUsedSuccessful

	ProviderLocal         = mediamemory.ProviderLocal
	ProviderSemanticIndex = mediamemory.ProviderSemanticIndex
	ProviderArtlist       = mediamemory.ProviderArtlist
	ProviderYouTube       = mediamemory.ProviderYouTube
	ProviderPexels        = mediamemory.ProviderPexels
)

var (
	IsKnownConceptType          = mediamemory.IsKnownConceptType
	IsKnownApprovalStatus       = mediamemory.IsKnownApprovalStatus
	IsKnownOrigin               = mediamemory.IsKnownOrigin
	IsKnownDiscoveryStatus      = mediamemory.IsKnownDiscoveryStatus
	IsKnownMaterializationStatus = mediamemory.IsKnownMaterializationStatus
	IsKnownRightsStatus         = mediamemory.IsKnownRightsStatus
	IsKnownBatchMode            = mediamemory.IsKnownBatchMode
	IsKnownBatchState           = mediamemory.IsKnownBatchState
	IsKnownFeedbackAction       = mediamemory.IsKnownFeedbackAction
	IsKnownProvider             = mediamemory.IsKnownProvider
)