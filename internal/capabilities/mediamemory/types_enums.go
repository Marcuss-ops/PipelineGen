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

type BatchMode string

const (
	ModeCatalogOnly     BatchMode = "catalog_only"
	ModeMaterializeTopK BatchMode = "materialize_top_k"
)

func IsKnownBatchMode(m BatchMode) bool {
	switch m {
	case ModeCatalogOnly, ModeMaterializeTopK:
		return true
	default:
		return false
	}
}

// ── BatchState ───────────────────────────────────────────────────

type BatchState string

const (
	BatchPending     BatchState = "pending"
	BatchReconciling BatchState = "reconciling"
	BatchCompleted   BatchState = "completed"
	BatchFailed      BatchState = "failed"
)

func IsKnownBatchState(s BatchState) bool {
	switch s {
	case BatchPending, BatchReconciling, BatchCompleted, BatchFailed:
		return true
	default:
		return false
	}
}

// ── FeedbackAction ───────────────────────────────────────────────

type FeedbackAction string

const (
	FeedbackAccepted       FeedbackAction = "accepted"
	FeedbackRejected       FeedbackAction = "rejected"
	FeedbackReplaced       FeedbackAction = "replaced"
	FeedbackTrimmed        FeedbackAction = "trimmed"
	FeedbackUsedSuccessful FeedbackAction = "used_successfully"
)

func IsKnownFeedbackAction(a FeedbackAction) bool {
	switch a {
	case FeedbackAccepted, FeedbackRejected, FeedbackReplaced,
		FeedbackTrimmed, FeedbackUsedSuccessful:
		return true
	default:
		return false
	}
}

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
