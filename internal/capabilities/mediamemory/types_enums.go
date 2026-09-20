// Package mediamemory — types_enums.go: closed-set enums with
// canonical constants + IsKnown predicates + Provider tags.
package mediamemory

// ── ConceptType ───────────────────────────────────────────────────

type ConceptType string

const (
	ConceptPhrase   ConceptType = "phrase"
	ConceptEntity   ConceptType = "entity"
	ConceptPerson   ConceptType = "person"
	ConceptLocation ConceptType = "location"
	ConceptEvent    ConceptType = "event"
	ConceptAction   ConceptType = "action"
	ConceptObject   ConceptType = "object"
	ConceptTopic    ConceptType = "topic"
	ConceptEmotion  ConceptType = "emotion"
)

func IsKnownConceptType(c ConceptType) bool {
	switch c {
	case ConceptPhrase, ConceptEntity, ConceptPerson, ConceptLocation,
		ConceptEvent, ConceptAction, ConceptObject, ConceptTopic, ConceptEmotion:
		return true
	default:
		return false
	}
}

// ── ApprovalStatus ───────────────────────────────────────────────

type ApprovalStatus string

const (
	ApprovalPending  ApprovalStatus = "pending"
	ApprovalApproved ApprovalStatus = "approved"
	ApprovalRejected ApprovalStatus = "rejected"
)

func IsKnownApprovalStatus(s ApprovalStatus) bool {
	switch s {
	case ApprovalPending, ApprovalApproved, ApprovalRejected:
		return true
	default:
		return false
	}
}

// ── Origin ───────────────────────────────────────────────────────

type Origin string

const (
	OriginManual   Origin = "manual"
	OriginAutoLink Origin = "auto_link"
	OriginPhraseEq Origin = "phrase_equal"
	OriginSemantic Origin = "semantic"
)

func IsKnownOrigin(o Origin) bool {
	switch o {
	case OriginManual, OriginAutoLink, OriginPhraseEq, OriginSemantic:
		return true
	default:
		return false
	}
}

// ── DiscoveryStatus ──────────────────────────────────────────────

type DiscoveryStatus string

const (
	DiscoveryQueued       DiscoveryStatus = "queued"
	DiscoverySearched     DiscoveryStatus = "searched"
	DiscoveryAnalyzed     DiscoveryStatus = "analyzed"
	DiscoveryIndexed      DiscoveryStatus = "indexed"
	DiscoveryFailed       DiscoveryStatus = "failed"
	DiscoveryMaterialized DiscoveryStatus = "materialized"
)

func IsKnownDiscoveryStatus(s DiscoveryStatus) bool {
	switch s {
	case DiscoveryQueued, DiscoverySearched, DiscoveryAnalyzed,
		DiscoveryIndexed, DiscoveryFailed, DiscoveryMaterialized:
		return true
	default:
		return false
	}
}

// ── MaterializationStatus ────────────────────────────────────────

type MaterializationStatus string

const (
	MaterializationCold   MaterializationStatus = "cold"
	MaterializationWarm   MaterializationStatus = "warm"
	MaterializationHot    MaterializationStatus = "hot"
	MaterializationFailed MaterializationStatus = "failed"
)

func IsKnownMaterializationStatus(s MaterializationStatus) bool {
	switch s {
	case MaterializationCold, MaterializationWarm, MaterializationHot, MaterializationFailed:
		return true
	default:
		return false
	}
}

// ── RightsStatus ─────────────────────────────────────────────────

type RightsStatus string

const (
	RightsVerified RightsStatus = "verified"
	RightsUnknown  RightsStatus = "unknown"
	RightsDenied   RightsStatus = "denied"
	RightsExpired  RightsStatus = "expired"
)

func IsKnownRightsStatus(r RightsStatus) bool {
	switch r {
	case RightsVerified, RightsUnknown, RightsDenied, RightsExpired:
		return true
	default:
		return false
	}
}

// ── BatchMode ────────────────────────────────────────────────────

// BatchMode / ModeCatalogOnly / ModeMaterializeTopK / IsKnownBatchMode were
// DELETED 2026-09-20 with the Fase 3.4 batch surface that used them (BatchSpec
// carried the Mode field and batch_service was the only reader). `deadcode
// -test ./...` surfaced IsKnownBatchMode as newly unreachable the moment the
// batch service left, so it is removed in the same change rather than left
// behind as a validator with nothing to validate.

// ── BatchState ───────────────────────────────────────────────────

// BatchState / BatchPending / BatchReconciling / BatchCompleted / BatchFailed /
// IsKnownBatchState were DELETED in the same change with the batch orchestrator
// they described. The LIVE batch state machine is an unrelated type with the
// same name: stockpipeline.BatchState in
// internal/capabilities/assets/providers/stock.

// ── FeedbackAction ───────────────────────────────────────────────

// FeedbackAction / FeedbackAccepted / FeedbackRejected / FeedbackReplaced /
// FeedbackTrimmed / FeedbackUsedSuccessful / IsKnownFeedbackAction were DELETED
// here on 2026-09-20 with the Fase 2.3 feedback surface. This closed set was
// the input vocabulary of FeedbackService.Record and the key of
// DeltaForAction's score-delta table; both departed with the service, which
// composition never constructed, so the validator had no consumer left.

// ── Provider tag constants ───────────────────────────────────────

const (
	ProviderLocal         = "local"
	ProviderSemanticIndex = "mediamemory.semantic"
	ProviderArtlist       = "artlist"
	ProviderYouTube       = "youtube"
	ProviderPexels        = "pexels"
)

func IsKnownProvider(p string) bool {
	switch p {
	case ProviderLocal, ProviderSemanticIndex,
		ProviderArtlist, ProviderYouTube, ProviderPexels:
		return true
	}
	return false
}
