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
package plan

import (
	"context"
	"errors"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/core"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
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

// ErrPlannerBudgetTooSmall signals that the per-source budget
// (seconds) is smaller than the requested clip duration. This is a
// caller-side configuration error (NOT a transient runtime error),
// so the caller should fail-closed with the typed error.
var ErrPlannerBudgetTooSmall = errors.New("deterministic planner: budget must be >= clip duration")

// NewDeterministicPlanner returns the canonical ClipPlanner.
// Today there is exactly one implementation; the constructor lets
// future variants (e.g. policy-aware planners) plug in without a
// Service constructor signature change.
func NewDeterministicPlanner() core.ClipPlanner {
	return &deterministicPlanner{}
}

// deterministicPlanner is the canonical ClipPlanner.
// Stable permutation via SHA-256 hashing of (URL + index) mod
// sliceSec — replaces the rng.Float64()-based random scheduler.
type deterministicPlanner struct{}

// Compile-time assertion: deterministicPlanner satisfies ClipPlanner.
var _ core.ClipPlanner = (*deterministicPlanner)(nil)

func (p *deterministicPlanner) Plan(_ context.Context, src core.VideoSource, budgetSec int, clipDur int, policyVer string) ([]core.ClipPlan, error) {
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
		return []core.ClipPlan{
			buildClipPlan(src, 0, float64(clipDur), 0, policyVer),
		}, nil
	}

	// The output budget is not the source-video duration. When the source
	// duration is known, distribute the windows across that source. Direct
	// URL runs may not have metadata yet, so use a conservative deterministic
	// sampling horizon; extraction still fails closed if the source is shorter
	// than the planned end offsets.
	// Known source length → distribute the windows across the REAL source.
	// Unknown source length (bare direct URL with no metadata yet) → keep the
	// conservative deterministic sampling horizon; extraction still fails
	// closed if the source turns out to be shorter than the planned offsets.
	horizonSec := budgetSec * 10
	if src.DurationSec > 0 {
		// Subtract the metadata rounding margin so the last clip window
		// stays inside the REAL (probed) source length even when the
		// provider-declared duration is a few tenths of a second longer
		// than the downloaded file's measured duration.
		horizonSec = int(src.DurationSec) - sourceDurationHorizonMarginSec
		if horizonSec < clipDur {
			horizonSec = clipDur
		}
		// A source shorter than the requested output budget can only yield
		// as many clips as physically fit inside it. Without this cap the
		// planner kept the budget-driven count and spread the windows
		// across `budgetSec` seconds — past the end of the file — which
		// stock.extract_clips then rejects with ErrStockClipsOutOfRange
		// instead of producing the clips that actually fit.
		if fitting := horizonSec / clipDur; fitting >= 1 && fitting < count {
			count = fitting
		}
	}
	maxStart := horizonSec - clipDur
	out := make([]core.ClipPlan, 0, count)
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
func buildClipPlan(src core.VideoSource, start, end float64, idx int, policyVer string) core.ClipPlan {
	return core.ClipPlan{
		SourceID:        src.URL,
		SourceProvider:  core.InferSourceProvider(src.URL),
		SourceVideoID:   core.InferSourceVideoID(src.URL),
		SourceVersion:   "v1",
		StartSec:        start,
		EndSec:          end,
		OutputLogicalID: mintOutputLogicalID(src.URL, idx, policyVer, start, end),
		PolicyVersion:   policyVer,
	}
}

// mintOutputLogicalID derives the canonical plan identity for one clip window.
//
// godlike/06 SSOT: this is the ONLY place the OutputLogicalID formula lives.
// It is a separate function (not inlined in buildClipPlan) because the
// sections_only layout pass re-anchors an already-built plan: the ID covers the
// clip window, so moving a window MUST re-mint the ID through the same formula —
// otherwise an asset would claim a timestamp range it no longer describes, and
// two runs of the same source would dedupe against the wrong identity.
func mintOutputLogicalID(sourceURL string, idx int, policyVer string, start, end float64) string {
	h := digest.SHA256Bytes([]byte(fmt.Sprintf("%s|%d|%s|%s|%s", sourceURL, idx, policyVer, windowKey(start), windowKey(end))))
	return fmt.Sprintf("planner:%x:%d", h[:8], idx)
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
func NewExplicitPlanner(clips []core.ClipSpec) core.ClipPlanner {
	return &explicitPlanner{clips: clips}
}

type explicitPlanner struct {
	clips []core.ClipSpec
}

var _ core.ClipPlanner = (*explicitPlanner)(nil)

func (p *explicitPlanner) Plan(_ context.Context, src core.VideoSource, budgetSec int, clipDur int, policyVer string) ([]core.ClipPlan, error) {
	if len(p.clips) == 0 {
		return nil, ErrExplicitPlannerNoClips
	}
	plans := make([]core.ClipPlan, 0, len(p.clips))
	for i, clip := range p.clips {
		clipSource := src
		if clipURL := strings.TrimSpace(clip.URL); clipURL != "" {
			// Explicit clip requests may mix different source videos. Their
			// per-clip URL is authoritative; using the first request URL for
			// every plan made the pipeline cut the first file repeatedly.
			clipSource.URL = clipURL
			clipSource.Source = clipURL
			if strings.TrimSpace(clip.Title) != "" {
				clipSource.Title = clip.Title
			}
		}
		// godlike/06 SSOT — route through buildClipPlan so the
		// inference + ID derivation stay canonical (PR-003 only
		// added fields; the literal was deliberately modified here
		// to share buildClipPlan rather than duplicate the
		// 11-field struct literal — future field additions stay
		// in one place).
		plan := buildClipPlan(clipSource, clip.StartSec, clip.EndSec, i, policyVer)
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
