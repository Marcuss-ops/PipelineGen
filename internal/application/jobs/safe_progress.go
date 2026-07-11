package jobs

// SafeProgressFn returns a nil-safe progress callback function.
//
// Creator-runtime wrap (internal/app/creator_runtime.go::worker.Handler)
// passes tools=nil per AGENTS.md runtime-wrap contract. Without this
// utility, every consumer of tools.Progress would panic when invoked
// via the Creator path. SafeProgressFn captures the canonical
// nil-tolerance gate (`tools != nil && tools.Progress != nil`) so
// all 3 call sites (voiceover.generate, voiceover.generate_item,
// script.generate) consume the SAME closure-with-safety contract.
//
// Returned closure is safely callable any number of times:
//   - tools == nil           → no-op closure (idempotent across calls)
//   - tools.Progress == nil  → no-op closure (idempotent across calls)
//   - tools.Progress != nil  → returns tools.Progress directly (1:1 forwarding)
//
// godlike/07 contract: zero silent failures; an unreachable caller
// observes no-op semantics rather than nil-deref panic. Pattern 0 /
// godlike/06 SSOT: single canonical owner for nil-safe progress
// invocation; ad-hoc per-handler `if tools != nil && tools.Progress != nil`
// checks are forward-rejected (the canonical checker lives here).
//
// Migration pattern (P0/P1 handlers):
//
//	// At top of HandleJob (voiceover + scripts handlers):
//	pf := appjobs.SafeProgressFn(tools)
//	h.logger.Info("handling ...", zap.String("job_id", j.ID))
//	// ... later at each progress site:
//	pf(5, "starting voiceover.generate_item")
//	pf(100, "voiceover.generate_item execution complete")
//
// The fast-path (tools.Progress != nil) is exactly the same pointer
// dereference as the pre-SafeProgressFn inline check, so there's no
// runtime overhead compared to the legacy shape.
//
// Forward-pointer: a symmetric `SafeEventFn(tools *JobTools) func(eventType,
// message, data)` + `SafeIsCancelledFn(tools *JobTools) func() bool` would
// cover the other 2 JobTools fields. Not in this commit — only the
// `tools.Progress(nil pointer)` panic risk was surfaced by the audit.
func SafeProgressFn(tools *JobTools) func(progress int, message string) {
	if tools == nil || tools.Progress == nil {
		return func(progress int, message string) {}
	}
	return tools.Progress
}

// SafeEventFn returns a nil-safe event callback function.
//
// Mirrors SafeProgressFn: when tools or tools.Event is nil, the
// returned closure is a no-op so handlers can emit events without
// per-call nil checks. Errors from the underlying event port are
// intentionally swallowed at the callback boundary; event emission
// must never fail a job.
func SafeEventFn(tools *JobTools) func(eventType, message string, data map[string]any) {
	if tools == nil || tools.Event == nil {
		return func(eventType, message string, data map[string]any) {}
	}
	return tools.Event
}
