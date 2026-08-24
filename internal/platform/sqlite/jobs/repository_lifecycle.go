// Package jobs — split topology for repository_lifecycle.go
//
// LONG-FILES-SPLIT-2026-07-06 Band A #4: the original 658-LOC
// repository_lifecycle.go has been decomposed into 4 single-purpose
// lifecycle-phase files per AGENTS.md Pattern 5:
//
//	lifecycle_complete.go    — Complete, Fail (terminal transitions, lease fence)
//	lifecycle_progress.go    — SetProgress, AddEvent, MarkRunningJobsOlderThanFailed,
//	                            validateOwnership
//	lifecycle_finalize.go    — FinalizeAggregateParent, aggregateFlipper,
//	                            parentStateTypedColumn
//	lifecycle_aggregation.go — ScheduleRetry, Cancel, DeadLetter, Retry
//
// godlike/06 SSOT (one canonical owner per fact): each file owns
// exactly one lifecycle phase; cross-cutting helpers (validateOwnership,
// mustRowsAffected, ErrTransitionConflict, ErrJobNotFound) are co-located
// with their primary consumer.
//
// godlike/07 minimum-blast-radius: pure code-motion, zero logic changes.
package jobs
