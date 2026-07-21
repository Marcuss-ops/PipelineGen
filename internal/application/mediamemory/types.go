// Package mediamemory — types.go is the canonical SSOT for the
// MediaMemory capability wire shapes.
//
// godlike/06 SSOT (one canonical owner per fact): every visual-memory
// entity (concept, binding, candidate, plan layer) is owned by this
// file. Future capabilities that need to read or mutate visual-memory
// state import the types from here, not a parallel fork.
//
// godlike/06 SSOT (sister to search.Candidate): MediaCandidate mirrors
// the canonical search.Candidate projection: NO LocalPath, NO
// DriveLink, NO server-internal locator in the wire shape. The
// binder layer reads AssetDeliveryService to mint short-lived URLs
// at the HTTP boundary. This package only owns the binding surface
// (concept → asset_id → slot → score), not delivery URLs.
//
// godlike/07 NO-FAKE-AVAILABILITY (typed fail-closed boundary):
//
//   - ErrInvalidPhrase            : malformed input to Normalizer
//   - ErrConceptNotFound          : concept_id absent from concept repository
//   - ErrBindingNotFound          : binding_id absent from binding repository
//   - ErrDuplicateBinding         : (concept_id, asset_id, slot_kind) already present
//   - ErrInvalidSlotKind          : slot_kind outside the canonical closed set
//   - ErrApprovalRequired         : ranker refuses to expose unapproved binding
//   - ErrCandidateMaterializationFailed : materialize worker returned no asset_id
//   - ErrBatchNotFound            : batch_id unknown to BatchService
//   - ErrBatchNotReconcilable    : batch is in a terminal state (already reconciled)
//
// Each sentinel is wrapped with %w in the service methods so callers
// probe via errors.Is, not string-match.
//
// Phase 1.1 (skeleton): only types and sentinels are declared here.
// No business logic — phase-specific implementations live in
// resolver.go / binding_service.go / ranker.go (siblings).
package mediamemory

import (
	"errors"
	"time"
)

// ── Enumerations (godlike/06 closed-set SSOT) ──────────────────────

// SlotKind is the canonical visual slot a binding can occupy in a
// SceneVisualPlan. Closed set; new kinds require a godlike/06 SSOT
// review because the ranker, resolver, and renderer all switch on
// this value.
type SlotKind string

const (
	SlotPrimaryVideo    SlotKind = "primary_video"
	SlotSecondaryImage  SlotKind = "secondary_image"
	SlotEvidenceOverlay SlotKind = "evidence_overlay"
	SlotMap             SlotKind = "map"
	SlotPortrait        SlotKind = "portrait"
	SlotDocument        SlotKind = "document"
	SlotBackground      SlotKind = "background"
)

// IsKnownSlotKind reports whether k is in the canonical closed set.
// Used by binding_service and resolver to distinguish ErrInvalidSlotKind
// from programmatic string drift.
func IsKnownSlotKind(k SlotKind) bool {
	switch k {
	case SlotPrimaryVideo, SlotSecondaryImage, SlotEvidenceOverlay,
		SlotMap, SlotPortrait, SlotDocument, SlotBackground:
		return true
	default:
		return false
	}
}

// ConceptType classifies a MediaConcept so the ranker can apply
// concept-type-aware weights. Closed set; see IsKnownConceptType.
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

// IsKnownConceptType reports whether c is in the canonical closed set.
func IsKnownConceptType(c ConceptType) bool {
	switch c {
	case ConceptPhrase, ConceptEntity, ConceptPerson, ConceptLocation,
		ConceptEvent, ConceptAction, ConceptObject, ConceptTopic, ConceptEmotion:
		return true
	default:
		return false
	}
}

// ApprovalStatus tracks whether a binding can be auto-promoted by
// the resolver without further human review.
type ApprovalStatus string

const (
	ApprovalPending  ApprovalStatus = "pending"
	ApprovalApproved ApprovalStatus = "approved"
	ApprovalRejected ApprovalStatus = "rejected"
)

// Origin distinguishes manual bindings (dashboard / curated) from
// auto-derived ones (linker worker / parafrase discovery).
type Origin string

const (
	OriginManual   Origin = "manual"
	OriginAutoLink Origin = "auto_link"
	OriginPhraseEq Origin = "phrase_equal"
	OriginSemantic Origin = "semantic"
)

// DiscoveryStatus tracks the worker's progress on a media_candidates row.
type DiscoveryStatus string

const (
	DiscoveryQueued       DiscoveryStatus = "queued"
	DiscoverySearched     DiscoveryStatus = "searched"
	DiscoveryAnalyzed     DiscoveryStatus = "analyzed"
	DiscoveryIndexed      DiscoveryStatus = "indexed"
	DiscoveryFailed       DiscoveryStatus = "failed"
	DiscoveryMaterialized DiscoveryStatus = "materialized"
)

// MaterializationStatus tracks hot/warm/cold tiers (godlike/06 SSOT
// for the three-tier cache model):
//
//   - Cold  : only metadata+URL stored; bytes not downloaded
//   - Warm  : bytes on Drive or segmentable on demand
//   - Hot   : bytes staged locally and ready for binding
//
// Top-K candidates from a batch jump to Warm on materialize; only
// rights-verified candidates can be promoted to Hot.
type MaterializationStatus string

const (
	MaterializationCold   MaterializationStatus = "cold"
	MaterializationWarm   MaterializationStatus = "warm"
	MaterializationHot    MaterializationStatus = "hot"
	MaterializationFailed MaterializationStatus = "failed"
)

// RightsStatus classifies the rights envelope of a candidate.
// godlike/07 NO-FAKE-AVAILABILITY: rights-uncertain candidates MUST
// NOT be promoted to Hot; the ranker must apply rights_penalty when
// status == RightsUnknown. Closed set; see IsKnownRightsStatus.
type RightsStatus string

const (
	RightsVerified RightsStatus = "verified"
	RightsUnknown  RightsStatus = "unknown"
	RightsDenied   RightsStatus = "denied"
	RightsExpired  RightsStatus = "expired"
)

// IsKnownRightsStatus reports whether r is in the canonical closed set.
func IsKnownRightsStatus(r RightsStatus) bool {
	switch r {
	case RightsVerified, RightsUnknown, RightsDenied, RightsExpired:
		return true
	default:
		return false
	}
}

// ── Canonical entities ─────────────────────────────────────────────

// MediaConcept is the canonical concept row. UNIQUE(language,
// phrase_fingerprint) is the SQL SSOT — duplicates are fail-closed
// at the repository level, surfacing ErrDuplicateBinding-equivalent
// sentinel errors wrapped up the stack.
type MediaConcept struct {
	ID                string
	Language          string
	CanonicalText     string
	NormalizedText    string
	PhraseFingerprint string
	ConceptType       ConceptType
	EmbeddingVersion  string // "" until first indexing phase
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// MediaBinding links a MediaConcept to a canonical media asset for a
// specific SlotKind. The binding may carry a sub-clip window via
// StartMs/EndMs; image/document bindings set both to 0.
//
// godlike/06 SSOT: AssetID is the canonical media_assets.id reference,
// NOT a local file path. LocalPath/DriveLink are owned by the
// AssetDeliveryService (sister to clipresolve.AssetMapping).
type MediaBinding struct {
	ID             string
	ConceptID      string
	AssetID        string
	StartMs        int64
	EndMs          int64
	SlotKind       SlotKind
	Origin         Origin
	ApprovalStatus ApprovalStatus
	ManualScore    float64
	SemanticScore  float64
	QualityScore   float64
	SuccessScore   float64
	UsageCount     int
	LastUsedAt     *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// MediaCandidate stores metadata about a clip/image discovered by
// the discovery worker but NOT yet promoted to a MediaBinding.
// godlike/06 SSOT: once linker promotes a candidate, it MUST create
// a MediaBinding row referencing the same AssetID.
//
// godlike/07 NO-FAKE-AVAILABILITY: rights-uncertain candidates stay
// in Cold tier until RightsValidator verifies them. The ranker MUST
// apply rights_penalty for RightsStatus != RightsVerified.
type MediaCandidate struct {
	ID                    string
	Provider              string
	ProviderAssetID       string
	SourceURL             string
	ThumbnailURL          string
	Title                 string
	Description           string
	DurationMs            int64
	CandidateScore        float64
	RightsStatus          RightsStatus
	LicenseBasis          string
	AllowedChannels       []string
	AllowedRegions        []string
	Owner                 string
	Expiration            *time.Time
	DiscoveryStatus       DiscoveryStatus
	MaterializationStatus MaterializationStatus
	AssetID               string // "" until materialize produces a media_assets row
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// VisualIntent is the resolver input. Produced by the upstream
// sentence/phrase splitter and consumed by VisualResolver.Resolve.
type VisualIntent struct {
	Text           string
	Language       string
	Entities       []string
	Concepts       []string
	VisualActions  []string
	PreferredSlots []SlotKind
}

// SceneSpec is one scene from a project. The resolver merges a
// ResolveRequest (project-shared) with N SceneSpecs to produce N
// LayerGroups.
type SceneSpec struct {
	ID         string
	Text       string
	DurationMs int64
	Slots      []SlotKind
	Language   string
}

// Layer is one entry in a SceneVisualPlan.
type Layer struct {
	Slot           SlotKind
	AssetID        string
	BindingID      string
	StartMs        int64
	EndMs          int64
	Layout         string // "fullscreen", "right_panel", "fullscreen_fade", ...
	CandidateScore float64
}

// SceneVisualPlan is the canonical output of the ranker, consumed
// by the headless renderer. The plan carries 1–3 layers per scene
// (godlike/06 SSOT: 1 ≤ len(Layers) ≤ 3 for current renderer).
type SceneVisualPlan struct {
	ProjectID  string
	SceneID    string
	Text       string
	Language   string
	DurationMs int64
	Layers     []Layer
	Source     string // "exact", "semantic", "local", "external", "mixed"
}

// ResolvePolicy bundles the controller knobs that VisualResolver
// reads on each Resolve call.
type ResolvePolicy struct {
	PreferApprovedBindings      bool
	AllowExternalSearch         bool
	MaxCandidatesPerSlot        int
	MaxExternalMaterializations int
	AvoidRecentAssets           bool
}

// ResolveRequest is the top-level controller input to the resolver.
type ResolveRequest struct {
	ProjectID string
	Language  string
	Scenes    []SceneSpec
	Policy    ResolvePolicy
}

// ResolveResult is the per-project batched output of ResolveRequest.
type ResolveResult struct {
	ProjectID string
	Plans     []SceneVisualPlan
	Warnings  []string
}

// BatchSpec is the canonical input to BatchService for catalog-only
// discovery runs (e.g. 1000 candidates across multiple queries).
type BatchSpec struct {
	Name            string
	Queries         []string
	Language        string
	MediaTypes      []string // "video", "image", ...
	Providers       []string // "artlist", "youtube", "images"
	MaxCandidates   int
	MaterializeTopK int
	Mode            string // "catalog_only" | "materialize_top_k"
}

// BatchState is the per-batch audit envelope (godlike/07).
//
//   - Pending     : accept new candidates from worker
//   - Reconciling : worker is filling materialize_top_k
//   - Completed   : all candidates have a terminal DiscoveryStatus
//   - Failed      : AbortOnError strict mode tripped, see Failures[]
type BatchState string

const (
	BatchPending     BatchState = "pending"
	BatchReconciling BatchState = "reconciling"
	BatchCompleted   BatchState = "completed"
	BatchFailed      BatchState = "failed"
)

// Batch is the parent; BatchChild is each (query × provider) sub-job.
// godlike/06 SSOT: parent's MaxCandidates / MaterializeTopK are
// canonical for ALL children; on resume, children re-read from the
// parent so the policy is consistent across resumption.
type Batch struct {
	ID                string
	Name              string
	Spec              BatchSpec
	State             BatchState
	Children          []string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	CompletedAt       *time.Time
	Failures          []string
	CandidateCount    int
	IndexedCount      int
	MaterializedCount int
}

// BatchChild is one (query × provider) sub-job.
type BatchChild struct {
	ID           string
	BatchID      string
	Query        string
	Provider     string
	State        BatchState
	CandidateIDs []string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// UsageEvent records one human/auto action in the feedback loop.
// godlike/06 SSOT: the ranker promotes success_score from these
// rows (SuccessScore increment on RenderCompleted + !Rejected).
type UsageEvent struct {
	ID               string
	ProjectID        string
	SceneID          string
	ConceptID        string
	AssetID          string
	BindingID        string
	SlotKind         SlotKind
	Selected         bool
	ManuallySelected bool
	Rejected         bool
	RenderCompleted  bool
	CreatedAt        time.Time
}

// FeedbackAction is the wire enum for POST /api/media-memory/feedback.
type FeedbackAction string

const (
	FeedbackAccepted       FeedbackAction = "accepted"
	FeedbackRejected       FeedbackAction = "rejected"
	FeedbackReplaced       FeedbackAction = "replaced"
	FeedbackTrimmed        FeedbackAction = "trimmed"
	FeedbackUsedSuccessful FeedbackAction = "used_successfully"
)

// IsKnownFeedbackAction reports whether a is in the canonical closed set.
func IsKnownFeedbackAction(a FeedbackAction) bool {
	switch a {
	case FeedbackAccepted, FeedbackRejected, FeedbackReplaced,
		FeedbackTrimmed, FeedbackUsedSuccessful:
		return true
	default:
		return false
	}
}

// IsKnownApprovalStatus reports whether s is in the canonical closed
// set. godlike/06 SSOT: every enum exposes IsKnownXxx so callers
// validate input ONCE, ON THE BOUNDARY, then trust the value.
func IsKnownApprovalStatus(s ApprovalStatus) bool {
	switch s {
	case ApprovalPending, ApprovalApproved, ApprovalRejected:
		return true
	default:
		return false
	}
}

// IsKnownOrigin reports whether o is in the canonical closed set.
func IsKnownOrigin(o Origin) bool {
	switch o {
	case OriginManual, OriginAutoLink, OriginPhraseEq, OriginSemantic:
		return true
	default:
		return false
	}
}

// IsKnownDiscoveryStatus reports whether s is in the canonical closed set.
func IsKnownDiscoveryStatus(s DiscoveryStatus) bool {
	switch s {
	case DiscoveryQueued, DiscoverySearched, DiscoveryAnalyzed,
		DiscoveryIndexed, DiscoveryFailed, DiscoveryMaterialized:
		return true
	default:
		return false
	}
}

// IsKnownMaterializationStatus reports whether s is in the canonical
// closed set (hot/warm/cold tier SSOT).
func IsKnownMaterializationStatus(s MaterializationStatus) bool {
	switch s {
	case MaterializationCold, MaterializationWarm, MaterializationHot, MaterializationFailed:
		return true
	default:
		return false
	}
}

// IsKnownBatchState reports whether s is in the canonical closed set.
func IsKnownBatchState(s BatchState) bool {
	switch s {
	case BatchPending, BatchReconciling, BatchCompleted, BatchFailed:
		return true
	default:
		return false
	}
}

// IsKnownRightsVerdict intentionally lives in ports.go alongside
// the RightsVerdict enum (canonical SSOT pattern: predicate next
// to enum). The typed-sentinel envelope ErrInvalidRightsVerdict
// (forward-pointer Phase 1.2) is likewise defined there.

// ── Typed error envelope (godlike/07) ──────────────────────────────

var (
	ErrInvalidPhrase = errors.New(
		"mediamemory: invalid phrase (empty or unparsable by the canonical Normalizer)",
	)
	ErrConceptNotFound = errors.New(
		"mediamemory: concept_id absent from concept repository",
	)
	ErrBindingNotFound = errors.New(
		"mediamemory: binding_id absent from binding repository",
	)
	ErrDuplicateBinding = errors.New(
		"mediamemory: duplicate (concept_id, asset_id, slot_kind) — UNIQUE(language, phrase_fingerprint) equivalent",
	)
	ErrInvalidSlotKind = errors.New(
		"mediamemory: slot_kind outside canonical closed set (use IsKnownSlotKind)",
	)
	ErrApprovalRequired = errors.New(
		"mediamemory: binding is not approved (resolver refuses to expose unapproved binding)",
	)
	ErrCandidateMaterializationFailed = errors.New(
		"mediamemory: materialize worker returned no asset_id for the candidate (stockpipeline failed)",
	)
	ErrBatchNotFound = errors.New(
		"mediamemory: batch_id unknown to BatchService",
	)
	ErrBatchNotReconcilable = errors.New(
		"mediamemory: batch is in a terminal state (already Completed/Failed) — start a new batch",
	)
	ErrInvalidFeedbackAction = errors.New(
		"mediamemory: unknown FeedbackAction value (closed set: accepted/rejected/replaced/trimmed/used_successfully)",
	)
)
