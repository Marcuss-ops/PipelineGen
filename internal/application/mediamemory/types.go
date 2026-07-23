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

	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
)

// SlotKind is an alias for the canonical media.SlotKind. It is kept
// for backward compatibility until all callers are migrated to
// media.SlotKind directly.
type SlotKind = media.SlotKind

// ── Enumerations (godlike/06 closed-set SSOT) ──────────────────────

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
// specific media.SlotKind. The binding may carry a sub-clip window via
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
	SlotKind       media.SlotKind
	Origin         Origin
	ApprovalStatus ApprovalStatus
	// Provider is the canonical source tag from the candidate
	// that produced this binding (godlike/06 SSOT: see the
	// closed-set constant group below). Phase 4.3 wires this so
	// deriveLayerProvider can return the real provider tag
	// (enables the SceneVisualPlan.Source = "mixed" branch).
	// Defaults to ProviderLocal when empty (binding_service
	// applyDefaults backfills manual edits).
	Provider      string
	ManualScore   float64
	SemanticScore float64
	QualityScore  float64
	SuccessScore  float64
	UsageCount    int
	LastUsedAt    *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// MediaCandidate stores metadata about a clip/image discovered by
// the discovery worker but NOT yet promoted to a MediaBinding.
// godlike/06 SSOT: once linker promotes a candidate, it MUST create
// a MediaBinding row referencing the same AssetID.
//
// godlike/07 NO-FAKE-AVAILABILITY: rights-uncertain candidates stay
// in Cold tier until RightsValidator verifies them. The ranker MUST
// apply rights_penalty for RightsStatus != RightsVerified.
//
// MediaType is the canonical wire-mirrored media category used by
// the ranker gates (aspect-ratio / format checks). Values follow
// the canonical search.Candidate.MediaType vocabulary (video /
// image / audio / music). Empty string is treated as ambiguous and
// bypasses the gate (legacy rows pending backfill).
type MediaCandidate struct {
	ID                    string
	Provider              string
	ProviderAssetID       string
	SourceURL             string
	ThumbnailURL          string
	Title                 string
	Description           string
	MediaType             string // "video" / "image" / "audio" / "music" or "" (legacy ambiguous)
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
	// ChannelID and VideoID are the Fase 2.3 anti-repetition
	// identity fields. godlike/06 SSOT (Fase 3 linker
	// forward-pointer): the linker worker enriches a candidate
	// row with these after discovery+analysis so the resolver's
	// PopulateRepetitionPenalty can cross-reference the
	// append-only UsageEvent history.
	// Empty values are valid for pre-Fase-2.3 rows; the ranker
	// treats empty as "no penalty input available" but the
	// same-asset penalty (UsageCount + SuccessScore) still
	// drives the contract.
	ChannelID string
	VideoID   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// VisualIntent is the resolver input. Produced by the upstream
// sentence/phrase splitter and consumed by VisualResolver.Resolve.
type VisualIntent struct {
	Text           string
	Language       string
	Entities       []string
	Concepts       []string
	VisualActions  []string
	PreferredSlots []media.SlotKind
}

// SceneSpec is one scene from a project. The resolver merges a
// ResolveRequest (project-shared) with N SceneSpecs to produce N
// LayerGroups.
type SceneSpec struct {
	ID         string
	Text       string
	DurationMs int64
	Slots      []media.SlotKind
	Language   string
	// SceneConcepts is the Fase 4.3 per-scene concept_id
	// list. godlike/06 SSOT (scene-concepts union): when
	// non-empty it overrides the request-level
	// PlanGeneratorRequest.SceneConcepts so the
	// SceneVisualPlanGenerator scopes pickBindingForSlot to
	// the scene's actual concept set. Empty (the default)
	// falls back to the request-level filter.
	SceneConcepts []string
}

// Layer is one entry in a SceneVisualPlan.
type Layer struct {
	Slot           media.SlotKind
	AssetID        string
	CandidateID    string
	BindingID      string
	StartMs        int64
	EndMs          int64
	Layout         string // "fullscreen", "right_panel", "fullscreen_fade", ...
	CandidateScore float64
	// Provider is the canonical source tag from the winning
	// MediaCandidate that produced this layer (godlike/06
	// SSOT propagation: the Level 3-7 semantic adapter
	// stamps mediamemory.ProviderSemanticIndex, the Level 9
	// SearchFanOutAdapter stamps the forwarding provider
	// name, ...).
	Provider string
}

type CandidateOption struct {
	AssetID      string
	CandidateID  string
	SourceURL    string
	Provider     string
	Score        float64
	DurationMs   int64
	MediaType    string
	RightsStatus string
}

// SceneIntent captures what the brain understood about a scene.
// It mirrors the brain's VisualIntent without importing the brain
// package into the mediamemory SSOT.
type SceneIntent struct {
	Entities []string
	Concepts []string
	Actions  []string
	Keywords []string
}

// SceneBackendCall records one backend invocation performed by the
// brain for a scene. It mirrors brain.BackendCall.
type SceneBackendCall struct {
	Backend string
	Hits    int
	Error   string
}

// SceneResolutionTrace records how the brain arrived at its
// decisions for a scene. It mirrors brain.ResolutionTrace, scoped
// to the subset of fields useful for diagnostics on the wire.
type SceneResolutionTrace struct {
	NormalizedText string
	BackendCalls   []SceneBackendCall
	Reasons        []string
}

// SceneVisualPlan is the canonical output of the ranker, consumed
// by the headless renderer. The plan carries 1–3 layers per scene
// (godlike/06 SSOT: 1 ≤ len(Layers) ≤ 3 for current renderer).
type SceneVisualPlan struct {
	ProjectID  string
	SceneID    string
	SegmentID  string
	Text       string
	Language   string
	DurationMs int64
	Layers     []Layer
	Source     string // "exact", "semantic", "local", "external", "mixed"
	// Intent, Trace and DecisionFingerprint are produced by the
	// brain-backed resolver to aid debugging. They are optional:
	// the legacy VisualResolver leaves them zero-valued.
	Intent              SceneIntent
	Trace               SceneResolutionTrace
	DecisionFingerprint string
	Candidates          []CandidateOption
}

// ResolvePolicy bundles the controller knobs that VisualResolver
// reads on each Resolve call.
//
// godlike/06 SSOT: the MaxExternalMaterializations knob was
// retired in Fase 1.5 cleanup — materialization is owned by the
// BatchService.MaterializeTopK path (BatchSpec.MaterializeTopK),
// not by per-request policy. The remaining knobs are the live
// controls the dashboard preview / API consumers supply.
//
// SearchPolicy carries the canonical search knobs forwarded to the
// underlying SearchFanOut. Legacy fields are still honoured when
// SearchPolicy is zero so existing callers keep working; new code
// should populate SearchPolicy directly.
type ResolvePolicy struct {
	PreferApprovedBindings bool
	AllowExternalSearch    bool
	MaxCandidatesPerSlot   int
	AvoidRecentAssets      bool
	SearchPolicy           media.ResolutionSearchPolicy
}

// OptionalResolvePolicy carries the client-supplied overrides before
// canonical defaults are applied. Pointer bools distinguish "field
// absent" from "field explicitly false". The zero value means "use
// the application-layer defaults" (see ResolutionPolicyResolver).
//
// godlike/06 SSOT: the API layer maps its wire DTO directly to this
// struct and does NOT apply defaults; defaulting is the sole
// responsibility of ResolutionPolicyResolver in the application
// layer.
type OptionalResolvePolicy struct {
	PreferApprovedBindings *bool
	AllowExternalSearch    *bool
	MaxCandidatesPerSlot   int
	AvoidRecentAssets      *bool
	Mode                   string
	AllowedProviders       []string
	CacheRead              *bool
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
	Mode            BatchMode // canonical closed-set: ModeCatalogOnly | ModeMaterializeTopK
}

// BatchMode is the canonical closed-set enum for BatchSpec.Mode
// (godlike/06 SSOT: every enum gets a typed predicate next to its
// constants). The wire code branches on these via IsKnownBatchMode
// so a drift surfaces as ErrInvalidBatchMode rather than a silent
// zero-return.
type BatchMode string

const (
	// ModeCatalogOnly is the canonical "save metadata for all N
	// candidates without downloading" mode (architecture doc
	// section 8). The discovery worker runs cold-tier writes only.
	ModeCatalogOnly BatchMode = "catalog_only"
	// ModeMaterializeTopK is the canonical "save metadata + promote
	// materialize_top_k candidates to Warm tier" mode. AcquisitionPlanner
	// picks the top K from the catalog_only result set.
	ModeMaterializeTopK BatchMode = "materialize_top_k"
)

// IsKnownBatchMode reports whether m is in the canonical closed set.
func IsKnownBatchMode(m BatchMode) bool {
	switch m {
	case ModeCatalogOnly, ModeMaterializeTopK:
		return true
	default:
		return false
	}
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
//
// godlike/06 SSOT (Fase 2.3 anti-repetition contract): ChannelID
// and VideoID are recorded alongside the existing ProjectID /
// AssetID so the resolver can apply repetition_penalty deterministically
// without a runtime join against media_assets. ChannelID is
// the canonical YouTube channel_id (or any equivalent publishing
// channel); VideoID is the canonical source_video_id of the
// underlying clip/image. Empty values are valid (caller-side
// omitted, e.g. legacy log rows pre-Fase 2.3) — the ranker treats
// empty channel/video as "no penalty input available" but the
// same-asset penalty still drives the contract.
type UsageEvent struct {
	ID               string
	ProjectID        string
	SceneID          string
	ConceptID        string
	AssetID          string
	BindingID        string
	SlotKind         media.SlotKind
	ChannelID        string
	VideoID          string
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
		"mediamemory: slot_kind outside canonical closed set (use media.IsKnownSlotKind)",
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
	// ErrInvalidAggregateSince is the canonical Fase 1.6 sentinel
	// for a malformed `since` input to FeedbackService.AggregateSince.
	// godlike/07 NO-FAKE-AVAILABILITY: an invalid timestamp MUST
	// surface as a typed envelope (NOT a silent zero-value
	// time.Time) so the wire handler can branch via errors.Is and
	// return a 400 to the caller. godlike/06 SSOT: distinct from
	// ErrInvalidPhrase (reserved for Normalizer input corruption).
	ErrInvalidAggregateSince = errors.New(
		"mediamemory: AggregateSince `since` is not a valid RFC3339 timestamp",
	)
	ErrCandidateNotFound = errors.New(
		"mediamemory: candidate_id absent from candidate repository",
	)
	// ErrInvalidBindingInput is the canonical sentinel for binding
	// payloads that miss required fields (concept_id, asset_id,
	// slot_kind) but are otherwise well-typed. godlike/06 SSOT:
	// distinct from ErrInvalidSlotKind (which means "slot kind is
	// outside the canonical closed set"); the wire code branches on
	// these separately.
	ErrInvalidBindingInput = errors.New(
		"mediamemory: binding input missing required field(s) (concept_id / asset_id / slot_kind)",
	)
	// ErrBindingMutationDispatcherUnavailable is the canonical sentinel
	// returned when BindingService detects that the canonical
	// BindingMutationDispatcher was not wired at composition time. A nil
	// dispatcher must never be treated as a silent no-op.
	ErrBindingMutationDispatcherUnavailable = errors.New(
		"mediamemory: BindingMutationDispatcher unavailable",
	)
	// ErrSemanticNotConfigured is the canonical sentinel for a
	// missing/broken semantic backend at the mediamemory
	// capability boundary (godlike/07 NO-FAKE-AVAILABILITY —
	// absent backend MUST NOT silently degrade to zero-candidate
	// reads).
	ErrSemanticNotConfigured = errors.New(
		"mediamemory: semantic lookup backend not configured (Qdrant not reachable at boot, or composition wiring missing)",
	)
	// ErrSemanticBackendFailed is the canonical sentinel for an
	// operational failure of the semantic backend (Qdrant
	// HybridSearch returned an error envelope, embedding call
	// failed with a transient/non-ignored error).
	ErrSemanticBackendFailed = errors.New(
		"mediamemory: semantic lookup backend failed (Qdrant query returned an error envelope, embedding call failed)",
	)
	// ErrInvalidBatchMode is the canonical sentinel for a
	// BatchSpec whose Mode is not in the closed set
	// (catalog_only / materialize_top_k). godlike/06 SSOT:
	// distinct from ErrBatchNotReconcilable (terminal-state
	// refusal) so the wire handler can branch 400 (mode) vs 409
	// (terminal-state) cleanly.
	ErrInvalidBatchMode = errors.New(
		"mediamemory: batch mode outside canonical closed set (use catalog_only or materialize_top_k)",
	)
	// ErrBatchSpecDrift is the canonical sentinel for the
	// idempotent-by-name CreateBatch path: when a caller supplies
	// the same Spec.Name + a different Spec body (e.g. switched
	// Mode from catalog_only to materialize_top_k after the
	// parent was already created), the canonical SSOT rejects
	// the second call. godlike/06 SSOT: Spec is immutable
	// post-CreateBatch so the worker treats the parent shape
	// as fixed for the batch lifetime.
	ErrBatchSpecDrift = errors.New(
		"mediamemory: batch Spec drift on idempotent CreateBatch (same Name + different body)",
	)
)

// Provider is the canonical small string the mediamemory
// capability stamps on every MediaCandidate it emits,
// regardless of source (Level 1-2 exact, Level 3-7 semantic
// index, Level 8 local catalog, Level 9 external SearchFanOut).
//
// godlike/06 SSOT (closed set): every Provider the mediamemory
// capability emits is one of the canonical values below; the
// ranker and dashboard switch on these strings. Adding a new
// value requires a godlike/06 SSOT review because both the
// ranker's Source upgrade logic and the dashboard's per-source
// diagnostics aggregate over the closed set.
//
// Note: MediaCandidate.Provider (and MediaBinding.Provider)
// MAY also carry forwarded Source values from external
// SearchFanOut (e.g. "artlist", "youtube") that originate
// outside the mediamemory capability and are not closed-set
// here — those are translucent handoffs, not first-class
// providers. The IsKnownProvider predicate returns true for
// both mediamemory-owned tags (ProviderLocal /
// ProviderSemanticIndex) AND the translucent handoff tags so
// a binding row never carries an unknown provider string.
const (
	// ProviderLocal is the canonical tag for bindings created
	// by the dashboard manual-editor path. Migration 170
	// backfills pre-Fase-4.3 rows to this value via the
	// ALTER TABLE DEFAULT 'local' clause. godlike/06 SSOT:
	// dispatch on this string only through IsKnownProvider.
	ProviderLocal = "local"
	// ProviderSemanticIndex is the tag stamped on candidates
	// emitted by mediamemory.SemanticLookup
	// (QdrantSemanticLookup). godlike/06 SSOT: do NOT
	// dispatch on string compare; use IsKnownProvider.
	ProviderSemanticIndex = "mediamemory.semantic"
	// ProviderArtlist is the translucent handoff tag for
	// bindings auto-linked from the Artlist SearchFanOut
	// provider. NOT a mediamemory-owned first-class provider;
	// accepted verbatim on MediaBinding.Provider / MediaCandidate.Provider
	// rows so the dashboard's per-source diagnostics can
	// aggregate per-Artlist.
	ProviderArtlist = "artlist"
	// ProviderYouTube is the translucent handoff tag for
	// bindings auto-linked from the YouTube SearchFanOut
	// provider.
	ProviderYouTube = "youtube"
	// ProviderPexels is the translucent handoff tag for
	// bindings auto-linked from the Pexels image provider
	// (Fase 4.1).
	ProviderPexels = "pexels"
)

// IsKnownProvider reports whether p is in the canonical
// closed set of mediamemory-owned provider tags AND the
// translucent handoff tags from external SearchFanOut
// providers. godlike/06 SSOT (one-canonical-predicate-next-
// to-closed-set): a companion predicate is the standard SSOT
// pattern for any enum-style string surface. A return value
// of false means the caller should reject the binding /
// candidate row rather than silently rank an unknown provider.
func IsKnownProvider(p string) bool {
	switch p {
	case ProviderLocal, ProviderSemanticIndex,
		ProviderArtlist, ProviderYouTube, ProviderPexels:
		return true
	}
	return false
}

// ── Fase 3.2 linker wire envelopes (godlike/06 SSOT) ─────────────

// LinkerRequest is the per-candidate input bundle consumed by
// LinkerWorker.EnrichCandidate. godlike/06 SSOT (narrow port
// doctrine): the envelope carries ONLY the candidate and a
// ProjectID for rights-trail context. Provider name + media
// type are derived from the candidate itself so the worker
// cannot branch on caller-supplied ownership data that could
// drift from the canonical MediaCandidate row.
type LinkerRequest struct {
	Candidate MediaCandidate
	ProjectID string
	Language  string
}

// LinkerResult is the per-candidate output of EnrichCandidate.
// godlike/06 SSOT: PersistedBindingIDs + IndexedConceptIDs +
// DiscoveryStatus are the canonical durable footprint of one
// EnrichCandidate call; Failures is the canonical per-step
// failure record for the dashboard. The orchestrator (batch_
// service.EnrichLinker) accumulates Failures into the parent
// Failure channel without re-formatting them.
type LinkerResult struct {
	PersistedBindingIDs []string
	IndexedConceptIDs   []string
	DetectedEntities    []string // canonical free-text labels, NOT concept IDs
	Status              DiscoveryStatus
	Failures            []string
	Empty               bool // true when the linker short-circuited via the idempotency no-op path
}

// EncodingChannels is the canonical multichannel input bundle
// for EmbeddingEncoder.Encode. godlike/06 SSOT (channel
// SSOT): text + transcript + visual_desc + audio + BM25
// sparse are the canonical channels. Empty strings are NOT a
// silent zero-output (godlike/07) — receivers can choose to
// reject an Encode call whose Text AND Transcript AND
// VisualDesc are all empty, but the canonical default is to
// return a zero-vector so the canonical embedding call site
// surfaces ErrLinkerEmbeddingFailed on its own predicate.
type EncodingChannels struct {
	Text       string
	Transcript string
	VisualDesc string
	Audio      string
}

// MediaEmbedding is the canonical model output of
// EmbeddingEncoder.Encode. godlike/06 SSOT (vector SSOT):
// Vector is dense float32 in the model's native dimensionality.
type MediaEmbedding struct {
	Vector []float32
	Dim    int
	Model  string // encoder identifier (used by EmbeddingIndexer for Qdrant payload stamping)
}

// TranscriptSegment is one window of the transcriber output.
// godlike/06 SSOT: StartMs / EndMs / Text is the canonical
// 3-tuple. Phase 3.5 stockpipeline-level transcriber is the
// production adapter.
type TranscriptSegment struct {
	StartMs int64
	EndMs   int64
	Text    string
}

// Keyframe is one still-frame the linker indexes for visual
// description. godlike/06 SSOT: the canonical wire shape
// carries timestamp + raw URL/blob + an optional pre-computed
// embedding (forward-pointer to Fase 4.1 visual channel).
// For Fase 3.2 the URL is the canonical envelope; ImageData
// is left optional.
type Keyframe struct {
	Ms        int64
	ImageURL  string
	ImageData []byte    // optional pre-fetched bytes for offline encoders
	Embedding []float32 // optional, set by Fase 4.1 visual-channel encoder
}

// Linker sentinels (godlike/06 SSOT: same envelope family as
// discovery-worker sentinels; godlike/07 NO-FAKE-AVAILABILITY:
// every non-trivial linker failure surfaces a typed envelope
// so the BatchService.EnrichLinker orchestrator can branch
// hard-fail (Failed+continue) vs resumable (leave Searched +
// return).

// ErrLinkerUnmappableConcept is the HARD-fail sentinel: the
// linker could not produce any (concept × slot_kind) tuple
// for the candidate (e.g. zero detectable entities, zero
// mappable visual actions). The candidate's DiscoveryStatus
// is set to DiscoveryFailed at the batch orchestrator and the
// batch continues with the next candidate. Operator-visible
// in the dashboard per-candidate Failures[] column.
var ErrLinkerUnmappableConcept = errors.New(
	"mediamemory: linker could not map candidate to any (concept × slot_kind) tuple (no detectable entities / no mappable visual actions)",
)

// ErrLinkerExtractFailed is the FAIL-CLOSED envelope for any
// failure in the extraction phase (TranscriptExtractor /
// KeyframeExtractor / VisualDescriptionGenerator). Wrapped
// with %w so the BatchService orchestrator's errors.Is branch
// routes it through its per-candidate failure record. The
// candidate's DiscoveryStatus is NOT mutated on this envelope
// so a subsequent EnrichLinker retry re-runs the full
// extraction pipeline naturally (idempotent Resume contract).
var ErrLinkerExtractFailed = errors.New(
	"mediamemory: linker extractor failed (transcript / keyframe / visual description generator)",
)

// ErrLinkerEmbeddingFailed is the FAIL-CLOSED envelope for the
// multichannel embedding encoder path. Same Resume-on-retry
// semantics as ErrLinkerExtractFailed — the candidate stays
// at DiscoverySearched until the encoder returns a valid
// vector. godlike/07 NO-FAKE-AVAILABILITY: a zero-length
// vector is NOT silently accepted; canonical embedding call
// sites check len(MediaEmbedding.Vector) > 0 before stamping
// the Qdrant payload and surface ErrLinkerEmbeddingFailed
// otherwise.
var ErrLinkerEmbeddingFailed = errors.New(
	"mediamemory: linker multichannel embedding encoder failed",
)

// ErrLinkerConceptAssignmentFailed is the FAIL-CLOSED envelope
// for the concept-assignment phase: the EntityDetector /
// ConceptAssigner generated zero canonical concepts.
// godlike/06 SSOT distinct from ErrLinkerUnmappableConcept
// (assignment-failure vs unmappable: a zero-concept assignment
// result is operational; an unmappable result is semantic).
// Both end up at DiscoveryFailed on the candidate.
var ErrLinkerConceptAssignmentFailed = errors.New(
	"mediamemory: linker concept assignment layer returned zero concepts",
)

// ErrLinkerInvariantBroken is the PANIC-equivalent sentinel:
// the linker reached an internal post-write state that the
// canonical invariants forbid (e.g. binding persisted without
// a concept row, embedding stamped to Qdrant without a
// concept_id Match). godlike/07 NO-FAKE-AVAILABILITY: this is
// NEVER recoverable from Resume — it surfaces a 500-level
// typed envelope and the candidate goes to DiscoveryFailed.
var ErrLinkerInvariantBroken = errors.New(
	"mediamemory: linker internal invariant broken (binding-without-concept or embedding-without-binding detected post-write)",
)
