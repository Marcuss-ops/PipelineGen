// Package outbox — asset_published_errors.go carries the canonical
// typed-error contract for the asset.published handler
// (SEMANTIC-LOCATION-API-2026-07-06 Wave 5, July 2026).
//
// godlike/06 SSOT: this file is the SINGLE canonical owner of the
// 9 typed-error sentinels. No other file in the codebase MAY declare
// these variables; any drift between AssetPublished* sentinels and
// parallel literals surfaces as a build failure at the
// var _ TypedSentinelProbe = (AssetPublishedTerminalEnvelopeProbe)()
// seam.
//
// godlike/07 typed-error contract:
//
//   - The terminal envelope sentinel ErrAssetPublishedTerminalEnvelope
//     aggregates every "retry cannot conjure a missing field"
//     condition. Sub-sentinels wrap it via fmt.Errorf("%w: ...",
//     ErrAssetPublishedTerminalEnvelope, ...) so errors.Is returns
//     true for ANY validation failure path via the umbrella probe.
//   - ErrAssetPublishedQdrantUpsertFailed is RETRYABLE — transient
//     Qdrant / SQLite failures ride the pool's exponential backoff.
//     NOT a terminal sentinel; producers do NOT need to upgrade.
//   - ErrAssetPublishedPublisherNotWired is STICKY-PENDING — the
//     composition root failed to wire the AssetPublisher port. The
//     handler returns this NOT wrapped in TerminalError so the pool's
//     IsTerminal classifier routes the event back to pending (operator
//     re-enables Qdrant + re-emits; NOT a producer-side upgrade).
//
// All 3 sentinel classes form a closed typed contract. Callers MUST
// probe via errors.Is(err, ErrAssetPublishedTerminalEnvelope) for
// terminal-class failures, errors.Is(err, ErrAssetPublishedQdrantUpsertFailed)
// for retryable Qdrant failures, and errors.Is(err, ErrAssetPublishedPublisherNotWired)
// for sticky-pending composition-root misconfigs.
package outbox

import (
	"errors"
	"fmt"
)

// ErrAssetPublishedTerminalEnvelope aggregates every
// payload-validation failure. Sub-sentinels wrap this sentinel via
// fmt.Errorf("%w: ...", ErrAssetPublishedTerminalEnvelope, ...) so
// errors.Is(err, ErrAssetPublishedTerminalEnvelope) returns true for
// ANY validation failure path.
var ErrAssetPublishedTerminalEnvelope = errors.New("asset.published: terminal envelope error")

// ErrAssetPublishedPayloadParse fires when the JSON body is
// malformed — retrying won't help; the producer must fix the
// payload. Wraps the terminal sentinel via %w.
var ErrAssetPublishedPayloadParse = fmt.Errorf("%w: payload parse failed", ErrAssetPublishedTerminalEnvelope)

// ErrAssetPublishedSchemaVersionMismatch fires when the envelope's
// schema_version is not AssetPublishedSchemaVersion. Retry cannot
// conjure a different version. Terminal via the umbrella.
var ErrAssetPublishedSchemaVersionMismatch = fmt.Errorf("%w: schema version mismatch", ErrAssetPublishedTerminalEnvelope)

// ErrAssetPublishedAssetIDMissing fires when asset_id is empty.
// Terminal — retry cannot conjure an id.
var ErrAssetPublishedAssetIDMissing = fmt.Errorf("%w: asset_id is required", ErrAssetPublishedTerminalEnvelope)

// ErrAssetPublishedDestinationMissing fires when destination is
// empty. The handler needs at least the destination to compose the
// semantic embedding text part label. Terminal.
var ErrAssetPublishedDestinationMissing = fmt.Errorf("%w: destination is required", ErrAssetPublishedTerminalEnvelope)

// ErrAssetPublishedEventIDMissing fires when event_id is empty.
// Terminal — retry cannot conjure an id.
var ErrAssetPublishedEventIDMissing = fmt.Errorf("%w: event_id is required", ErrAssetPublishedTerminalEnvelope)

// ErrAssetPublishedIdempotencyKeyMissing fires when idempotency_key
// is empty. Terminal — the handler must log the payload for forensics.
var ErrAssetPublishedIdempotencyKeyMissing = fmt.Errorf("%w: idempotency_key is required", ErrAssetPublishedTerminalEnvelope)

// ErrAssetPublishedQdrantUpsertFailed wraps ANY failure from the
// AssetPublisher port (UpsertFromClip OR SetIndexState). RETRYABLE —
// transient Qdrant / SQLite failures ride the pool's exponential
// backoff and the max_attempts dead-letter cap is the natural safety
// net. NOT a terminal sentinel; producers do NOT need to upgrade.
var ErrAssetPublishedQdrantUpsertFailed = errors.New("asset.published: qdrant upsert failed (retryable)")

// ErrAssetPublishedPublisherNotWired is the canonical sentinel when
// the composition root failed to wire the AssetPublisher port (e.g.
// Qdrant disabled at boot). The handler logs+skips rather than
// returning terminal (no point retrying if the port is structurally
// absent — operator must re-enable Qdrant then re-enqueue).
//
// Errors.Is check is the seam for an operator dashboard: any
// producer that sees this sentinel in the audit log should
// re-emit the event once the indexer is back online.
var ErrAssetPublishedPublisherNotWired = errors.New("asset.published: publisher not wired (Qdrant disabled at composition root)")
