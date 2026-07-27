// Package mediamemory — types_entities.go is the canonical home
// for the persistent/projected entity types: MediaConcept
// (concept row), MediaBinding (concept × asset × slot_kind link),
// MediaCandidate (discovery result, pre-binding), BatchSpec /
// Batch / BatchChild (the Fase 3.4 catalog-only batch surface
// godlike/06 SSOT), and UsageEvent (append-only feedback audit).
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

	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
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
