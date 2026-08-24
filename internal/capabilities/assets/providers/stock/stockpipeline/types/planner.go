// Package stock — planner.go (Stock Cutover Commit 1, July 2026).
//
// ClipPlanner is the deterministic replacement for the pre-cutover
// rng-based clip-sampling logic that lived inside processSingleVideo
// (used the usedOffsets map + ran up to 20 attempts + picked random
// start offsets via rng.Float64()). The new planner:
//
//  1. Takes (source, budgetSec, clipDur, policyVersion) — no rng.
//  2. Produces N non-overlapping ClipPlan entries where N =
//     budgetSec / clipDur, with each entry carrying its source ID,
//     start/end, deterministic OutputLogicalID, and the policy
//     version tag it was locked against.
//  3. SHA-256 hash of (URL + index) mod sliceSec gives a stable
//     permutation that is identical across runs (replacing rng
//     which gave a different permutation every time the binary
//     started — the rng seed was time.Now().UnixNano()).
//
// Same input → same output. That is the only invariant the
// orchestrator and downstream consumers (cut, retry, render) lock
// against: the plan is persisted as data and consumed verbatim
// rather than re-computed.
package types

import (
	"context"
	"errors"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"net/url"
	"strconv"
	"strings"

	urlutil "github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

const (
	explicitClipAutoSegmentThresholdSec = 60
	explicitClipAutoSegmentSeconds      = 5
	// sourceDurationHorizonMarginSec is subtracted from the provider-declared
	// source duration when computing the planner's clip-window horizon.
	//
	// Rationale: the declared duration comes from provider metadata
	// (e.g. YouTube's watch-page/API duration), which can exceed the
	// ACTUAL duration probed on the downloaded file by a fraction of a
	// second (observed: declared 902s vs measured 901.89s → 0.11s
	// overrun on the last clip). Without the margin, the last planned
	// clip's EndSec lands a few hundredths of a second past the real
	// end of the file and the whole run fails closed with
	// ErrStockClipsOutOfRange. The margin keeps the strong fail-closed
	// contract for genuinely out-of-range plans while absorbing benign
	// metadata rounding.
	sourceDurationHorizonMarginSec = 2
)

// Canonical SourceProvider bucket identifiers (PR-003, July 2026).
// godlike/06 SSOT — one canonical owner per fact: the inference
// helper inferSourceProvider lives in this file (the canonical
// place where SourceID is consumed). All downstream consumers
// (ChunkState, ChunkMetadataEntry, StepRunner publishers) reference
// these constants via plan.SourceProvider — no stringly-typed
// branch anywhere in the pipeline.
const (
	SourceProviderYouTube = "youtube"
	SourceProviderPexels  = "pexels"
	SourceProviderPixabay = "pixabay"
	// SourceProviderUnknown is the godlike/07 NO-FAKE-AVAILABILITY
	// sentinel for non-classifiable URLs (direct mp4 blobs, Vimeo,
	// archive.org, etc.). Inference emits this literal — empty
	// string is reserved for "not yet inferred" (defensive only;
	// build paths always fill this field on a populated plan).
	SourceProviderUnknown = "unknown"
)

// InferSourceProvider classifies a source URL into the canonical
// 4-bucket provider taxonomy. Single canonical implementer per
// godlike/06 SSOT — every consumer reads plan.SourceProvider which
// is set ONCE at plan-build time.
//
// godlike/07 NO-FAKE-AVAILABILITY: parsing the hostname (not
// substring-matching the full URL) closes the SSRF-style false-
// positive class — a URL like
// `https://fake-youtube.com.attacker.io/video.mp4` would have
// matched the substring-based classification. We delegate to
// net/url.Parse + Hostname() + suffix/exact-host match so the
// only counts are on real YouTube/Pexels/Pixabay domains. Bare
// non-URL inputs (parse error OR empty host) collapse to
// SourceProviderUnknown — the bucket is observable and never
// silent-empty.
func InferSourceProvider(rawURL string) string {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return SourceProviderUnknown
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return SourceProviderUnknown
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return SourceProviderUnknown
	}
	switch {
	case host == "youtu.be",
		host == "youtube.com",
		strings.HasSuffix(host, ".youtube.com"):
		return SourceProviderYouTube
	case host == "pexels.com", strings.HasSuffix(host, ".pexels.com"):
		return SourceProviderPexels
	case host == "pixabay.com", strings.HasSuffix(host, ".pixabay.com"):
		return SourceProviderPixabay
	}
	return SourceProviderUnknown
}

// InferSourceVideoID extracts the canonical video ID ONLY when
// the URL belongs to the YouTube provider bucket. Returns "" for
// non-YouTube URLs (provider mismatch — caller can rely on
// plan.SourceProvider too) AND for malformed YouTube URLs where
// ExtractVideoID errors (channel pages, playlists, bare /UCxxx).
//
// godlike/07 fail-open rationale: SourceVideoID is an
// observability field (not a gate). Failing the entire chunk
// build because a YouTube channel-page URL has no watch-ID would
// surface false-positive FAILED jobs for an otherwise valid
// SourceURL. Callers that NEED the ID can re-run the extraction
// verbatim — the function is pure + deterministic.
func InferSourceVideoID(rawURL string) string {
	vid, err := urlutil.ExtractVideoID(rawURL)
	if err != nil {
		return ""
	}
	return vid
}

// ClipPlan is the deterministic output of the ClipPlanner.
// The plan is persisted as data so subsequent phases (download,
// cut, retry) consume it without re-computing offsets.
type ClipPlan struct {
	// SourceID is the canonical identifier of the source (URL or
	// VideoID). Carried through the pipeline so retries can
	// re-fetch without re-planning.
	SourceID string
	// StageKey identifies the section-local staged file when the run uses
	// sections_only. Empty means the whole source is staged once.
	StageKey string

	// SourceProvider is the canonical provider bucket for
	// SourceID — inference happens at plan-build time so all
	// downstream consumers (stager, cutter, publish step,
	// metadata.json) read the same value. Possible values
	// (canonical constants above): youtube / pexels / pixabay /
	// unknown. Locked to inferSourceProvider() — call sites
	// MUST NOT re-parse the URL.
	SourceProvider string

	// SourceVideoID is the canonical provider-native identifier
	// (YouTube video ID when SourceProvider == youtube; ""
	// otherwise). Extracted via pkg/urlutil.ExtractVideoID at
	// plan-build time. Empty string is the canonical zero
	// value for non-YouTube providers AND for YouTube URLs that
	// are not watch pages (channel, playlist, live, embed —
	// see pkg/urlutil::ExtractVideoID for the exact contract).
	SourceVideoID string

	// SourceVersion is a content-derived hash that locks the plan
	// to a specific source snapshot. v1 today (producers don't
	// compute one); future PRs will hash the source bytes.
	SourceVersion string

	// StartSec / EndSec are the canonical clip-window boundaries.
	// Cutters consume them verbatim; retry paths pre/post-compute
	// any drift with the planner's stable permutation.
	StartSec float64
	EndSec   float64

	// Title carries the human-readable clip/timestamp label when the
	// plan originates from explicit clips. Deterministic planner runs
	// leave it empty.
	Title string

	// Description carries the human-readable English summary for the
	// timestamp. Explicit clip payloads can populate it per segment;
	// deterministic planner runs leave it empty.
	Description string

	// Round carries the boxing-style round number when set. Surfaced
	// into ChunkState + ChunkMetadataEntry + Qdrant semantic payload
	// (PR-STOCK-TIMESTAMP-CLIPS Front 2, July 2026). Zero is the
	// canonical "not set" value (godlike/07 NO-FAKE-AVAILABILITY —
	// never use -1 or a magic literal). Deterministic planner runs
	// leave it at zero.
	Round int

	// Tags is the free-form per-clip tag list. Carries people, theme,
	// technique — any metadata that should surface in BM25 sparse
	// search. Empty slice = no tags; nil/empty are byte-equivalent
	// for the wire shape.
	Tags []string

	// Category is the content category for this clip (boxing / running
	// etc.). Surfaces in metadata.json + Qdrant payload for semantic
	// filtering. Empty = not specified.
	Category string

	// Slug is the explicit operator-supplied Drive folder slug for
	// this clip. Per godlike/07 user-spec directive: Slug is the
	// EXPLICIT OVERRIDE that wins over Title-derived slug in
	// perClipLeafName. Use this when the title contains characters
	// that don't slugify cleanly (e.g. accented Portuguese "veredito"
	// stays verbatim) or when the operator wants a canonical
	// machine-friendly folder name.
	Slug string

	// ParentSlug is the explicit timestamp-group folder slug for
	// expanded 5-second children. When explicit clips are split into
	// child slices, ParentSlug preserves the original parent folder
	// identity while Slug may carry the child-specific timestamp
	// suffix. Nil/empty means "derive from Title / other cascade".
	ParentSlug string

	// OutputLogicalID is the deterministic asset ID the chunk
	// producer will mint. Format: planner:<sha256-prefix>:<index>
	// — opaque hash so callers don't depend on its internal shape.
	// The hash covers the clip window (start/end) so clips of the
	// same source at different timestamps never collide.
	OutputLogicalID string

	// PolicyVersion is the planner policy version at plan-time.
	// Stored on the plan so a future policy upgrade can detect
	// (and re-plan) old plans instead of silently miscomputing.
	PolicyVersion string
}

// ClipPlanner produces a deterministic clip plan from (source, budget).
//
// Contract:
//   - Same input tuple (src, budgetSec, clipDur, policyVer) always
//     produces the same plan slice (same length, same OutputLogicalIDs,
//     same StartSec/EndSec) regardless of clock, process restart,
//     or parallel worker. This replaces the rng-based path that gave
//     a different permutation on every invocation.
//   - Returns nil, ErrPlannerBudgetTooSmall if budget < clipDur so
//     callers can short-circuit on impossible requests.
type ClipPlanner interface {
	Plan(ctx context.Context, src VideoSource, budgetSec int, clipDur int, policyVer string) ([]ClipPlan, error)
}

// ErrPlannerBudgetTooSmall signals that the per-source budget
// (seconds) is smaller than the requested clip duration. This is a
// caller-side configuration error (NOT a transient runtime error),
// so the caller should fail-closed with the typed error.
var ErrPlannerBudgetTooSmall = errors.New("deterministic planner: budget must be >= clip duration")

// NewDeterministicPlanner returns the canonical ClipPlanner.
// Today there is exactly one implementation; the constructor lets
// future variants (e.g. policy-aware planners) plug in without a
// Service constructor signature change.
func NewDeterministicPlanner() ClipPlanner {
	return &deterministicPlanner{}
}

// deterministicPlanner is the canonical ClipPlanner.
// Stable permutation via SHA-256 hashing of (URL + index) mod
// sliceSec — replaces the rng.Float64()-based random scheduler.
type deterministicPlanner struct{}

// Compile-time assertion: deterministicPlanner satisfies ClipPlanner.
var _ ClipPlanner = (*deterministicPlanner)(nil)

func (p *deterministicPlanner) Plan(_ context.Context, src VideoSource, budgetSec int, clipDur int, policyVer string) ([]ClipPlan, error) {
	// Guard: budgetSec <= 0 || clipDur <= 0 → invalid input.
	// Without this guard, `count = budgetSec / clipDur` panics
	// on zero-divide when both are zero (the `budgetSec < clipDur`
	// check returns false for `0 < 0`). The orchestrator clamps
	// its own input but the planner MUST NOT trust callers.
	if budgetSec <= 0 || clipDur <= 0 {
		return nil, ErrPlannerBudgetTooSmall
	}
	if budgetSec < clipDur {
		return nil, ErrPlannerBudgetTooSmall
	}

	// Number of non-overlapping clips per source = budgetSec / clipDur.
	// We round DOWN so the last budget tail may be skipped (it is
	// shorter than clipDur and would produce partial windows). If a
	// future caller wants to consume the tail, change to math.Ceil
	// and make the last clip's EndSec == budgetSec.
	count := budgetSec / clipDur
	if count < 1 {
		// Single full-window clip spanning budget.
		return []ClipPlan{
			buildClipPlan(src, 0, float64(clipDur), 0, policyVer),
		}, nil
	}

	// The output budget is not the source-video duration. When the source
	// duration is known, distribute the windows across that source. Direct
	// URL runs may not have metadata yet, so use a conservative deterministic
	// sampling horizon; extraction still fails closed if the source is shorter
	// than the planned end offsets.
	horizonSec := int(src.DurationSec)
	if src.DurationSec > 0 {
		// Subtract the metadata rounding margin so the last clip window
		// stays inside the REAL (probed) source length even when the
		// provider-declared duration is a few tenths of a second longer
		// than the downloaded file's measured duration.
		horizonSec -= sourceDurationHorizonMarginSec
	}
	if horizonSec < budgetSec {
		horizonSec = budgetSec
	}
	if src.DurationSec <= 0 {
		horizonSec = budgetSec * 10
	}
	maxStart := horizonSec - clipDur
	out := make([]ClipPlan, 0, count)
	for i := 0; i < count; i++ {
		start := float64(0)
		if count > 1 && maxStart > 0 {
			start = float64(i * maxStart / (count - 1))
		}
		end := start + float64(clipDur)
		out = append(out, buildClipPlan(src, start, end, i, policyVer))
	}
	return out, nil
}

// buildClipPlan mints a ClipPlan with the deterministic OutputLogicalID.
//
// Format: planner:<sha256-prefix>:<index>. The hash prefix is
// stable across runs + across implementations and covers the full
// clip identity (source URL + index + policy version + start/end
// window), so re-planning the same clip yields the identical ID
// while two clips of the same source at different timestamps never
// collide — downstream producers use the Logical ID as a dedupe key.
//
// SourceProvider + SourceVideoID are inferred ONCE at plan-build
// time (per godlike/06 SSOT — both deterministicPlanner and
// explicitPlanner go through this single constructor; there is
// exactly one canonical place that classifies URLs).
//
// Description is caller-populated: this constructor intentionally
// leaves plan.Description at the zero value. The deterministic
// planner runs (which use this constructor directly) leave
// Description empty and rely on the post-extract LLM enrichment
// pass to populate it (forward-pointer PR-ENRICHMENT-PLAN-DESC).
// The explicit planner is the ONLY caller that mutates
// plan.Description after this function returns — see
// (p *explicitPlanner).Plan below.
func buildClipPlan(src VideoSource, start, end float64, idx int, policyVer string) ClipPlan {
	h := digest.SHA256Bytes([]byte(fmt.Sprintf("%s|%d|%s|%s|%s", src.URL, idx, policyVer, windowKey(start), windowKey(end))))
	id := fmt.Sprintf("planner:%x:%d", h[:8], idx)
	return ClipPlan{
		SourceID:        src.URL,
		SourceProvider:  InferSourceProvider(src.URL),
		SourceVideoID:   InferSourceVideoID(src.URL),
		SourceVersion:   "v1",
		StartSec:        start,
		EndSec:          end,
		OutputLogicalID: id,
		PolicyVersion:   policyVer,
	}
}

// windowKey renders a clip-window boundary deterministically for the
// OutputLogicalID hash. Fixed 3-decimal precision keeps the ID stable
// across process restarts while making clips that share a source URL +
// index but different timestamps hash to different IDs.
func windowKey(sec float64) string {
	return strconv.FormatFloat(sec, 'f', 3, 64)
}

// ErrExplicitPlannerNoClips signals that the explicit planner was
// invoked with an empty clips slice. The caller should fail-closed
// rather than producing a nil output.
var ErrExplicitPlannerNoClips = errors.New("explicit planner: no clips provided")

// NewExplicitPlanner returns a ClipPlanner that uses pre-defined
// ClipSpec entries instead of computing deterministic offsets from
// a budget. Each ClipSpec produces exactly one ClipPlan.
//
// The explicit planner ignores the budgetSec and clipDur parameters
// — it always returns len(clips) ClipPlan entries regardless of
// the source duration. SourceID is set to src.URL for all plans.
//
// godlike/07 typed-error contract: Plan returns
// ErrExplicitPlannerNoClips when clips is empty.
func NewExplicitPlanner(clips []ClipSpec) ClipPlanner {
	return &explicitPlanner{clips: clips}
}

type explicitPlanner struct {
	clips []ClipSpec
}

var _ ClipPlanner = (*explicitPlanner)(nil)

func (p *explicitPlanner) Plan(_ context.Context, src VideoSource, budgetSec int, clipDur int, policyVer string) ([]ClipPlan, error) {
	if len(p.clips) == 0 {
		return nil, ErrExplicitPlannerNoClips
	}
	plans := make([]ClipPlan, 0, len(p.clips))
	for i, clip := range p.clips {
		// godlike/06 SSOT — route through buildClipPlan so the
		// inference + ID derivation stay canonical (PR-003 only
		// added fields; the literal was deliberately modified here
		// to share buildClipPlan rather than duplicate the
		// 11-field struct literal — future field additions stay
		// in one place).
		plan := buildClipPlan(src, clip.StartSec, clip.EndSec, i, policyVer)
		plan.Title = clip.Title
		plan.Description = clip.Description
		// PR-STOCK-TIMESTAMP-CLIPS Front 2 (July 2026): thread the 4
		// new content fields from ClipSpec → ClipPlan so downstream
		// consumers (ChunkState, ChunkMetadataEntry, perClipLeafName)
		// see them verbatim. godlike/06 SSOT one canonical owner per
		// fact: explicitPlanner is the SOLE place that copies
		// operator-supplied clip metadata into the plan shape.
		plan.Round = clip.Round
		plan.Tags = append([]string(nil), clip.Tags...) // defensive copy so the plan doesn't share the caller's slice
		plan.Category = clip.Category
		plan.Slug = clip.Slug
		// PR-STOCK-TIMESTAMP-CLIPS (July 2026): thread ParentSlug so
		// timestampParentLeafName uses the correct parent identity for
		// expanded 5-second children. Without this, children whose
		// Title is empty would fall through to their child-specific
		// Slug (e.g. "la-fase-di-studio-0-0-0_to_0-0-5") instead of
		// sharing the parent folder name.
		plan.ParentSlug = clip.ParentSlug
		plans = append(plans, plan)
	}
	return plans, nil
}

// ExpandExplicitClipSpecs splits explicit clips into successive
// fixed-length slices when secondsPerSegment is positive.
//
// Backward-compat fallback: when secondsPerSegment is zero, clips
// whose duration is at least 60 seconds are still expanded into
// 5-second slices. Shorter explicit clips remain single chunks.
func ExpandExplicitClipSpecs(clips []ClipSpec, secondsPerSegment int) []ClipSpec {
	if len(clips) == 0 {
		return append([]ClipSpec(nil), clips...)
	}
	expanded := make([]ClipSpec, 0, len(clips))
	for _, clip := range clips {
		stepSec := secondsPerSegment
		if stepSec <= 0 && clip.EndSec > clip.StartSec {
			if clip.EndSec-clip.StartSec >= explicitClipAutoSegmentThresholdSec {
				stepSec = explicitClipAutoSegmentSeconds
			}
		}
		if stepSec <= 0 || clip.EndSec <= clip.StartSec {
			expanded = append(expanded, clip)
			continue
		}
		step := float64(stepSec)
		for cursor := clip.StartSec; cursor < clip.EndSec; cursor += step {
			next := cursor + step
			if next > clip.EndSec {
				next = clip.EndSec
			}
			child := clip
			child.ParentSlug = clip.ParentSlug
			if child.ParentSlug == "" {
				child.ParentSlug = clip.Slug
			}
			child.StartSec = cursor
			child.EndSec = next
			if clip.Slug != "" {
				child.Slug = clip.Slug + "-" + formatTimestampForSlug(cursor, next)
			}
			expanded = append(expanded, child)
		}
	}
	return expanded
}

func formatTimestampForSlug(startSec, endSec float64) string {
	return formatTimestampSeconds(startSec) + "_to_" + formatTimestampSeconds(endSec)
}

func formatTimestampSeconds(sec float64) string {
	total := int(sec)
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	return strconv.Itoa(h) + "-" + strconv.Itoa(m) + "-" + strconv.Itoa(s)
}
