package stockpipeline

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

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

	sliceSec := (budgetSec - clipDur) / count
	out := make([]ClipPlan, 0, count)
	for i := 0; i < count; i++ {
		// SHA-256-mod-sliceSec gives a stable permutation. Note:
		// sliceSec collapses to 0 when budget == N*clipDur; the hash
		// fallback to 0 in that case produces overlapping copies,
		// which we accept as degenerate (the budget is already
		// exactly N clips back-to-back — there is no "shuffle" room).
		start := deterministicStart(src.URL, i, sliceSec)
		end := start + float64(clipDur)
		if end > float64(budgetSec) {
			end = float64(budgetSec)
		}
		out = append(out, buildClipPlan(src, start, end, i, policyVer))
	}
	return out, nil
}

// deterministicStart returns a stable offset in [0, sliceSec)
// derived from SHA-256(URL + ":" + idx).
//
// Replace-only path: the legacy rng-based picked random offsets
// inside a loop that capped at 20 attempts. That non-replayable
// behaviour is replaced by this hash-derived stable permutation so
// the plan can be persisted + replayed (and so retries don't
// produce visually-identical-offset clips mismatched with their
// logical IDs across runs).
func deterministicStart(url string, idx int, sliceSec int) float64 {
	if sliceSec <= 0 {
		return 0
	}
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%d", url, idx)))
	n := binary.BigEndian.Uint32(h[:4])
	return float64(int(n) % sliceSec)
}

// buildClipPlan mints a ClipPlan with the deterministic OutputLogicalID.
//
// Format: planner:<sha256-prefix>:<index>. The hash prefix is
// stable across runs + across implementations (URL-driven), so
// re-planning the same source yields identical IDs — downstream
// producers can use the Logical ID as a dedupe key.
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
// explicitPlanner.Plan in planner_explicit.go.
func buildClipPlan(src VideoSource, start, end float64, idx int, policyVer string) ClipPlan {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%s", src.URL, idx, policyVer)))
	id := fmt.Sprintf("planner:%x:%d", h[:8], idx)
	return ClipPlan{
		SourceID:        src.URL,
		SourceProvider:  inferSourceProvider(src.URL),
		SourceVideoID:   inferSourceVideoID(src.URL),
		SourceVersion:   "v1",
		StartSec:        start,
		EndSec:          end,
		OutputLogicalID: id,
		PolicyVersion:   policyVer,
	}
}
