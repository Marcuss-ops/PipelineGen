// Package clipindexer — errors.go carries the typed sentinel
// errors the indexer surfaces to the IndexingHandler (outbox
// worker). godlike/07 typed-error contract: each sentinel is a
// errors.Is probe target so the handler can route on the
// signal without parsing the message string.
//
// godlike/06 SSOT: this file is the SOLE declaration surface
// for the typed-error contract that ships across package
// boundaries. A second declaration elsewhere (e.g. in
// application/jobs/outbox/indexing.go mimicking the same
// message) is a godlike/06 drift anti-pattern — every consumer
// imports this symbol via the canonical import path.
package clipindexer

import "errors"

// ErrIndexClipDisabledButEventRequested is the canonical typed
// sentinel returned by Service.IndexClip when cfg.Enabled=false
// (the indexer is disabled at runtime) but an
// asset.index.requested event arrived anyway.
//
// Historical anti-pattern (PR-QDRANT-INDEXCLIP-GUARD, July 2026):
// pre-fix, Service.IndexClip returned nil when disabled, making
// the outbox worker treat the index-request as a silent success
// (fake-availability). The pool's IsTerminal classifier had no
// way to distinguish a real success from a "not actually indexed"
// success — the asset never received embeddings + never appeared
// in Qdrant, but the outbox_events table recorded the event as
// "completed". Operators found out about it only via downstream
// Qdrant count drift.
//
// Post-fix (this PR, deadline 2026-07-15):
//
//   - Service.IndexClip returns ErrIndexClipDisabledButEventRequested
//     when cfg.Enabled=false (line ~57 of indexing.go).
//   - The IndexingHandler (outbox/indexing.go::Handle) detects the
//     sentinel via errors.Is, calls IndexingStateUpdater
//     .MarkIndexingSkippedNoIndexer to record the canonical
//     transient state INDEXING_SKIPPED_NO_INDEXER on
//     media_assets.index_state, and returns a NON-terminal
//     retryable error so the outbox pool re-emits the event when
//     the indexer is re-enabled (pending+retry per godlike/07
//     fail-closed).
//
// godlike/07 minimal-blast-radius: only the
// `!s.cfg.Enabled` early-return is changed. The shouldSkipByName
// path STILL returns nil (legitimate skip semantics for
// non-media assets unchanged — a future PR may wish to add
// ErrIndexClipSkippableAsset if shouldSkipByName needs the same
// pending+retry treatment, but that is out of scope for this
// PR).
//
// godlike/06 one-owner-per-fact: this sentinel is the single
// canonical signal for "indexer disabled at runtime but event
// arrived". Other transient failures (network blips, Qdrant
// 5xx, embedding server timeouts) retain their own typed
// errors downstream — the sentinel fires ONLY on the
// runtime-disabled path so retry-time diagnostics are
// unambiguous.
var ErrIndexClipDisabledButEventRequested = errors.New("clipindexer disabled but asset.index.requested event arrived")
