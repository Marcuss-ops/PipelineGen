// Package mediamemory — types_entities.go is the canonical home
// for the persistent/projected entity types: MediaConcept
// (concept row), MediaBinding (concept × asset × slot_kind link),
// MediaCandidate (discovery result, pre-binding), BatchSpec /
// Batch / BatchChild (the Fase 3.4 catalog-only batch surface
// godlike/06 SSOT). UsageEvent left with the Fase 2.3 anti-repetition surface
// on 2026-09-20 (see the note where BatchSpec used to be).
//
// godlike/06 SSOT (sister to search.Candidate): MediaCandidate
// mirrors the canonical search.Candidate projection: NO
// LocalPath, NO DriveLink, NO server-internal locator in the
// wire shape. The binder layer reads AssetDeliveryService to
// mint short-lived URLs at the HTTP boundary. This package only
// owns the binding surface (concept → asset_id → slot → score),
// not delivery URLs.
//
// godlike/06 SSOT (idempotency anchor): Batch is the parent;
// BatchChild is the (query × provider) sub-job. The parent's
// Spec (BatchSpec) is the canonical immutable input — Fase 3.4
// SQL durability lands behind media_batches with a UNIQUE(name)
// constraint so resume-after-crash flow sees the same canonical
// Spec across recovery.
//
// File split ownership (godlike/06 SSOT):
//   - types.go               : package doc + SlotKind alias
//   - types_enums.go         : 9 enums + their constants + 9 IsKnown predicates + Provider tag constants + IsKnownProvider
//   - types_entities.go      : MediaConcept + MediaBinding + MediaCandidate + BatchSpec + Batch + BatchChild + UsageEvent  ← this file
//   - types_resolver.go      : VisualIntent + SceneSpec + Layer + CandidateOption + SceneIntent + SceneBackendCall + SceneResolutionTrace + SceneVisualPlan + ResolvePolicy + OptionalResolvePolicy + ResolveRequest + ResolveResult
//   - types_linker.go        : LinkerRequest + LinkerResult + EncodingChannels + MediaEmbedding + TranscriptSegment + Keyframe
//   - types_sentinels.go     : 19 sentinel errors (14 phase 1.x + 5 ErrLinker*)
package mediamemory

import (
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

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
// skips the normal validation (legacy rows pending backfill).
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

// BatchSpec / Batch / BatchChild were DELETED here on 2026-09-20 with the
// Fase 3.4 catalog-only batch surface they described (batch_service*,
// discovery_worker, acquisition_planner, types_linker). Nothing in production
// ever constructed a BatchService, so these envelopes were reachable only from
// `batch_service_test.go`. MaterializationRequest (media_materialize_worker.go)
// is the surviving worker input, and MediaCandidate below is still the
// candidate envelope the production materialize path uses.

// UsageEvent was DELETED here on 2026-09-20. It was the append-only feedback
// audit row: FeedbackService wrote it and the resolver's project-history read
// consumed it to derive the same-asset / channel-saturation / channel-recency
// penalties. With the feedback service gone (never constructed) and the
// UsageRepository port gone (zero callers), nothing could produce or read a
// UsageEvent, so the type and the three penalty components it fed left in the
// same change — see the retirement note on PopulateRepetitionPenalty in
// ranker_repetition.go.
