// Package overlays — visual_scheduler.go owns the temporal VisualScheduler:
// the deterministic gate that stops too many overlays from firing at once,
// back-to-back, or for the same entity in quick succession.
//
// Pipeline position:
//
//	[]VisualIntent → VisualBudget.Apply (per-kind count caps)
//	               → VisualScheduler.Schedule (temporal constraints)
//	               → scheduled set
//
// Three constraints are enforced, in priority order (a lower-priority intent
// is dropped first, ties broken by start then original order):
//
//  1. MaxConcurrent — at most K overlays active at any instant;
//  2. MinGap — no two accepted overlays begin closer than MinGap (start
//     spacing: bursts of simultaneous starts are forbidden, while staggered
//     overlap up to K is still allowed);
//  3. EntityCooldown — an intent carrying an EntityID must start at least
//     EntityCooldown after the previous occurrence of that entity ENDED.
//
// The scheduler is pure and deterministic: it reads no wall clock, random
// state or external service, so the same intents + config always schedule the
// same survivors on every host.
package overlays

import "sort"

// SchedulerConfig are the temporal knobs of the VisualScheduler. A non-positive
// value falls back to the canonical default (MaxConcurrent=2, MinGapUS=800ms,
// EntityCooldownUS=15s).
type SchedulerConfig struct {
	// MaxConcurrent is the max number of overlays active at any instant.
	MaxConcurrent int
	// MinGapUS is the minimum start-to-start spacing between accepted overlays
	// in microseconds (default 800ms).
	MinGapUS int64
	// EntityCooldownUS is the minimum time between the end of an entity's
	// occurrence and the next start of the same entity, in microseconds
	// (default 15s).
	EntityCooldownUS int64
}

// DefaultSchedulerConfig returns the canonical scheduler knobs: 2 concurrent
// overlays, 800ms minimum gap, 15s same-entity cooldown.
func DefaultSchedulerConfig() SchedulerConfig {
	return SchedulerConfig{
		MaxConcurrent:    2,
		MinGapUS:         800_000,
		EntityCooldownUS: 15_000_000,
	}
}

// normalized applies the canonical defaults to any non-positive knob.
func (c SchedulerConfig) normalized() SchedulerConfig {
	if c.MaxConcurrent <= 0 {
		c.MaxConcurrent = 2
	}
	if c.MinGapUS <= 0 {
		c.MinGapUS = 800_000
	}
	if c.EntityCooldownUS <= 0 {
		c.EntityCooldownUS = 15_000_000
	}
	return c
}

// VisualScheduler enforces the temporal overlay constraints. It is stateless
// and safe for concurrent use.
type VisualScheduler struct{}

// Schedule returns the intents that fit within the temporal constraints,
// preserving the original relative order of the survivors. Intents are
// admitted in editorial priority order (descending, ties broken by start then
// original order); a candidate is dropped when it would violate MaxConcurrent,
// MinGap or the same-entity cooldown. The returned slice shares no storage
// with the input.
func (VisualScheduler) Schedule(intents []VisualIntent, cfg SchedulerConfig) []VisualIntent {
	if len(intents) == 0 {
		return nil
	}
	cfg = cfg.normalized()

	order := make([]int, len(intents))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		if intents[order[a]].Priority != intents[order[b]].Priority {
			return intents[order[a]].Priority > intents[order[b]].Priority
		}
		if intents[order[a]].StartUS != intents[order[b]].StartUS {
			return intents[order[a]].StartUS < intents[order[b]].StartUS
		}
		return order[a] < order[b]
	})

	kept := make([]bool, len(intents))
	var accepted []VisualIntent
	entityLastEnd := make(map[string]int64)
	for _, idx := range order {
		cand := intents[idx]
		end := cand.StartUS + cand.DurationUS

		if maxConcurrentVisual(accepted, cand.StartUS, end) >= cfg.MaxConcurrent {
			continue
		}
		if withinGap(accepted, cand.StartUS, cfg.MinGapUS) {
			continue
		}
		if cand.EntityID != "" {
			if last, ok := entityLastEnd[cand.EntityID]; ok && cand.StartUS < last+cfg.EntityCooldownUS {
				continue
			}
		}

		accepted = append(accepted, cand)
		kept[idx] = true
		if cand.EntityID != "" {
			entityLastEnd[cand.EntityID] = end
		}
	}

	out := make([]VisualIntent, 0, len(accepted))
	for i, it := range intents {
		if kept[i] {
			out = append(out, it)
		}
	}
	return out
}

// DefaultVisualScheduler is the process-wide scheduler. Every call site
// schedules through this single instance so the constraints are uniform.
var DefaultVisualScheduler = VisualScheduler{}

// maxConcurrentVisual returns the max number of intents in `accepted` that are
// simultaneously active within the half-open window [start, end). It counts
// only accepted intents that overlap the window (sweep over their start/end
// points).
func maxConcurrentVisual(accepted []VisualIntent, start, end int64) int {
	var starts, ends []int64
	for _, it := range accepted {
		itEnd := it.StartUS + it.DurationUS
		if it.StartUS < end && start < itEnd {
			starts = append(starts, it.StartUS)
			ends = append(ends, itEnd)
		}
	}
	if len(starts) == 0 {
		return 0
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })
	sort.Slice(ends, func(i, j int) bool { return ends[i] < ends[j] })
	max, cur := 0, 0
	i, j := 0, 0
	for i < len(starts) {
		if j < len(ends) && ends[j] <= starts[i] {
			cur--
			j++
		} else {
			cur++
			if cur > max {
				max = cur
			}
			i++
		}
	}
	return max
}

// withinGap reports whether start is closer than gap to the start of any
// accepted intent (start-to-start spacing).
func withinGap(accepted []VisualIntent, start, gap int64) bool {
	for _, a := range accepted {
		d := start - a.StartUS
		if d < 0 {
			d = -d
		}
		if d < gap {
			return true
		}
	}
	return false
}
