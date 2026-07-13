// Package usecase — clip_budget.go derives clip-native narration
// budgets from resolved clip duration.
//
// The canonical clip budget is duration-driven: accepted clips are
// ordered as resolved, their usable durations are summed, and the
// total spoken-word budget is computed from the repository-wide WPM
// SSOT. The helper is shared by the prompt builder and the editorial
// quality gate so both surfaces use the same budget rule.
package usecase

import (
	"github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/defaults"
)

type clipWordBudget struct {
	ClipID     string
	DurationMs int64
	WordBudget int
}

// clipRuntimeBudget returns the total usable clip runtime, the total
// word budget implied by that runtime, and a per-clip budget slice in
// accepted-clip order.
func clipRuntimeBudget(plan *script.ResolvedGenerationPlan) (int64, int, []clipWordBudget) {
	if plan == nil || plan.ClipEvidence == nil || len(plan.ClipEvidence.AcceptedClipIDs) == 0 {
		return 0, 0, nil
	}

	accepted := plan.ClipEvidence.AcceptedClipIDs
	slots := make([]script.ClipSearchSlot, 0, len(accepted))
	budgets := make([]clipWordBudget, 0, len(accepted))

	var totalDurationMs int64
	for _, clipID := range accepted {
		if plan.ClipEvidence.ClipDetails == nil {
			continue
		}
		detail, ok := plan.ClipEvidence.ClipDetails[clipID]
		if !ok {
			detail = script.ClipDetail{}
		}
		durationMs := script.ClipDurationMs(detail.StartMs, detail.EndMs)
		if durationMs <= 0 {
			durationMs = script.ClipDurationMsFromAssetID(clipID)
		}
		if durationMs <= 0 {
			continue
		}
		totalDurationMs += durationMs
		slots = append(slots, script.ClipSearchSlot{TargetDurationMs: durationMs})
		budgets = append(budgets, clipWordBudget{
			ClipID:     clipID,
			DurationMs: durationMs,
		})
	}
	if len(budgets) == 0 || totalDurationMs <= 0 {
		return 0, 0, nil
	}

	totalWords := durationMsToWords(totalDurationMs)
	perClipBudgets := distributeWordBudget(slots, totalWords)
	for i := range budgets {
		budgets[i].WordBudget = perClipBudgets[i]
	}
	return totalDurationMs, totalWords, budgets
}

func durationMsToWords(durationMs int64) int {
	if durationMs <= 0 {
		return 0
	}
	wpm := defaults.DefaultScriptConfig().WordsPerMinute
	if wpm <= 0 {
		return 0
	}
	words := int((durationMs * int64(wpm)) / 60000)
	if words <= 0 {
		return 1
	}
	return words
}
