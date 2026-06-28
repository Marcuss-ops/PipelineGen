package assets

// ── PR1 (June 2026) — file role ───────────────────────────────────────────
//
// clips_statistics.go is the canonical home for unconditional metric /
// aggregation methods on *ClipsRepository: cross-state counters,
// distribution summaries, and operationally-oriented gauges that
// callers (e.g. IndexHealth, dashboards, readiness barriers) poll
// without per-call filter scoping.
//
// CURRENT STATE (intentionally empty as of PR1):
//
// The existing aggregation surface
// (CountAll, CountIndexed, CountIndexable, CountPendingOutbox,
// CountDeadLetter) already lives in clips_repository_queries.go
// (Wave 15 ownership boundary, June 2026 PR cluster). Per the
// strict-6 PR1 plan, this file is created for naming consistency
// so that the future metric-shaped additions have an unambiguous
// owner; the existing Wave 15 methods are LEFT in their established
// home (a follow-up migration wave may consolidate the metric-shaped
// methods here, but is OUT OF SCOPE for PR1 which is mechanical
// zero-behavior-change only).
//
// OWNERSHIP RULE FOR FUTURE CONTRIBUTORS:
//
//   * stats-shaped (un-filtered, metric-packaging) → clips_statistics.go (here)
//   * query-shaped (caller-filtered)              → clips_queries.go
//   * counts of outbox events                     → clips_repository_queries.go
//                                                (Wave 15 SSOT, not migrated
//                                                by PR1)
//   * tx-scoped metrics                           → clips_transactions.go
//
// Adding a NEW method here is the right move when a future PR
// introduces unconditional metric aggregation (e.g. CountBySource
// for dashboard top-N, distribution of quality_score buckets, etc.).
// Do NOT add metric-shaped methods into clips_queries.go — that
// folder is caller-filtered query territory, by deliberate
// separation.
