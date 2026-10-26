package script

// VisualTimingBudget is the kernel-level timing input for visual planning.
// Source is voiceover, scene, or estimated and is informational; DurationMs
// is the authoritative clock used to allocate beats.
//
// Legacy note (2026-10-26 dead-code sweep): only this concrete type survived
// of the old beat planner — the planner pipeline itself was unreachable from
// the production entrypoints; internal/capabilities/scripts/adapters aliases
// it as SegmentTimingBudget.
type VisualTimingBudget struct {
	SegmentID  string `json:"segment_id"`
	DurationMs int64  `json:"duration_ms"`
	Source     string `json:"source"`
}
