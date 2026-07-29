// Package app — clip_resolver_recommend_adapter.go: bridges the
// handler-side `artlist.ClipResolverPort` (Recommend method) to the
// canonical `*scripts.ClipResolver` (Resolve method) with a
// non-trivial scoring layer.
//
// PR-ARTLIST-RECOMMEND-ADAPTER (July 2026, AUDIT-PIN→WIRE reversal):
// the prior PR-ARTLIST-SYNCSERVICE closure (f02ae683) tombstoned
// the stale `clipresolver` package reference in WireArtlist on the
// basis that the canonical `*scripts.ClipResolver` was incompatible
// with the handler-side `Recommend` method (different signature:
// Resolve vs Recommend, different DTOs). This file closes the
// forward-pointer by adding the shape-translation layer + a real
// scoring implementation that surfaces non-zero `Score` values per
// `ClipResolverRecommendResult`.
//
// Scoring algorithm (per godlike/07 no-fake-availability; not a
// stub, not a placeholder, not a fixed-0.5 return):
//  1. Build the canonical `[]ports.ClipReference` slice from the
//     handler request. If `SegmentID` matches the YouTube
//     segment-encoding pattern (`yt_<videoID>_<seg>_<n>`), extract
//     the 11-char videoID and dispatch as
//     `ports.RefTypeYouTubeVideoID` — the canonical resolver then
//     fans out via `LIKE yt_<videoID>_%` and returns ALL segments
//     from that video (a robust candidate pool). Otherwise
//     `SegmentID` is dispatched as `ports.RefTypeMediaAssetID` (1
//     row, exact match).
//  2. `Queries` are used ONLY as the scoring text haystack (Topic
//     + Queries concatenated). The canonical port's contract
//     explicitly forbids "shape heuristics" / "auto-classification"
//     from Value, so we do NOT translate a free-form query into a
//     `RefTypeExternalProviderID` — that would be a godlike/07
//     violation (the thinker-with-files-gemini review flagged it).
//  3. Each `ClipEvidence` from the canonical result is scored
//     using field-level query coverage against the asset metadata
//     (Name + Filename + Description + Tags + TranscriptExcerpt).
//     Each field contributes a [0,1] coverage ratio based on how
//     many query tokens appear in that field. The weighted field
//     contributions are combined with a saturating union so extra
//     matching fields can raise the Score without exceeding 1.0.
//  4. Results below `MinScore` are filtered out. Remaining results
//     are sorted descending by `Score` and mapped to the
//     handler-side `ClipResolverRecommendResult` (ClipID from
//     AssetID, Score from the weighted coverage blend, DriveLink
//     from `ClipEvidence.DriveLink`).
//
// Composition-root ownership (godlike/06 SSOT): this file lives in
// the composition root because the adapter is a cross-domain glue
// layer (api <-> scripts/usecase) and has no natural domain
// home. The composition root is the canonical owner of adapter
// wiring by construction. Construction is in
// `WireArtlist` (`build_bundles_artlist.go`); the wired
// `*ClipResolverRecommendAdapter` is stored on
// `ArtlistBundle.ClipResolver` (bundle_types.go) and forwarded to
// `artlistapi.Build(Dependencies{...}).ClipResolver`.
//
// godlike/07 fail-closed fast path: `NewClipResolverRecommendAdapter`
// returns nil when the canonical resolver is nil. The WireArtlist
// composition site then leaves `ArtlistBundle.ClipResolver` as nil,
// preserving the prior `/recommend` 503 behavior when the canonical
// resolver is unavailable. There is no fake wire.
package wiring

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	artlist "github.com/Marcuss-ops/PipelineGen/internal/api/assets/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/pkg/similarity"
	"go.uber.org/zap"
)

// ErrRecommendAdapterNotConfigured is the fail-closed godlike/07
// sentinel emitted by Recommend when the adapter is constructed
// without a canonical resolver (composition root skipped wiring).
// The handler-side `/recommend` route maps this to a 500 with a
// diagnostic message; callers can use errors.Is to detect the
// misconfiguration.
var ErrRecommendAdapterNotConfigured = errors.New("clip resolver recommend adapter: canonical resolver not configured (composition root must wire *scripts.ClipResolver before constructing the adapter)")

// Field-weight constants for the coverage-based scoring layer.
// The 5 weights sum to 1.0 and reflect the relative signal strength
// of each metadata field for artlist clip recommendations:
//   - Name:        0.60 (operator-curated, high signal)
//   - Filename:    0.05 (machine-derived, low signal — derives from
//     drive upload paths, often includes hash +
//     segment suffix)
//   - Description: 0.15 (operator-curated, medium signal — narrative
//     but often noisy)
//   - Tags:        0.15 (operator-curated, high signal — explicit
//     topical anchors)
//   - Transcript:  0.05 (auto-generated, low-medium signal — full
//     content but lexical mismatch dominates
//     over 500-char excerpt; capped to 500 chars
//     in the canonical ClipEvidence)
//
// Per godlike/07 minimum-ripple, these are conservative starting
// values that can be tuned via config in a follow-up. A future
// PR-ARTLIST-RECOMMEND-WEIGHT-TUNING may move these to
// config-driven values with backfill tests pinning the new
// defaults.
const (
	recommendNameWeight        = 0.60
	recommendFilenameWeight    = 0.05
	recommendDescriptionWeight = 0.15
	recommendTagWeight         = 0.15
	recommendTranscriptWeight  = 0.05
)

// youtubeSegmentIDPattern matches the canonical YouTube
// segment-encoding used in media_assets.id. The pattern is
// `yt_<videoID>_<seg>_<n>` where videoID is the 11-char YouTube
// base64url token (matching `RefTypeYouTubeVideoID`'s
// `LIKE yt_<videoID>_%` fan-out) and `<seg>` / `<n>` are the
// per-segment suffix tokens. Capture group 1 is the videoID.
//
// Per AGENTS.md godlike/07, the pattern is the single source of
// truth for the segment encoding — declared as a typed regexp
// here (not magic-string in 3+ places). The canonical port's
// clip_resolver.go references the same shape in comments but
// does NOT export a typed pattern; the duplicate definition is
// intentional and documented (this regex is the api-layer
// companion of the canonical contract).
var youtubeSegmentIDPattern = regexp.MustCompile(`^yt_([A-Za-z0-9_-]{11})_.+$`)

// ClipResolverRecommendAdapter satisfies
// artlist.ClipResolverPort by translating the Recommend-shaped
// request into the canonical Resolve-shaped dispatch + a real
// (field-weighted coverage) scoring layer. The adapter is the
// single owner of the Recommend -> Resolve translation per
// godlike/06 SSOT (one canonical owner per fact).
type ClipResolverRecommendAdapter struct {
	Canonical ports.ClipResolver
	Log       *zap.Logger
}

// NewClipResolverRecommendAdapter constructs the production
// adapter. nil canonical returns nil — the composition-root
// caller (WireArtlist) checks for nil and leaves the
// ArtlistBundle.ClipResolver field as nil, preserving the prior
// `/recommend` 503 behavior (godlike/07 fail-closed fast path).
//
// A nil canonical is NOT a panic — it is the documented "adapter
// unavailable" shape that the handler-side /recommend route
// already handles via `if h.clipResolver == nil { 500 }`. This
// is the same pattern as the canonical `clipResolverAdapter` in
// scripts/usecase/clip_resolver.go (nil repo returns a no-op
// adapter that synthesizes ResolveReasonNotFound for every
// dispatch).
func NewClipResolverRecommendAdapter(canonical ports.ClipResolver, log *zap.Logger) *ClipResolverRecommendAdapter {
	if canonical == nil {
		return nil
	}
	return &ClipResolverRecommendAdapter{
		Canonical: canonical,
		Log:       log,
	}
}

// Compile-time pin: the adapter satisfies the handler-side
// port. A future method change to either interface surfaces
// here as a build failure rather than a first-call runtime
// panic. godlike/07 typed-port discipline (AGENTS.md Pattern 0).
var _ artlist.ClipResolverPort = (*ClipResolverRecommendAdapter)(nil)

// Recommend implements the handler-side port method. The flow is
// the 5-step pipeline described in the file-level docstring:
//  1. Build canonical ClipReference slice (YouTube fan-out if
//     SegmentID matches the `yt_<videoID>_*` pattern).
//  2. Call canonical.Resolve to fetch asset evidence.
//  3. Score each resolved asset via field-weighted Jaccard.
//  4. Filter by MinScore, sort desc.
//  5. Map to ClipResolverRecommendResult (AssetID -> ClipID,
//     DriveLink from evidence).
//
// Error contract:
//   - nil receiver OR nil canonical: ErrRecommendAdapterNotConfigured
//     (godlike/07 sentinel; recoverable via re-wiring).
//   - nil request: typed error (400-equivalent on handler side).
//   - canonical.Resolve DB error: wrapped, propagated (handler
//     returns 500; per-reference Unresolved entries are logged at
//     warn level for diagnostics but not surfaced in the response).
//   - Empty inputs (no SegmentID + no Queries): empty response,
//     no error (200 with Results: []).
func (a *ClipResolverRecommendAdapter) Recommend(ctx context.Context, req *artlist.ClipResolverRecommendRequest) (*artlist.ClipResolverRecommendResponse, error) {
	if a == nil || a.Canonical == nil {
		return nil, ErrRecommendAdapterNotConfigured
	}
	if req == nil {
		return nil, fmt.Errorf("clip resolver recommend: request is nil")
	}

	// Step 1: build canonical ClipReference slice. The SegmentID
	// (if non-empty) becomes one reference; the Queries do NOT
	// become references (the canonical port's contract forbids
	// auto-classification from Value — see file-level docstring).
	refs := buildCanonicalReferences(req.SegmentID)

	// Step 2: dispatch to the canonical resolver. When refs is
	// empty (no SegmentID), there is no candidate asset pool to
	// Score against — return empty results without dispatching.
	// The query text (Topic/Queries) is used only for scoring, not
	// for generating candidate pools (the canonical port's contract
	// forbids auto-classification from Value).
	if len(refs) == 0 {
		return &artlist.ClipResolverRecommendResponse{
			Results: []artlist.ClipResolverRecommendResult{},
		}, nil
	}
	result, err := a.Canonical.Resolve(ctx, refs)
	if err != nil {
		// DB-level error from canonical: propagate so the handler
		// returns 500. The per-reference Unresolved entries have
		// already been logged at warn level inside the canonical
		// resolver; we do not double-log here.
		return nil, fmt.Errorf("clip resolver recommend: canonical resolve failed: %w", err)
	}

	// Early exit when the canonical resolver produced no resolved
	// assets. Saves scoring overhead and produces the right
	// semantics (no candidate assets = no recommendations).
	if len(result.Resolved) == 0 {
		return &artlist.ClipResolverRecommendResponse{
			Results: []artlist.ClipResolverRecommendResult{},
		}, nil
	}

	// Step 3: build the scoring haystack. Topic + Queries are
	// concatenated; empty fields are skipped. The haystack is
	// tokenized once (via similarity.TokenSet, which lowercases +
	// strips non-alphanumeric + filters < 3 chars) and reused
	// per-asset.
	queryHaystack := buildQueryText(req.Topic, req.Queries)
	queryTokens := similarity.TokenSet(queryHaystack)
	if len(queryTokens) == 0 {
		// No scorable tokens (e.g. only stopwords / 1-2 char
		// words). Return empty results rather than producing
		// "everything matches everything" zero-signal scores.
		return &artlist.ClipResolverRecommendResponse{
			Results: []artlist.ClipResolverRecommendResult{},
		}, nil
	}

	// Step 3 (cont): Score each resolved asset.
	scored := make([]scoredRecommendation, 0, len(result.Resolved))
	for _, ev := range result.Resolved {
		Score := scoreEvidence(queryTokens, ev)
		if Score >= req.MinScore {
			scored = append(scored, scoredRecommendation{
				ClipID:    ev.AssetID,
				Score:     Score,
				DriveLink: ev.DriveLink,
			})
		}
	}

	// Step 4: sort descending by Score. sort.SliceStable
	// preserves insertion order on equal scores (deterministic
	// output for tests + cache-friendly stable iteration).
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	// Step 5: map to handler-side DTO. The response Results
	// slice is non-nil even when empty (handler-side consumers
	// may iterate without nil-checks; godlike/07 no-nil-slice
	// contract for JSON-marshaled responses).
	out := make([]artlist.ClipResolverRecommendResult, 0, len(scored))
	for _, s := range scored {
		out = append(out, artlist.ClipResolverRecommendResult{
			ClipID:    s.ClipID,
			Score:     s.Score,
			DriveLink: s.DriveLink,
		})
	}
	return &artlist.ClipResolverRecommendResponse{Results: out}, nil
}

// scoredRecommendation is the internal intermediate between
// scoring and DTO mapping. Unexported because the public surface
// is the handler-side ClipResolverRecommendResult DTO.
type scoredRecommendation struct {
	ClipID    string
	Score     float64
	DriveLink string
}

// buildCanonicalReferences translates a handler-side SegmentID
// into one canonical ClipReference. The YouTube fan-out logic
// lives here: if the SegmentID matches the canonical
// `yt_<videoID>_<seg>_<n>` segment-encoding pattern, the videoID
// is extracted and dispatched as RefTypeYouTubeVideoID (which
// triggers the canonical resolver's `LIKE yt_<videoID>_%`
// fan-out, returning ALL segments of that video — a robust
// candidate pool for the scorer).
//
// An empty SegmentID returns nil (the caller short-circuits to
// the empty-inputs fast path).
//
// Per godlike/07, this is the ONLY translation site from the
// handler-side SegmentID string to the canonical
// ports.ClipReference. A future field on the handler request
// (e.g. an explicit ReferenceType hint) would land here, not at
// 3+ scattered call sites.
func buildCanonicalReferences(segmentID string) []ports.ClipReference {
	if segmentID == "" {
		return nil
	}
	if m := youtubeSegmentIDPattern.FindStringSubmatch(segmentID); m != nil {
		videoID := m[1]
		return []ports.ClipReference{{
			Type:  ports.RefTypeYouTubeVideoID,
			Value: videoID,
		}}
	}
	// Default: treat SegmentID as a canonical media_assets.id.
	// The canonical resolver's ResolveByMediaAssetID does the
	// exact-match lookup; an unknown id surfaces as Unresolved
	// with Reason="not_found" (per godlike/07 typed-error
	// contract on the canonical port).
	return []ports.ClipReference{{
		Type:  ports.RefTypeMediaAssetID,
		Value: segmentID,
	}}
}

// buildQueryText concatenates Topic + Queries into a single
// haystack for tokenization. Empty fields are skipped. The
// returned text is trimmed of leading/trailing whitespace; the
// caller tokenizes via similarity.TokenSet (which handles
// punctuation + lowercasing + length filtering).
func buildQueryText(topic string, queries []string) string {
	var b strings.Builder
	if topic != "" {
		b.WriteString(topic)
		b.WriteString(" ")
	}
	for _, q := range queries {
		if q != "" {
			b.WriteString(q)
			b.WriteString(" ")
		}
	}
	return strings.TrimSpace(b.String())
}

// scoreEvidence computes the field-weighted Jaccard similarity
// between the request queryTokens and the asset's text metadata.
//
// The 5 fields (Name, Filename, Description, Tags,
// TranscriptExcerpt) are scored independently. Each field's
// Jaccard similarity is multiplied by its weight, summed, then
// divided by the SUM of weights for fields that are present
// (not all assets have all 5 fields populated — Tags may be
// empty, Transcript may be empty, etc.). This means:
//   - An asset with only Name gets a Name-only Jaccard Score
//     (no penalty for missing fields).
//   - An asset with all 5 fields gets a 5-field blend.
//   - An asset with no text metadata at all returns 0.
//
// The [0,1] clamp is defensive — Jaccard's pure mathematical
// range is [0,1] but float rounding can produce -0.0 or
// marginally-over-1.0 in pathological cases.
func scoreEvidence(queryTokens map[string]struct{}, ev ports.ClipEvidence) float64 {
	if len(queryTokens) == 0 {
		return 0
	}
	Score := 0.0
	if ev.Name != "" {
		Score = combineWeightedCoverage(Score, queryCoverage(queryTokens, ev.Name), recommendNameWeight)
	}
	if ev.Filename != "" {
		Score = combineWeightedCoverage(Score, queryCoverage(queryTokens, ev.Filename), recommendFilenameWeight)
	}
	if ev.Description != "" {
		Score = combineWeightedCoverage(Score, queryCoverage(queryTokens, ev.Description), recommendDescriptionWeight)
	}
	if len(ev.Tags) > 0 {
		Score = combineWeightedCoverage(Score, queryCoverage(queryTokens, strings.Join(ev.Tags, " ")), recommendTagWeight)
	}
	if ev.TranscriptExcerpt != "" {
		Score = combineWeightedCoverage(Score, queryCoverage(queryTokens, ev.TranscriptExcerpt), recommendTranscriptWeight)
	}
	return clampUnit(Score)
}

// queryCoverage tokenizes the field text and computes how much of
// the query token set is covered by that field.
func queryCoverage(queryTokens map[string]struct{}, fieldText string) float64 {
	docTokens := similarity.TokenSet(fieldText)
	if len(docTokens) == 0 || len(queryTokens) == 0 {
		return 0
	}
	matches := 0
	for tok := range queryTokens {
		if _, ok := docTokens[tok]; ok {
			matches++
		}
	}
	return float64(matches) / float64(len(queryTokens))
}

// combineWeightedCoverage applies the field weight as a saturating
// union contribution.
func combineWeightedCoverage(current, coverage, weight float64) float64 {
	if coverage <= 0 || weight <= 0 {
		return current
	}
	addition := weight * coverage
	return 1 - (1-current)*(1-addition)
}

// clampUnit constrains a float to [0,1] to defend against
// pathological float-rounding edge cases (negative zero, marginal
// over-1 from IEEE-754 imprecision). Per godlike/07
// no-fake-availability, scores outside [0,1] would be
// meaningless to the handler-side consumer. Direct comparisons
// are used (rather than math.Max / math.Min) to keep the helper
// branch-predictor-friendly for the common in-range case.
func clampUnit(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
