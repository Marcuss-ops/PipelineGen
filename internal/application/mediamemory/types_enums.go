// Package mediamemory — types_enums.go is the canonical home for
// the closed-set enums (ConceptType / ApprovalStatus / Origin /
// DiscoveryStatus / MaterializationStatus / RightsStatus /
// BatchMode / BatchState / FeedbackAction) with their canonical
// constants + IsKnownXxx predicates, plus the Provider tag closed
// set (ProviderLocal / ProviderSemanticIndex / ProviderArtlist /
// ProviderYouTube / ProviderPexels) + IsKnownProvider predicate.
//
// godlike/06 SSOT (one-canonical-(type + constants + predicate)
// triple): every enum declares its closed-set constants
// immediately after the type AND the IsKnownXxx sentinel-predicate
// helper next to the enum so the closed-set membership check
// never drifts from the type itself.
//
// godlike/06 SSOT (Provider SSOT, closed-set transparency): the
// Provider tag is the canonical source-tag the mediamemory
// capability stamps on every binding / candidate; both
// mediamemory-owned tags AND the translucent handoff tags from
// external SearchFanOut providers are validated by IsKnownProvider
// so a binding row never carries an unknown provider string.
//
// File split ownership (godlike/06 SSOT):
//   - types.go               : package doc + SlotKind alias  ← canonical doc + dm alias only
//   - types_enums.go         : 9 enums + their constants + 9 IsKnown predicates + Provider tag constants + IsKnownProvider  ← this file
//   - types_entities.go      : MediaConcept + MediaBinding + MediaCandidate + BatchSpec + Batch + BatchChild + UsageEvent
//   - types_resolver.go      : VisualIntent + SceneSpec + Layer + CandidateOption + SceneIntent + SceneBackendCall + SceneResolutionTrace + SceneVisualPlan + ResolvePolicy + OptionalResolvePolicy + ResolveRequest + ResolveResult
//   - types_linker.go        : LinkerRequest + LinkerResult + EncodingChannels + MediaEmbedding + TranscriptSegment + Keyframe
//   - types_sentinels.go     : 19 sentinel errors (14 phase 1.x + 5 ErrLinker*)
package mediamemory

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

// ── Provider tag constants (godlike/06 closed-set SSOT + IsKnown) ─

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
