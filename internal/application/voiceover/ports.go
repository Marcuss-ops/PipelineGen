// Package voiceover — port interface for transactional outbox enqueue
// (AGENTS.md Pattern 0, June 2026).
//
// PR-VO-A3 (Outbox-based Qdrant indexing, June 2026): the canonical
// `asset.index.requested` enqueue site moved INTO swapVoiceoverRow's
// SQLite transaction. The metadata UPSERT (voiceovers row) and the
// outbox event INSERT (asset.index.requested) now commit atomically —
// no orphan events, no orphan embeddings, no async goroutine race.
//
// The service no longer reads from a ClipIndexFunc callback. It emits
// the canonical v1 envelope through this narrow port interface inside
// the caller-owned *sql.Tx.
//
// The production concrete is *outbox.Dispatcher, which satisfies the
// interface structurally via Go's implicit-interface rules; no code
// in the voiceover package imports the infrastructure layer. Tests
// substitute a stub that records EnqueueIndexEvent invocations.
package voiceover

import (
	"context"
	"database/sql"
)

// TxOutboxEnqueuer is the canonical narrow port for transactional
// outbox enqueue from inside the voiceover-swap transaction.
//
// Calling sites MUST pass a caller-owned *sql.Tx so the producer
// commit collapses both the voiceovers INSERT and the indexing event
// INSERT into a single atomic visibility boundary. A nil implementation
// is allowed at construction time (ProcessAsset guards nil at the
// call site so the optional behaviour degrades to "skip indexing" —
// same pattern as the previous ClipIndexFunc callback).
type TxOutboxEnqueuer interface {
	// EnqueueIndexEvent emits the canonical asset.index.requested
	// envelope (schema_version="asset.index.requested.v1") inside
	// the caller-owned transaction.
	//
	// assetID — the voiceover ID (voiceovers.id), used as the
	// canonical aggregate identifier. Matches the convention used
	// by outbox.Dispatcher.EnqueueAndIndex (whose assetID is the
	// media_assets.id).
	//
	// contentHash — the content fingerprint (typically the file MD5)
	// used for the supersede gate (the worker's source_version
	// check) and event_key derivation (idempotency). MUST be
	// non-empty; the canonical dispatcher rejects empty hashes
	// because the supersede gate cannot function without them.
	EnqueueIndexEvent(ctx context.Context, tx *sql.Tx, assetID, contentHash string) error
}
