package vidrush

import (
	"context"
	"strings"
	"time"
	"unicode"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/cacheutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"golang.org/x/text/unicode/norm"
)

// entityImageSurfaceEnabled applies both the entity-image policy and the
// caller's retrieval mode. explicit_only grants the retrieval stage authority
// only to the searches in the media plan; disabled grants no image search to
// any stage. Without this shared guard, later materialization could launch a
// second entity query after the fan-out had correctly honored the mode.
func entityImageSurfaceEnabled(plan *scriptpkg.ResolvedGenerationPlan) bool {
	if plan == nil || !plan.MediaPlan.Extraction.EntityImageSurfaceEnabled() {
		return false
	}
	mode := strings.TrimSpace(plan.MediaPlan.Extraction.ImageSearchMode)
	return mode != mediadomain.ImageSearchModeExplicitOnly && mode != mediadomain.ImageSearchModeDisabled
}

// normalizeEntityMatch is the identity join for entity-image candidates:
// case-folded, accent-stripped and English-possessive-stripped.
func normalizeEntityMatch(value string) string {
	decomposed := norm.NFD.String(strings.ToLower(strings.TrimSpace(value)))
	var b strings.Builder
	for _, r := range decomposed {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(r)
	}
	// NER commonly emits an English possessive when the name is the subject
	// of a sentence ("Dwayne Johnson's journey"), while image candidates are
	// keyed by the canonical identity ("Dwayne Johnson").
	return strings.TrimSuffix(strings.TrimSuffix(b.String(), "'s"), "’s")
}

// internetImageNegativeTTL bounds how long a completed-but-empty provider
// search is remembered. Empty is a temporary observation, never a fact: 15
// minutes is long enough to stop a scene loop from paying the same 20-117 s
// wait for an entity that has no usable portrait, and short enough that a
// provider index change is picked up by the next run (ELIM-01).
const internetImageNegativeTTL = 15 * time.Minute

// internetImageCachePayload stores only the image-provider delta so a cache
type internetImageCachePayload struct {
	Candidates []scriptpkg.SegmentAssetCandidate
	// Negative marks a search that COMPLETED with zero usable candidates.
	// It is a reason not to wait again, never proof of absence: every
	// correctness gate still sees an empty candidate set, and readers must
	// ignore the entry after internetImageNegativeTTL or on ForceRefresh.
	Negative       bool      `json:"negative,omitempty"`
	NegativeReason string    `json:"negative_reason,omitempty"`
	ObservedAt     time.Time `json:"observed_at,omitempty"`
}

// internetImageNegativeFresh reports whether a negative entry may skip the
// provider call under the canonical 15-minute observation window.
func internetImageNegativeFresh(payload internetImageCachePayload, now time.Time) bool {
	return internetImageNegativeFreshWithin(payload, now, internetImageNegativeTTL)
}

// internetImageNegativeFreshWithin is the same rule parameterized by the
// policy TTL. A zero timestamp is a legacy/unknown observation and is treated
// as stale, so an old payload can never become a permanent block; a non-positive
// TTL means negative payloads are never replayable.
func internetImageNegativeFreshWithin(payload internetImageCachePayload, now time.Time, ttl time.Duration) bool {
	if !payload.Negative || payload.ObservedAt.IsZero() || ttl <= 0 {
		return false
	}
	return !now.After(payload.ObservedAt.Add(ttl))
}

// entityImageL1Capacity bounds the canonical entity-key L1 (see
// vidrush_helpers.go for the VidRush L1 cache contract).
const entityImageL1Capacity = 4096

var (
	// entityImageCache is the bounded L1 for canonical entity key ->
	// []SegmentAssetCandidate (see vidrush_helpers.go for the L1 contract).
	entityImageCache = cacheutil.NewLRU(entityImageL1Capacity)
	// entityImageLocks is the canonical reference-counted per-key locker: it
	// serialises concurrent provider/catalog lookups for the same entity key
	// while leaving different keys fully parallel. Unlike the retired
	// sync.Map-of-mutexes registry it never leaks entries — the key is removed
	// when the last holder releases (pkg/concurrent.KeyedLocker).
	entityImageLocks = concurrent.NewKeyedLocker()
	// entityImageDiscoveryLocks serializes the first provider discovery for a
	// canonical entity. It is deliberately separate from entityImageLocks:
	// the catalog-state lock must be released before network I/O, while this
	// lock keeps concurrent cold requests for the same identity from fanning
	// out N provider calls. Different entities use different keys.
	entityImageDiscoveryLocks = concurrent.NewKeyedLocker()
)

// MediaResolverImageStage handles image discovery for the canonical media
// resolver path. It attaches provider candidates but never selects a winner.
// attaches every unique result returned for the segment queries.
type MediaResolverImageStage struct {
	searcher InternetImageSearcher
	metrics  VidRushMetrics
	cache    scriptports.VidRushCachePort
	catalog  entitycatalog.Repository
	// semanticScorer may be absent for contextual images. Person-identity
	// candidates still fail closed at the binding boundary without a visual
	// verdict.
	semanticScorer SemanticCandidateScorer
	// manifestPins is the opt-in pinned-manifest fast path (see
	// media_manifest_shim.go). Nil keeps every call site byte-for-byte
	// identical to the un-pinned behavior.
	manifestPins *MediaManifestPins
	// contentStore resolves a materialized catalog candidate's local path from
	// its content address.
	contentStore EntityImageContentStore
}

// WithCASStore installs the content-addressed store read side. Optional.
func (p *MediaResolverImageStage) WithCASStore(store EntityImageContentStore) *MediaResolverImageStage {
	if p != nil {
		p.contentStore = store
	}
	return p
}

func NewMediaResolverImageStageWithCatalog(searcher InternetImageSearcher, cache scriptports.VidRushCachePort, catalog entitycatalog.Repository, metrics ...VidRushMetrics) *MediaResolverImageStage {
	var m VidRushMetrics
	if len(metrics) > 0 {
		m = metrics[0]
	}
	return &MediaResolverImageStage{searcher: searcher, metrics: m, cache: cache, catalog: catalog}
}

// WithSemanticScorer attaches the optional semantic candidate scorer. Person
// identity candidates require a trusted verdict before selection.
func (p *MediaResolverImageStage) WithSemanticScorer(scorer SemanticCandidateScorer) *MediaResolverImageStage {
	if p != nil {
		p.semanticScorer = scorer
	}
	return p
}

func (p *MediaResolverImageStage) Name() ProcessorName { return ProcessorInternetImages }

func (p *MediaResolverImageStage) Policy(_ *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return ProcessorBestEffort
}

func (p *MediaResolverImageStage) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error) {
	if plan == nil {
		return &PostProcessResult{}, nil
	}
	options := internetImageOptions(plan)
	if result, handled := p.handleBypassOrUnavailable(plan, input, options); handled {
		return result, nil
	}
	if len(input.VidRushSegments) == 0 {
		return &PostProcessResult{}, nil
	}
	return p.processInternetImageSegments(ctx, plan, input, options)
}

type internetImageProcessOptions struct {
	cacheOnly           bool
	entityImagesEnabled bool
}

func (p *MediaResolverImageStage) handleBypassOrUnavailable(plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput, options internetImageProcessOptions) (*PostProcessResult, bool) {
	if !plan.MediaPlan.ProviderPolicy.InternetImages.AsBool() && !options.entityImagesEnabled {
		segments := markInternetImagesBypassed(input.VidRushSegments)
		if len(segments) == 0 {
			return &PostProcessResult{}, true
		}
		return &PostProcessResult{VidRushSegments: segments, Changed: true}, true
	}
	if !hasImageSearchSegments(plan, input) {
		return &PostProcessResult{VidRushSegments: markInternetImagesBypassed(input.VidRushSegments), Changed: true}, true
	}
	if p.searcher == nil && !options.cacheOnly {
		return &PostProcessResult{Changed: true, Warnings: []string{"internet_images: InternetImageSearcher not configured"}}, true
	}
	return nil, false
}

func markInternetImagesBypassed(input []scriptpkg.VidRushSegmentResult) []scriptpkg.VidRushSegmentResult {
	segments := make([]scriptpkg.VidRushSegmentResult, 0, len(input))
	for _, segment := range input {
		cloned := CloneVidRushSegmentResult(segment)
		cloned.Cache.InternetImages = "BYPASSED"
		segments = append(segments, cloned)
	}
	return segments
}

// internetImageDiscoveryMode is what a segment's discovery pass decides to do.
// It is a closed set because the caller branches on it: a new outcome must be a
// new constant, not a silently different query list.
type internetImageDiscoveryMode string

const (
	// internetImageDiscoveryBypass means the segment asked for no image at all
	// (no contextual query, no entity query, no manual query).
	internetImageDiscoveryBypass internetImageDiscoveryMode = "bypass"
	// internetImageDiscoveryReuse means the fan-out already owns discovery for
	// every requested identity; the provider must not be called again.
	internetImageDiscoveryReuse internetImageDiscoveryMode = "reuse_fanout"
	// internetImageDiscoverySearch means the merged queries must be resolved by
	// the query-keyed cache/provider path.
	internetImageDiscoverySearch internetImageDiscoveryMode = "search"
)

// internetImageDiscoveryPlan is the pure answer to "what does this segment
// search, and why?".
type internetImageDiscoveryPlan struct {
	Mode    internetImageDiscoveryMode
	Queries []string
}

// planInternetImageDiscovery merges the three query sources and decides whether
// discovery already happened. It is the rule that used to live inline in the
// segment loop, where the only way to test it was to run the stage:
//
//  1. identity wins over topic: in entity-image mode the entity queries REPLACE
//     the contextual ones (a courthouse view cannot stand in for a portrait);
//  2. an operator's manual query is always kept, appended after the derived
//     ones and deduped case-insensitively;
//  3. with no query left there is nothing to search, so the segment is bypassed;
//  4. a fan-out hit is sufficient for contextual images, but in entity mode it
//     only counts when every requested identity already has a matching
//     candidate — otherwise the segment search would be suppressed by a warm
//     generic scene image.
func planInternetImageDiscovery(contextual, entity, manual []string, entityMode, fanoutOwned bool, covers func(string) bool) internetImageDiscoveryPlan {
	// Explicit media-plan searches are the operator's complete retrieval scope
	// for this segment. Mixing them with inferred entities or semantic hints
	// lets generic fragments (for example a month or a court label) consume the
	// image budget with unrelated results.
	if explicit := imageQueryList(manual); len(explicit) > 0 {
		if fanoutOwned {
			return internetImageDiscoveryPlan{Mode: internetImageDiscoveryReuse, Queries: explicit}
		}
		return internetImageDiscoveryPlan{Mode: internetImageDiscoverySearch, Queries: explicit}
	}
	base := imageQueryList(contextual)
	if entityMode {
		if identity := imageQueryList(entity); len(identity) > 0 {
			base = identity
		}
	}
	queries := imageQueryList(append(append([]string(nil), base...), manual...))
	if len(queries) == 0 {
		return internetImageDiscoveryPlan{Mode: internetImageDiscoveryBypass}
	}
	covered := true
	if entityMode && covers != nil {
		for _, query := range queries {
			if !covers(query) {
				covered = false
				break
			}
		}
	}
	if fanoutOwned && covered {
		return internetImageDiscoveryPlan{Mode: internetImageDiscoveryReuse, Queries: queries}
	}
	return internetImageDiscoveryPlan{Mode: internetImageDiscoverySearch, Queries: queries}
}

// imageQueryList trims, drops empties and dedupes while preserving the order,
// so the same inputs always produce the same provider calls.
func imageQueryList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

// fanoutAlreadyOwnsInternetImageDiscovery reports whether the incremental
// VidRush fan-out already produced the internet-image candidate set for this
// segment. HIT_EXACT and REFRESHED are authoritative: a second provider search
// in the postprocessor would duplicate the exact work the fan-out just did.
//
// MISS is deliberately excluded: an empty/negative fan-out answer stays
// retryable by the postprocessor, matching the existing no-negative-cache
// contract. REFRESHED is also authoritative on a force-refresh plan because
// the fan-out performed that refresh itself.
func fanoutAlreadyOwnsInternetImageDiscovery(plan *scriptpkg.ResolvedGenerationPlan, segment *scriptpkg.VidRushSegmentResult) bool {
	if plan == nil || segment == nil {
		return false
	}
	state := strings.ToUpper(strings.TrimSpace(segment.Cache.InternetImages))
	if state != "HIT_EXACT" && state != "REFRESHED" {
		return false
	}
	if (plan.ForceRefresh || plan.MediaPlan.ForceRefreshAssets) && state != "REFRESHED" {
		return false
	}
	return len(internetImageCandidatesFromSelection(segment.Assets)) > 0
}

// reuseFanoutInternetImageCandidates projects the candidates already attached
// by the fan-out onto both asset surfaces the materializer reads. It never
// invents a candidate and never calls a provider.
func reuseFanoutInternetImageCandidates(segment *scriptpkg.VidRushSegmentResult) {
	if segment == nil {
		return
	}
	candidates := internetImageCandidatesFromSelection(segment.Assets)
	if len(candidates) == 0 {
		return
	}
	segment.Assets.Candidates = AppendProviderCandidatesUnique(segment.Assets.Candidates, candidates)
	segment.Assets.SecondaryImages = AppendProviderCandidatesUnique(segment.Assets.SecondaryImages, candidates)
}

func internetImageCandidatesFromSelection(assets scriptpkg.SegmentAssetSelection) []scriptpkg.SegmentAssetCandidate {
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(assets.Candidates)+len(assets.SecondaryImages))
	seen := make(map[string]struct{}, cap(out))
	for _, candidate := range append(append([]scriptpkg.SegmentAssetCandidate(nil), assets.Candidates...), assets.SecondaryImages...) {
		if !strings.EqualFold(strings.TrimSpace(candidate.Provider), scriptpkg.VidRushProviderInternetImages) {
			continue
		}
		key := vidRushCandidateIdentity(candidate)
		if key == "" {
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, candidate)
	}
	return out
}
